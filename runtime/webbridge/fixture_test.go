package webbridge

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/peer"
)

const testPrefix = "/app/bridge-test"

type fixtureApplication struct {
	mu                          sync.Mutex
	running                     map[uint32]bool
	endpoint                    *Endpoint
	selectEndpoint              func(string) *Endpoint
	onStart, onStop             func() error
	denied                      bool
	acquireError                error
	starts, stops, acquisitions int
}

func (a *fixtureApplication) Authorize(_ *http.Request, _ identity.Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.denied {
		return errors.New("private authorization detail")
	}
	return nil
}

func (a *fixtureApplication) Running(_ context.Context, id identity.Request) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running[id.UID], nil
}

func (a *fixtureApplication) start(id identity.Request) error {
	if a.running[id.UID] {
		return nil
	}
	if a.onStart != nil {
		if err := a.onStart(); err != nil {
			return err
		}
	}
	a.running[id.UID] = true
	a.starts++
	return nil
}

func (a *fixtureApplication) Start(_ context.Context, id identity.Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.start(id)
}

func (a *fixtureApplication) Stop(_ context.Context, id identity.Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stops++
	if a.onStop != nil {
		if err := a.onStop(); err != nil {
			return err
		}
	}
	a.running[id.UID] = false
	return nil
}

func (a *fixtureApplication) Acquire(_ context.Context, id identity.Request, relative string) (*Endpoint, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.acquisitions++
	if a.acquireError != nil {
		return nil, a.acquireError
	}
	if err := a.start(id); err != nil {
		return nil, err
	}
	if a.selectEndpoint != nil {
		return a.selectEndpoint(relative), nil
	}
	return a.endpoint, nil
}

type fixture struct {
	bridge *Bridge
	app    *fixtureApplication
	client *http.Client
	socket string
}

func newFixture(t *testing.T, endpoint *Endpoint) *fixture {
	t.Helper()
	app := &fixtureApplication{running: make(map[uint32]bool), endpoint: endpoint}
	bridge, err := New(Config{Name: "桥接测试", Gateway: gatewaypolicy.Config{
		Prefix: testPrefix, AllowSameOriginFetch: true,
		HandshakeGrants: &gatewaypolicy.GrantConfig{Lifetime: time.Minute, Capacity: 32, PerUser: 8},
	}, WebSocketPaths: []string{"/ws", "/hmr"}}, app)
	if err != nil {
		t.Fatal(err)
	}
	socket := socketPath(t, "entry.sock")
	base, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := peer.NewListener(base, []uint32{uint32(os.Getuid())}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: bridge, ConnContext: trusted.ConnContext, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(trusted) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		trusted.CloseConnections()
		_ = server.Close()
	})
	return &fixture{bridge, app, &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, socket}
}

func socketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return filepath.Join(dir, name)
}

func (f *fixture) request(method, relative, body string) *http.Request {
	r, _ := http.NewRequest(method, "http://nas.test"+testPrefix+relative, strings.NewReader(body))
	r.Header.Set("X-Trim-Userid", "1000")
	r.Header.Set("X-Trim-Username", "owner")
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Origin", "http://nas.test")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FnPackKit-Control", "1")
	}
	return r
}

func (f *fixture) do(t *testing.T, request *http.Request) (*http.Response, string) {
	t.Helper()
	r, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return r, string(body)
}

func endpointFor(t *testing.T, handler http.HandlerFunc, config EndpointConfig) *Endpoint {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	config.URL = server.URL
	endpoint, err := NewEndpoint(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(endpoint.CloseIdleConnections)
	return endpoint
}

func (f *fixture) userRequest(method, relative, body string, uid uint32) *http.Request {
	r := f.request(method, relative, body)
	r.Header.Set("X-Trim-Userid", strconv.FormatUint(uint64(uid), 10))
	return r
}
