//go:build linux && (amd64 || arm64)

package securefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

const pathOnly = 0x200000

// openBeneath rejects symlink ancestors and mount transitions, including bind mounts.
// O_PATH|O_NOFOLLOW permits inspecting a final symlink without following it.
func openBeneath(parent int, path string, flags int) (int, error) {
	name, err := syscall.BytePtrFromString(path)
	if err != nil {
		return -1, err
	}
	how := struct{ Flags, Mode, Resolve uint64 }{
		Flags:   uint64(flags | syscall.O_NOFOLLOW | syscall.O_CLOEXEC),
		Resolve: 0x01 | 0x04 | 0x08,
	}
	fd, _, errno := syscall.Syscall6(437, uintptr(parent), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&how)), unsafe.Sizeof(how), 0, 0)
	if errno != 0 {
		return -1, fmt.Errorf("openat2 %q: %w", path, errno)
	}
	return int(fd), nil
}

func inspectBeneath(parent int, path string) (int, syscall.Stat_t, error) {
	var stat syscall.Stat_t
	fd, err := openBeneath(parent, path, pathOnly)
	if err != nil {
		return -1, stat, err
	}
	if err = syscall.Fstat(fd, &stat); err != nil {
		syscall.Close(fd)
		return -1, stat, err
	}
	return fd, stat, nil
}

func readNames(parent int, path string, remaining int) ([]string, error) {
	fd, err := openBeneath(parent, path, syscall.O_RDONLY|syscall.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	// Read at most one more than the available budget; never allocate an unbounded directory listing.
	names, err := file.Readdirnames(remaining + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > remaining {
		return nil, fmt.Errorf("entry budget exceeded at %q", path)
	}
	return names, nil
}

func sameEntry(before, after syscall.Stat_t) bool {
	return before.Dev == after.Dev && before.Ino == after.Ino && before.Mode == after.Mode &&
		before.Uid == after.Uid && before.Gid == after.Gid && before.Nlink == after.Nlink &&
		before.Size == after.Size && before.Mtim == after.Mtim && before.Ctim == after.Ctim
}
