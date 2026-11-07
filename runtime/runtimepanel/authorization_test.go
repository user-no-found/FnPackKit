package runtimepanel

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestTypedCommandAuthorizationAndSafeDiagnostics(t *testing.T) {
	calls := 0
	id := identity.Request{UID: 1000, Name: "fixture-owner"}
	h := Handler{Prefix: "/app/test", Commands: []Command{{ID: "update-backend", Confirm: true}},
		CommandHandler: func(_ context.Context, user identity.Request, command backend.Command) (backend.Snapshot, error) {
			calls++
			if user != id || command.Password != "FIXTURE-SECRET" {
				t.Fatal("authorization or identity was lost")
			}
			return backend.Snapshot{Schema: 1, State: "unavailable"}, backend.ErrControlAuthentication
		}}
	r := httptest.NewRequest("POST", "/app/test/_runtime/control", strings.NewReader(`{"action":"update-backend","confirm":true,"password":"FIXTURE-SECRET"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://nas")
	r.Header.Set("X-FnPackKit-Control", "1")
	w := httptest.NewRecorder()
	h.Handle(w, r, id)
	if calls != 1 || w.Code != 503 || !strings.Contains(w.Body.String(), `"password_required":true`) || strings.Contains(w.Body.String(), "FIXTURE-SECRET") {
		t.Fatalf("unsafe response: %d %s", w.Code, w.Body)
	}
}

func TestLegacyCallbackDoesNotSilentlyDiscardAuthorization(t *testing.T) {
	h := Handler{Prefix: "/app/test", Commands: []Command{{ID: "update", Confirm: true}},
		ExecuteCommand: func(context.Context, identity.Request, string) (backend.Snapshot, error) {
			t.Fatal("authorization was discarded")
			return backend.Snapshot{}, nil
		}}
	r := httptest.NewRequest("POST", "/app/test/_runtime/control", strings.NewReader(`{"action":"update","confirm":true,"password":"FIXTURE-SECRET"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://nas")
	r.Header.Set("X-FnPackKit-Control", "1")
	w := httptest.NewRecorder()
	h.Handle(w, r, identity.Request{})
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
