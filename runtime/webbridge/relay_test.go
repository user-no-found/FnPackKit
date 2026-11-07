package webbridge

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/runtimepanel"
)

// This service deliberately has no Running, Start, Stop, build or Git methods.
type relayService struct {
	endpoint *Endpoint
	denied   atomic.Bool
	err      error
}

func (s *relayService) Authorize(*http.Request, identity.Request) error {
	if s.denied.Load() {
		return errors.New("relay permission denied")
	}
	return nil
}

func (s *relayService) Acquire(context.Context, identity.Request, string) (*Endpoint, error) {
	return s.endpoint, s.err
}

func relayFixture(t *testing.T, service *relayService) *fixture {
	t.Helper()
	f := newFixture(t, nil)
	relay, err := NewRelay(Config{Name: "独立本地应用", Gateway: gatewaypolicy.Config{
		Prefix: testPrefix, AllowSameOriginFetch: true}}, service)
	if err != nil {
		t.Fatal(err)
	}
	*f.bridge = *relay
	return f
}

func TestRelayDelegatesControlBodyStatusAndAssetsWithoutLocalExecution(t *testing.T) {
	var calls atomic.Int32
	body := `{"action":"update-backend","confirm":true}`
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/_runtime/control":
			data, _ := io.ReadAll(r.Body)
			if string(data) != body || r.Header.Get("X-FnPackKit-Control") != "1" {
				t.Error("relay modified the management intent")
			}
			w.WriteHeader(409)
			_, _ = io.WriteString(w, "local update rejected")
		case "/_runtime/manifest", "/_runtime/source.js":
			_, _ = io.WriteString(w, "local management owns this resource")
		default:
			w.WriteHeader(418)
		}
	}, EndpointConfig{StripPrefix: true})
	f := relayFixture(t, &relayService{endpoint: endpoint})
	r := f.request("POST", "/_runtime/control", body)
	r.Header.Set("X-FnPackKit-Control", "1")
	res, data := f.do(t, r)
	if res.StatusCode != 409 || data != "local update rejected" {
		t.Fatalf("relay replaced local management decision: %d %s", res.StatusCode, data)
	}
	for _, path := range []string{"/_runtime/manifest", "/_runtime/source.js"} {
		res, data = f.do(t, f.request("GET", path, ""))
		if res.StatusCode != 200 || data != "local management owns this resource" {
			t.Fatalf("relay served its own control resource: %d %s", res.StatusCode, data)
		}
	}
	res, _ = f.do(t, f.request("GET", "/_runtime/unknown", ""))
	if res.StatusCode != 418 || calls.Load() != 4 {
		t.Fatal("local manager did not handle every runtime path")
	}
	res, _ = f.do(t, f.request("GET", "/_gateway/unknown", ""))
	if res.StatusCode != 404 || calls.Load() != 4 {
		t.Fatal("platform gateway paths must remain reserved")
	}
	if _, err := f.bridge.Status(context.Background(), identity.Request{}); !errors.Is(err, backend.ErrAction) {
		t.Fatal("relay manufactured a local runtime status")
	}
}

func TestRelayPreservesNativeHTMLAndCompressedResponses(t *testing.T) {
	html := `<!doctype html><html><head><title>Local app</title></head><body>Native manager</body></html>`
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = io.WriteString(writer, html)
	_ = writer.Close()
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.Header.Get("Accept-Encoding") == "gzip" {
			if r.Header.Get("If-None-Match") != `"native-html"` {
				t.Error("relay stripped a native cache condition")
			}
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(compressed.Bytes())
			return
		}
		_, _ = io.WriteString(w, html)
	}, EndpointConfig{StripPrefix: true})
	f := relayFixture(t, &relayService{endpoint: endpoint})
	plain := f.request("GET", "/", "")
	plain.Header.Set("Accept-Encoding", "identity")
	res, data := f.do(t, plain)
	if res.StatusCode != 200 || data != html {
		t.Fatal("FPK relay injected or altered the application document")
	}
	r := f.request("GET", "/", "")
	r.Header.Set("Accept-Encoding", "gzip")
	r.Header.Set("If-None-Match", `"native-html"`)
	res, data = f.do(t, r)
	if res.StatusCode != 200 || res.Header.Get("Content-Encoding") != "gzip" || data != compressed.String() {
		t.Fatal("FPK relay altered compressed native HTML")
	}
}

func TestRelayRejectsFPKControlsAndKeepsAuthorization(t *testing.T) {
	service := &relayService{}
	for _, config := range []Config{
		{Actions: []runtimepanel.Action{{ID: "settings"}}},
		{Commands: []runtimepanel.Command{{ID: "update-backend"}}},
		{ExecuteCommand: func(context.Context, identity.Request, string) (backend.Snapshot, error) {
			return backend.Snapshot{}, nil
		}},
	} {
		config.Name, config.Gateway.Prefix = "Relay", testPrefix
		if _, err := NewRelay(config, service); err == nil {
			t.Fatal("relay accepted local FPK-owned control execution")
		}
	}
	f := relayFixture(t, service)
	service.denied.Store(true)
	res, _ := f.do(t, f.request("POST", "/_runtime/control", `{"action":"start"}`))
	if res.StatusCode != 403 {
		t.Fatal("relay skipped the platform application's authorization")
	}
	service.denied.Store(false)
	res, _ = f.do(t, f.request("POST", "/_runtime/control", `{"action":"start"}`))
	if res.StatusCode != 503 {
		t.Fatal("relay must report a missing local management endpoint")
	}
	for _, err := range []error{errors.New("unavailable"), backend.ErrPaused} {
		service.err = err
		res, data := f.do(t, f.request("GET", "/", ""))
		if res.StatusCode != 503 || strings.Contains(data, "data-runtime-page") || strings.Contains(data, "启动后端") {
			t.Fatal("relay fabricated a local recovery panel")
		}
	}
}
