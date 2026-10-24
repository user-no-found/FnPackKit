// Package backend serializes per-user runtime controls independently of application process policy.
package backend

import (
	"context"
	"errors"
	"sync"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

var ErrPaused = errors.New("backend manually paused")
var ErrConfirmation = errors.New("stop or restart requires confirmation")
var ErrAction = errors.New("unknown backend action")

// Command carries intent only; the caller supplies the authenticated identity separately.
type Command struct {
	Action  string `json:"action"`
	Confirm bool   `json:"confirm,omitempty"`
}

// Snapshot exposes no process addresses, credentials, or other users' state.
type Snapshot struct {
	Schema int    `json:"schema"`
	State  string `json:"state"`
	Paused bool   `json:"paused"`
}

// Target owns entitlement, real process checks, graceful shutdown, and startup.
// Running must inspect without starting work. All methods must support concurrent reads.
type Target interface {
	Running(context.Context, identity.Request) (bool, error)
	Start(context.Context, identity.Request) error
	Stop(context.Context, identity.Request) error
}

type slot struct {
	operation  sync.Mutex
	mu         sync.Mutex
	paused     bool
	transition string
}

// Controller keeps manual pauses for the gateway lifetime; idle recycling does not pause users.
type Controller struct {
	target Target
	mu     sync.Mutex
	users  map[identity.Request]*slot
}

func New(target Target) *Controller {
	return &Controller{target: target, users: make(map[identity.Request]*slot)}
}

func (c *Controller) user(id identity.Request) *slot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.users[id]
	if s == nil {
		s = &slot{}
		c.users[id] = s
	}
	return s
}

// Use guards acquisition, not the whole HTTP response. Target.Stop must drain active leases.
func (c *Controller) Use(ctx context.Context, id identity.Request, acquire func() error) error {
	s := c.user(id)
	s.mu.Lock()
	pausedBeforeWait := s.paused
	s.mu.Unlock()
	if pausedBeforeWait {
		return ErrPaused
	}
	s.operation.Lock()
	defer s.operation.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()
	if paused {
		return ErrPaused
	}
	return acquire()
}

func (c *Controller) read(ctx context.Context, id identity.Request, s *slot) (Snapshot, error) {
	s.mu.Lock()
	paused, transition := s.paused, s.transition
	s.mu.Unlock()
	result := Snapshot{Schema: 1, Paused: paused}
	if transition != "" {
		result.State = transition
		return result, nil
	}
	running, err := c.target.Running(ctx, id)
	switch {
	case err != nil:
		result.State = "unavailable"
	case running:
		result.State = "running"
	case paused:
		result.State = "paused"
	default:
		result.State = "stopped"
	}
	return result, err
}

// Execute requires a confirmed destructive intent and never auto-starts for status reads.
func (c *Controller) Execute(ctx context.Context, id identity.Request, command Command) (Snapshot, error) {
	if command.Action != "status" && command.Action != "start" && command.Action != "stop" && command.Action != "restart" {
		return Snapshot{}, ErrAction
	}
	if (command.Action == "stop" || command.Action == "restart") && !command.Confirm {
		return Snapshot{}, ErrConfirmation
	}
	s := c.user(id)
	if command.Action == "status" {
		return c.read(ctx, id, s)
	}
	s.operation.Lock()
	defer s.operation.Unlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	if command.Action != "start" {
		s.paused = true
		s.transition = "stopping"
	} else {
		s.transition = "starting"
	}
	s.mu.Unlock()
	var err error
	if command.Action != "start" {
		err = c.target.Stop(ctx, id)
	}
	if err == nil && command.Action != "stop" {
		s.mu.Lock()
		s.transition = "starting"
		s.mu.Unlock()
		err = c.target.Start(ctx, id)
		if err == nil {
			s.mu.Lock()
			s.paused = false
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	s.transition = ""
	s.mu.Unlock()
	result, readErr := c.read(ctx, id, s)
	if err != nil {
		return result, err
	}
	return result, readErr
}
