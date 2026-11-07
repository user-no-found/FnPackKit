package localrepo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/peer"
	"github.com/user-no-found/FnPackKit/runtime/runtimepanel"
	"github.com/user-no-found/FnPackKit/runtime/webbridge"
)

type localApplication struct {
	*Manager
	endpoint *webbridge.Endpoint
}

func (a *localApplication) Authorize(_ *http.Request, id identity.Request) error {
	return a.authorize(id)
}
func (a *localApplication) Acquire(ctx context.Context, id identity.Request, _ string) (*webbridge.Endpoint, error) {
	if err := a.Start(ctx, id); err != nil {
		return nil, err
	}
	return a.endpoint, nil
}

func TestLocalRepositoryThroughAuthenticatedWebBridge(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	business := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, err := f.runtime.State(r.Context())
		if err != nil || !state.Running {
			http.Error(w, "stopped", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><head></head><body>Local workspace service</body></html>")
	}))
	defer business.Close()
	endpoint, err := webbridge.NewEndpoint(webbridge.EndpointConfig{URL: business.URL, StripPrefix: true})
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.CloseIdleConnections()
	app := &localApplication{m, endpoint}
	var bridge *webbridge.Bridge
	bridge, err = webbridge.New(webbridge.Config{Name: "Local fixture", Gateway: gatewaypolicy.Config{Prefix: "/app/local", AllowSameOriginFetch: true},
		Commands: []runtimepanel.Command{runtimepanel.RebuildLocalCommand(), runtimepanel.UpdateBackendCommand()},
		ExecuteCommand: func(ctx context.Context, id identity.Request, command string) (backend.Snapshot, error) {
			var err error
			switch command {
			case "rebuild-local":
				err = m.Rebuild(ctx, id)
			case "update-backend":
				err = m.Update(ctx, id)
			default:
				err = backend.ErrAction
			}
			status, readErr := bridge.Status(ctx, id)
			return status, errors.Join(err, readErr)
		}}, app)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(f.root, "entry.sock"))
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := peer.NewListener(listener, []uint32{uint32(os.Geteuid())}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: bridge, ConnContext: trusted.ConnContext}
	go server.Serve(trusted)
	defer func() { server.Close(); trusted.CloseConnections() }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", listener.Addr().String())
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	request := func(method, path, body string, authorized bool) (int, []byte) {
		t.Helper()
		r, _ := http.NewRequest(method, "http://nas.test/app/local"+path, strings.NewReader(body))
		if authorized {
			r.Header.Set("X-Trim-Userid", strconv.Itoa(int(m.owner.UID)))
			r.Header.Set("X-Trim-Username", m.owner.Name)
		}
		if method == "POST" {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-FnPackKit-Control", "1")
			r.Header.Set("Origin", "http://nas.test")
		}
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, data
	}
	status := func() backend.Snapshot {
		code, data := request("GET", "/_runtime/control", "", true)
		if code != 200 {
			t.Fatal(code, string(data))
		}
		var value backend.Snapshot
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	initial := status()
	if initial.State != "stopped" || initial.Source.Workspace == nil || f.runtime.starts != 0 {
		t.Fatal("inspection started runtime")
	}
	if code, _ := request("GET", "/_runtime/control", "", false); code != 403 {
		t.Fatal("source status bypassed authentication")
	}
	if code, _ := request("GET", "/_runtime/source.js", "", true); code != 200 {
		t.Fatal("source renderer not served")
	}
	if code, _ := request("GET", "/", "", true); code != 200 {
		t.Fatal("local service did not acquire")
	}
	writeTest(t, filepath.Join(f.directory, "message.txt"), "edited locally")
	if !status().Source.Changed {
		t.Fatal("shared source status missed drift")
	}
	if code, _ := request("POST", "/_runtime/control", `{"action":"rebuild-local"}`, true); code != 400 {
		t.Fatal("unconfirmed rebuild executed")
	}
	if code, _ := request("POST", "/_runtime/control", `{"action":"rebuild-local","confirm":true}`, true); code != 200 {
		t.Fatal("local edit application failed")
	}
	if status().Source.Changed || f.runtime.content != "edited locally" {
		t.Fatal("new process does not use local changes")
	}
	if code, _ := request("POST", "/_runtime/control", `{"action":"update-backend","confirm":true}`, true); code != 503 {
		t.Fatal("dirty remote update accepted")
	}
	writeTest(t, filepath.Join(f.directory, "message.txt"), "first")
	target := f.advance(t, "remote after pause")
	if code, _ := request("POST", "/_runtime/control", `{"action":"stop","confirm":true}`, true); code != 200 {
		t.Fatal("stop failed")
	}
	starts := f.runtime.starts
	code, data := request("POST", "/_runtime/control", `{"action":"update-backend","confirm":true}`, true)
	if code != 200 || !strings.Contains(string(data), `"paused":true`) {
		t.Fatal("custom update lost shared manual pause", code, string(data))
	}
	if code, data := request("GET", "/", "", true); code != 200 || !strings.Contains(string(data), "data-runtime-page") {
		t.Fatal("paused navigation acquired service")
	}
	if f.runtime.starts != starts || status().Source.Workspace.Commit != target {
		t.Fatal("update woke paused runtime or used stale commit")
	}
	if code, _ := request("POST", "/_runtime/control", `{"action":"start"}`, true); code != 200 {
		t.Fatal("start failed")
	}
	final := status()
	if final.Source.Changed || final.Source.Running.Commit != target || f.runtime.content != "remote after pause" {
		t.Fatal("final process does not run fetched commit")
	}
}
