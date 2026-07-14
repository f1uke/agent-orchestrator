package daemon

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/evidenceretention"
	smokesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/smoke"
)

func newTestSweeper(t *testing.T, s evidenceretention.Settings, now time.Time, purge func(context.Context, time.Time) (smokesvc.EvidencePurgeResult, error)) *evidenceSweeper {
	t.Helper()
	store, err := evidenceretention.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(s); err != nil {
		t.Fatal(err)
	}
	return &evidenceSweeper{settings: store, purge: purge, clock: func() time.Time { return now }, log: slog.Default()}
}

func TestEvidenceSweeper_DisabledIsNoOp(t *testing.T) {
	var calls int32
	sw := newTestSweeper(t, evidenceretention.Settings{Enabled: false, MaxAgeDays: 30}, time.Now(),
		func(context.Context, time.Time) (smokesvc.EvidencePurgeResult, error) {
			atomic.AddInt32(&calls, 1)
			return smokesvc.EvidencePurgeResult{Purged: 99}, nil
		})
	purged, freed, err := sw.SweepEvidenceNow(context.Background())
	if err != nil || purged != 0 || freed != 0 {
		t.Fatalf("disabled sweep = (%d, %d, %v), want (0, 0, nil)", purged, freed, err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("purge invoked while retention disabled")
	}
}

func TestEvidenceSweeper_EnabledPurgesWithCutoff(t *testing.T) {
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	var gotCutoff time.Time
	sw := newTestSweeper(t, evidenceretention.Settings{Enabled: true, MaxAgeDays: 30}, base,
		func(_ context.Context, cutoff time.Time) (smokesvc.EvidencePurgeResult, error) {
			gotCutoff = cutoff
			return smokesvc.EvidencePurgeResult{Purged: 2, FreedBytes: 2048}, nil
		})
	purged, freed, err := sw.SweepEvidenceNow(context.Background())
	if err != nil || purged != 2 || freed != 2048 {
		t.Fatalf("sweep = (%d, %d, %v), want (2, 2048, nil)", purged, freed, err)
	}
	if want := base.Add(-30 * 24 * time.Hour); !gotCutoff.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", gotCutoff, want)
	}
}

func TestStartEvidenceRetentionSweep_DisabledClosesImmediately(t *testing.T) {
	var calls int32
	done := startEvidenceRetentionSweep(context.Background(), 0, func(context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}, slog.Default())

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("done channel not closed for a disabled (interval<=0) sweep")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("sweep called %d times when disabled, want 0", atomic.LoadInt32(&calls))
	}
}

func TestStartEvidenceRetentionSweep_RunsImmediatelyThenStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticked := make(chan struct{}, 8)
	done := startEvidenceRetentionSweep(ctx, 5*time.Millisecond, func(context.Context) error {
		select {
		case ticked <- struct{}{}:
		default:
		}
		return nil
	}, slog.Default())

	// The immediate first run fires without waiting for the interval.
	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep was never called")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("done channel not closed after context cancel")
	}
}
