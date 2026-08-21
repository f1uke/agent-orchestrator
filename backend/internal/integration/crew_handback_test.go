package integration

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgqueue"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// THE HANDBACK, end to end: what qa is now obliged to do when it finishes, done
// against the real store and the real queue.
//
// The first full crew run did the work and then simply stopped - dev asleep, the
// queue empty, nobody told. qa's instructions now end with one command,
// `ao send --session "$AO_CREW_ID"`, so this asserts the two things that command
// depends on: that AO_CREW_ID is the address of the member who owns the branch
// and the PR, and that a report sent to it survives dev being asleep and is
// there, once, when dev next has the turn.
func TestCrewHandback_QAsFinishReachesDev(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t) // dev stood down; qa holds the slot

	// The address qa is told to use. It is dev's id, in qa's own environment, so
	// the command in qa's prompt is correct without qa having to work anything out.
	crewID := s.rt.lastCfg.Env[sessionmanager.EnvCrewID]
	if crewID != string(dev.ID) {
		t.Fatalf("AO_CREW_ID in qa's environment = %q, want dev %q", crewID, dev.ID)
	}

	pane := &paneSpy{}
	now := s.now
	queue := msgqueue.New(s.store, pane, pane, slog.New(slog.NewTextHandler(io.Discard, nil)),
		msgqueue.WithClock(func() time.Time { return now }))
	messenger := queueingMessenger{store: s.store, pane: pane, queue: queue}

	report := "qa done at 4a1b2c3: committed lane rollup tests, recorded 1 case, retired none; 2 cases left for the human to play."
	out, err := messenger.Send(ctx, domain.SessionID(crewID), report)
	if err != nil {
		t.Fatalf("qa's handback to dev: %v", err)
	}
	// dev is asleep, so the report is HELD rather than lost - which is the whole
	// reason the obligation can be unconditional: qa never has to decide whether
	// dev is up first.
	if !out.Queued {
		t.Fatal("a handback to a sleeping dev was typed at a pane that is gone instead of held")
	}
	if typed := pane.typed(); len(typed) != 0 {
		t.Fatalf("the handback was delivered somewhere while dev was asleep: %v", typed)
	}

	// dev takes the turn back, and the report is waiting for it.
	if _, err := s.mgr.HandOverCrewSlot(ctx, qa.ID, dev.ID, domain.WokenByWake); err != nil {
		t.Fatalf("hand the turn back to dev: %v", err)
	}
	for range 3 {
		if err := queue.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		now = now.Add(2 * time.Second)
	}
	typed := pane.typed()
	if len(typed) != 1 || !strings.Contains(typed[0], "recorded 1 case") {
		t.Fatalf("dev received %v, want exactly qa's report", typed)
	}
}
