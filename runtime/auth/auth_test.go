package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestHeaderBoundary(t *testing.T) {
	for _, uid := range []string{"0", "01000", "1000,1001", "65534", "4294967295"} {
		if _, err := Headers(http.Header{"X-Trim-Userid": {uid}, "X-Trim-Username": {"alice"}}); err == nil {
			t.Fatalf("accepted %q", uid)
		}
	}
	if _, err := Headers(http.Header{"X-Trim-Userid": {"1000", "1001"}, "X-Trim-Username": {"alice"}}); err == nil {
		t.Fatal("accepted duplicate identity")
	}
	if _, err := Headers(http.Header{"X-Trim-Userid": {"1000"}, "X-Trim-Username": {"alice"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(context.Background(), identity.Request{UID: 1000, Name: "alice"}, nil); err == nil {
		t.Fatal("missing authorization accepted")
	}
	if err := ValidateMode("oidc"); err == nil {
		t.Fatal("unsupported mode accepted")
	}
}
