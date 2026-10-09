// Package securefs checks filesystem boundaries before privileged operations.
package securefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// RootPath resolves package symlinks, then rejects mutable nonroot path components.
// A root-owned sticky ancestor such as /tmp is safe for a root-owned child.
func RootPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("root-controlled path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	for current := resolved; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || (info.Mode().Perm()&0022 != 0 && !(info.IsDir() && info.Mode()&os.ModeSticky != 0)) {
			return "", fmt.Errorf("path is not controlled by root: %s", current)
		}
		if current == "/" {
			break
		}
	}
	return resolved, nil
}

// RootDirectory creates a root-owned directory only under a verified parent.
func RootDirectory(path string, mode os.FileMode) (string, error) {
	parent, err := RootPath(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	path = filepath.Join(parent, filepath.Base(path))
	err = os.Mkdir(path, mode)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if err == nil {
		if err := os.Chmod(path, mode); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm() != mode {
		return "", fmt.Errorf("directory must be root-owned with mode %04o: %s", mode, path)
	}
	return path, nil
}

// UserDirectory creates a private user directory without touching user-owned contents.
// Its root-controlled parent prevents a user from replacing the directory entry.
func UserDirectory(parent, name string, uid, gid uint32) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return "", errors.New("user directory name must be one safe component")
	}
	if uid == 0 || uid == 4294967295 || gid == 4294967295 {
		return "", errors.New("user directory requires a nonroot identity")
	}
	resolved, err := RootPath(parent)
	if err != nil {
		return "", err
	}
	path := filepath.Join(resolved, name)
	err = os.Mkdir(path, 0700)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	created := err == nil
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer syscall.Close(fd)
	if created {
		if err := syscall.Fchown(fd, int(uid), int(gid)); err != nil {
			return "", err
		}
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return "", err
	}
	if stat.Uid != uid || stat.Mode&0777 != 0700 {
		return "", fmt.Errorf("unsafe private directory: %s", path)
	}
	if stat.Gid != gid {
		if err := syscall.Fchown(fd, -1, int(gid)); err != nil {
			return "", err
		}
	}
	return path, nil
}
