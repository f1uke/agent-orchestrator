package sim

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// This file is package sim (white-box), not sim_test, on purpose: the test
// below asserts directly on the size of the in-memory pending stash, which is
// unexported. The leak under test is exactly that field, so measuring it
// beats inferring it from the recording's steps.

type pendingLeakScreenReader struct{}

func (pendingLeakScreenReader) AX(_ context.Context, _ string) (simbridge.Snapshot, error) {
	return simbridge.Snapshot{}, nil
}

// A client that dies mid-gesture never releases its hold, so nothing ever
// deletes its stashed step. The hold itself recovers because its TTL lapses in
// the database; the stash needs the same lifetime or it grows forever in a
// daemon that stays up for weeks.
func TestAcquireHold_StashDoesNotOutliveTheHoldItBelongsTo(t *testing.T) {
	const udid = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"
	start := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	clock := start

	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", RegisteredAt: start,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: start},
		CreatedAt: start,
		UpdatedAt: start,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	owner := session.ID

	svc := New(store, WithClock(func() time.Time { return clock }), WithRecorder(pendingLeakScreenReader{}))
	if _, err := svc.Acquire(ctx, owner, udid, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.StartRecording(ctx, owner, udid, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	// Acquire a hold and never release it - the client died mid-gesture.
	first, err := svc.AcquireHold(ctx, owner, udid, MinHoldTTL, GestureIntent{Kind: "tap"})
	if err != nil {
		t.Fatalf("first hold: %v", err)
	}
	if n := len(svc.pending); n != 1 {
		t.Fatalf("pending = %d right after the first acquire, want 1", n)
	}

	// Advance the service clock past that hold's expiry - the same way its TTL
	// lapses in the database, letting a second acquire through.
	clock = first.ExpiresAt.Add(time.Second)

	if _, err := svc.AcquireHold(ctx, owner, udid, MinHoldTTL, GestureIntent{Kind: "tap"}); err != nil {
		t.Fatalf("second hold: %v", err)
	}

	if _, stillThere := svc.pending[first.Token]; stillThere {
		t.Fatal("the first hold's stash entry must not outlive the hold it belongs to")
	}
	if n := len(svc.pending); n != 1 {
		t.Fatalf("pending = %d after the second acquire, want 1 (only the second hold's own entry)", n)
	}
}
