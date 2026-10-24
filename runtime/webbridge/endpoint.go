package webbridge

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// EndpointConfig describes one independently deployed local service.
// Transport is cloned; its timeouts and TLS settings come from the application's configuration.
// A nil Transport uses Go's default transport settings with environment proxies disabled.
type EndpointConfig struct {
	URL             string
	UnixSocket      string
	StripPrefix     bool
	ForwardIdentity bool
	Authorization   string
	Cookies         []string
	Transport       *http.Transport
}

// Endpoint is an immutable, validated connection description shared across requests.
// Create it after configuration loading or a runtime address change, rather than per request.
type Endpoint struct {
	origin          *url.URL
	transport       *http.Transport
	stripPrefix     bool
	forwardIdentity bool
	authorization   string
	cookies         map[string]bool
}

func NewEndpoint(config EndpointConfig) (*Endpoint, error) {
	origin, err := url.Parse(config.URL)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Opaque != "" ||
		(origin.Scheme != "http" && origin.Scheme != "https") ||
		(origin.Path != "" && origin.Path != "/") || origin.RawPath != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" {
		return nil, errors.New("endpoint URL must be an HTTP(S) origin without credentials, path, query or fragment")
	}
	if config.UnixSocket == "" {
		ip := net.ParseIP(origin.Hostname())
		port, err := strconv.ParseUint(origin.Port(), 10, 16)
		if ip == nil || !ip.IsLoopback() || err != nil || port == 0 {
			return nil, errors.New("TCP endpoint requires a numeric loopback address and explicit port")
		}
	} else if origin.Scheme != "http" || !filepath.IsAbs(config.UnixSocket) ||
		filepath.Clean(config.UnixSocket) != config.UnixSocket || strings.ContainsRune(config.UnixSocket, '\x00') {
		return nil, errors.New("Unix endpoint requires HTTP and an absolute canonical private socket path")
	}
	if strings.ContainsAny(config.Authorization, "\r\n\x00") {
		return nil, errors.New("invalid endpoint authorization header")
	}
	if config.ForwardIdentity && config.UnixSocket == "" && config.Authorization == "" {
		return nil, errors.New("identity forwarding over TCP requires explicit bridge authorization")
	}
	cookies := make(map[string]bool)
	for _, name := range config.Cookies {
		if name == "" || cookies[name] || strings.HasPrefix(name, "__Host-") || (&http.Cookie{Name: name, Value: "test"}).Valid() != nil {
			return nil, errors.New("endpoint cookies require unique valid names scoped to the application path")
		}
		cookies[name] = true
	}
	base := config.Transport
	if base == nil {
		var ok bool
		base, ok = http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, errors.New("an explicit HTTP transport is required when the default transport is replaced")
		}
	}
	transport := base.Clone()
	transport.Proxy = nil
	if config.UnixSocket != "" {
		dial := transport.DialContext
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, "unix", config.UnixSocket)
		}
	}
	return &Endpoint{origin: origin, transport: transport, stripPrefix: config.StripPrefix,
		forwardIdentity: config.ForwardIdentity, authorization: config.Authorization, cookies: cookies}, nil
}

// CloseIdleConnections releases idle upstream connections when the application retires this endpoint.
// Active HTTP streams and WebSockets remain the application's shutdown responsibility.
func (e *Endpoint) CloseIdleConnections() { e.transport.CloseIdleConnections() }

func (e *Endpoint) validCookies(r *http.Request) bool {
	seen := make(map[string]bool)
	for _, cookie := range r.Cookies() {
		if e.cookies[cookie.Name] {
			if seen[cookie.Name] {
				return false
			}
			seen[cookie.Name] = true
		}
	}
	return true
}
