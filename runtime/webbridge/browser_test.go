package webbridge

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/user-no-found/FnPackKit/runtime/runtimepanel"
)

// This check uses the real bridge and local HTTP services inside a simulated desktop iframe.
func TestBrowserIndependentDeploymentAndControls(t *testing.T) {
	if os.Getenv("FNPACKKIT_BROWSER_E2E") != "1" {
		t.Skip("set FNPACKKIT_BROWSER_E2E=1 and registered BuildFlow browser environment")
	}
	node := os.Getenv("BUILDFLOW_BROWSER_NODE")
	if node == "" || os.Getenv("BUILDFLOW_BROWSER_RUNTIME") == "" {
		t.Fatal("resolve the registered BuildFlow browser environment before running this test")
	}
	dir := t.TempDir()
	pageFile, apiFile := filepath.Join(dir, "index.html"), filepath.Join(dir, "api-release")
	writeRelease := func(version string) error {
		html := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>独立 Web 应用</title></head><body><h1>界面 ` + version + `</h1><button id="settings-toggle" onclick="document.getElementById('settings').hidden=false">原设置入口</button><div id="settings" hidden>业务设置</div></body></html>`
		if err := os.WriteFile(pageFile, []byte(html), 0600); err != nil {
			return err
		}
		return os.WriteFile(apiFile, []byte(version), 0600)
	}
	if err := writeRelease("v1"); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, nil)
	f.bridge.panel.Actions = []runtimepanel.Action{{ID: "settings", Label: "应用设置", Selector: "#settings-toggle"}}
	var business *httptest.Server
	f.app.onStart = func() error {
		release, err := os.ReadFile(apiFile)
		if err != nil {
			return err
		}
		business = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer bridge-test" || r.Header.Get("X-Trim-Userid") != "1000" {
				http.Error(w, "unauthorized bridge", 403)
				return
			}
			switch r.URL.Path {
			case "/api/version":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"version": string(release)})
			case "/hmr":
				echoWebSocket(w, r)
			default:
				data, err := os.ReadFile(pageFile)
				if err != nil {
					http.Error(w, "missing frontend", 500)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(data)
			}
		}))
		f.app.endpoint, err = NewEndpoint(EndpointConfig{URL: business.URL, StripPrefix: true,
			Authorization: "Bearer bridge-test", ForwardIdentity: true})
		return err
	}
	f.app.onStop = func() error {
		if business != nil {
			f.app.endpoint.CloseIdleConnections()
			business.Close()
		}
		return nil
	}
	t.Cleanup(func() {
		if business != nil {
			business.Close()
		}
	})
	transport := f.client.Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	target, _ := url.Parse("http://unix-entry")
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.Out.Host = p.In.Host
		p.Out.Header.Set("X-Trim-Userid", "1000")
		p.Out.Header.Set("X-Trim-Username", "owner")
	}}
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/desktop":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<iframe id="app" title="飞牛应用窗口" src="%s/" style="width:1000px;height:760px;border:0"></iframe>`, testPrefix)
		case "/__fixture/release":
			if r.Method != http.MethodPost {
				w.WriteHeader(405)
				return
			}
			if err := writeRelease("v2"); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.WriteHeader(204)
		case "/__fixture/counters":
			f.app.mu.Lock()
			defer f.app.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]int{"starts": f.app.starts, "stops": f.app.stops, "acquisitions": f.app.acquisitions})
		default:
			proxy.ServeHTTP(w, r)
		}
	}))
	defer entry.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	source, err := os.ReadFile("../../tests/browser_web_bridge.mjs")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", string(source))
	command.Env = append(os.Environ(), "FNPACKKIT_BRIDGE_URL="+entry.URL)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"pageErrors":0`) {
		t.Fatalf("browser bridge: %v\n%s", err, output)
	}
	t.Log(string(output))
}
