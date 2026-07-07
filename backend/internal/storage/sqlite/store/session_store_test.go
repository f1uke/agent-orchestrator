package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sqlitestore "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// TestPurgeSession_RemovesTerminalRowAndCascades covers the hard-delete path
// used to reclaim finished sessions: the session row itself must disappear,
// and dependent rows without their own explicit cleanup (session_worktrees,
// which relies on the sessions FK's ON DELETE CASCADE) must cascade away too.
// telemetry_event has no FK at all, so it needs its own explicit delete inside
// PurgeSession's transaction; this test also guards against that orphaning.
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

	// Seed a telemetry row referencing this session (no FK, so it would
	// otherwise orphan silently on purge).
	projectID := domain.ProjectID("proj-1")
	if err := s.CreateTelemetryEvent(ctx, sqlitestore.TelemetryEventRecord{
		ID:          "tev_1",
		OccurredAt:  time.Now().UTC().Truncate(time.Second),
		Name:        "ao.session.reclaimed",
		Source:      "daemon",
		Level:       "info",
		ProjectID:   &projectID,
		SessionID:   &sessID,
		RequestID:   "req_1",
		PayloadJSON: `{}`,
	}); err != nil {
		t.Fatalf("CreateTelemetryEvent: %v", err)
	}

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
	tevRows, err := s.ListTelemetryEventsSince(ctx, time.Now().UTC().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tevRows) != 0 {
		t.Fatalf("telemetry_event rows not purged: %+v", tevRows)
	}
}
