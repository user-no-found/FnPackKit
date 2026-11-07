// Package docker provides optional, scoped Docker lifecycle operations.
// Prefer native applications unless containers solve an actual deployment need.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

type Client struct{ Binary string }

type output struct {
	bytes.Buffer
	limit int
}

func (b *output) Write(value []byte) (int, error) {
	if b.Len()+len(value) > b.limit {
		return 0, errors.New("Docker output exceeds limit")
	}
	return b.Buffer.Write(value)
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	binary := c.Binary
	if binary == "" {
		binary = "/usr/bin/docker"
	}
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary {
		return nil, errors.New("Docker executable must be an absolute canonical path")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = []string{"PATH=/usr/bin:/usr/sbin:/bin:/sbin", "LANG=C"}
	stdout, stderr := &output{limit: 262144}, &output{limit: 16384}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("Docker %s failed: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

// Check verifies the actual daemon and Compose. runtime may be empty.
func (c Client) Check(ctx context.Context, runtime string) error {
	if runtime != "" && !namePattern.MatchString(runtime) {
		return errors.New("invalid Docker runtime name")
	}
	if _, err := c.run(ctx, "compose", "version"); err != nil {
		return fmt.Errorf("Docker Compose is unavailable: %w", err)
	}
	data, err := c.run(ctx, "info", "--format", "{{json .Runtimes}}")
	if err != nil {
		return fmt.Errorf("Docker daemon is unavailable: %w", err)
	}
	var runtimes map[string]json.RawMessage
	if json.Unmarshal(data, &runtimes) != nil || runtimes == nil {
		return errors.New("invalid Docker daemon runtime information")
	}
	if runtime != "" && runtimes[runtime] == nil {
		return fmt.Errorf("Docker runtime %s is unavailable", runtime)
	}
	return nil
}

var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:-]{1,220}@sha256:[a-f0-9]{64}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// EnsureImage uses the local cache first and only pulls the selected digest.
// The caller supplies the pull deadline; there is no automatic engine installation.
func (c Client) EnsureImage(ctx context.Context, image string) error {
	if !imagePattern.MatchString(image) {
		return errors.New("Docker image must be pinned by SHA-256 digest")
	}
	if _, err := c.run(ctx, "image", "inspect", "--format", "{{.Id}}", image); err == nil {
		return nil
	}
	_, err := c.run(ctx, "pull", image)
	return err
}
