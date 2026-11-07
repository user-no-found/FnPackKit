package localrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	"github.com/user-no-found/FnPackKit/runtime/backend"
)

func (m *Manager) inspect(ctx context.Context) (backend.SourceVersion, error) {
	var version backend.SourceVersion
	directory, err := filepath.EvalSymlinks(m.config.Directory)
	if err != nil || directory != m.config.Directory {
		return version, errors.New("configured workspace path changed")
	}
	root, err := m.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(string(root)) != directory {
		return version, errors.New("configured workspace root changed")
	}
	index, err := m.git(ctx, "ls-files", "--stage", "-z")
	if err != nil {
		return version, err
	}
	for _, entry := range strings.Split(string(index), "\x00") {
		if strings.HasPrefix(entry, "160000 ") {
			return version, errors.New("Git submodules require an application-owned dependency workflow")
		}
	}
	head, err := m.git(ctx, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return version, err
	}
	version.Commit = strings.TrimSpace(string(head))
	status, err := m.git(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return version, err
	}
	version.Dirty = len(status) != 0
	diff, err := m.git(ctx, "diff", "--no-ext-diff", "--no-textconv", "--binary", "HEAD", "--")
	if err != nil {
		return version, err
	}
	untracked, err := m.git(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return version, err
	}
	hash := sha256.New()
	for _, part := range [][]byte{head, index, status, diff, untracked} {
		hash.Write(part)
		hash.Write([]byte{0})
	}
	paths := strings.Split(strings.TrimSuffix(string(untracked), "\x00"), "\x00")
	if len(untracked) == 0 {
		paths = nil
	}
	if len(paths) > 4096 {
		return version, errors.New("untracked source file count exceeds limit")
	}
	for len(paths) > 0 {
		count := min(128, len(paths))
		objects, err := m.git(ctx, append([]string{"hash-object", "--no-filters", "--"}, paths[:count]...)...)
		if err != nil {
			return version, err
		}
		hash.Write(objects)
		paths = paths[count:]
	}
	last, err := m.git(ctx, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return version, err
	}
	if string(last) != string(head) {
		return version, ErrChanged
	}
	version.Fingerprint = hex.EncodeToString(hash.Sum(nil))
	return version, nil
}

func (m *Manager) cleanBranch(ctx context.Context) (backend.SourceVersion, error) {
	version, err := m.inspect(ctx)
	if err != nil {
		return version, err
	}
	if version.Dirty {
		return version, ErrDirty
	}
	branch, err := m.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(branch)) != m.config.Branch {
		return version, errors.New("workspace is not on the configured update branch")
	}
	return version, nil
}
