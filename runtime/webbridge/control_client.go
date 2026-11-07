package webbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

// ErrControl means the local API did not confirm a valid, successful operation.
var ErrControl = errors.New("local management request failed")

// ControlClient sends typed intent to a local HTTP control API in any language.
// Its Endpoint uses the same validated local transport and credentials as proxying.
type ControlClient struct {
	endpoint *Endpoint
	path     string
	client   *http.Client
}

// NewControlClient requires an explicit management path and operation timeout.
// Authentication and the mapping from NAS identity to privileges remain server-owned.
func NewControlClient(endpoint *Endpoint, controlPath string, timeout time.Duration) (*ControlClient, error) {
	if endpoint == nil || endpoint.origin == nil || endpoint.transport == nil || timeout <= 0 ||
		!strings.HasPrefix(controlPath, "/") || controlPath == "/" || path.Clean(controlPath) != controlPath ||
		strings.ContainsAny(controlPath, "?#%\\\x00\r\n") {
		return nil, errors.New("control client requires a local endpoint, canonical path and timeout")
	}
	return &ControlClient{endpoint: endpoint, path: controlPath, client: &http.Client{
		Transport: endpoint.transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Execute performs one request without command retries. GET reads status; POST
// sends action/confirm only. The local service validates and executes the command.
func (c *ControlClient) Execute(ctx context.Context, id identity.Request, command backend.Command) (backend.Snapshot, error) {
	unavailable := backend.Snapshot{Schema: 1, State: "unavailable"}
	if command.Action == "" || len(command.Action) > 64 {
		return unavailable, backend.ErrAction
	}
	method := http.MethodGet
	var body io.Reader
	if command.Action != "status" {
		method = http.MethodPost
		// Interactive sudo authorization belongs to the FPK's local adapter;
		// never forward it into a business or management HTTP API.
		data, err := json.Marshal(backend.Command{Action: command.Action, Confirm: command.Confirm})
		if err != nil {
			return unavailable, errors.Join(ErrControl, err)
		}
		body = bytes.NewReader(data)
	}
	target := *c.endpoint.origin
	target.Path = c.path
	r, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return unavailable, errors.Join(ErrControl, err)
	}
	if c.endpoint.authorization != "" {
		r.Header.Set("Authorization", c.endpoint.authorization)
	}
	if c.endpoint.forwardIdentity {
		r.Header.Set("X-Trim-Userid", strconv.FormatUint(uint64(id.UID), 10))
		r.Header.Set("X-Trim-Username", id.Name)
	}
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FnPackKit-Control", "1")
		r.Header.Set("Origin", c.endpoint.origin.Scheme+"://"+c.endpoint.origin.Host)
	}
	response, err := c.client.Do(r)
	if err != nil {
		return unavailable, errors.Join(ErrControl, backend.ErrControlUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return unavailable, errors.Join(ErrControl, backend.ErrControlAuthentication)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return unavailable, errors.Join(ErrControl, backend.ErrControlResponse)
	}
	var result struct {
		backend.Snapshot
		Failed  bool             `json:"failed"`
		Problem *backend.Problem `json:"problem"`
	}
	if json.Unmarshal(data, &result) != nil || result.Schema != 1 || !controlState(result.State) {
		return unavailable, errors.Join(ErrControl, backend.ErrControlResponse)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Failed || result.State == "unavailable" {
		if result.Problem != nil && result.Problem.Code == "update_not_configured" {
			return result.Snapshot, errors.Join(ErrControl, backend.ErrUpdateNotConfigured)
		}
		return result.Snapshot, ErrControl
	}
	if (command.Action == "start" || command.Action == "restart") && (result.State != "running" || result.Paused) {
		return result.Snapshot, ErrControl
	}
	if command.Action == "stop" && result.State != "paused" && result.State != "stopped" {
		return result.Snapshot, ErrControl
	}
	return result.Snapshot, nil
}

func controlState(state string) bool {
	switch state {
	case "running", "stopped", "paused", "starting", "stopping", "unavailable":
		return true
	default:
		return false
	}
}
