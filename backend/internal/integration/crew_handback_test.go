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
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
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

// THE GATE, against the real store: a checklist written the way a crew writes
// one, and qa ending its run over cases it never touched.
//
// The failure this rules out is not hypothetical. A qa held a simulator, ran
// three bracketed machine runs and handed the task back with 0 of 7 cases
// played - and nothing anywhere looked at the checklist, because a case nobody
// drove and a case nobody CAN drive are the same empty row.
func TestCrewHandback_SaysWhatTheRunLeftUndriven(t *testing.T) {
	ctx := context.Background()
	s := newCrewStack(t)
	dev, qa := s.spawnCrew(t)

	// The checklist belongs to the TASK, so it is written against dev's id.
	if _, _, err := s.store.ReplaceSmokeChecks(ctx, dev.ID, "mer", []domain.SmokeAuthoredCase{
		{ID: "mr-appears", Seq: 1, Name: "The MR appears"},
		{ID: "press-hold", Seq: 2, Name: "Press and hold opens the menu"},
		{ID: "tab-stays-live", Seq: 3, Name: "The tab stays live when unfocused"},
	}, domain.SmokeAuthor{ID: qa.ID, Role: domain.CrewRoleQA}, s.now); err != nil {
		t.Fatalf("author the checklist: %v", err)
	}
	// One case driven, one declared undriveable with its reason, one left alone.
	recordRun(ctx, t, s, qa.ID, "mr-appears", domain.SmokeAgentResult{Verdict: domain.SmokePass, Note: "listed within 40s, 3 runs"})
	recordRun(ctx, t, s, qa.ID, "press-hold", domain.SmokeAgentResult{
		Verdict: domain.SmokeSkip, Note: "tried a 1.2s ao sim drag; the menu never opened, so nothing was exercised",
	})

	before := len(s.msg.msgs)
	sent, err := s.svc.SendToCrewmate(ctx, qa.ID, sessionsvc.CrewSend{
		Role: domain.CrewRoleDev, Message: "run done at 4a1b2c3", Subject: "4a1b2c3",
	})
	if err != nil {
		t.Fatalf("qa handing back: %v", err)
	}
	if got := sent.Handback.NotDriven; len(got) != 1 || got[0] != "tab-stays-live" {
		t.Fatalf("cases left undriven = %v, want [tab-stays-live]", got)
	}
	if sent.Handback.Cases != 3 {
		t.Fatalf("cases = %d, want 3", sent.Handback.Cases)
	}
	// The handback is NOT refused - it lands, carrying the fact dev needs.
	if len(s.msg.msgs) != before+1 {
		t.Fatalf("the handback did not reach dev: %v", s.msg.msgs)
	}
	delivered := s.msg.msgs[len(s.msg.msgs)-1]
	if !strings.HasPrefix(delivered, "run done at 4a1b2c3") {
		t.Fatalf("qa's own words did not survive:\n%s", delivered)
	}
	for _, want := range []string{"[AO]", "1 of 3", "tab-stays-live"} {
		if !strings.Contains(delivered, want) {
			t.Fatalf("dev's copy does not say %q:\n%s", want, delivered)
		}
	}
	// The case qa DECLARED undriveable is not in the gap: saying it out loud is
	// exactly what takes it out.
	if strings.Contains(delivered, "press-hold") {
		t.Fatalf("a declared skip was counted as untouched:\n%s", delivered)
	}
}

// recordRun drives one case through the same store calls `ao smoke record` ends
// up in, so the run history the gate reads is the real one.
func recordRun(ctx context.Context, t *testing.T, s *crewStack, session domain.SessionID, checkID string, res domain.SmokeAgentResult) {
	t.Helper()
	run, opened, err := s.store.OpenSmokeRun(ctx, checkID, session, s.now)
	if err != nil || !opened {
		t.Fatalf("open run on %s: %v", checkID, err)
	}
	if closed, err := s.store.CloseSmokeRun(ctx, run.ID, res, s.now, s.now); err != nil || !closed {
		t.Fatalf("close run on %s: %v", checkID, err)
	}
}
