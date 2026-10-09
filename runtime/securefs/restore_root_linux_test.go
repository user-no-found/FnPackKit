//go:build linux && (amd64 || arm64)

package securefs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

// This regression mutates only its isolated temporary tree and requires explicit opt-in.
// FNPACKKIT_ROOT_RESTORE_UID selects an existing NAS account; no accounts are created.
func TestRestoreRootIsolatedTree(t *testing.T) {
	user := rootRestoreUser(t)
	value := strconv.FormatUint(uint64(user.UID), 10)
	var err error
	parent := t.TempDir()
	tree := filepath.Join(parent, value)
	child := filepath.Join(tree, "root-child")
	owned := filepath.Join(tree, "user-child")
	for _, name := range []string{tree, child, owned} {
		if err := os.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte("persistent bytes\x00\xff\n")
	regular := filepath.Join(child, "payload")
	untouched := filepath.Join(owned, "root-file")
	outside := filepath.Join(parent, "service.log")
	for _, name := range []string{regular, untouched, outside} {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(owned, int(user.UID), int(user.GID)); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tree, "external-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(child, "pipe")
	if err := syscall.Mkfifo(blocker, 0600); err != nil {
		t.Fatal(err)
	}
	opts := RestoreOptions{MaxEntries: 100, MaxDepth: 10}
	if _, err := RestoreUserState(parent, opts); err == nil {
		t.Fatal("special node accepted")
	}
	assertStateOwner(t, regular, 0, 0600)
	assertStateOwner(t, tree, 0, 0700)
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	report, err := RestoreUserState(parent, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.UsersRestored != 1 || report.EntriesRestored != 3 || report.UserOwnedTrees != 1 || report.SymlinksSkipped != 1 || report.IgnoredEntries != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	for _, name := range []string{tree, child} {
		assertStateOwner(t, name, user.UID, 0700)
	}
	assertStateOwner(t, regular, user.UID, 0600)
	for _, name := range []string{untouched, outside} {
		assertStateOwner(t, name, 0, 0600)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Fatalf("symlink ownership changed: %v", err)
	}
	got, err := os.ReadFile(regular)
	if err != nil || !bytes.Equal(data, got) {
		t.Fatalf("bytes changed: %v", err)
	}
	again, err := RestoreUserState(parent, opts)
	if err != nil || again.EntriesRestored != 0 || again.UsersRestored != 0 || again.UserOwnedTrees != 1 {
		t.Fatalf("not idempotent: %+v %v", again, err)
	}
	// Unknown accounts fail before touching an otherwise eligible tree.
	unknown := uint32(4294967294)
	if _, err := identity.ResolveUID(unknown); err == nil {
		t.Fatal("test requires unassigned UID 4294967294")
	}
	if err := os.Mkdir(filepath.Join(parent, strconv.FormatUint(uint64(unknown), 10)), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreUserState(parent, opts); err == nil {
		t.Fatal("unresolved NSS identity accepted")
	}
}

func assertStateOwner(t *testing.T, name string, uid uint32, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if info.Sys().(*syscall.Stat_t).Uid != uid || info.Mode().Perm() != mode {
		t.Fatalf("owner/mode changed incorrectly: %s %+v", name, info.Sys())
	}
}

func rootRestoreUser(t *testing.T) identity.User {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("FNPACKKIT_ROOT_RESTORE_TEST") != "1" {
		t.Skip("requires root and FNPACKKIT_ROOT_RESTORE_TEST=1")
	}
	value := os.Getenv("FNPACKKIT_ROOT_RESTORE_UID")
	if value == "" {
		value = "1000"
	}
	uid, valid := stateUID(value)
	if !valid {
		t.Fatal("invalid test UID")
	}
	user, err := identity.ResolveUID(uid)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestRestoreRootPartialRetry(t *testing.T) {
	user := rootRestoreUser(t)
	parent := t.TempDir()
	value := strconv.FormatUint(uint64(user.UID), 10)
	tree := filepath.Join(parent, value)
	if err := os.Mkdir(tree, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(tree, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "data"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fd, err := syscall.Open(parent, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	opts := RestoreOptions{MaxEntries: 100, MaxDepth: 10}
	walker := restoreWalker{fd: fd, options: opts}
	if err := walker.visit(value, user, 1); err != nil {
		t.Fatal(err)
	}
	// Fail after one complete subtree, then retry through the public API.
	changed := filepath.Join(parent, walker.entries[2].name)
	if err := os.Rename(changed, filepath.Join(parent, "saved")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(changed, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := walker.apply(); err == nil || walker.report.EntriesRestored != 2 {
		t.Fatalf("expected partial failure, got %+v %v", walker.report, err)
	}
	report, err := RestoreUserState(parent, opts)
	if err != nil || report.EntriesRestored != 3 || report.UserOwnedTrees != 1 || report.UsersRestored != 1 {
		t.Fatalf("retry failed: %+v %v", report, err)
	}
	for _, name := range []string{"a", "b"} {
		assertStateOwner(t, filepath.Join(tree, name), user.UID, 0700)
		assertStateOwner(t, filepath.Join(tree, name, "data"), user.UID, 0600)
	}
}

func TestRestoreRootSameDeviceBindMount(t *testing.T) {
	user := rootRestoreUser(t)
	parent, walker, _ := stateFixture(t)
	source := filepath.Join(parent, "source")
	target := filepath.Join(parent, "1000", "mounted")
	for _, name := range []string{source, target} {
		if err := os.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mount(source, target, "", syscall.MS_BIND, ""); err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("bind-mount regression requires CAP_SYS_ADMIN")
		}
		t.Fatal(err)
	}
	defer syscall.Unmount(target, 0)
	var sourceStat, targetStat syscall.Stat_t
	if err := syscall.Stat(source, &sourceStat); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat(target, &targetStat); err != nil {
		t.Fatal(err)
	}
	if sourceStat.Dev != targetStat.Dev {
		t.Fatal("fixture is not a same-device mount")
	}
	if err := walker.visit("1000", user, 1); !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("bind mount accepted: %v", err)
	}
}
