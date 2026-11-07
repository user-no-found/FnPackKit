package webbridge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestControlClientSendsIntentAndVerifiedIdentityToLocalAPI(t *testing.T) {
	var calls atomic.Int32
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/native/control" || r.Header.Get("Authorization") != "Bearer local-control-fixture" ||
			r.Header.Get("X-Trim-Userid") != "1000" || r.Header.Get("X-Trim-Username") != "owner" || r.Header.Get("Cookie") != "" {
			t.Error("local API did not receive the configured identity and connection boundary")
		}
		if r.Method == "GET" {
			_, _ = io.WriteString(w, `{"schema":1,"state":"paused","paused":true}`)
			return
		}
		if r.Header.Get("Origin") == "" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-FnPackKit-Control") != "1" {
			t.Error("local control intent is incomplete")
		}
		var command backend.Command
		if json.NewDecoder(r.Body).Decode(&command) != nil || command.Action != "update-backend" || !command.Confirm {
			t.Error("local API did not receive the exact confirmed update intent")
		}
		_, _ = io.WriteString(w, `{"schema":1,"state":"running","paused":false,"source":{"running":{"commit":"new-release","fingerprint":"new-build","dirty":false}}}`)
	}, EndpointConfig{Authorization: "Bearer local-control-fixture", ForwardIdentity: true})
	client, err := NewControlClient(endpoint, "/native/control", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	id := identity.Request{UID: 1000, Name: "owner"}
	state, err := client.Execute(context.Background(), id, backend.Command{Action: "status"})
	if err != nil || state.State != "paused" || !state.Paused {
		t.Fatal("status lost local backend state", state, err)
	}
	state, err = client.Execute(context.Background(), id, backend.Command{Action: "update-backend", Confirm: true})
	if err != nil || state.Source == nil || state.Source.Running.Commit != "new-release" || calls.Load() != 2 {
		t.Fatal("update response lost actual running version", state, err)
	}
}

func TestControlClientRejectsFailureAndMalformedResults(t *testing.T) {
	for _, test := range []struct {
		name, body string
		code       int
	}{
		{"failed", `{"schema":1,"state":"running","failed":true}`, 200},
		{"error-status", `{"schema":1,"state":"paused","paused":true}`, 503},
		{"schema", `{"schema":2,"state":"running"}`, 200},
		{"state", `{"schema":1,"state":"invented"}`, 200},
		{"unavailable", `{"schema":1,"state":"unavailable"}`, 200},
		{"start-not-completed", `{"schema":1,"state":"paused","paused":true}`, 200},
		{"html", `<html>not a status</html>`, 200},
		{"extra-object", `{"schema":1,"state":"running"}{}`, 200},
		{"oversize", strings.Repeat(" ", 64*1024+1), 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.code)
				_, _ = io.WriteString(w, test.body)
			}, EndpointConfig{})
			client, err := NewControlClient(endpoint, "/control", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Execute(context.Background(), identity.Request{}, backend.Command{Action: "start"}); !errors.Is(err, ErrControl) {
				t.Fatal("local failure was reported as success", err)
			}
		})
	}
}

func TestControlClientBoundsRequestsAndDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			return
		}
		<-r.Context().Done()
	}, EndpointConfig{})
	for _, name := range []string{"/redirect", "/wait"} {
		client, err := NewControlClient(endpoint, name, 50*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Execute(context.Background(), identity.Request{}, backend.Command{Action: "status"}); !errors.Is(err, ErrControl) {
			t.Fatal("unreachable control API reported success", err)
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("control credentials could follow a local API redirect")
	}
	for _, path := range []string{"", "/", "/control?x=1", "/a/../control", "/control%2f"} {
		if _, err := NewControlClient(endpoint, path, time.Second); err == nil {
			t.Fatal("invalid local control path accepted", path)
		}
	}
	if _, err := NewControlClient(endpoint, "/control", 0); err == nil {
		t.Fatal("missing operation timeout accepted")
	}
}
