package backend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

type fixtureTarget struct {
	mu                       sync.Mutex
	running                  map[uint32]bool
	starts, stops            int
	failStop                 bool
	stopEntered, stopRelease chan struct{}
}

func (f *fixtureTarget) Running(ctx context.Context, id identity.Request) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[id.UID], nil
}
func (f *fixtureTarget) Start(ctx context.Context, id identity.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.running[id.UID] = true
	return nil
}
func (f *fixtureTarget) Stop(ctx context.Context, id identity.Request) error {
	if f.stopEntered != nil {
		close(f.stopEntered)
		<-f.stopRelease
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	if f.failStop {
		return errors.New("busy")
	}
	f.running[id.UID] = false
	return nil
}
func controlFixture() (*Controller, *fixtureTarget, identity.Request) {
	f := &fixtureTarget{running: make(map[uint32]bool)}
	return New(f), f, identity.Request{UID: 1000, Name: "owner"}
}

func TestConfirmationAndStatusNeverStart(t *testing.T) {
	c, f, id := controlFixture()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		s, err := c.Execute(ctx, id, Command{Action: "status"})
		if err != nil || s.State != "stopped" {
			t.Fatal(s, err)
		}
	}
	for _, action := range []string{"stop", "restart"} {
		if _, err := c.Execute(ctx, id, Command{Action: action}); !errors.Is(err, ErrConfirmation) {
			t.Fatal(err)
		}
	}
	if _, err := c.Execute(ctx, id, Command{Action: "shell"}); !errors.Is(err, ErrAction) {
		t.Fatal(err)
	}
	if f.starts != 0 || f.stops != 0 {
		t.Fatal("status or rejected command changed target")
	}
}

func TestPauseBlocksReconnectionAndIsolatesUsers(t *testing.T) {
	c, f, id := controlFixture()
	ctx := context.Background()
	if _, err := c.Execute(ctx, id, Command{Action: "start"}); err != nil {
		t.Fatal(err)
	}
	s, err := c.Execute(ctx, id, Command{Action: "stop", Confirm: true})
	if err != nil || s.State != "paused" {
		t.Fatal(s, err)
	}
	for i := 0; i < 4; i++ {
		if err := c.Use(ctx, id, func() error { t.Fatal("paused acquisition executed"); return nil }); !errors.Is(err, ErrPaused) {
			t.Fatal(err)
		}
	}
	other := identity.Request{UID: 1001, Name: "other"}
	if err := c.Use(ctx, other, func() error { return f.Start(ctx, other) }); err != nil {
		t.Fatal(err)
	}
	if !f.running[other.UID] || f.running[id.UID] {
		t.Fatal("user isolation lost")
	}
	if _, err := c.Execute(ctx, id, Command{Action: "restart", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Use(ctx, id, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestFailedStopKeepsAcquisitionPaused(t *testing.T) {
	c, f, id := controlFixture()
	ctx := context.Background()
	f.running[id.UID] = true
	f.failStop = true
	s, err := c.Execute(ctx, id, Command{Action: "restart", Confirm: true})
	if err == nil || !s.Paused || f.starts != 0 {
		t.Fatal("failed retirement restarted the worker", s, err)
	}
	if err = c.Use(ctx, id, func() error { t.Fatal("reopened during failed shutdown"); return nil }); !errors.Is(err, ErrPaused) {
		t.Fatal(err)
	}
	f.failStop = false
	if _, err = c.Execute(ctx, id, Command{Action: "stop", Confirm: true}); err != nil {
		t.Fatal(err)
	}
}

func TestTransitionStatusDoesNotWaitForShutdown(t *testing.T) {
	c, f, id := controlFixture()
	f.stopEntered = make(chan struct{})
	f.stopRelease = make(chan struct{})
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { _, err := c.Execute(ctx, id, Command{Action: "stop", Confirm: true}); done <- err }()
	<-f.stopEntered
	s, err := c.Execute(ctx, id, Command{Action: "status"})
	if err != nil || s.State != "stopping" || !s.Paused {
		t.Fatal(s, err)
	}
	close(f.stopRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCanceledActionDoesNotChangeRuntime(t *testing.T) {
	c, f, id := controlFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Execute(ctx, id, Command{Action: "start"}); err == nil {
		t.Fatal("accepted canceled action")
	}
	if f.starts != 0 {
		t.Fatal("started after cancellation")
	}
}

func TestPausedAcquisitionDoesNotWaitForDrainingStop(t *testing.T) {
	c, f, id := controlFixture()
	f.stopEntered = make(chan struct{})
	f.stopRelease = make(chan struct{})
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { _, err := c.Execute(ctx, id, Command{Action: "stop", Confirm: true}); done <- err }()
	<-f.stopEntered
	acquired := make(chan error, 1)
	go func() { acquired <- c.Use(ctx, id, func() error { t.Error("acquired during drain"); return nil }) }()
	select {
	case err := <-acquired:
		if !errors.Is(err, ErrPaused) {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("new acquisition waited for stop instead of rejecting pause")
	}
	close(f.stopRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
