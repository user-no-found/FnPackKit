package backend

import (
	"context"
	"errors"
	"testing"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

type sourceTarget struct {
	*fixtureTarget
	fail bool
}

func (s *sourceTarget) SourceStatus(context.Context, identity.Request) (SourceState, error) {
	if s.fail {
		return SourceState{}, errors.New("private repository inaccessible")
	}
	return SourceState{Workspace: &SourceVersion{Commit: "fixture", Fingerprint: "snapshot", Dirty: true}}, nil
}

func TestOptionalSourceStatusNeverDisablesLifecycleControls(t *testing.T) {
	_, target, id := controlFixture()
	source := &sourceTarget{fixtureTarget: target}
	controller := New(source)
	first, err := controller.Execute(context.Background(), id, Command{Action: "start"})
	if err != nil || first.Source == nil || !first.Source.Workspace.Dirty {
		t.Fatal("source evidence missing", err)
	}
	source.fail = true
	paused, err := controller.Execute(context.Background(), id, Command{Action: "stop", Confirm: true})
	if err != nil || !paused.Paused || paused.Source == nil || !paused.Source.Unavailable {
		t.Fatal("source inspection failure blocked stop", err)
	}
}
