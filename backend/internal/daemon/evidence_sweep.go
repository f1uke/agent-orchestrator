package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/evidenceretention"
	smokesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/smoke"
)

// evidenceSweepIntervalDefault is how often the daemon re-checks for expired
// smoke-test evidence while running. Retention is measured in days, so a coarse
// cadence is plenty; the TTL (which decides WHAT expires) is a separate setting.
const evidenceSweepIntervalDefault = 6 * time.Hour

// evidenceSweeper reads the current retention policy and purges expired evidence
// (rows + blobs + export copies). It is the single sweep both the periodic
// background job and the manual-trigger REST endpoint invoke, so the two can
// never diverge. A disabled policy (or a non-positive TTL) is a no-op.
type evidenceSweeper struct {
	settings *evidenceretention.Store
	purge    func(context.Context, time.Time) (smokesvc.EvidencePurgeResult, error)
	clock    func() time.Time
	log      *slog.Logger
}

// SweepEvidenceNow runs one retention pass. It resolves the (clamped) cutoff from
// settings, no-ops when retention is disabled, purges everything older, and logs
// what it removed. The clamp guards against a misconfigured tiny TTL: a value
// below the floor is raised to it (and logged) so a fat-fingered setting can
// never wipe recent evidence.
func (e *evidenceSweeper) SweepEvidenceNow(ctx context.Context) (int, int64, error) {
	set := e.settings.Get()
	cutoff, ok := set.Cutoff(e.clock())
	if !ok {
		return 0, 0, nil // retention disabled → keep forever
	}
	if eff := evidenceretention.ClampDays(set.MaxAgeDays); eff != set.MaxAgeDays {
		e.log.Warn("evidence retention TTL clamped to a safe range",
			"requested_days", set.MaxAgeDays, "effective_days", eff)
	}
	res, err := e.purge(ctx, cutoff)
	if err != nil {
		return 0, 0, err
	}
	if res.Purged > 0 {
		e.log.Info("evidence retention sweep purged expired evidence",
			"count", res.Purged, "freed_bytes", res.FreedBytes, "cutoff", cutoff.Format(time.RFC3339))
	}
	return res.Purged, res.FreedBytes, nil
}

// startEvidenceRetentionSweep launches a background goroutine that runs sweep
// immediately (to reclaim evidence accumulated before this boot) and then on
// every tick until ctx is cancelled, returning a channel closed when the
// goroutine exits so daemon shutdown can drain it (mirroring startIdleSweep). A
// non-positive interval disables the loop: the channel is already closed and
// sweep is never called.
func startEvidenceRetentionSweep(ctx context.Context, interval time.Duration, sweep func(context.Context) error, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		if ctx.Err() != nil {
			return
		}
		if err := sweep(ctx); err != nil {
			log.Warn("evidence retention sweep failed", "err", err)
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := sweep(ctx); err != nil {
					log.Warn("evidence retention sweep failed", "err", err)
				}
			}
		}
	}()
	return done
}
