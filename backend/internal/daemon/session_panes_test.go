package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	iosrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
)

// fakePaneRuntime records what was destroyed and can refuse.
type fakePaneRuntime struct {
	destroyed []string
	err       error
}

func (f *fakePaneRuntime) Create(context.Context, ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	return ports.RuntimeHandle{}, nil
}

func (f *fakePaneRuntime) Destroy(_ context.Context, h ports.RuntimeHandle) error {
	f.destroyed = append(f.destroyed, h.ID)
	return f.err
}

func (f *fakePaneRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return false, nil
}

// `iosrun-advisor-ios-app-13` and `-14` were still running with their owning
// sessions long terminated: the run bar's build pane is named per session and
// nothing tore it down.
func TestSessionPaneReaper_ClosesTheRunPaneAndTheReviewer(t *testing.T) {
	rt := &fakePaneRuntime{}
	var reviewed []domain.SessionID
	reap := newSessionPaneReaper(func(_ context.Context, id domain.SessionID) error {
		reviewed = append(reviewed, id)
		return nil
	}, rt)

	if err := reap(context.Background(), "advisor-ios-app-13"); err != nil {
		t.Fatalf("reap = %v, want nil", err)
	}
	if len(reviewed) != 1 || reviewed[0] != "advisor-ios-app-13" {
		t.Errorf("reviewer reaped %v, want the session once", reviewed)
	}
	want := iosrunsvc.HandleID("advisor-ios-app-13")
	if len(rt.destroyed) != 1 || rt.destroyed[0] != want {
		t.Errorf("destroyed %v, want [%s]", rt.destroyed, want)
	}
	if want != "iosrun-advisor-ios-app-13" {
		t.Errorf("run pane handle = %q; the reap must address the name tmux actually holds", want)
	}
}

// One pane that will not die must not leave the other one running.
func TestSessionPaneReaper_AFailedReviewerStillReapsTheRunPane(t *testing.T) {
	rt := &fakePaneRuntime{}
	reap := newSessionPaneReaper(func(context.Context, domain.SessionID) error {
		return errors.New("reviewer pane wedged")
	}, rt)

	err := reap(context.Background(), "mer-1")
	if err == nil || !strings.Contains(err.Error(), "reviewer pane wedged") {
		t.Fatalf("err = %v, want the reviewer failure reported", err)
	}
	if len(rt.destroyed) != 1 {
		t.Fatalf("destroyed %v, want the run pane reaped despite the reviewer failing", rt.destroyed)
	}
}

// And the other way round: a run pane that cannot be destroyed is reported, not
// swallowed, so the caller's warning names it.
func TestSessionPaneReaper_AFailedRunPaneIsReported(t *testing.T) {
	rt := &fakePaneRuntime{err: errors.New("tmux unreachable")}
	reaped := false
	reap := newSessionPaneReaper(func(context.Context, domain.SessionID) error { reaped = true; return nil }, rt)

	err := reap(context.Background(), "mer-1")
	if err == nil || !strings.Contains(err.Error(), "tmux unreachable") {
		t.Fatalf("err = %v, want the run-pane failure reported", err)
	}
	if !reaped {
		t.Error("the reviewer was not reaped")
	}
}
