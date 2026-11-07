// Package webbridge connects an authenticated fnOS entry to independently deployed local Web services.
package webbridge

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/runtimepanel"
)

// Service authorizes gateway access and selects an independently running local endpoint.
// In relay mode Acquire must not build, update or start the application.
type Service interface {
	Authorize(*http.Request, identity.Request) error
	Acquire(context.Context, identity.Request, string) (*Endpoint, error)
}

// ControlledService delegates real lifecycle and update commands to the local
// management service. Control returns its authoritative state, not a prediction.
type ControlledService interface {
	Service
	Control(context.Context, identity.Request, backend.Command) (backend.Snapshot, error)
}

// Application additionally owns real processes in the explicitly managed mode.
// Acquire may start work on demand; Stop drains and terminates the owned runtime.
type Application interface {
	Service
	backend.Target
}

// Config is supplied by the application's existing runtime configuration boundary.
// WebSocketPaths are exact relative paths, such as /ws, eligible for handshake grants.
type Config struct {
	Name           string
	ControlScope   runtimepanel.Scope
	Gateway        gatewaypolicy.Config
	Actions        []runtimepanel.Action
	Commands       []runtimepanel.Command
	ExecuteCommand runtimepanel.CommandFunc
	WebSocketPaths []string
	ReportError    func(*http.Request, error)
}

// Bridge serves controls independently of the application's business endpoints.
// Use a peer.Listener and its ConnContext on the HTTP server; bare TCP is rejected.
type Bridge struct {
	prefix      string
	policy      *gatewaypolicy.Policy
	application Service
	controls    *backend.Controller
	panel       runtimepanel.Handler
	grants      map[string]string
	reportError func(*http.Request, error)
}

// Status reads the authoritative state selected by the configured control mode.
// A plain relay has no control adapter and returns ErrAction here.
// Direct callers must use an already authenticated and authorized identity.
func (b *Bridge) Status(ctx context.Context, id identity.Request) (backend.Snapshot, error) {
	if b.panel.Execute == nil {
		return backend.Snapshot{}, backend.ErrAction
	}
	return b.panel.Execute(ctx, id, backend.Command{Action: "status"})
}

func New(config Config, application Application) (*Bridge, error) {
	bridge, err := newBridge(config, application)
	if err != nil {
		return nil, err
	}
	bridge.controls = backend.New(application)
	bridge.panel = runtimepanel.Handler{Prefix: config.Gateway.Prefix, Name: config.Name, Scope: config.ControlScope,
		Execute: bridge.controls.Execute, ExecuteCommand: config.ExecuteCommand,
		Actions:  append([]runtimepanel.Action(nil), config.Actions...),
		Commands: append([]runtimepanel.Command(nil), config.Commands...)}
	return bridge, nil
}

// NewRelay forwards pages, API, streams and /_runtime controls to a local service.
// The local service owns its panel, processes, updates, configuration and data.
// Closing a relay never stops that service or its business processes.
func NewRelay(config Config, service Service) (*Bridge, error) {
	if len(config.Actions) != 0 || len(config.Commands) != 0 || config.ExecuteCommand != nil || config.ControlScope != "" {
		return nil, errors.New("relay controls and commands must be provided by the local service")
	}
	return newBridge(config, service)
}

// NewControlledRelay provides the FPK panel while delegating all real commands
// and state to an independent local management service. It owns no business process.
func NewControlledRelay(config Config, service ControlledService) (*Bridge, error) {
	if config.ExecuteCommand != nil {
		return nil, errors.New("controlled relay commands must use the local control adapter")
	}
	bridge, err := newBridge(config, service)
	if err != nil {
		return nil, err
	}
	bridge.panel = runtimepanel.Handler{Prefix: config.Gateway.Prefix, Name: config.Name, Scope: config.ControlScope,
		Execute: service.Control, Actions: append([]runtimepanel.Action(nil), config.Actions...),
		Commands:       append([]runtimepanel.Command(nil), config.Commands...),
		CommandHandler: service.Control}
	return bridge, nil
}

func newBridge(config Config, application Service) (*Bridge, error) {
	if application == nil || strings.TrimSpace(config.Name) == "" || !runtimepanel.ValidPrefix(config.Gateway.Prefix) || !config.ControlScope.Valid() {
		return nil, errors.New("web bridge requires an application, display name and safe gateway prefix")
	}
	policy, err := gatewaypolicy.New(config.Gateway)
	if err != nil {
		return nil, err
	}
	grants := make(map[string]string)
	for _, target := range config.WebSocketPaths {
		if target == "/" || !strings.HasPrefix(target, "/") || path.Clean(target) != target || strings.ContainsAny(target, "?#%\\\x00") || reserved(target) {
			return nil, errors.New("websocket paths must be canonical business paths relative to the application prefix")
		}
		key := "/_gateway/grant" + target
		if _, exists := grants[key]; exists || config.Gateway.HandshakeGrants == nil {
			return nil, errors.New("websocket paths require unique paths and explicit gateway handshake grant configuration")
		}
		grants[key] = config.Gateway.Prefix + target
	}
	if len(config.WebSocketPaths) == 1 {
		grants["/_gateway/grant"] = config.Gateway.Prefix + config.WebSocketPaths[0]
	}
	return &Bridge{
		prefix: config.Gateway.Prefix, policy: policy, application: application,
		grants: grants, reportError: config.ReportError,
	}, nil
}

func runtimePath(relative string) bool {
	return relative == "/_runtime" || strings.HasPrefix(relative, "/_runtime/")
}

func reserved(relative string) bool {
	return runtimePath(relative) ||
		relative == "/_gateway" || strings.HasPrefix(relative, "/_gateway/")
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, rejection := b.policy.Check(r)
	if rejection != nil {
		gatewaypolicy.WriteRejection(w, r, rejection)
		return
	}
	if err := b.application.Authorize(r, id); err != nil {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "application access denied", http.StatusForbidden)
		return
	}
	if b.panel.Execute != nil && b.panel.Handle(w, r, id) {
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, b.prefix)
	if target, ok := b.grants[relative]; ok {
		grant, rejection := b.policy.IssueGrant(r, target)
		if rejection != nil {
			gatewaypolicy.WriteRejection(w, r, rejection)
		} else {
			gatewaypolicy.WriteGrant(w, grant)
		}
		return
	}
	if reserved(relative) && (b.panel.Execute != nil || !runtimePath(relative)) {
		http.NotFound(w, r)
		return
	}
	if relative == "" {
		target := *r.URL
		target.Path = b.prefix + "/"
		http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
		return
	}
	if b.controls == nil && b.panel.Execute != nil && document(r, b.prefix) {
		state, err := b.Status(r.Context(), id)
		switch {
		case err != nil || state.State == "unavailable" || state.State == "starting" || state.State == "stopping" || state.Paused && state.State == "running":
			b.panel.Recovery(w, r)
			return
		case state.State == "paused" || state.State == "stopped":
			b.panel.Unavailable(w, r)
			return
		}
	}
	var endpoint *Endpoint
	acquire := func() error {
		var err error
		endpoint, err = b.application.Acquire(r.Context(), id, relative)
		if err == nil && (endpoint == nil || endpoint.origin == nil || endpoint.transport == nil) {
			return errors.New("application returned no web endpoint")
		}
		return err
	}
	var err error
	if b.controls == nil {
		err = acquire()
	} else {
		err = b.controls.Use(r.Context(), id, acquire)
	}
	if b.controls != nil && errors.Is(err, backend.ErrPaused) {
		b.panel.Unavailable(w, r)
		return
	}
	if err != nil {
		b.failure(w, r, err, http.StatusServiceUnavailable)
		return
	}
	if !endpoint.validCookies(r) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "ambiguous application cookie", http.StatusForbidden)
		return
	}
	b.proxy(endpoint, id).ServeHTTP(w, r)
}

func document(r *http.Request, prefix string) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		r.Header.Get("Upgrade") == "" &&
		(r.URL.Path == prefix+"/" || strings.Contains(r.Header.Get("Accept"), "text/html"))
}

func (b *Bridge) failure(w http.ResponseWriter, r *http.Request, err error, status int) {
	if b.reportError != nil {
		b.reportError(r, err)
	}
	if b.panel.Execute != nil && document(r, b.prefix) {
		b.panel.Recovery(w, r)
		return
	}
	code := "runtime_unavailable"
	if status == http.StatusBadGateway {
		code = "proxy_failure"
	}
	gatewaypolicy.WriteFailure(w, r, &gatewaypolicy.Rejection{Code: code}, status)
}
