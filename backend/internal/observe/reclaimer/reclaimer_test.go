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

func newAt(base time.Time, clk *time.Time) func() time.Time {
	return func() time.Time { return *clk }
}

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
