package localrepo

import (
	"context"
	"errors"
	"strings"

	"github.com/user-no-found/FnPackKit/runtime/backend"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func (m *Manager) begin(ctx context.Context, id identity.Request) (context.Context, func(), error) {
	if err := m.authorize(id); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	if err := ctx.Err(); err != nil {
		cancel()
		return nil, nil, err
	}
	select {
	case m.operation <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-m.operation
			cancel()
			return nil, nil, err
		}
		return ctx, func() { <-m.operation; cancel() }, nil
	case <-ctx.Done():
		cancel()
		return nil, nil, ctx.Err()
	}
}

func (m *Manager) state(ctx context.Context) (RuntimeState, error) {
	state, err := m.runtime.State(ctx)
	if err == nil && state.Running && (state.Directory != m.config.Directory || state.Version == nil ||
		state.Version.Commit == "" || state.Version.Fingerprint == "") {
		return state, ErrRuntime
	}
	return state, err
}

// Running checks the actual local runtime without building, fetching or starting.
func (m *Manager) Running(ctx context.Context, id identity.Request) (bool, error) {
	if err := m.authorize(id); err != nil {
		return false, err
	}
	state, err := m.state(ctx)
	return state.Running, err
}

// Start prepares the current local worktree, including explicitly marked dirty
// content. An already running process remains bound to its startup snapshot.
func (m *Manager) Start(ctx context.Context, id identity.Request) error {
	ctx, done, err := m.begin(ctx, id)
	if err != nil {
		return err
	}
	defer done()
	state, err := m.state(ctx)
	if err != nil || state.Running {
		return err
	}
	launch, err := m.prepare(ctx)
	if err != nil {
		return err
	}
	return m.startPrepared(ctx, launch)
}

func (m *Manager) startPrepared(ctx context.Context, launch Launch) error {
	before, err := m.inspect(ctx)
	if err != nil {
		return err
	}
	if before != launch.version {
		return ErrChanged
	}
	err = m.runtime.Start(ctx, launch)
	if err == nil {
		state, stateErr := m.state(ctx)
		err = stateErr
		if err == nil && (!state.Running || *state.Version != launch.version) {
			err = ErrRuntime
		}
	}
	if err == nil {
		after, inspectErr := m.inspect(ctx)
		err = inspectErr
		if err == nil && after != launch.version {
			err = ErrChanged
		}
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.config.Timeout)
		defer cancel()
		return errors.Join(err, m.runtime.Stop(cleanup))
	}
	return nil
}

// Stop delegates actual graceful exit and data saving; it never changes Git.
func (m *Manager) Stop(ctx context.Context, id identity.Request) error {
	ctx, done, err := m.begin(ctx, id)
	if err != nil {
		return err
	}
	defer done()
	return m.runtime.Stop(ctx)
}

// Rebuild validates current local changes and restarts only a previously running
// service. A stopped or manually paused service remains stopped.
func (m *Manager) Rebuild(ctx context.Context, id identity.Request) error {
	ctx, done, err := m.begin(ctx, id)
	if err != nil {
		return err
	}
	defer done()
	return m.rebuild(ctx, nil)
}

func (m *Manager) rebuild(ctx context.Context, change func() error) error {
	state, err := m.state(ctx)
	if err != nil {
		return err
	}
	if state.Running {
		if err := m.runtime.Stop(ctx); err != nil {
			return err
		}
		stopped, err := m.runtime.State(ctx)
		if err != nil {
			return err
		}
		if stopped.Running {
			return errors.New("runtime did not stop before changing workspace")
		}
	}
	if change != nil {
		if err := change(); err != nil {
			return err
		}
	}
	launch, err := m.prepare(ctx)
	if err != nil {
		return err
	}
	if state.Running {
		return m.startPrepared(ctx, launch)
	}
	return nil
}

// Update fetches the configured branch, pins that commit and fast-forwards a
// clean local branch. It never stashes, resets, discards or pushes local work.
func (m *Manager) Update(ctx context.Context, id identity.Request) error {
	ctx, done, err := m.begin(ctx, id)
	if err != nil {
		return err
	}
	defer done()
	before, err := m.cleanBranch(ctx)
	if err != nil {
		return err
	}
	ref := "refs/remotes/" + m.config.Remote + "/" + m.config.Branch
	if _, err := m.git(ctx, "fetch", "--no-tags", "--no-write-fetch-head", "--", m.config.Remote,
		"+refs/heads/"+m.config.Branch+":"+ref); err != nil {
		return err
	}
	value, err := m.git(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return err
	}
	target := strings.TrimSpace(string(value))
	if _, err := m.git(ctx, "merge-base", "--is-ancestor", before.Commit, target); err != nil {
		return errors.Join(ErrDiverged, err)
	}
	afterFetch, err := m.cleanBranch(ctx)
	if err != nil {
		return err
	}
	if afterFetch != before {
		return ErrChanged
	}
	if target == before.Commit {
		return nil
	}
	return m.rebuild(ctx, func() error {
		current, err := m.cleanBranch(ctx)
		if err != nil {
			return err
		}
		if current != before {
			return ErrChanged
		}
		if _, err := m.git(ctx, "merge", "--ff-only", "--no-edit", target); err != nil {
			return err
		}
		merged, err := m.cleanBranch(ctx)
		if err != nil {
			return err
		}
		if merged.Commit != target {
			return ErrChanged
		}
		return nil
	})
}

// SourceStatus reports working files and the actual process's startup evidence.
// Busy inspection does not stall controls behind a long fetch or verification.
func (m *Manager) SourceStatus(ctx context.Context, id identity.Request) (backend.SourceState, error) {
	var result backend.SourceState
	if err := m.authorize(id); err != nil {
		return result, err
	}
	select {
	case m.operation <- struct{}{}:
		defer func() { <-m.operation }()
	default:
		state, err := m.state(ctx)
		if state.Running {
			result.Running = state.Version
		}
		result.Unavailable = true
		return result, err
	}
	state, err := m.state(ctx)
	if state.Running {
		result.Running = state.Version
	}
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	defer cancel()
	workspace, err := m.inspect(ctx)
	if err != nil {
		return result, err
	}
	result.Workspace = &workspace
	result.Changed = result.Running != nil && workspace != *result.Running
	return result, nil
}
