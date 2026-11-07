package backend

import (
	"context"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

// SourceVersion identifies a workspace snapshot, including uncommitted content.
// Fingerprint covers tracked differences and non-ignored untracked files.
type SourceVersion struct {
	Commit      string `json:"commit"`
	Fingerprint string `json:"fingerprint"`
	Dirty       bool   `json:"dirty"`
}

// SourceState contains public version evidence, never paths or Git credentials.
type SourceState struct {
	Workspace   *SourceVersion `json:"workspace,omitempty"`
	Running     *SourceVersion `json:"running,omitempty"`
	Changed     bool           `json:"changed"`
	Unavailable bool           `json:"unavailable,omitempty"`
}

// SourceReporter is optional; lifecycle controls remain usable if inspection fails.
type SourceReporter interface {
	SourceStatus(context.Context, identity.Request) (SourceState, error)
}
