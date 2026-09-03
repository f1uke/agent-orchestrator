package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// THE WARNING THAT REPLACED THE TRIGGER.
//
// A qa used to appear on its own the first time a task drove the app. Removing
// that is the point of this change - it fired while dev was still using the
// device, so the qa it created fought dev for it - but removing it and adding
// nothing hands back the failure it was buying protection against: a task that
// finished with no qa attached at all, in silence. These are the assertions that
// it is not silent.

// unreviewedStack builds a solo `standard` worker on an ordinary project, an
// orchestrator to report to, and a service wired to both.
func unreviewedStack(t *testing.T) (*Service, *fakeStore, *fakeCommander) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/tmp/mer"}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		TaskSize: domain.TaskSizeStandard,
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions["mer-orch"] = domain.SessionRecord{
		ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	fc := &fakeCommander{}
	return &Service{store: st, manager: fc, clock: func() time.Time { return time.Unix(1000, 0).UTC() }}, st, fc
}

func drove(st *fakeStore, id domain.SessionID, touch domain.RuntimeTouch) {
	rec := st.sessions[id]
	rec.RuntimeTouch = touch
	st.sessions[id] = rec
}

// TestUnreviewed_FiresWhenDevClosesOutHavingDrivenTheApp. dev's report to the
// orchestrator is its close-out - the point at which somebody else is about to be
// told the task is finished - so it is where the truth has to be attached. It
// goes to BOTH parties: the delivered message carries an [AO] line for the
// orchestrator, and the result carries the same fact back to dev's own CLI.
func TestUnreviewed_FiresWhenDevClosesOutHavingDrivenTheApp(t *testing.T) {
	svc, st, fc := unreviewedStack(t)
	drove(st, "mer-1", domain.RuntimeTouchSim)

	out, err := svc.SendFrom(context.Background(), "mer-orch", "done, PR is green", CrewTalk{From: "mer-1"})
	if err != nil {
		t.Fatalf("SendFrom: %v", err)
	}
	if !out.Unreviewed.Unreviewed() {
		t.Fatal("dev closed out on work nobody checked and AO said nothing")
	}
	if out.Unreviewed.Touch != domain.RuntimeTouchSim {
		t.Fatalf("the warning names %q, want %q", out.Unreviewed.Touch, domain.RuntimeTouchSim)
	}
	// NOT REFUSED. Refusing recreates the stall this exists to prevent, collides
	// with the crew-message refusal that parks a task at NEEDS YOU, and is the
	// version easiest to lie past.
	if len(fc.sentMessages) != 1 {
		t.Fatalf("the report was not delivered: %d messages sent", len(fc.sent))
	}
	delivered := fc.sentMessages[0]
	if !strings.HasPrefix(delivered, "done, PR is green") {
		t.Fatalf("AO replaced dev's report instead of adding to it:\n%s", delivered)
	}
	for _, want := range []string{
		"[AO]",                 // attributed: this is AO speaking, not dev
		"took the simulator",   // what the task actually did
		"no qa was ever on it", // and what nobody did
		"ao crew review",       // the verb that answers it
		"+ qa",                 // and the human's route
	} {
		if !strings.Contains(delivered, want) {
			t.Fatalf("the delivered report is missing %q:\n%s", want, delivered)
		}
	}
}

// TestUnreviewed_SaysNothingAboutABackendOnlyTask is what keeps the change free.
// A task with nothing to exercise drives no runtime surface, so it is never
// nagged about a tester it never needed and stays a one-agent task that costs
// nothing extra.
func TestUnreviewed_SaysNothingAboutABackendOnlyTask(t *testing.T) {
	svc, _, fc := unreviewedStack(t)

	out, err := svc.SendFrom(context.Background(), "mer-orch", "done, backend only", CrewTalk{From: "mer-1"})
	if err != nil {
		t.Fatalf("SendFrom: %v", err)
	}
	if out.Unreviewed.Unreviewed() {
		t.Fatalf("a task that never drove the app was warned: %+v", out.Unreviewed)
	}
	if !out.Unreviewed.Checked {
		t.Fatal("the report was not looked at at all; the check has to run to answer no")
	}
	if fc.sentMessages[0] != "done, backend only" {
		t.Fatalf("AO added something to a report it had nothing to say about:\n%s", fc.sentMessages[0])
	}
}

// TestUnreviewed_SaysNothingOnceTheTaskHasHadAQA. dev gains its crew columns in
// the write that creates the member and never loses them, so this stays quiet
// after a qa is stood down - which is right: somebody did look.
func TestUnreviewed_SaysNothingOnceTheTaskHasHadAQA(t *testing.T) {
	svc, st, _ := unreviewedStack(t)
	drove(st, "mer-1", domain.RuntimeTouchSim)
	rec := st.sessions["mer-1"]
	rec.CrewID, rec.CrewRole = "mer-1", domain.CrewRoleDev
	st.sessions["mer-1"] = rec

	out, err := svc.SendFrom(context.Background(), "mer-orch", "done", CrewTalk{From: "mer-1"})
	if err != nil {
		t.Fatalf("SendFrom: %v", err)
	}
	if out.Unreviewed.Unreviewed() {
		t.Fatal("a task that had a qa was told nobody looked at it")
	}
}

// TestUnreviewed_SaysNothingToATaskThatMayNotHaveAQA. A `mechanical` task is one
// agent by an explicit decision, and a crew-off project has made that decision
// for all of its work. Telling either to call a qa they would be refused is a
// warning nobody can act on, which is the kind nobody reads.
func TestUnreviewed_SaysNothingToATaskThatMayNotHaveAQA(t *testing.T) {
	t.Run("mechanical", func(t *testing.T) {
		svc, st, _ := unreviewedStack(t)
		drove(st, "mer-1", domain.RuntimeTouchPreview)
		rec := st.sessions["mer-1"]
		rec.TaskSize = domain.TaskSizeMechanical
		st.sessions["mer-1"] = rec

		out, err := svc.SendFrom(context.Background(), "mer-orch", "renamed it", CrewTalk{From: "mer-1"})
		if err != nil {
			t.Fatalf("SendFrom: %v", err)
		}
		if out.Unreviewed.Unreviewed() {
			t.Fatal("a mechanical task was told to ask for a qa it may not have")
		}
	})
	t.Run("a crew-off project", func(t *testing.T) {
		svc, st, _ := unreviewedStack(t)
		drove(st, "mer-1", domain.RuntimeTouchPreview)
		st.projects["mer"] = domain.ProjectRecord{
			ID: "mer", Path: "/tmp/mer", Config: domain.ProjectConfig{DisableAutoCrew: true},
		}

		out, err := svc.SendFrom(context.Background(), "mer-orch", "done", CrewTalk{From: "mer-1"})
		if err != nil {
			t.Fatalf("SendFrom: %v", err)
		}
		if out.Unreviewed.Unreviewed() {
			t.Fatal("a crew-off project's dev was told to ask for a qa it would be refused")
		}
	})
}

// TestUnreviewed_IsNotAskedOfEveryMessage. The check is about CLOSING OUT, so it
// runs on one leg only: a worker reporting to an orchestrator. A human's send, an
// orchestrator's nudge and a message between crewmates are all untouched - and a
// warning that fires everywhere is one nobody reads.
func TestUnreviewed_IsNotAskedOfEveryMessage(t *testing.T) {
	t.Run("a human sending to the worker", func(t *testing.T) {
		svc, st, fc := unreviewedStack(t)
		drove(st, "mer-1", domain.RuntimeTouchSim)

		out, err := svc.SendFrom(context.Background(), "mer-1", "how is it going?", CrewTalk{})
		if err != nil {
			t.Fatalf("SendFrom: %v", err)
		}
		if out.Unreviewed.Checked {
			t.Fatal("a human's message was checked for an unreviewed task")
		}
		if fc.sentMessages[0] != "how is it going?" {
			t.Fatalf("a human's message was rewritten:\n%s", fc.sentMessages[0])
		}
	})
	t.Run("the orchestrator nudging the worker", func(t *testing.T) {
		svc, st, _ := unreviewedStack(t)
		drove(st, "mer-1", domain.RuntimeTouchSim)

		out, err := svc.SendFrom(context.Background(), "mer-1", "CI is red", CrewTalk{From: "mer-orch"})
		if err != nil {
			t.Fatalf("SendFrom: %v", err)
		}
		if out.Unreviewed.Checked {
			t.Fatal("a message TO a worker was read as that worker closing out")
		}
	})
	t.Run("a worker messaging another worker", func(t *testing.T) {
		svc, st, _ := unreviewedStack(t)
		drove(st, "mer-1", domain.RuntimeTouchSim)
		st.sessions["mer-9"] = domain.SessionRecord{ID: "mer-9", ProjectID: "mer", Kind: domain.KindWorker}

		out, err := svc.SendFrom(context.Background(), "mer-9", "heads up", CrewTalk{From: "mer-1"})
		if err != nil {
			t.Fatalf("SendFrom: %v", err)
		}
		if out.Unreviewed.Checked {
			t.Fatal("a worker-to-worker message was read as a close-out report")
		}
	})
}
