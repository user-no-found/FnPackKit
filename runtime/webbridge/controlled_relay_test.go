package webbridge

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/runtimepanel"
)

type controlledRelayService struct {
	*relayService
	mu       sync.Mutex
	commands []backend.Command
	state    backend.Snapshot
}

func (s *controlledRelayService) Control(_ context.Context, id identity.Request, command backend.Command) (backend.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, command)
	if command.Action != "status" {
		s.state = backend.Snapshot{Schema: 1, State: "paused", Paused: true}
	}
	return s.state, nil
}

func TestControlledRelayProvidesPanelAndDelegatesEveryManagementIntent(t *testing.T) {
	endpoint := endpointFor(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_runtime/") {
			t.Error("FPK panel resource was incorrectly sent to the business endpoint")
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html><head></head><body>Independent application</body></html>`)
	}, EndpointConfig{StripPrefix: true})
	f := newFixture(t, nil)
	service := &controlledRelayService{relayService: &relayService{endpoint: endpoint}, state: backend.Snapshot{Schema: 1, State: "running"}}
	bridge, err := NewControlledRelay(Config{Name: "FPK 管理", ControlScope: runtimepanel.ScopeShared, Gateway: gatewaypolicy.Config{
		Prefix: testPrefix, AllowSameOriginFetch: true}, Commands: []runtimepanel.Command{runtimepanel.UpdateBackendCommand()}}, service)
	if err != nil {
		t.Fatal(err)
	}
	*f.bridge = *bridge
	if f.bridge.controls != nil {
		t.Fatal("controlled relay created a second process/pause authority")
	}
	res, html := f.do(t, f.request("GET", "/", ""))
	if res.StatusCode != 200 || !strings.Contains(html, "Independent application") || !strings.Contains(html, "/_runtime/bootstrap.js") {
		t.Fatal("FPK did not provide its management panel")
	}
	res, manifest := f.do(t, f.request("GET", "/_runtime/manifest", ""))
	if res.StatusCode != 200 || !strings.Contains(manifest, "FPK 管理") || !strings.Contains(manifest, "update-backend") || !strings.Contains(manifest, `"scope":"shared"`) {
		t.Fatal("FPK did not advertise its update controls")
	}
	for _, command := range []string{`{"action":"start"}`, `{"action":"stop","confirm":true}`,
		`{"action":"restart","confirm":true}`, `{"action":"update-backend","confirm":true}`} {
		r := f.request("POST", "/_runtime/control", command)
		r.Header.Set("X-FnPackKit-Control", "1")
		res, data := f.do(t, r)
		if res.StatusCode != 200 || !strings.Contains(data, `"paused":true`) {
			t.Fatal("FPK replaced the local management decision", res.StatusCode, data)
		}
	}
	state, err := f.bridge.Status(context.Background(), identity.Request{UID: 1000, Name: "owner"})
	if err != nil || !state.Paused || state.State != "paused" {
		t.Fatal("FPK status did not come from local management")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.commands) != 6 || service.commands[4].Action != "update-backend" || !service.commands[4].Confirm || service.commands[5].Action != "status" {
		t.Fatal("local service did not receive the exact lifecycle/update actions", service.commands)
	}
}

func TestControlledRelayRejectsUnknownControlScope(t *testing.T) {
	service := &controlledRelayService{relayService: &relayService{}}
	_, err := NewControlledRelay(Config{Name: "Test", ControlScope: "unknown", Gateway: gatewaypolicy.Config{Prefix: testPrefix}}, service)
	if err == nil {
		t.Fatal("invalid control scope accepted")
	}
}
