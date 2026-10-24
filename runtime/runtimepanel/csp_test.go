package runtimepanel

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInjectionUsesExistingNonceWithoutChangingPolicy(t *testing.T) {
	policy := "default-src 'self'; script-src 'nonce-TestBase64Abc123=' 'wasm-unsafe-eval' 'strict-dynamic'; style-src 'self'"
	r := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("<html><head></head><body></body></html>"))}
	r.Header.Set("Content-Type", "text/html")
	r.Header.Set("Content-Security-Policy", policy)
	if err := Inject(r, "/app/test"); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), `nonce="TestBase64Abc123="`) || r.Header.Get("Content-Security-Policy") != policy {
		t.Fatal("nonce not preserved", string(body))
	}
	r.Header.Add("Content-Security-Policy", "script-src 'nonce-AnotherNonce'")
	if _, err := scriptNonce(r.Header); err == nil {
		t.Fatal("contradictory strict policies accepted")
	}
}
