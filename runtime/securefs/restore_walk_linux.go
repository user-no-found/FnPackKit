//go:build linux && (amd64 || arm64)

package securefs

import (
	"fmt"
	"path"
	"syscall"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

type restoreEntry struct {
	name string
	stat syscall.Stat_t
	user identity.User
	top  bool
}

type restoreWalker struct {
	fd      int
	options RestoreOptions
	// Production always uses root; tests can preflight their own real filesystem.
	privilegedOwner uint32
	seen            int
	entries         []restoreEntry
	users           []identity.User
	report          RestoreReport
}

func (walker *restoreWalker) visit(name string, user identity.User, depth int) error {
	walker.seen++
	if walker.seen > walker.options.MaxEntries || depth > walker.options.MaxDepth {
		return fmt.Errorf("state restoration budget exceeded at %q", name)
	}
	fd, stat, err := inspectBeneath(walker.fd, name)
	if err != nil {
		return err
	}
	syscall.Close(fd)
	kind := stat.Mode & syscall.S_IFMT
	if depth == 1 && (kind != syscall.S_IFDIR || stat.Mode&07777 != 0700) {
		return fmt.Errorf("UID state directory must have mode 0700: %q", name)
	}
	if kind == syscall.S_IFLNK {
		walker.report.SymlinksSkipped++
		return nil
	}
	if kind != syscall.S_IFDIR && kind != syscall.S_IFREG {
		return fmt.Errorf("special state entry rejected: %q", name)
	}
	if stat.Uid == user.UID {
		if kind == syscall.S_IFDIR {
			walker.report.UserOwnedTrees++
		}
		return nil
	}
	if stat.Uid != walker.privilegedOwner {
		return fmt.Errorf("foreign owner at %q", name)
	}
	if stat.Mode&07000 != 0 {
		return fmt.Errorf("special permission bits at %q", name)
	}
	if kind == syscall.S_IFREG && stat.Nlink != 1 {
		return fmt.Errorf("hard-linked state file at %q", name)
	}
	if kind == syscall.S_IFDIR {
		if stat.Mode&0022 != 0 {
			return fmt.Errorf("writable state directory at %q", name)
		}
		names, err := readNames(walker.fd, name, walker.options.MaxEntries-walker.seen)
		if err != nil {
			return err
		}
		for _, child := range names {
			if err := walker.visit(path.Join(name, child), user, depth+1); err != nil {
				return err
			}
		}
	}
	// Postorder keeps every ancestor root-controlled until its children finish.
	walker.entries = append(walker.entries, restoreEntry{name: name, stat: stat, user: user, top: depth == 1})
	return nil
}

func (walker *restoreWalker) apply() error {
	for _, entry := range walker.entries {
		fd, stat, err := inspectBeneath(walker.fd, entry.name)
		if err != nil {
			return err
		}
		if !sameEntry(entry.stat, stat) {
			syscall.Close(fd)
			return fmt.Errorf("state changed after preflight: %q", entry.name)
		}
		// AT_EMPTY_PATH changes this pinned inode, never a subsequently replaced path.
		err = syscall.Fchownat(fd, "", int(entry.user.UID), int(entry.user.GID), 0x1000)
		syscall.Close(fd)
		if err != nil {
			return fmt.Errorf("restore %q: %w", entry.name, err)
		}
		walker.report.EntriesRestored++
		if entry.top {
			walker.report.UsersRestored++
		}
	}
	return nil
}
