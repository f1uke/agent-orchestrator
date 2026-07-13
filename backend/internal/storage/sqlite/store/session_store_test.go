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

func boolPtr(b bool) *bool { return &b }

// TestSetSessionAutoNudgeRoundTrip covers the per-session nullable override
// for "auto-nudge the worker on unresolved PR comments": a freshly inserted
// session has no opinion (nil, inherit the global default), SetSessionAutoNudge
// can force it on or off, and setting nil again clears the override back to
// inherit. Also covers the not-found path (unknown session id).
func TestSetSessionAutoNudgeRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}

	// Fresh session: no override, inherits the global default.
	got, ok, err := s.GetSession(ctx, r.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.AutoNudgeComments != nil {
		t.Fatalf("fresh session auto-nudge override = %v, want nil", got.AutoNudgeComments)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Force on.
	ok, err = s.SetSessionAutoNudge(ctx, r.ID, boolPtr(true), now)
	if err != nil || !ok {
		t.Fatalf("set true: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.GetSession(ctx, r.ID)
	if got.AutoNudgeComments == nil || *got.AutoNudgeComments != true {
		t.Fatalf("auto-nudge override = %v, want true", got.AutoNudgeComments)
	}

	// Force off.
	ok, err = s.SetSessionAutoNudge(ctx, r.ID, boolPtr(false), now)
	if err != nil || !ok {
		t.Fatalf("set false: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.GetSession(ctx, r.ID)
	if got.AutoNudgeComments == nil || *got.AutoNudgeComments != false {
		t.Fatalf("auto-nudge override = %v, want false", got.AutoNudgeComments)
	}

	// Clear back to inherit.
	ok, err = s.SetSessionAutoNudge(ctx, r.ID, nil, now)
	if err != nil || !ok {
		t.Fatalf("set nil: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.GetSession(ctx, r.ID)
	if got.AutoNudgeComments != nil {
		t.Fatalf("auto-nudge override = %v, want nil after clearing", got.AutoNudgeComments)
	}

	// Unknown session id: not found.
	ok, err = s.SetSessionAutoNudge(ctx, "mer-missing", boolPtr(true), now)
	if err != nil {
		t.Fatalf("set on missing session: %v", err)
	}
	if ok {
		t.Fatal("set on missing session ok=true, want false")
	}
}

// TestSetSessionAutoResolveRoundTrip covers the per-session nullable gate for
// "auto-resolve a review thread when our side replies": a freshly inserted session
// is OFF (nil), SetSessionAutoResolve can force it on or off, setting nil clears it
// back to OFF, and an unknown session id is a no-op (ok=false).
func TestSetSessionAutoResolveRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}

	// Fresh session: OFF (nil).
	got, ok, err := s.GetSession(ctx, r.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.AutoResolveOnReply != nil {
		t.Fatalf("fresh session auto-resolve gate = %v, want nil", got.AutoResolveOnReply)
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Force on.
	ok, err = s.SetSessionAutoResolve(ctx, r.ID, boolPtr(true), now)
	if err != nil || !ok {
		t.Fatalf("set true: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.GetSession(ctx, r.ID)
	if got.AutoResolveOnReply == nil || *got.AutoResolveOnReply != true {
		t.Fatalf("auto-resolve gate = %v, want true", got.AutoResolveOnReply)
	}

	// Force off (explicit false, distinct from nil).
	ok, err = s.SetSessionAutoResolve(ctx, r.ID, boolPtr(false), now)
	if err != nil || !ok {
		t.Fatalf("set false: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.GetSession(ctx, r.ID)
	if got.AutoResolveOnReply == nil || *got.AutoResolveOnReply != false {
		t.Fatalf("auto-resolve gate = %v, want false", got.AutoResolveOnReply)
	}

	// Clear back to nil.
	ok, err = s.SetSessionAutoResolve(ctx, r.ID, nil, now)
	if err != nil || !ok {
		t.Fatalf("set nil: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.GetSession(ctx, r.ID)
	if got.AutoResolveOnReply != nil {
		t.Fatalf("auto-resolve gate = %v, want nil after clearing", got.AutoResolveOnReply)
	}

	// Unknown session id: not found.
	ok, err = s.SetSessionAutoResolve(ctx, "mer-missing", boolPtr(true), now)
	if err != nil {
		t.Fatalf("set on missing session: %v", err)
	}
	if ok {
		t.Fatal("set on missing session ok=true, want false")
	}
}
