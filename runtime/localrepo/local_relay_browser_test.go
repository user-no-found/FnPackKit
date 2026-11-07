package localrepo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBrowserNativeManagementThroughFPKRelay(t *testing.T) {
	if os.Getenv("FNPACKKIT_BROWSER_E2E") != "1" {
		t.Skip("set FNPACKKIT_BROWSER_E2E=1 with registered BuildFlow browser tools")
	}
	node := os.Getenv("BUILDFLOW_BROWSER_NODE")
	if node == "" || os.Getenv("BUILDFLOW_BROWSER_RUNTIME") == "" {
		t.Fatal("registered browser runtime is required")
	}
	f := newIndependentFixture(t)
	controlState(t, f.client, f.address, `{"action":"start"}`, 200)
	relay := f.controlledRelay(t)
	target, _ := url.Parse(relay.address)
	proxy := httputil.NewSingleHostReverseProxy(target)
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture/advance" && r.Method == "POST" {
			commit := f.repo.advance(t, "browser updated backend")
			_ = json.NewEncoder(w).Encode(map[string]string{"commit": commit})
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer entry.Close()
	source, err := os.ReadFile("../../tests/browser_local_application.mjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "--input-type=module", "-e", string(source))
	command.Env = append(os.Environ(), "LOCAL_NATIVE_URL="+f.address, "LOCAL_RELAY_URL="+entry.URL)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `"pageErrors":0`) {
		t.Fatalf("independent local application browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}
