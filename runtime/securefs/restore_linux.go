//go:build linux && (amd64 || arm64)

package securefs

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"syscall"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

// RestoreOptions bounds an offline repair and excludes service identities.
// Both budgets must be positive; their maximum values are 1,000,000 and 256.
type RestoreOptions struct {
	ExcludedUIDs []uint32
	MaxEntries   int
	MaxDepth     int
}

// RestoreReport describes completed changes and entries deliberately left alone.
// A failure may leave completed descendants restored; retrying skips their user-owned trees.
type RestoreReport struct {
	UsersRestored   int
	EntriesRestored int
	UserOwnedTrees  int
	SymlinksSkipped int
	IgnoredEntries  int
}

// RestoreUserState restores root-owned persistent UID trees to their current NSS owner.
// The caller must stop every application writer and hold its lifecycle lock. This
// function requires root, fully preflights before changing owners, preserves modes
// and bytes, and never enters a user-owned directory or crosses a mount point.
// It cannot prove that a historical UID has not been reassigned to another account.
func RestoreUserState(parent string, options RestoreOptions) (RestoreReport, error) {
	var report RestoreReport
	if os.Geteuid() != 0 {
		return report, errors.New("state restoration requires root")
	}
	if options.MaxEntries < 1 || options.MaxEntries > 1000000 || options.MaxDepth < 1 || options.MaxDepth > 256 {
		return report, errors.New("state restoration requires entry budget 1..1000000 and depth budget 1..256")
	}
	resolved, err := RootPath(parent)
	if err != nil {
		return report, fmt.Errorf("state parent: %w", err)
	}
	fd, err := syscall.Open(resolved, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return report, err
	}
	defer syscall.Close(fd)
	var parentStat syscall.Stat_t
	if err := syscall.Fstat(fd, &parentStat); err != nil {
		return report, err
	}
	if parentStat.Uid != 0 || parentStat.Mode&0022 != 0 {
		return report, errors.New("state parent must be a non-writable root-owned directory")
	}
	walker := restoreWalker{fd: fd, options: options, privilegedOwner: 0}
	names, err := readNames(fd, ".", options.MaxEntries)
	if err != nil {
		return report, err
	}
	for _, name := range names {
		uid, valid := stateUID(name)
		if !valid || slices.Contains(options.ExcludedUIDs, uid) {
			walker.report.IgnoredEntries++
			walker.seen++
			if walker.seen > options.MaxEntries {
				return walker.report, errors.New("state restoration entry budget exceeded")
			}
			continue
		}
		user, err := identity.ResolveUID(uid)
		if err != nil {
			return walker.report, fmt.Errorf("state user %s: %w", name, err)
		}
		if err := walker.visit(name, user, 1); err != nil {
			return walker.report, err
		}
		walker.users = append(walker.users, user)
	}
	// Check every account again before the first mutation, not just before its own tree.
	for _, user := range walker.users {
		current, err := identity.ResolveUID(user.UID)
		if err != nil || current.Name != user.Name || current.GID != user.GID || current.Home != user.Home || !slices.Equal(current.Groups, user.Groups) {
			return walker.report, fmt.Errorf("NSS identity changed for UID %d", user.UID)
		}
	}
	if err := walker.apply(); err != nil {
		return walker.report, err
	}
	return walker.report, nil
}

func stateUID(name string) (uint32, bool) {
	value, err := strconv.ParseUint(name, 10, 32)
	uid := uint32(value)
	return uid, err == nil && uid >= 1000 && uid != 65534 && uid != 4294967295 && strconv.FormatUint(value, 10) == name
}
