package webbridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestControlClientClassifiesRejectedAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); w.Write([]byte(`PRIVATE_BODY`)) }))
	defer server.Close()
	endpoint, err := NewEndpoint(EndpointConfig{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewControlClient(endpoint, "/control", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Execute(context.Background(), identity.Request{}, backend.Command{Action: "status"})
	if !errors.Is(err, backend.ErrControlAuthentication) || strings.Contains(err.Error(), "PRIVATE_BODY") {
		t.Fatal(err)
	}
}

func TestControlClientNeverForwardsSudoPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "password") || strings.Contains(string(body), "FIXTURE-SECRET") {
			t.Error("sudo secret reached native API")
		}
		w.Write([]byte(`{"schema":1,"state":"running","paused":false}`))
	}))
	defer server.Close()
	endpoint, err := NewEndpoint(EndpointConfig{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewControlClient(endpoint, "/control", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Execute(context.Background(), identity.Request{}, backend.Command{Action: "update-backend", Confirm: true, Password: "FIXTURE-SECRET"})
	if err != nil {
		t.Fatal(err)
	}
}
