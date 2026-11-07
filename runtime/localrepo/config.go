// Package localrepo prepares and runs one owner's local Git workspace without
// copying business code into an FPK installation or acquiring root privileges.
package localrepo

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

var (
	ErrDirty    = errors.New("local workspace has uncommitted changes")
	ErrDiverged = errors.New("workspace cannot fast-forward to the configured remote branch")
	ErrChanged  = errors.New("workspace changed during the operation")
	ErrRuntime  = errors.New("runtime does not match the prepared local workspace")
)

// Command is application-owned argv, executed without an implicit shell.
type Command struct {
	Program string
	Args    []string
}

// Config is supplied by the application's editable configuration loader.
// Build may be empty for interpreted code. Verify must explicitly be provided.
type Config struct {
	Directory string
	Git       string
	Remote    string
	Branch    string
	Build     []Command
	Verify    []Command
	Timeout   time.Duration
	Output    io.Writer // Private application log; never browser output.
}

// RuntimeState must come from the actual process or its authenticated supervisor.
// Version is captured at process startup, not read afresh from the workspace.
type RuntimeState struct {
	Running   bool
	Directory string
	Version   *backend.SourceVersion
}

// Runtime owns real process handles, readiness, graceful shutdown and data.
type Runtime interface {
	State(context.Context) (RuntimeState, error)
	Start(context.Context, Launch) error
	Stop(context.Context) error
}

// Manager serializes this owner's local start, stop, rebuild and remote update.
// Use one manager per workspace; the application owns cross-process exclusivity.
type Manager struct {
	operation chan struct{}
	config    Config
	owner     identity.Request
	runtime   Runtime
}

func New(ctx context.Context, cfg Config, runtime Runtime) (*Manager, error) {
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() {
		return nil, errors.New("local repository operations require the nonroot workspace owner")
	}
	if runtime == nil || cfg.Timeout <= 0 || len(cfg.Verify) == 0 ||
		!filepath.IsAbs(cfg.Git) || !filepath.IsAbs(cfg.Directory) || filepath.Clean(cfg.Directory) != cfg.Directory ||
		strings.ContainsAny(cfg.Directory, "\x00\r\n") || !validRemote(cfg.Remote) || strings.HasPrefix(cfg.Branch, "-") {
		return nil, errors.New("local repository requires an explicit directory, Git, branch, remote, verification and timeout")
	}
	directory, err := filepath.EvalSymlinks(cfg.Directory)
	if err != nil {
		return nil, err
	}
	cfg.Directory = directory
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("workspace must belong to the current nonroot owner")
	}
	user, err := identity.ResolveUID(uint32(os.Geteuid()))
	if err != nil {
		return nil, err
	}
	cfg.Build = copyCommands(cfg.Build)
	cfg.Verify = copyCommands(cfg.Verify)
	for _, commands := range [][]Command{cfg.Build, cfg.Verify} {
		for _, command := range commands {
			if command.Program == "" {
				return nil, errors.New("repository command program must be explicit")
			}
		}
	}
	m := &Manager{operation: make(chan struct{}, 1), config: cfg, owner: identity.Request{UID: user.UID, Name: user.Name}, runtime: runtime}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	root, err := m.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(string(root)) != directory {
		return nil, errors.New("configured source must be the local Git workspace root")
	}
	if _, err := m.git(ctx, "check-ref-format", "refs/heads/"+cfg.Branch); err != nil {
		return nil, err
	}
	return m, nil
}

func validRemote(remote string) bool {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return false
	}
	for _, r := range remote {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func copyCommands(commands []Command) []Command {
	result := make([]Command, len(commands))
	for i, command := range commands {
		result[i] = Command{command.Program, append([]string(nil), command.Args...)}
	}
	return result
}

func (m *Manager) authorize(id identity.Request) error {
	if os.Geteuid() != int(m.owner.UID) || id != m.owner {
		return errors.New("local workspace owner mismatch")
	}
	return nil
}
