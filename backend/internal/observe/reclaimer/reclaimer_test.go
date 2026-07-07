package reclaimer

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
)

type fakeSvc struct {
	candidates []domain.SessionID
	reclaimed  []domain.SessionID
}

func (f *fakeSvc) ListReclaimable(context.Context) ([]domain.SessionID, error) { return f.candidates, nil }
func (f *fakeSvc) Reclaim(_ context.Context, id domain.SessionID) error {
	f.reclaimed = append(f.reclaimed, id)
	return nil
}

type fakeSettings struct{ s reclaimsettings.Settings }

func (f fakeSettings) Get() reclaimsettings.Settings { return f.s }

func TestTick_ReclaimsOnlyAfterGrace(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []domain.SessionID{"sess-1"}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: 15}},
		Config{Clock: func() time.Time { return now }})

	// First tick: stamps first-seen, does NOT reclaim.
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 0 {
		t.Fatalf("reclaimed too early: %v", svc.reclaimed)
	}

	// Advance past grace, tick again: reclaims.
	now = now.Add(16 * time.Minute)
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 1 || svc.reclaimed[0] != "sess-1" {
		t.Fatalf("want reclaim sess-1, got %v", svc.reclaimed)
	}
}

func TestTick_DisabledSetting_NoReclaim(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []domain.SessionID{"sess-1"}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: false, GraceMinutes: 0}},
		Config{Clock: func() time.Time { return now }})
	_ = r.Tick(context.Background())
	now = now.Add(time.Hour)
	_ = r.Tick(context.Background())
	if len(svc.reclaimed) != 0 {
		t.Fatalf("reclaimed while disabled: %v", svc.reclaimed)
	}
}

func TestTick_CandidateDisappears_ClearsClock(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []domain.SessionID{"sess-1"}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: 15}},
		Config{Clock: func() time.Time { return now }})
	_ = r.Tick(context.Background()) // stamps sess-1

	// sess-1 no longer a candidate; then reappears later — grace restarts.
	svc.candidates = nil
	now = now.Add(20 * time.Minute)
	_ = r.Tick(context.Background())

	svc.candidates = []domain.SessionID{"sess-1"}
	now = now.Add(1 * time.Minute)
	_ = r.Tick(context.Background()) // re-stamp, not reclaim
	if len(svc.reclaimed) != 0 {
		t.Fatalf("grace should have restarted, got %v", svc.reclaimed)
	}
}

// TestTick_Converges guards against the real bug: ListReclaimable (the real
// service) keeps listing an already-reclaimed session forever because Kill
// never clears WorkspacePath/RuntimeHandleID (Restore needs WorkspacePath).
// A static candidate set must therefore be reclaimed exactly once, not once
// per grace period indefinitely.
func TestTick_Converges(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []domain.SessionID{"sess-1"}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: 15}},
		Config{Clock: func() time.Time { return now }})

	_ = r.Tick(context.Background()) // t0: stamps first-seen, no reclaim

	now = now.Add(16 * time.Minute)
	_ = r.Tick(context.Background()) // t0+16m: past grace, reclaims once
	if len(svc.reclaimed) != 1 || svc.reclaimed[0] != "sess-1" {
		t.Fatalf("want single reclaim of sess-1, got %v", svc.reclaimed)
	}

	// The candidate stays listed (Kill doesn't clear the metadata that makes
	// it a candidate). A second grace period passing must NOT reclaim again.
	now = now.Add(16 * time.Minute)
	_ = r.Tick(context.Background()) // t0+32m: must not reclaim again
	if len(svc.reclaimed) != 1 {
		t.Fatalf("reclaimer did not converge: reclaimed %v", svc.reclaimed)
	}
}

// TestTick_ReclaimedThenDisappears_CanReclaimAgain covers the escape hatch:
// once a reclaimed session leaves candidacy (e.g. it was restored, which sets
// fresh runtime/workspace metadata and moves it off merged/terminated status)
// and later finishes again, it must be reclaimable again — the "reclaimed"
// mark is pruned along with firstSeen when a session drops off the candidate
// list.
func TestTick_ReclaimedThenDisappears_CanReclaimAgain(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	svc := &fakeSvc{candidates: []domain.SessionID{"sess-1"}}
	r := New(svc, fakeSettings{reclaimsettings.Settings{Enabled: true, GraceMinutes: 15}},
		Config{Clock: func() time.Time { return now }})

	_ = r.Tick(context.Background()) // stamps
	now = now.Add(16 * time.Minute)
	_ = r.Tick(context.Background()) // reclaims once
	if len(svc.reclaimed) != 1 {
		t.Fatalf("want one reclaim, got %v", svc.reclaimed)
	}

	// Restored: leaves candidacy for a tick.
	svc.candidates = nil
	now = now.Add(1 * time.Minute)
	_ = r.Tick(context.Background())

	// Finishes again: back on the candidate list, grace restarts, and it can
	// be reclaimed again once grace elapses.
	svc.candidates = []domain.SessionID{"sess-1"}
	now = now.Add(1 * time.Minute)
	_ = r.Tick(context.Background()) // re-stamp, not reclaim yet

	now = now.Add(16 * time.Minute)
	_ = r.Tick(context.Background()) // past grace again: reclaims a second time
	if len(svc.reclaimed) != 2 {
		t.Fatalf("want second reclaim after reappearing, got %v", svc.reclaimed)
	}
}
