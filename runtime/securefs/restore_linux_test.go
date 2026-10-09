//go:build linux && (amd64 || arm64)

package securefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func stateFixture(t *testing.T) (string, *restoreWalker, identity.User) {
	t.Helper()
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "1000"), 0700); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Open(parent, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	owner := uint32(os.Geteuid())
	walker := &restoreWalker{fd: fd, privilegedOwner: owner, options: RestoreOptions{MaxEntries: 100, MaxDepth: 10}}
	return parent, walker, identity.User{UID: owner + 100000, GID: uint32(os.Getegid()), Name: "fixture"}
}

func putState(t *testing.T, parent, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(parent, "1000", name), []byte("preserve these bytes\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRestorePreflightFilesystemBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, string, *restoreWalker, identity.User)
		fail  bool
	}{
		{"regular", func(t *testing.T, p string, w *restoreWalker, u identity.User) { putState(t, p, "data") }, false},
		{"hardlink", func(t *testing.T, p string, w *restoreWalker, u identity.User) {
			putState(t, p, "data")
			if err := os.Link(filepath.Join(p, "1000", "data"), filepath.Join(p, "outside")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"fifo", func(t *testing.T, p string, w *restoreWalker, u identity.User) {
			if err := syscall.Mkfifo(filepath.Join(p, "1000", "pipe"), 0600); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"foreign_owner", func(t *testing.T, p string, w *restoreWalker, u identity.User) { w.privilegedOwner++ }, true},
		{"public_UID_directory", func(t *testing.T, p string, w *restoreWalker, u identity.User) {
			if err := os.Chmod(filepath.Join(p, "1000"), 0755); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"writable_child_directory", func(t *testing.T, p string, w *restoreWalker, u identity.User) {
			name := filepath.Join(p, "1000", "child")
			if err := os.Mkdir(name, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(name, 0777); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"special_permissions", func(t *testing.T, p string, w *restoreWalker, u identity.User) {
			putState(t, p, "data")
			if err := os.Chmod(filepath.Join(p, "1000", "data"), 0600|os.ModeSetuid); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"entry_budget", func(t *testing.T, p string, w *restoreWalker, u identity.User) {
			putState(t, p, "data")
			w.options.MaxEntries = 1
		}, true},
		{"depth_budget", func(t *testing.T, p string, w *restoreWalker, u identity.User) {
			putState(t, p, "data")
			w.options.MaxDepth = 1
		}, true},
		{"symlink", func(t *testing.T, p string, w *restoreWalker, u identity.User) {
			if err := os.Symlink("/etc/passwd", filepath.Join(p, "1000", "link")); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent, walker, user := stateFixture(t)
			tc.setup(t, parent, walker, user)
			err := walker.visit("1000", user, 1)
			if (err != nil) != tc.fail {
				t.Fatalf("preflight error=%v, want failure=%v", err, tc.fail)
			}
			if !tc.fail && walker.entries[len(walker.entries)-1].name != "1000" {
				t.Fatal("UID directory must be last")
			}
			if tc.name == "symlink" && (walker.report.SymlinksSkipped != 1 || len(walker.entries) != 1) {
				t.Fatal("symlink included in mutation plan")
			}
		})
	}
}

func TestRestoreSkipsUserOwnedSubtree(t *testing.T) {
	parent, walker, user := stateFixture(t)
	// An invalid descendant proves the walker never enters the already-owned tree.
	if err := syscall.Mkfifo(filepath.Join(parent, "1000", "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	user.UID = uint32(os.Geteuid())
	if err := walker.visit("1000", user, 1); err != nil {
		t.Fatal(err)
	}
	if len(walker.entries) != 0 || walker.report.UserOwnedTrees != 1 || walker.seen != 1 {
		t.Fatalf("user subtree traversed: %+v", walker)
	}
}

func TestRestoreRejectsReplacement(t *testing.T) {
	parent, walker, user := stateFixture(t)
	putState(t, parent, "data")
	if err := walker.visit("1000", user, 1); err != nil {
		t.Fatal(err)
	}
	before := walker.entries[0]
	if err := os.Rename(filepath.Join(parent, "1000", "data"), filepath.Join(parent, "old")); err != nil {
		t.Fatal(err)
	}
	putState(t, parent, "data")
	fd, after, err := inspectBeneath(walker.fd, before.name)
	if err != nil {
		t.Fatal(err)
	}
	syscall.Close(fd)
	if sameEntry(before.stat, after) {
		t.Fatal("replaced inode accepted")
	}
	// apply must stop at the first changed inode, before reaching a chown syscall.
	if err := walker.apply(); err == nil || !strings.Contains(err.Error(), "changed after preflight") || walker.report.EntriesRestored != 0 {
		t.Fatalf("replacement mutated: report=%+v err=%v", walker.report, err)
	}
}

func TestRestoreOpenat2Boundaries(t *testing.T) {
	parent, walker, _ := stateFixture(t)
	if err := os.Symlink("/tmp", filepath.Join(parent, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../", "escape/"} {
		fd, err := openBeneath(walker.fd, name, pathOnly)
		if err == nil {
			syscall.Close(fd)
			t.Fatalf("escape accepted: %s", name)
		}
	}
	root, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(root)
	fd, err := openBeneath(root, "proc", pathOnly)
	if err == nil {
		syscall.Close(fd)
		t.Fatal("proc mount boundary accepted")
	}
	if !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("expected mount rejection, got %v", err)
	}
}

func TestRestoreCanonicalUIDs(t *testing.T) {
	for _, name := range []string{"", "0", "999", "01000", "+1000", "65534", "4294967295", "4294967296", "service.log", "1000/other"} {
		if _, ok := stateUID(name); ok {
			t.Fatalf("accepted %q", name)
		}
	}
	if uid, ok := stateUID("1000"); !ok || uid != 1000 {
		t.Fatal("canonical UID rejected")
	}
}

func TestRestoreRequiresRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("nonroot rejection test")
	}
	if _, err := RestoreUserState(t.TempDir(), RestoreOptions{MaxEntries: 100, MaxDepth: 10}); err == nil {
		t.Fatal("nonroot restoration accepted")
	}
}

func TestRestoreRejectsTopSymlink(t *testing.T) {
	parent, walker, user := stateFixture(t)
	if err := os.Remove(filepath.Join(parent, "1000")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp", filepath.Join(parent, "1000")); err != nil {
		t.Fatal(err)
	}
	if err := walker.visit("1000", user, 1); err == nil {
		t.Fatal("top UID symlink accepted")
	}
}

func TestRestorePostorder(t *testing.T) {
	parent, walker, user := stateFixture(t)
	if err := os.Mkdir(filepath.Join(parent, "1000", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	putState(t, parent, "nested/data")
	if err := walker.visit("1000", user, 1); err != nil {
		t.Fatal(err)
	}
	want := []string{"1000/nested/data", "1000/nested", "1000"}
	if len(walker.entries) != len(want) {
		t.Fatalf("unexpected plan %+v", walker.entries)
	}
	for index, entry := range walker.entries {
		if entry.name != want[index] {
			t.Fatalf("unsafe order: %+v", walker.entries)
		}
	}
}
