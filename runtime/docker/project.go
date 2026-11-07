package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
)

// Project paths and their parent directories must be controlled by the caller's
// lifecycle identity. They must never come from an HTTP request or container data.
type Project struct {
	Client      Client
	Name        string
	ComposeFile string
	EnvFile     string
}

var projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func (p Project) validate() error {
	if !projectPattern.MatchString(p.Name) {
		return errors.New("invalid Docker Compose project name")
	}
	paths := []string{p.ComposeFile}
	if p.EnvFile != "" {
		paths = append(paths, p.EnvFile)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("Docker project paths must be absolute and canonical")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("unsafe Docker project source: %s", filepath.Base(path))
		}
		if path == p.EnvFile && info.Mode().Perm()&0077 != 0 {
			return errors.New("Docker environment file must be private")
		}
	}
	return nil
}

func (p Project) command(ctx context.Context, args ...string) error {
	if err := p.validate(); err != nil {
		return err
	}
	base := []string{"compose", "--project-name", p.Name}
	if p.EnvFile != "" {
		base = append(base, "--env-file", p.EnvFile)
	}
	base = append(base, "--file", p.ComposeFile)
	_, err := p.Client.run(ctx, append(base, args...)...)
	return err
}

func (p Project) Up(ctx context.Context) error {
	return p.command(ctx, "up", "-d", "--remove-orphans")
}

// Down removes this project's containers/network and always retains volumes.
func (p Project) Down(ctx context.Context, stopSeconds int) error {
	if stopSeconds < 1 || stopSeconds > 120 {
		return errors.New("Docker stop timeout must be 1..120 seconds")
	}
	return p.command(ctx, "down", "--timeout", strconv.Itoa(stopSeconds), "--remove-orphans")
}
