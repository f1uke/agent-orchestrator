// Package reclaimer is the OBSERVE-layer poll loop that auto-reclaims finished
// worker sessions (tear down tmux + worktree, keep branch) once they have sat
// in a merged/terminated state past the configured grace period.
package reclaimer

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
)

// DefaultTickInterval is the poll cadence. Grace is in minutes, so a slow tick
// is fine.
const DefaultTickInterval = time.Minute

type reclaimService interface {
	ListReclaimable(ctx context.Context) ([]domain.SessionID, error)
	Reclaim(ctx context.Context, id domain.SessionID) error
}

type settingsReader interface {
	Get() reclaimsettings.Settings
}

// Config holds optional knobs; zero values fall back to safe defaults.
type Config struct {
	Tick   time.Duration
	Clock  func() time.Time
	Logger *slog.Logger
	// OnTick, when non-nil, fires once before each poll cycle; the daemon's
	// loop-timing seam (see internal/looptelemetry).
	OnTick func()
}

// Reclaimer holds the grace clock: first-seen timestamps per candidate
// session, plus a set of sessions already reclaimed so a stale candidate
// (ListReclaimable keeps listing it because Kill never clears
// WorkspacePath/RuntimeHandleID — and must not, Restore needs WorkspacePath)
// is not reclaimed again on every subsequent tick.
type Reclaimer struct {
	svc       reclaimService
	settings  settingsReader
	firstSeen map[domain.SessionID]time.Time
	reclaimed map[domain.SessionID]bool
	tick      time.Duration
	clock     func() time.Time
	logger    *slog.Logger
	onTick    func()
}

// New constructs a Reclaimer.
func New(svc reclaimService, settings settingsReader, cfg Config) *Reclaimer {
	r := &Reclaimer{
		svc:       svc,
		settings:  settings,
		firstSeen: map[domain.SessionID]time.Time{},
		reclaimed: map[domain.SessionID]bool{},
		tick:      cfg.Tick,
		clock:     cfg.Clock,
		logger:    cfg.Logger,
		onTick:    cfg.OnTick,
	}
	if r.tick <= 0 {
		r.tick = DefaultTickInterval
	}
	if r.clock == nil {
		r.clock = time.Now
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	return r
}

// Start runs the loop until ctx is cancelled; the returned channel closes when
// the loop exits. Mirrors the reaper's shutdown contract.
func (r *Reclaimer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, r.tick, r.Tick, r.logger, "reclaimer", r.onTick)
}

// Tick runs one grace-clock pass. Disabled settings make it a no-op.
func (r *Reclaimer) Tick(ctx context.Context) error {
	set := r.settings.Get()
	if !set.Enabled {
		return nil
	}
	now := r.clock()
	grace := time.Duration(set.GraceMinutes) * time.Minute

	candidates, err := r.svc.ListReclaimable(ctx)
	if err != nil {
		return err
	}
	current := make(map[domain.SessionID]bool, len(candidates))
	for _, id := range candidates {
		current[id] = true
	}
	// Drop clock entries for sessions no longer available so grace restarts if
	// they return. Also drop reclaimed-marks for sessions no longer available:
	// if one later leaves candidacy (e.g. restored) and comes back, it must be
	// reclaimable again.
	for id := range r.firstSeen {
		if !current[id] {
			delete(r.firstSeen, id)
		}
	}
	for id := range r.reclaimed {
		if !current[id] {
			delete(r.reclaimed, id)
		}
	}
	for _, id := range candidates {
		// Kill never clears WorkspacePath/RuntimeHandleID (Restore needs
		// WorkspacePath), so ListReclaimable keeps listing an already-torn-down
		// session as a candidate forever. Skip it here instead so it is not
		// reclaimed (and logged) again every grace period.
		if r.reclaimed[id] {
			continue
		}
		seen, ok := r.firstSeen[id]
		if !ok {
			r.firstSeen[id] = now
			continue
		}
		if now.Sub(seen) >= grace {
			if err := r.svc.Reclaim(ctx, id); err != nil {
				r.logger.Error("reclaimer: reclaim failed", "session", id, "err", err)
				continue
			}
			r.logger.Info("reclaimer: reclaimed finished session", "session", id)
			delete(r.firstSeen, id)
			r.reclaimed[id] = true
		}
	}
	return nil
}
