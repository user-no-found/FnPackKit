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

// Application owns entitlement, real per-user processes, and deployment configuration.
// Acquire may start work on demand and selects an endpoint for the validated relative path.
// Running only inspects; Stop drains requests and terminates the actual owned runtime.
type Application interface {
	backend.Target
	Authorize(*http.Request, identity.Request) error
	Acquire(context.Context, identity.Request, string) (*Endpoint, error)
}

// Config is supplied by the application's existing runtime configuration boundary.
// WebSocketPaths are exact relative paths, such as /ws, eligible for handshake grants.
type Config struct {
	Name           string
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
	application Application
	controls    *backend.Controller
	panel       runtimepanel.Handler
	grants      map[string]string
	reportError func(*http.Request, error)
}

func New(config Config, application Application) (*Bridge, error) {
	if application == nil || strings.TrimSpace(config.Name) == "" || !runtimepanel.ValidPrefix(config.Gateway.Prefix) {
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
	controls := backend.New(application)
	return &Bridge{
		prefix: config.Gateway.Prefix, policy: policy, application: application,
		controls: controls, grants: grants, reportError: config.ReportError,
		panel: runtimepanel.Handler{Prefix: config.Gateway.Prefix, Name: config.Name,
			Execute:        controls.Execute,
			ExecuteCommand: config.ExecuteCommand,
			Actions:        append([]runtimepanel.Action(nil), config.Actions...),
			Commands:       append([]runtimepanel.Command(nil), config.Commands...)},
	}, nil
}

func reserved(relative string) bool {
	return relative == "/_runtime" || strings.HasPrefix(relative, "/_runtime/") ||
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
	if b.panel.Handle(w, r, id) {
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
	if reserved(relative) {
		http.NotFound(w, r)
		return
	}
	if relative == "" {
		target := *r.URL
		target.Path = b.prefix + "/"
		http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
		return
	}
	var endpoint *Endpoint
	err := b.controls.Use(r.Context(), id, func() error {
		var err error
		endpoint, err = b.application.Acquire(r.Context(), id, relative)
		if err == nil && (endpoint == nil || endpoint.origin == nil || endpoint.transport == nil) {
			return errors.New("application returned no web endpoint")
		}
		return err
	})
	if errors.Is(err, backend.ErrPaused) {
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
	if document(r, b.prefix) {
		b.panel.Recovery(w, r)
		return
	}
	code := "runtime_unavailable"
	if status == http.StatusBadGateway {
		code = "proxy_failure"
	}
	gatewaypolicy.WriteFailure(w, r, &gatewaypolicy.Rejection{Code: code}, status)
}
