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

// THE HANDBACK, end to end: what qa is obliged to do when it finishes, done
// against the real store and the real queue.
//
// The first full crew run did the work and then simply stopped - dev asleep, the
// queue empty, nobody told. Both members run at once now, so the common case is
// no longer a message waiting in a queue: dev is RIGHT THERE, and qa's report
// reaches it the moment it is sent. This asserts that, and that the ids qa is
// handed to talk with are the ones the task is actually built from.
func TestCrewHandback_QAsFinishReachesDev(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t) // both awake, one worktree

	// The ids in qa's own environment. AO_CREW_ID is the TASK (what `ao smoke`
	// takes); AO_CREW_DEV_ID names the member that owns the branch and the PR.
	env := s.rt.lastCfg.Env
	if got := env[sessionmanager.EnvCrewID]; got != string(dev.ID) {
		t.Fatalf("AO_CREW_ID in qa's environment = %q, want dev %q", got, dev.ID)
	}
	if got := env[sessionmanager.EnvCrewDevID]; got != string(dev.ID) {
		t.Fatalf("AO_CREW_DEV_ID in qa's environment = %q, want dev %q", got, dev.ID)
	}
	if got := env[sessionmanager.EnvCrewQAID]; got != string(qa.ID) {
		t.Fatalf("AO_CREW_QA_ID in qa's environment = %q, want its own id %q", got, qa.ID)
	}
	crewID := string(dev.ID)

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
	// dev is awake beside qa, so there is nothing to hold: the report lands now.
	if out.Queued {
		t.Fatal("the handback was held although dev is running right beside qa")
	}
	typed := pane.typed()
	if len(typed) != 1 || !strings.Contains(typed[0], "recorded 1 case") {
		t.Fatalf("dev received %v, want exactly qa's report", typed)
	}

	// And the queue is still the safety net for the case that remains: a member
	// the idle sweep paused. Nothing is lost, and it lands when that member is back.
	if err := s.lcm.MarkSuspended(ctx, dev.ID, domain.SleepReasonIdle); err != nil {
		t.Fatal(err)
	}
	held, err := messenger.Send(ctx, domain.SessionID(crewID), "and CI went green")
	if err != nil {
		t.Fatalf("send to a paused dev: %v", err)
	}
	if !held.Queued {
		t.Fatal("a message for a paused member was typed at a pane that is gone instead of held")
	}
	if _, err := s.mgr.Resume(ctx, dev.ID, domain.WokenByWake); err != nil {
		t.Fatalf("resume dev: %v", err)
	}
	for range 3 {
		if err := queue.Drain(ctx); err != nil {
			t.Fatalf("drain: %v", err)
		}
		now = now.Add(2 * time.Second)
	}
	if got := pane.typed(); len(got) != 2 {
		t.Fatalf("messages delivered = %v, want the report and the held follow-up", got)
	}
}
