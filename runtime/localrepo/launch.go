package localrepo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/user-no-found/FnPackKit/runtime/backend"
)

const sourceEnvironment = "FNPACKKIT_LOCAL_SOURCE"

// Launch binds a prepared source snapshot to its only working directory.
type Launch struct {
	directory string
	version   backend.SourceVersion
	owner     uint32
}

func (l Launch) Directory() string              { return l.directory }
func (l Launch) Version() backend.SourceVersion { return l.version }

// Command constructs a native launch with frozen source evidence in the child's
// environment. The application owns process.Start, readiness and graceful exit.
func (l Launch) Command(program string, args ...string) (*exec.Cmd, error) {
	if os.Geteuid() == 0 || uint32(os.Geteuid()) != l.owner || l.directory == "" || l.version.Fingerprint == "" || program == "" {
		return nil, errors.New("a prepared nonroot local launch is required")
	}
	if !filepath.IsAbs(program) && filepath.Dir(program) != "." {
		program = filepath.Join(l.directory, program)
	}
	cmd := exec.Command(program, args...)
	cmd.Dir = l.directory
	version, err := json.Marshal(l.version)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(os.Environ(), sourceEnvironment+"="+string(version))
	return cmd, nil
}

// StartupVersion is read once by the actual child at startup and returned over
// its authenticated status channel. It must not be regenerated from live files.
func StartupVersion() (backend.SourceVersion, error) {
	var result backend.SourceVersion
	err := json.Unmarshal([]byte(os.Getenv(sourceEnvironment)), &result)
	if err != nil || result.Commit == "" || result.Fingerprint == "" {
		return result, errors.New("local startup source evidence is missing")
	}
	return result, nil
}

func (m *Manager) prepare(ctx context.Context) (Launch, error) {
	before, err := m.inspect(ctx)
	if err != nil {
		return Launch{}, err
	}
	for _, commands := range [][]Command{m.config.Build, m.config.Verify} {
		for _, command := range commands {
			if _, err := m.command(ctx, command, before); err != nil {
				return Launch{}, err
			}
		}
	}
	after, err := m.inspect(ctx)
	if err != nil {
		return Launch{}, err
	}
	if before != after {
		return Launch{}, ErrChanged
	}
	return Launch{m.config.Directory, after, m.owner.UID}, nil
}
