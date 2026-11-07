package runtimepanel

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestScopeManifestIsExplicitAndDefaultRemainsPerUser(t *testing.T) {
	for _, scope := range []Scope{"", ScopeUser, ScopeShared, "unknown"} {
		t.Run(string(scope), func(t *testing.T) {
			h := Handler{Prefix: "/app/test", Name: "Test", Scope: scope}
			w := httptest.NewRecorder()
			h.Handle(w, httptest.NewRequest("GET", "/app/test/_runtime/manifest", nil), identity.Request{})
			if !scope.Valid() {
				if w.Code != 500 {
					t.Fatal("invalid scope was advertised")
				}
				return
			}
			var result map[string]any
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatal("invalid manifest")
			}
			if scope == "" {
				if _, exists := result["scope"]; exists {
					t.Fatal("default manifest behavior changed")
				}
			} else if result["scope"] != string(scope) {
				t.Fatal("scope not propagated")
			}
		})
	}
}

func TestScopePresentationModuleIsServedFromEmbeddedAssets(t *testing.T) {
	w := httptest.NewRecorder()
	Handler{Prefix: "/app/test", Scope: ScopeShared}.Handle(w,
		httptest.NewRequest("GET", "/app/test/_runtime/scope.js", nil), identity.Request{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "scopeNotice") || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatal("shared scope module was not included in the runtime panel")
	}
}
