package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestPurgeSession_RemovesTerminalRowAndCascades covers the hard-delete path
// used to reclaim finished sessions: the session row itself must disappear,
// and dependent rows without their own explicit cleanup (session_worktrees,
// which relies on the sessions FK's ON DELETE CASCADE) must cascade away too.
func TestPurgeSession_RemovesTerminalRowAndCascades(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Seed a project + a terminated session with a worktree row (a dependent
	// that MUST cascade away).
	seedProject(t, s, "proj-1")
	rec := domain.SessionRecord{
		ID: "sess-1", ProjectID: "proj-1", Kind: domain.KindWorker,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/tmp/x", RuntimeHandleID: "h1"},
	}
	if _, err := s.CreateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{SessionID: "proj-1-1", Branch: "feat/x"}); err != nil {
		t.Fatal(err)
	}

	// CreateSession assigns the real per-project ID; find it via ListSessions
	// since the seed record above passed a placeholder ID that CreateSession
	// ignores.
	got, err := s.ListSessions(ctx, "proj-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("list sessions after create: %+v err=%v", got, err)
	}
	sessID := got[0].ID

	if err := s.PurgeSession(ctx, sessID); err != nil {
		t.Fatalf("PurgeSession: %v", err)
	}

	if _, ok, err := s.GetSession(ctx, sessID); err != nil || ok {
		t.Fatalf("session row still present (ok=%v err=%v)", ok, err)
	}
	rows, err := s.ListSessionWorktrees(ctx, sessID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("worktree rows not cascaded: %d", len(rows))
	}
}
