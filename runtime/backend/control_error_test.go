package backend

import (
	"errors"
	"strings"
	"testing"
)

func TestControlDiagnosticsNeverExposeLocalErrors(t *testing.T) {
	for _, item := range []struct {
		err      error
		code     string
		password bool
	}{
		{ErrConnectionRequired, "connection_required", true},
		{ErrConnectionInvalid, "connection_invalid", false},
		{ErrControlAuthentication, "authentication_failed", true},
		{ErrControlUnavailable, "management_unreachable", false},
		{ErrControlResponse, "management_invalid_response", false},
		{errors.New("PRIVATE_TOKEN_AND_PATH"), "operation_failed", false},
	} {
		problem := DescribeFailure(errors.Join(item.err, errors.New("PRIVATE_TOKEN_AND_PATH")))
		if problem.Code != item.code || problem.PasswordRequired != item.password || strings.Contains(problem.Message, "PRIVATE") {
			t.Fatalf("unsafe or incorrect diagnostic: %+v", problem)
		}
	}
	if DescribeFailure(nil) != nil {
		t.Fatal("successful operation has a failure")
	}
}
