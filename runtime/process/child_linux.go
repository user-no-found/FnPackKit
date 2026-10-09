// Package process owns native child lifetime and nonroot launch credentials.
package process

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Child retains the os.Process handle. Go uses a Linux pidfd, avoiding PID reuse.
type Child struct {
	cmd  *exec.Cmd
	done chan struct{}
	mu   sync.Mutex
	err  error
}

// Start waits exactly once and retains the process handle.
func Start(cmd *exec.Cmd) (*Child, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &Child{cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()
	return process, nil
}

// Alive reports whether Wait has completed.
func (process *Child) Alive() bool {
	select {
	case <-process.done:
		return false
	default:
		return true
	}
}

// Terminate requests graceful termination without signalling a reused PID.
func (process *Child) Terminate(timeout time.Duration) error {
	if process == nil || !process.Alive() {
		return nil
	}
	if err := process.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-process.done:
		return nil
	case <-time.After(timeout):
		return errors.New("child has not finished its graceful shutdown")
	}
}

// Result returns the recorded Wait result.
func (process *Child) Result() error {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.err
}

// Credential refuses root and sentinel IDs for a per-user process.
func Credential(uid, gid uint32, groups []uint32) (*syscall.SysProcAttr, error) {
	if uid == 0 || uid == 4294967295 || gid == 4294967295 {
		return nil, errors.New("worker credentials require nonroot UID and valid GID")
	}
	for _, group := range groups {
		if group == 4294967295 {
			return nil, errors.New("invalid supplementary group")
		}
	}
	return &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: append([]uint32(nil), groups...)}, Pdeathsig: syscall.SIGTERM}, nil
}

// Done closes after the child's process has been reaped.
func (process *Child) Done() <-chan struct{} { return process.done }
