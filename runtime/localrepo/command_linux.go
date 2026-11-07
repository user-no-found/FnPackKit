package localrepo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/backend"
)

const outputLimit = 8 << 20

type limitedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedOutput) Write(data []byte) (int, error) {
	n := len(data)
	remaining := outputLimit - b.Len()
	if len(data) > remaining {
		data = data[:remaining]
		b.overflow = true
	}
	b.Buffer.Write(data)
	return n, nil
}

func (m *Manager) command(ctx context.Context, command Command, source backend.SourceVersion) ([]byte, error) {
	return m.run(ctx, command, true, &source)
}

func (m *Manager) run(ctx context.Context, command Command, logOutput bool, source *backend.SourceVersion) ([]byte, error) {
	if command.Program == "" {
		return nil, errors.New("empty repository command")
	}
	program := command.Program
	if !filepath.IsAbs(program) && filepath.Dir(program) != "." {
		program = filepath.Join(m.config.Directory, program)
	}
	cmd := exec.CommandContext(ctx, program, command.Args...)
	cmd.Dir = m.config.Directory
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if source != nil {
		encoded, err := json.Marshal(source)
		if err != nil {
			return nil, err
		}
		cmd.Env = append(cmd.Env, sourceEnvironment+"="+string(encoded))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	var output, stderr limitedOutput
	cmd.Stdout, cmd.Stderr = &output, &stderr
	if m.config.Output != nil {
		cmd.Stderr = io.MultiWriter(&stderr, m.config.Output)
		if logOutput {
			cmd.Stdout = io.MultiWriter(&output, m.config.Output)
		}
	}
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	if output.overflow || stderr.overflow {
		return nil, errors.New("repository command output exceeds limit")
	}
	if err != nil {
		return nil, fmt.Errorf("repository command failed: %w", err)
	}
	return output.Bytes(), nil
}

func (m *Manager) git(ctx context.Context, args ...string) ([]byte, error) {
	return m.run(ctx, Command{m.config.Git, append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false"}, args...)}, false, nil)
}
