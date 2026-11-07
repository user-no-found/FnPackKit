package localrepo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFailedRemoteBuildLeavesUpdatedWorkspaceStopped(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	ctx := context.Background()
	if err := m.Start(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	target := f.advance(t, "requires failing build")
	m.config.Verify = []Command{{Program: "/bin/false"}}
	if err := m.Update(ctx, m.owner); err == nil {
		t.Fatal("failed verification reported update success")
	}
	if running, _ := m.Running(ctx, m.owner); running {
		t.Fatal("failed update ran an unverified candidate")
	}
	if gitTest(t, f.directory, "rev-parse", "HEAD") != target {
		t.Fatal("failure silently reset source")
	}
	if f.runtime.starts != 1 {
		t.Fatal("failure restarted the old process")
	}
}

func TestWorkspacePathReplacementIsRejected(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	old := f.directory + "-original"
	if err := os.Rename(f.directory, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.seed, f.directory); err != nil {
		t.Fatal(err)
	}
	if err := m.Update(context.Background(), m.owner); err == nil {
		t.Fatal("update followed replaced workspace path")
	}
	if gitTest(t, f.seed, "rev-parse", "HEAD") != gitTest(t, old, "rev-parse", "HEAD") {
		t.Fatal("replacement modified unrelated repository")
	}
}

func TestUntrackedContentAndBuildEvidenceAreBoundToLaunch(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	ctx := context.Background()
	path := filepath.Join(f.directory, "local-file.txt")
	writeTest(t, path, "first untracked value")
	first, err := m.SourceStatus(ctx, m.owner)
	if err != nil {
		t.Fatal(err)
	}
	writeTest(t, path, "different untracked value")
	second, err := m.SourceStatus(ctx, m.owner)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Workspace.Dirty || first.Workspace.Fingerprint == second.Workspace.Fingerprint {
		t.Fatal("untracked content not fingerprinted")
	}
	m.config.Build = []Command{{Program: "/bin/sh", Args: []string{"-c", `printf '%s' "$FNPACKKIT_LOCAL_SOURCE" > .build-cwd`}}}
	if err := m.Start(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filepath.Join(f.directory, ".build-cwd"))
	if err != nil {
		t.Fatal(err)
	}
	var built map[string]any
	if err := json.Unmarshal(encoded, &built); err != nil {
		t.Fatal(err)
	}
	state, err := f.runtime.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if built["fingerprint"] != state.Version.Fingerprint {
		t.Fatal("build and actual startup used different source evidence")
	}
}

func TestCancellingVerificationTerminatesItsCommands(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	m.config.Verify = []Command{{Program: "/bin/sh", Args: []string{"-c", "sleep 30"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := m.Start(ctx, m.owner)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatal("verification cancellation was not bounded", err)
	}
	if f.runtime.starts != 0 {
		t.Fatal("cancelled verification launched runtime")
	}
}

func TestUnknownUserAndUnpreparedLaunchCannotExecute(t *testing.T) {
	if _, err := (Launch{}).Command("/bin/true"); err == nil {
		t.Fatal("unprepared launch accepted")
	}
	f := newRepoFixture(t)
	cfg := f.manager.config
	cfg.Directory = filepath.Join(f.directory, "nested")
	if err := os.Mkdir(cfg.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), cfg, f.runtime); err == nil {
		t.Fatal("nested directory accepted as source root")
	}
}

func TestSubmoduleCannotMasqueradeAsFullyInspectedSource(t *testing.T) {
	f := newRepoFixture(t)
	head := gitTest(t, f.directory, "rev-parse", "HEAD")
	gitTest(t, f.directory, "update-index", "--add", "--cacheinfo", "160000,"+head+",dependency")
	if _, err := f.manager.SourceStatus(context.Background(), f.manager.owner); err == nil {
		t.Fatal("submodule source was reported without inspecting its dependency workflow")
	}
}
