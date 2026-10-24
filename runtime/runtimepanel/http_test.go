package runtimepanel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestControlAcceptsIntentOnlyAndNeverBodyIdentity(t *testing.T) {
	id := identity.Request{UID: 1000, Name: "owner"}
	calls := 0
	h := Handler{Prefix: "/app/test", Name: "Test", Execute: func(ctx context.Context, user identity.Request, c backend.Command) (backend.Snapshot, error) {
		calls++
		if user != id {
			t.Fatal("body replaced authenticated identity")
		}
		return backend.Snapshot{Schema: 1, State: "paused", Paused: true}, nil
	}}
	cases := []struct {
		method, body, kind, origin, header string
		code                               int
		calls                              int
	}{
		{"GET", "", "", "", "", 200, 1},
		{"POST", `{"action":"stop","confirm":true}`, "application/json", "http://nas", "1", 200, 1},
		{"POST", `{"action":"stop","confirm":true,"uid":1001}`, "application/json", "http://nas", "1", 400, 0},
		{"POST", `{"action":"start"} {}`, "application/json", "http://nas", "1", 400, 0},
		{"POST", `{"action":"start"}`, "text/plain", "http://nas", "1", 403, 0},
		{"POST", `{"action":"start"}`, "application/json", "", "1", 403, 0},
		{"POST", `{"action":"start"}`, "application/json", "http://nas", "", 403, 0},
		{"DELETE", "", "", "", "", 405, 0},
	}
	for _, c := range cases {
		before := calls
		r := httptest.NewRequest(c.method, "/app/test/_runtime/control", strings.NewReader(c.body))
		r.Header.Set("Content-Type", c.kind)
		r.Header.Set("Origin", c.origin)
		r.Header.Set("X-FnPackKit-Control", c.header)
		w := httptest.NewRecorder()
		if !h.Handle(w, r, id) || w.Code != c.code || calls-before != c.calls {
			t.Fatalf("%+v: code=%d body=%s calls=%d", c, w.Code, w.Body, calls-before)
		}
	}
}

func TestCustomCommandNeedsManifestAndConfirmation(t *testing.T) {
	id := identity.Request{UID: 1000, Name: "owner"}
	calls := 0
	h := Handler{
		Prefix:   "/app/test",
		Commands: []Command{{ID: "update", Label: "更新后端", Confirm: true}},
		ExecuteCommand: func(_ context.Context, user identity.Request, action string) (backend.Snapshot, error) {
			calls++
			if user != id || action != "update" {
				t.Fatal("custom command identity or action mismatch")
			}
			return backend.Snapshot{Schema: 1, State: "stopped"}, nil
		},
	}
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/app/test/_runtime/control", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://nas")
		r.Header.Set("X-FnPackKit-Control", "1")
		w := httptest.NewRecorder()
		if !h.Handle(w, r, id) {
			t.Fatal("control request not handled")
		}
		return w
	}
	if w := post(`{"action":"update"}`); w.Code != 400 || calls != 0 {
		t.Fatalf("unconfirmed custom command: %d %s", w.Code, w.Body)
	}
	if w := post(`{"action":"update","confirm":true}`); w.Code != 200 || calls != 1 || !strings.Contains(w.Body.String(), `"state":"stopped"`) {
		t.Fatalf("confirmed custom command: %d %s", w.Code, w.Body)
	}
	h.Commands = nil
	if w := post(`{"action":"update","confirm":true}`); w.Code != 400 {
		t.Fatalf("undeclared custom command: %d %s", w.Code, w.Body)
	}
	h.Commands = []Command{{ID: "update", Label: "更新后端", Confirm: true}}
	w := httptest.NewRecorder()
	h.Handle(w, httptest.NewRequest("GET", "/app/test/_runtime/manifest", nil), id)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"update"`) || !strings.Contains(w.Body.String(), `"confirm":true`) {
		t.Fatalf("manifest missing custom command: %d %s", w.Code, w.Body)
	}
}

func TestEmbeddedResourcesAndStoppedNavigation(t *testing.T) {
	h := Handler{Prefix: "/app/test", Name: "<safe>"}
	for _, name := range []string{"bootstrap.js", "panel.js", "drag.js", "panel.css", "manifest"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/app/test/_runtime/"+name, nil)
		if !h.Handle(w, r, identity.Request{}) || w.Code != 200 || w.Body.Len() == 0 {
			t.Fatal(name, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.Unavailable(w, httptest.NewRequest("GET", "/app/test/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "bootstrap.js") || !strings.Contains(w.Body.String(), "&lt;safe&gt;") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.Unavailable(w, httptest.NewRequest("POST", "/app/test/api/run", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "backend_paused") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestResponseInjectionPreservesHTMLAndIsIdempotent(t *testing.T) {
	r := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("<html><head><title>app</title></head><body>work</body></html>"))}
	r.Header.Set("Content-Type", "text/html; charset=utf-8")
	r.Header.Set("ETag", "stale")
	if err := Inject(r, "/app/test"); err != nil {
		t.Fatal(err)
	}
	if err := Inject(r, "/app/test"); err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	if bytes.Count(data, []byte("/_runtime/bootstrap.js")) != 1 || !bytes.Contains(data, []byte("<body>work</body>")) || r.ContentLength != int64(len(data)) || r.Header.Get("ETag") != "" {
		t.Fatal(string(data))
	}
	r.Body = io.NopCloser(strings.NewReader("compressed"))
	r.Header.Set("Content-Encoding", "gzip")
	if err := Inject(r, "/app/test"); err == nil {
		t.Fatal("accepted compressed response")
	}
	for _, prefix := range []string{"/app/'bad", "/app/<bad>", "/app/a?b", "//app", "/app/../x"} {
		if ValidPrefix(prefix) {
			t.Fatal(prefix)
		}
	}
}

func TestConfirmationFailureReturnsRejectedStatus(t *testing.T) {
	h := Handler{Prefix: "/app/test", Execute: func(context.Context, identity.Request, backend.Command) (backend.Snapshot, error) {
		return backend.Snapshot{}, backend.ErrConfirmation
	}}
	r := httptest.NewRequest("POST", "/app/test/_runtime/control", strings.NewReader(`{"action":"stop"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://nas")
	r.Header.Set("X-FnPackKit-Control", "1")
	w := httptest.NewRecorder()
	h.Handle(w, r, identity.Request{})
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	h.Execute = func(context.Context, identity.Request, backend.Command) (backend.Snapshot, error) {
		return backend.Snapshot{Schema: 1, State: "running", Paused: true}, errors.New("private filesystem path")
	}
	r.Body = io.NopCloser(strings.NewReader(`{"action":"stop","confirm":true}`))
	w = httptest.NewRecorder()
	h.Handle(w, r, identity.Request{})
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHeadResponseDoesNotRequireHTMLBody(t *testing.T) {
	r := &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody, Request: httptest.NewRequest("HEAD", "/app/test/", nil), ContentLength: 1000}
	r.Header.Set("Content-Type", "text/html")
	if err := Inject(r, "/app/test"); err != nil || r.ContentLength != 1000 {
		t.Fatal(err, r.ContentLength)
	}
}
