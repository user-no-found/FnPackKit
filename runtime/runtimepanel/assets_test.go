package runtimepanel

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestEveryEmbeddedPanelAssetHasExactGetHeadRoute(t *testing.T) {
	names := map[string]string{
		"bootstrap.js": "text/javascript", "panel.js": "text/javascript", "scope.js": "text/javascript",
		"source.js": "text/javascript", "extensions.js": "text/javascript", "drag.js": "text/javascript",
		"presentation.js": "text/javascript", "viewport.js": "text/javascript", "state.js": "text/javascript",
		"geometry.js": "text/javascript", "panel.css": "text/css",
	}
	calls := 0
	h := Handler{Prefix: "/app/test", Execute: func(context.Context, identity.Request, backend.Command) (backend.Snapshot, error) {
		calls++
		return backend.Snapshot{}, nil
	}}
	entries, err := assets.ReadDir("web")
	if err != nil || len(entries) != len(names) {
		t.Fatalf("embedded inventory differs from explicit routes: %v, %d", err, len(entries))
	}
	imports := regexp.MustCompile(`from\s+['"]\./([^'"]+)['"]`)
	for _, entry := range entries {
		name := entry.Name()
		kind, ok := names[name]
		if !ok || entry.IsDir() {
			t.Fatalf("embedded resource lacks an explicit HTTP route: %s", name)
		}
		data, err := assets.ReadFile("web/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if lines := bytes.Count(data, []byte{'\n'}); lines > 400 {
			t.Errorf("%s exceeds the shared module limit: %d lines", name, lines)
		}
		for _, dependency := range imports.FindAllSubmatch(data, -1) {
			if _, ok := names[string(dependency[1])]; !ok {
				t.Errorf("%s imports unavailable module %s", name, dependency[1])
			}
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := httptest.NewRecorder()
			if !h.Handle(w, httptest.NewRequest(method, "/app/test/_runtime/"+name, nil), identity.Request{}) || w.Code != 200 {
				t.Fatalf("%s %s: %d", method, name, w.Code)
			}
			if w.Header().Get("Content-Type") != kind+"; charset=utf-8" ||
				w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("%s %s has unsafe resource headers: %v", method, name, w.Header())
			}
			if method == http.MethodGet && !bytes.Equal(w.Body.Bytes(), data) || method == http.MethodHead && w.Body.Len() != 0 {
				t.Errorf("%s %s did not preserve embedded bytes/HEAD semantics", method, name)
			}
		}
		w := httptest.NewRecorder()
		h.Handle(w, httptest.NewRequest(http.MethodPost, "/app/test/_runtime/"+name, nil), identity.Request{})
		if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("POST %s bypassed method restrictions: %d", name, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("static resource delivery executed a backend command")
	}
}

func TestPanelAssetRoutesRejectTraversalUnknownAndSuffixes(t *testing.T) {
	h := Handler{Prefix: "/app/test"}
	for _, suffix := range []string{"../web/state.js", "web/state.js", "state.js/private", "state.js/..", "idle.js", "geometry.js.map", "%2e%2e/state.js"} {
		w := httptest.NewRecorder()
		h.Handle(w, httptest.NewRequest(http.MethodGet, "/app/test/_runtime/"+suffix, nil), identity.Request{})
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "createInteraction") {
			t.Errorf("unexpected resource escaped the allowlist: %s: %d", suffix, w.Code)
		}
	}
}
