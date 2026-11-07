package localrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/peer"
	"github.com/user-no-found/FnPackKit/runtime/process"
	"github.com/user-no-found/FnPackKit/runtime/runtimepanel"
	"github.com/user-no-found/FnPackKit/runtime/webbridge"
)

type independentFixture struct {
	repo       *fixture
	config     localServiceConfig
	address    string
	managerPID int
	client     *http.Client
}

func newIndependentFixture(t *testing.T) *independentFixture {
	t.Helper()
	repo := newRepoFixture(t)
	if err := os.Mkdir(filepath.Join(repo.seed, "frontend"), 0700); err != nil {
		t.Fatal(err)
	}
	writeTest(t, filepath.Join(repo.seed, "frontend/index.html"), `<!doctype html><html><head><meta charset="utf-8"><title>本地独立应用</title></head><body><h1>本地界面</h1></body></html>`)
	gitTest(t, repo.seed, "add", "frontend")
	gitTest(t, repo.seed, "commit", "-m", "local frontend fixture")
	gitTest(t, repo.seed, "push", "origin", "main")
	gitTest(t, repo.directory, "pull", "--ff-only", "origin", "main")
	data := filepath.Join(repo.root, "application-state")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	owner, err := identity.ResolveUID(uint32(os.Getuid()))
	if err != nil {
		t.Fatal(err)
	}
	f := &independentFixture{repo: repo, config: localServiceConfig{Workspace: repo.directory, Data: data,
		Owner: identity.Request{UID: owner.UID, Name: owner.Name}}, client: &http.Client{Timeout: 15 * time.Second}}
	encoded, _ := json.Marshal(f.config)
	executable, _ := os.Executable()
	command := exec.Command(executable, "-test.run=^TestIndependentManagementProcess$")
	command.Dir, command.Stderr = repo.directory, os.Stderr
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "TRIM_") && !strings.HasPrefix(value, "LOCAL_SERVICE_") && !strings.HasPrefix(value, "FNPACKKIT_LOCAL_SOURCE=") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "LOCAL_SERVICE_CHILD=manager", "LOCAL_SERVICE_CONFIG="+string(encoded))
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child, err := process.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		select {
		case <-child.Done():
		case <-time.After(5 * time.Second):
			if err := child.KillAndWait(time.Second); err != nil {
				t.Error(err)
			}
		}
	})
	var ready struct {
		URL string
		PID int
	}
	decoded := make(chan error, 1)
	go func() { decoded <- json.NewDecoder(output).Decode(&ready) }()
	select {
	case err = <-decoded:
	case <-time.After(5 * time.Second):
		err = errors.New("local manager readiness timed out")
	}
	if err != nil {
		t.Fatal(err)
	}
	f.address, f.managerPID = ready.URL, ready.PID
	return f
}

func localRequest(t *testing.T, client *http.Client, method, address, body string) (int, string) {
	t.Helper()
	r, err := http.NewRequest(method, address, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(&http.Cookie{Name: "local_session", Value: "independent-local-fixture"})
	if method == "POST" {
		origin, _ := url.Parse(address)
		r.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FnPackKit-Control", "1")
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data)
}

// Only an upstream endpoint and platform authorization exist on the FPK side.
type localRelayService struct {
	endpoint *webbridge.Endpoint
	owner    identity.Request
}

func (s localRelayService) Authorize(_ *http.Request, user identity.Request) error {
	if user != s.owner {
		return errors.New("application owner mismatch")
	}
	return nil
}

func (s localRelayService) Acquire(context.Context, identity.Request, string) (*webbridge.Endpoint, error) {
	return s.endpoint, nil
}

type localControlledService struct {
	localRelayService
	control *webbridge.ControlClient
}

func (s localControlledService) Control(ctx context.Context, id identity.Request, command backend.Command) (backend.Snapshot, error) {
	return s.control.Execute(ctx, id, command)
}

type localRelayEntry struct {
	address string
	root    string
	close   func()
}

func (f *independentFixture) relay(t *testing.T) *localRelayEntry {
	return f.relayMode(t, false)
}

func (f *independentFixture) controlledRelay(t *testing.T) *localRelayEntry {
	return f.relayMode(t, true)
}

func (f *independentFixture) relayMode(t *testing.T, controlled bool) *localRelayEntry {
	t.Helper()
	root := t.TempDir()
	endpoint, err := webbridge.NewEndpoint(webbridge.EndpointConfig{URL: f.address, ForwardIdentity: true,
		Authorization: fixtureAuthorization, Cookies: []string{"local_session"}})
	if err != nil {
		t.Fatal(err)
	}
	config := webbridge.Config{Name: "FPK 访问入口", Gateway: gatewaypolicy.Config{Prefix: localPrefix, AllowSameOriginFetch: true}}
	service := localRelayService{endpoint, f.config.Owner}
	var bridge *webbridge.Bridge
	if controlled {
		control, controlErr := webbridge.NewControlClient(endpoint, localPrefix+"/_runtime/control", 15*time.Second)
		if controlErr != nil {
			t.Fatal(controlErr)
		}
		config.Name = "FPK 后端管理"
		config.Commands = []runtimepanel.Command{runtimepanel.UpdateBackendCommand(), runtimepanel.RebuildLocalCommand()}
		bridge, err = webbridge.NewControlledRelay(config, localControlledService{service, control})
	} else {
		bridge, err = webbridge.NewRelay(config, service)
	}
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "bridge.sock")
	base, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := peer.NewListener(base, []uint32{uint32(os.Getuid())}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: bridge, ConnContext: trusted.ConnContext, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(trusted) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	target, _ := url.Parse("http://local-entry")
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.Out.Host = p.In.Host
		p.Out.Header.Set("X-Trim-Userid", fmt.Sprint(f.config.Owner.UID))
		p.Out.Header.Set("X-Trim-Username", f.config.Owner.Name)
	}}
	entry := httptest.NewServer(proxy)
	close := func() {
		entry.Close()
		transport.CloseIdleConnections()
		endpoint.CloseIdleConnections()
		trusted.CloseConnections()
		_ = server.Close()
	}
	t.Cleanup(close)
	return &localRelayEntry{entry.URL, root, close}
}
