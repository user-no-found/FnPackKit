// Package auth defines the boundary between trusted FN identities and application access.
// Callers must use a peer-verified Unix listener before parsing forwarded headers.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

// Authorizer checks current application entitlement using application/platform authority.
// NSS existence alone does not establish entitlement or an enabled FN account.
type Authorizer interface {
	Authorize(context.Context, identity.User) error
}

// AuthorizeFunc adapts an application's explicit authorization policy.
type AuthorizeFunc func(context.Context, identity.User) error

// Authorize invokes the configured policy.
func (f AuthorizeFunc) Authorize(ctx context.Context, user identity.User) error {
	if f == nil {
		return errors.New("application authorization policy is required")
	}
	return f(ctx, user)
}

// Check resolves the current NSS identity and fails closed without an access policy.
func Check(ctx context.Context, request identity.Request, policy Authorizer) (identity.User, error) {
	if policy == nil {
		return identity.User{}, errors.New("application authorization policy is required")
	}
	user, err := identity.Resolve(request)
	if err != nil {
		return identity.User{}, err
	}
	if err = policy.Authorize(ctx, user); err != nil {
		return identity.User{}, err
	}
	return user, nil
}

// Headers parses one canonical identity supplied over an already trusted connection.
// It does not authenticate a request received over arbitrary TCP or prove entitlement.
func Headers(header http.Header) (identity.Request, error) {
	single := func(name string) (string, error) {
		values := header.Values(name)
		if len(values) != 1 || values[0] == "" || strings.ContainsAny(values[0], ",\r\n\x00") {
			return "", errors.New("identity requires exactly one header value")
		}
		return values[0], nil
	}
	text, err := single("X-Trim-Userid")
	if err != nil {
		return identity.Request{}, err
	}
	uid, err := strconv.ParseUint(text, 10, 32)
	if err != nil || uid < 1000 || uid == 65534 || uid == 4294967295 || strconv.FormatUint(uid, 10) != text {
		return identity.Request{}, errors.New("invalid NAS UID")
	}
	name, err := single("X-Trim-Username")
	if err != nil || !identity.ValidName(name) {
		return identity.Request{}, errors.New("invalid NAS username")
	}
	return identity.Request{UID: uint32(uid), Name: name}, nil
}

// ValidateMode accepts only explicit integration contracts; account mapping remains application-owned.
func ValidateMode(mode string) error {
	switch mode {
	case "per-user-process", "trusted-proxy":
		return nil
	}
	return errors.New("unsupported authentication integration mode")
}
