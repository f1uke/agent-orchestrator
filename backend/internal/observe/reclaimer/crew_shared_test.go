package reclaimer

import (
	"context"
	"testing"
	"time"

	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// TestTick_SharedCrewWorktreeIsARefusalNotASuccess.
//
// A crew's worktree is kept while another member is still alive in it, and the
// teardown reports that as workspace_shared. The loop must read it exactly the
// way it reads workspace_dirty: a REFUSAL. Recording it as a reclaim would leave
// a worktree on disk that the audit log claims was freed, and - worse - the loop
// would stop tracking the session, so nothing would ever free it once the
// crewmate finished.
func TestTick_SharedCrewWorktreeIsARefusalNotASuccess(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{
		candidates: []sessionsvc.ReclaimCandidate{candidate("sess-qa", now)},
		outcome:    &sessionsvc.ReclaimOutcome{Freed: false, Reason: sessionmanager.ReasonWorkspaceShared},
	}
	log := &memLog{}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere", Audit: log})

	runPast(t, r, &now, 15*time.Minute)

	if got := log.actions(); len(got) != 1 || got[0] != "skipped" {
		t.Fatalf("audit actions = %v, want exactly one skipped", got)
	}
	if got := log.entries[0].Reason; got != sessionmanager.ReasonWorkspaceShared {
		t.Fatalf("skip reason = %q, want %q", got, sessionmanager.ReasonWorkspaceShared)
	}
	if log.entries[0].Branch == "" {
		t.Fatal("a refusal must still name the branch: the log line is the recovery instruction")
	}

	// And it must be RETRIED once the crewmate is gone, rather than written off.
	svc.outcome = &sessionsvc.ReclaimOutcome{Freed: true, BytesFreed: 4096}
	now = now.Add(16 * time.Minute)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := log.actions(); len(got) != 2 || got[1] != "reclaimed" {
		t.Fatalf("audit actions = %v, want the retry to have reclaimed", got)
	}
}

// TestTick_SharedWorktreeLogsOncePerReason: a crew that stays half-finished for
// weeks must not append a line every tick. The loop dedupes by reason, and this
// pins that the new reason participates in that.
func TestTick_SharedWorktreeLogsOncePerReason(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{
		candidates: []sessionsvc.ReclaimCandidate{candidate("sess-qa", now)},
		outcome:    &sessionsvc.ReclaimOutcome{Freed: false, Reason: sessionmanager.ReasonWorkspaceShared},
	}
	log := &memLog{}
	r := New(svc, on(15), Config{Clock: func() time.Time { return now }, SelfPath: "/elsewhere", Audit: log})

	for i := 0; i < 5; i++ {
		runPast(t, r, &now, 15*time.Minute)
	}
	if got := log.actions(); len(got) != 1 {
		t.Fatalf("audit actions = %v, want exactly one line for a permanently shared worktree", got)
	}
}
