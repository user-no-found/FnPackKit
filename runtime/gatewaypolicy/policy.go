// Package gatewaypolicy validates browser request boundaries behind authenticated fnOS Unix peers.
package gatewaypolicy

import (
	"errors"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/auth"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/peer"
)

// Config is supplied by the application's editable runtime configuration.
// No external origin, platform port, prefix or same-origin opt-in is implicit.
type Config struct {
	Prefix               string
	AllowedHosts         []string
	ExternalOriginPorts  map[string][]uint16
	AllowedOrigins       []string
	AllowSameOriginFetch bool
	HandshakeGrants      *GrantConfig
}

// Policy owns a validated, immutable copy of the request policy.
type Policy struct {
	config Config
	grants *grantStore
}

// Rejection contains a fixed public reason, never request data or credentials.
type Rejection struct{ Code string }

func (rejection *Rejection) Error() string { return "gateway request rejected: " + rejection.Code }

// New checks configuration before any application request can use it.
func New(config Config) (*Policy, error) {
	if !strings.HasPrefix(config.Prefix, "/") || config.Prefix == "/" || path.Clean(config.Prefix) != config.Prefix || strings.ContainsAny(config.Prefix, "?#%\\\x00") {
		return nil, errors.New("gateway prefix must be a clean absolute path")
	}
	hosts := make([]string, 0, len(config.AllowedHosts))
	for _, host := range config.AllowedHosts {
		if !validHost(host) {
			return nil, errors.New("allowed_hosts contains an invalid authority")
		}
		hosts = append(hosts, strings.ToLower(host))
	}
	origins := append([]string(nil), config.AllowedOrigins...)
	for _, origin := range origins {
		if _, ok := parseOrigin(origin); !ok || strings.Contains(origin, "*") {
			return nil, errors.New("allowed_origins requires complete HTTP(S) origins without wildcards")
		}
	}
	ports := make(map[string][]uint16, len(config.ExternalOriginPorts))
	for scheme, values := range config.ExternalOriginPorts {
		if scheme != "http" && scheme != "https" {
			return nil, errors.New("external_origin_ports scheme must be http or https")
		}
		seen := make(map[uint16]bool)
		for _, port := range values {
			if port == 0 || seen[port] {
				return nil, errors.New("external_origin_ports requires unique nonzero ports")
			}
			seen[port] = true
		}
		ports[scheme] = append([]uint16(nil), values...)
	}
	config.AllowedHosts = hosts
	config.AllowedOrigins = origins
	config.ExternalOriginPorts = ports
	policy := &Policy{config: config}
	if config.HandshakeGrants != nil {
		grant := *config.HandshakeGrants
		if grant.Lifetime < time.Second || grant.Lifetime > 300*time.Second || grant.Lifetime%time.Second != 0 || grant.Capacity < 1 || grant.Capacity > 8192 || grant.PerUser < 1 || grant.PerUser > grant.Capacity {
			return nil, errors.New("handshake grants require lifetime 1..300 seconds and capacity 1..8192 with per-user quota")
		}
		policy.config.HandshakeGrants = &grant
		policy.grants = &grantStore{config: grant, entries: make(map[[32]byte]grantBinding)}
	}
	return policy, nil
}

func reject(code string) (identity.Request, *Rejection) {
	return identity.Request{}, &Rejection{Code: code}
}

// Check authenticates the transport and NAS header shape before using browser metadata.
// The application must still authorize the identity and recheck NSS before launching work.
func (policy *Policy) Check(request *http.Request) (identity.Request, *Rejection) {
	if _, ok := peer.TrustedUID(request.Context()); !ok {
		return reject("untrusted_peer")
	}
	user, err := auth.Headers(request.Header)
	if err != nil {
		return reject("invalid_identity")
	}
	cfg := policy.config
	if !validHost(request.Host) || len(cfg.AllowedHosts) > 0 && !slices.Contains(cfg.AllowedHosts, strings.ToLower(request.Host)) {
		return reject("invalid_host")
	}
	if request.URL.RawPath != "" || path.Clean(request.URL.Path) != strings.TrimSuffix(request.URL.Path, "/") || strings.ContainsAny(request.URL.Path, "\\\x00") || request.URL.Path != cfg.Prefix && !strings.HasPrefix(request.URL.Path, cfg.Prefix+"/") {
		return reject("invalid_path")
	}
	origins := request.Header.Values("Origin")
	if len(origins) > 1 {
		return reject("duplicate_origin")
	}
	rawOrigin := ""
	if len(origins) == 1 {
		rawOrigin = origins[0]
		if _, ok := parseOrigin(rawOrigin); !ok {
			return reject("invalid_origin")
		}
	}
	for _, name := range []string{"Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
		values := request.Header.Values(name)
		if len(values) > 1 || len(values) == 1 && (len(values[0]) > 64 || strings.ContainsAny(values[0], ",\r\n\x00")) {
			return reject("invalid_fetch_metadata")
		}
	}
	sites := request.Header.Values("Sec-Fetch-Site")
	if len(sites) > 1 || len(sites) == 1 && (sites[0] == "" || strings.ContainsAny(sites[0], ",\r\n\x00")) {
		return reject("invalid_fetch_metadata")
	}
	site := ""
	if len(sites) == 1 {
		site = sites[0]
	}
	switch site {
	case "same-site", "cross-site":
		return reject("cross_site")
	case "", "same-origin", "none":
	default:
		return reject("invalid_fetch_metadata")
	}
	websocket := false
	for _, header := range request.Header.Values("Upgrade") {
		for _, item := range strings.Split(header, ",") {
			if strings.EqualFold(strings.TrimSpace(item), "websocket") {
				websocket = true
			}
		}
	}
	if !websocket {
		for _, header := range request.Header.Values("Sec-WebSocket-Protocol") {
			for _, protocol := range strings.Split(header, ",") {
				protocol = strings.TrimSpace(protocol)
				if protocol == HandshakeProtocol || strings.HasPrefix(protocol, grantPrefix) {
					return reject("grant_invalid")
				}
			}
		}
	}
	safe := request.Method == http.MethodGet || request.Method == http.MethodHead
	if site == "none" && (rawOrigin != "" || websocket || !safe || token(request.Header, "Sec-Fetch-Mode", "navigate") != "navigate") {
		return reject("invalid_fetch_metadata")
	}
	if rawOrigin == "" {
		if websocket || !safe {
			return reject("origin_required")
		}
		return user, nil
	}
	if websocket {
		granted, rejection := policy.consumeGrant(request, user, rawOrigin)
		if rejection != nil {
			return identity.Request{}, rejection
		}
		if granted {
			return user, nil
		}
	}
	origin, _ := parseOrigin(rawOrigin)
	if cfg.AllowSameOriginFetch && site == "same-origin" {
		return user, nil
	}
	if strictOrigin(origin, request.Host, cfg.ExternalOriginPorts) || slices.Contains(cfg.AllowedOrigins, rawOrigin) {
		return user, nil
	}
	return reject("origin_mismatch")
}
