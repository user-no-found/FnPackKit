package webbridge

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
)

func TestIndependentUpdatesAndNativeControls(t *testing.T) {
	var version atomic.Int32
	version.Store(1)
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"version":%d}`, version.Load())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "script-src 'nonce-abc123'; object-src 'none'")
		fmt.Fprintf(w, "<html><head></head><body>release %d</body></html>", version.Load())
	}, EndpointConfig{StripPrefix: true})
	f := newFixture(t, endpoint)
	for _, relative := range []string{"/_runtime/control", "/_runtime/manifest", "/_runtime/bootstrap.js", "/_runtime/panel.css"} {
		r, _ := f.do(t, f.request("GET", relative, ""))
		if r.StatusCode != 200 || f.app.starts != 0 || f.app.acquisitions != 0 {
			t.Fatal("control resource acquired business runtime", relative, r.StatusCode)
		}
	}
	r, body := f.do(t, f.request("GET", "/", ""))
	if r.StatusCode != 200 || !strings.Contains(body, "release 1") || !strings.Contains(body, `nonce="abc123"`) ||
		!strings.Contains(body, testPrefix+"/_runtime/bootstrap.js") || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(r.StatusCode, body, r.Header)
	}
	version.Store(2)
	_, body = f.do(t, f.request("GET", "/", ""))
	if !strings.Contains(body, "release 2") {
		t.Fatal("frontend update required replacing bridge", body)
	}
	_, body = f.do(t, f.request("GET", "/api/version", ""))
	if body != `{"version":2}` {
		t.Fatal("backend update not visible", body)
	}
	r, _ = f.do(t, f.request("POST", "/_runtime/control", `{"action":"stop"}`))
	if r.StatusCode != 400 || f.app.stops != 0 {
		t.Fatal("unconfirmed stop executed")
	}
	r, body = f.do(t, f.request("POST", "/_runtime/control", `{"action":"stop","confirm":true}`))
	if r.StatusCode != 200 || !strings.Contains(body, `"state":"paused"`) || f.app.stops != 1 {
		t.Fatal(r.StatusCode, body)
	}
	acquisitions := f.app.acquisitions
	for range 3 {
		r, body = f.do(t, f.request("GET", "/", ""))
		if r.StatusCode != 200 || !strings.Contains(body, "data-runtime-page") || f.app.acquisitions != acquisitions {
			t.Fatal("reload escaped pause gate", r.StatusCode, body)
		}
	}
	r, _ = f.do(t, f.request("GET", "/api/version", ""))
	if r.StatusCode != 503 || f.app.acquisitions != acquisitions {
		t.Fatal("API escaped pause gate")
	}
	r, _ = f.do(t, f.userRequest("GET", "/api/version", "", 1001))
	if r.StatusCode != 200 {
		t.Fatal("another user's runtime was paused")
	}
	for _, action := range []string{"start", "restart"} {
		r, body = f.do(t, f.request("POST", "/_runtime/control", fmt.Sprintf(`{"action":%q,"confirm":true}`, action)))
		if r.StatusCode != 200 || !strings.Contains(body, `"state":"running"`) {
			t.Fatal(action, r.StatusCode, body)
		}
	}
}

func TestAuthenticationAndReservedRoutesPrecedeAcquisition(t *testing.T) {
	f := newFixture(t, nil)
	bare := httptest.NewServer(f.bridge)
	defer bare.Close()
	r := f.request("GET", "/_runtime/manifest", "")
	r.URL, _ = r.URL.Parse(bare.URL + testPrefix + "/_runtime/manifest")
	response, err := bare.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("bare TCP forged a trusted gateway")
	}
	r = f.request("GET", "/", "")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	response, _ = f.do(t, r)
	if response.StatusCode != 403 {
		t.Fatal("cross-site request accepted")
	}
	r = f.request("GET", "/", "")
	r.Header.Add("X-Trim-Userid", "1001")
	response, _ = f.do(t, r)
	if response.StatusCode != 403 {
		t.Fatal("duplicate NAS identity accepted")
	}
	for _, relative := range []string{"/_runtime", "/_gateway/unknown", "/_gateway/grant/other"} {
		response, _ = f.do(t, f.request("GET", relative, ""))
		if response.StatusCode != 404 {
			t.Fatal(relative, response.StatusCode)
		}
	}
	f.app.mu.Lock()
	f.app.denied = true
	f.app.mu.Unlock()
	for _, relative := range []string{"/", "/_runtime/manifest", "/_runtime/control", "/_gateway/grant/ws"} {
		response, body := f.do(t, f.request("POST", relative, `{"action":"start"}`))
		if response.StatusCode != 403 || strings.Contains(body, "private") {
			t.Fatal("application authorization bypassed", relative, response.StatusCode, body)
		}
	}
	if f.app.starts != 0 || f.app.acquisitions != 0 {
		t.Fatal("rejected request acquired a runtime")
	}
}

func TestRecoveryRetainsControlsAndDoesNotClaimSavedState(t *testing.T) {
	f := newFixture(t, nil)
	f.app.acquireError = errors.New("secret backend address")
	r, body := f.do(t, f.request("GET", "/", ""))
	if r.StatusCode != 503 || !strings.Contains(body, "data-runtime-page") ||
		strings.Contains(body, "secret") || strings.Contains(body, "数据已保留") {
		t.Fatal(r.StatusCode, body)
	}
	r, body = f.do(t, f.request("GET", "/api/version", ""))
	if r.StatusCode != 503 || !strings.Contains(body, "runtime_unavailable") {
		t.Fatal(r.StatusCode, body)
	}
	r, _ = f.do(t, f.request("GET", "/_runtime/panel.js", ""))
	if r.StatusCode != 200 {
		t.Fatal("backend failure removed controls")
	}
}

func TestBridgeConfigurationRejectsUnsafeGrantTargets(t *testing.T) {
	app := &fixtureApplication{}
	for _, target := range []string{"/", "relative", "/a/../b", "/ws?uid=1", "/_runtime/control", "/_gateway/grant/ws"} {
		if _, err := New(Config{Name: "App", Gateway: gatewaypolicy.Config{Prefix: testPrefix}, WebSocketPaths: []string{target}}, app); err == nil {
			t.Fatal("accepted unsafe websocket configuration", target)
		}
	}
	if _, err := New(Config{Name: "App", Gateway: gatewaypolicy.Config{Prefix: testPrefix}}, nil); err == nil {
		t.Fatal("accepted missing application")
	}
}
