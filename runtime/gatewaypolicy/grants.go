package gatewaypolicy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

// GrantConfig bounds outstanding handshake proofs; nil configuration disables them.
type GrantConfig struct {
	Lifetime time.Duration
	Capacity int
	PerUser  int
}

// Grant is a short-lived, single-use subprotocol capability, never a URL parameter.
type Grant struct {
	Protocol         string `json:"protocol"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

// HandshakeProtocol is the nonsecret marker a gateway may select in its 101 response.
const HandshakeProtocol = "fnpackkit.gateway.v1"
const grantPrefix = "fpk-gateway."

type grantBinding struct {
	user           identity.Request
	origin, target string
	expires        time.Time
	created        time.Time
}

type grantStore struct {
	mu      sync.Mutex
	config  GrantConfig
	entries map[[32]byte]grantBinding
}

func (store *grantStore) expire(now time.Time) {
	for key, binding := range store.entries {
		if !now.Before(binding.expires) {
			delete(store.entries, key)
		}
	}
}

// IssueGrant proves that an authenticated caller passed the normal HTTP origin policy.
// The application selects the exact permitted WebSocket path, never a redirect target.
func (policy *Policy) IssueGrant(request *http.Request, target string) (Grant, *Rejection) {
	user, rejection := policy.Check(request)
	if rejection != nil {
		return Grant{}, rejection
	}
	if request.Method != http.MethodPost || request.Header.Get("Origin") == "" {
		return Grant{}, &Rejection{Code: "origin_required"}
	}
	if target == "" || path.Clean(target) != target || strings.ContainsAny(target, "?#%\\\x00") || target != policy.config.Prefix && !strings.HasPrefix(target, policy.config.Prefix+"/") {
		return Grant{}, &Rejection{Code: "invalid_path"}
	}
	store := policy.grants
	if store == nil {
		return Grant{}, &Rejection{Code: "grant_disabled"}
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Grant{}, &Rejection{Code: "grant_unavailable"}
	}
	token := base64.RawURLEncoding.EncodeToString(nonce[:])
	key := sha256.Sum256([]byte(token))
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	store.expire(now)
	count := 0
	var oldest [32]byte
	var oldestTime time.Time
	for candidate, binding := range store.entries {
		if binding.user.UID == user.UID {
			count++
			if oldestTime.IsZero() || binding.created.Before(oldestTime) {
				oldest = candidate
				oldestTime = binding.created
			}
		}
	}
	if count >= store.config.PerUser {
		delete(store.entries, oldest)
	}
	if len(store.entries) >= store.config.Capacity {
		return Grant{}, &Rejection{Code: "grant_capacity"}
	}
	store.entries[key] = grantBinding{user: user, origin: request.Header.Get("Origin"), target: target, created: now, expires: now.Add(store.config.Lifetime)}
	return Grant{Protocol: grantPrefix + token, ExpiresInSeconds: int(store.config.Lifetime / time.Second)}, nil
}

// WriteGrant sends only the bounded handshake capability with no cache or CORS permission.
func WriteGrant(writer http.ResponseWriter, grant Grant) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(writer).Encode(struct {
		Schema int    `json:"schema"`
		Status string `json:"status"`
		Grant
	}{1, "granted", grant})
}

func (policy *Policy) consumeGrant(request *http.Request, user identity.Request, origin string) (bool, *Rejection) {
	var ticket string
	seenTicket := false
	markers := 0
	reserved := false
	var application []string
	for _, value := range request.Header.Values("Sec-WebSocket-Protocol") {
		for _, protocol := range strings.Split(value, ",") {
			protocol = strings.TrimSpace(protocol)
			switch {
			case protocol == HandshakeProtocol:
				markers++
				reserved = true
			case strings.HasPrefix(protocol, grantPrefix):
				reserved = true
				if seenTicket || protocol == grantPrefix {
					return false, &Rejection{Code: "grant_invalid"}
				}
				seenTicket = true
				ticket = strings.TrimPrefix(protocol, grantPrefix)
			default:
				application = append(application, protocol)
			}
		}
	}
	if !reserved {
		return false, nil
	}
	if request.Method != http.MethodGet {
		return false, &Rejection{Code: "grant_invalid"}
	}
	store := policy.grants
	if store == nil {
		return false, &Rejection{Code: "grant_disabled"}
	}
	bytes, err := base64.RawURLEncoding.Strict().DecodeString(ticket)
	if markers != 1 || err != nil || len(ticket) != 43 || len(bytes) != 32 {
		return false, &Rejection{Code: "grant_invalid"}
	}
	key := sha256.Sum256([]byte(ticket))
	store.mu.Lock()
	now := time.Now()
	store.expire(now)
	binding, ok := store.entries[key]
	if !ok || binding.user != user || binding.origin != origin || binding.target != request.URL.Path {
		store.mu.Unlock()
		return false, &Rejection{Code: "grant_invalid"}
	}
	delete(store.entries, key)
	store.mu.Unlock()
	request.Header.Del("Sec-WebSocket-Protocol")
	if len(application) > 0 {
		request.Header.Set("Sec-WebSocket-Protocol", strings.Join(application, ", "))
	}
	markHandshake(request, application)
	return true, nil
}
