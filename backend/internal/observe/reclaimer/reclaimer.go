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
}

// Reclaimer holds the grace clock: first-seen timestamps per candidate session.
type Reclaimer struct {
	svc       reclaimService
	settings  settingsReader
	firstSeen map[domain.SessionID]time.Time
	tick      time.Duration
	clock     func() time.Time
	logger    *slog.Logger
}

// New constructs a Reclaimer.
func New(svc reclaimService, settings settingsReader, cfg Config) *Reclaimer {
	r := &Reclaimer{
		svc:       svc,
		settings:  settings,
		firstSeen: map[domain.SessionID]time.Time{},
		tick:      cfg.Tick,
		clock:     cfg.Clock,
		logger:    cfg.Logger,
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
	return observe.StartPollLoop(ctx, r.tick, r.Tick, r.logger, "reclaimer")
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
	// Drop clock entries for sessions no longer eligible so grace restarts if
	// they return.
	for id := range r.firstSeen {
		if !current[id] {
			delete(r.firstSeen, id)
		}
	}
	for _, id := range candidates {
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
		}
	}
	return nil
}
