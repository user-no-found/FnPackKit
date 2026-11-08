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
	"strings"
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
		candidate := filepath.Join(m.config.Directory, program)
		relative, err := filepath.Rel(m.config.Directory, candidate)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil, errors.New("repository command must stay inside the workspace")
		}
		program = candidate
	}
	cmd := exec.CommandContext(ctx, program, command.Args...)
	cmd.Dir = m.config.Directory
	cmd.Env = repositoryEnvironment()
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
	arguments := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=" + os.DevNull}, args...)
	return m.run(ctx, Command{m.config.Git, arguments}, false, nil)
}

func repositoryEnvironment() []string {
	blocked := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_COMMON_DIR": true, "GIT_NAMESPACE": true, "GIT_CONFIG": true,
		"GIT_CONFIG_COUNT": true, "GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true,
	}
	environment := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if !blocked[name] {
			environment = append(environment, value)
		}
	}
	return append(environment, "GIT_TERMINAL_PROMPT=0")
}
