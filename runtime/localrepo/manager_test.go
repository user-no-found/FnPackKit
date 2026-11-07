package localrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestLocalWorkspaceIsTheBuildAndProcessDirectory(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	ctx := context.Background()
	if err := m.Start(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	state, err := f.runtime.State(ctx)
	if err != nil || !state.Running || state.Directory != f.directory || state.Version.Dirty {
		t.Fatal("runtime did not use prepared repository", err)
	}
	cwd, err := os.ReadFile(filepath.Join(f.directory, ".build-cwd"))
	if err != nil || strings.TrimSpace(string(cwd)) != f.directory {
		t.Fatal("build ran outside repository", err)
	}
	writeTest(t, filepath.Join(f.directory, "message.txt"), "local change")
	source, err := m.SourceStatus(ctx, m.owner)
	if err != nil || !source.Changed || !source.Workspace.Dirty || source.Running.Dirty {
		t.Fatal("local edits were confused with running code", err)
	}
	if f.runtime.content != "first" {
		t.Fatal("running process read current workspace instead of startup content")
	}
	if err := m.Rebuild(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	source, err = m.SourceStatus(ctx, m.owner)
	if err != nil || source.Changed || !source.Running.Dirty || f.runtime.content != "local change" {
		t.Fatal("local rebuild did not run edited source", err)
	}
	if f.runtime.starts != 2 || f.runtime.stops != 1 {
		t.Fatal("local rebuild did not restart actual process")
	}
}

func TestRemoteUpdateFastForwardsTheSameWorkspace(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	ctx := context.Background()
	writeTest(t, filepath.Join(f.root, "data-sentinel"), "private data")
	if err := m.Start(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	target := f.advance(t, "remote second")
	if err := m.Update(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	source, err := m.SourceStatus(ctx, m.owner)
	if err != nil || source.Changed || source.Workspace.Commit != target || source.Running.Commit != target || f.runtime.content != "remote second" {
		t.Fatal("updated source differs from running process", err)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "data-sentinel")); string(data) != "private data" {
		t.Fatal("update touched application data")
	}
	if err := m.Stop(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	f.advance(t, "remote third")
	if err := m.Update(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	if running, _ := m.Running(ctx, m.owner); running {
		t.Fatal("update woke a stopped service")
	}
	if f.runtime.starts != 2 {
		t.Fatal("stopped update started a process")
	}
}

func TestUpdatePreservesUncommittedAndDivergentWork(t *testing.T) {
	for _, kind := range []string{"tracked", "untracked", "divergent", "wrong-branch"} {
		t.Run(kind, func(t *testing.T) {
			f := newRepoFixture(t)
			m := f.manager
			ctx := context.Background()
			if err := m.Start(ctx, m.owner); err != nil {
				t.Fatal(err)
			}
			before := gitTest(t, f.directory, "rev-parse", "HEAD")
			switch kind {
			case "tracked":
				writeTest(t, filepath.Join(f.directory, "message.txt"), "uncommitted")
			case "untracked":
				writeTest(t, filepath.Join(f.directory, "new.txt"), "untracked")
			case "divergent":
				writeTest(t, filepath.Join(f.directory, "message.txt"), "local commit")
				gitTest(t, f.directory, "add", ".")
				gitTest(t, f.directory, "commit", "-m", "local fixture")
				before = gitTest(t, f.directory, "rev-parse", "HEAD")
			case "wrong-branch":
				gitTest(t, f.directory, "checkout", "-b", "other")
			}
			f.advance(t, "remote commit")
			err := m.Update(ctx, m.owner)
			if err == nil {
				t.Fatal("unsafe update accepted")
			}
			if (kind == "tracked" || kind == "untracked") && !errors.Is(err, ErrDirty) {
				t.Fatal(err)
			}
			if kind == "divergent" && !errors.Is(err, ErrDiverged) {
				t.Fatal(err)
			}
			if gitTest(t, f.directory, "rev-parse", "HEAD") != before || f.runtime.stops != 0 {
				t.Fatal("rejected update changed code or stopped service")
			}
			if running, _ := m.Running(ctx, m.owner); !running {
				t.Fatal("rejected update stopped process")
			}
		})
	}
}

func TestBuildFailureAndChangedSourceNeverStart(t *testing.T) {
	for _, kind := range []string{"verify", "mutation", "runtime"} {
		t.Run(kind, func(t *testing.T) {
			f := newRepoFixture(t)
			m := f.manager
			switch kind {
			case "verify":
				m.config.Verify = []Command{{Program: "/bin/false"}}
			case "mutation":
				m.config.Build = []Command{{Program: "/bin/sh", Args: []string{"-c", "echo changed > message.txt"}}}
			case "runtime":
				f.runtime.badVersion = true
			}
			err := m.Start(context.Background(), m.owner)
			if err == nil {
				t.Fatal("invalid candidate started")
			}
			if kind == "mutation" && !errors.Is(err, ErrChanged) {
				t.Fatal(err)
			}
			if kind == "runtime" && !errors.Is(err, ErrRuntime) {
				t.Fatal(err)
			}
			if running, _ := m.Running(context.Background(), m.owner); running {
				t.Fatal("failed candidate left process running")
			}
		})
	}
}

func TestFetchFailureOwnerBoundaryAndCancellation(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	ctx := context.Background()
	if err := m.Start(ctx, m.owner); err != nil {
		t.Fatal(err)
	}
	gitTest(t, f.directory, "remote", "set-url", "origin", filepath.Join(f.root, "missing-remote"))
	if err := m.Update(ctx, m.owner); err == nil || f.runtime.stops != 0 {
		t.Fatal("fetch failure modified runtime")
	}
	wrong := identity.Request{UID: m.owner.UID + 1, Name: "other"}
	if err := m.Stop(ctx, wrong); err == nil {
		t.Fatal("another user stopped workspace")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.Rebuild(cancelled, m.owner); err == nil {
		t.Fatal("cancelled operation executed")
	}
	state, _ := m.SourceStatus(ctx, m.owner)
	if state.Workspace == nil || state.Running == nil {
		t.Fatal("Git authentication failure broke local source inspection")
	}
}

func TestOperationsSerializeAndWaitingCancellationIsBounded(t *testing.T) {
	f := newRepoFixture(t)
	m := f.manager
	m.operation <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := m.Stop(ctx, m.owner); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting operation ignored cancellation", err)
	}
	state, err := m.SourceStatus(context.Background(), m.owner)
	if err != nil || !state.Unavailable {
		t.Fatal("busy inspection blocked controls", err)
	}
	<-m.operation
}
