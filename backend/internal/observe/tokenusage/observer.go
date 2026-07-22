// Package tokenusage implements the background observer that reads each
// claude-code session's harness transcript, sums the real per-message token usage,
// and persists per-session totals on the session row. It exists because AO recorded
// zero token telemetry: a runaway session (one worker hit ~990M tokens) was
// invisible in-app. The observer is deliberately additive — it never touches session
// lifecycle and never blocks teardown (it only reads transcript files off the
// lifecycle path, on a timer), and a parse/persist failure is logged and skipped so
// telemetry can never break a session.
package tokenusage

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
)

// DefaultTickInterval is the refresh cadence. Token totals are visibility data, not
// control signals, so a gentle minute-scale poll is plenty: it refreshes a live
// session's numbers roughly once a minute and finalizes a session shortly after it
// ends. Re-reading a transcript is streaming I/O off the hot path.
const DefaultTickInterval = 60 * time.Second

// Store is the persistence contract: enumerate sessions to consider and write a
// session's parsed token totals.
type Store interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	SetSessionTokenUsage(ctx context.Context, id domain.SessionID, usage domain.TokenUsage, parsedAt time.Time) (bool, error)
}

// UsageReader sums a session's token usage from its harness transcript. ok=false
// means there is no telemetry for this session (a non-claude-code agent whose
// transcript AO cannot read, or a session with no transcript on disk yet); the
// observer skips it so those sessions degrade gracefully to "no chip". A returned
// error is a real read/parse failure the observer logs and skips.
type UsageReader func(rec domain.SessionRecord) (domain.TokenUsage, bool, error)

// Config holds optional observer knobs; zero values use production defaults.
type Config struct {
	// Tick is the refresh cadence. Zero uses DefaultTickInterval.
	Tick time.Duration
	// Clock supplies the parsed-at stamp. Nil uses time.Now.
	Clock func() time.Time
	// Logger receives parse/persist diagnostics. Nil uses slog.Default.
	Logger *slog.Logger
	// OnTick, when non-nil, fires once before each poll cycle; the daemon's
	// loop-timing seam (see internal/looptelemetry).
	OnTick func()
}

// Observer polls sessions and refreshes their token telemetry.
type Observer struct {
	store  Store
	read   UsageReader
	tick   time.Duration
	clock  func() time.Time
	logger *slog.Logger
	onTick func()
}

// New constructs an Observer. read locates + sums a session's transcript usage.
func New(store Store, read UsageReader, cfg Config) *Observer {
	o := &Observer{
		store:  store,
		read:   read,
		tick:   cfg.Tick,
		clock:  cfg.Clock,
		logger: cfg.Logger,
		onTick: cfg.OnTick,
	}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start launches the poll loop and returns a channel that closes when it exits.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.poll, o.logger, "token-usage observer", o.onTick)
}

// poll refreshes token telemetry for every available session. It never returns on a
// per-session parse/persist failure — one bad transcript must not stall the others
// or kill the loop. It returns an error only when the session enumeration itself
// fails (so StartPollLoop logs it and retries next tick).
func (o *Observer) poll(ctx context.Context) error {
	if o.read == nil {
		return nil
	}
	sessions, err := o.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	for _, rec := range sessions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !shouldParse(rec) {
			continue
		}
		usage, ok, err := o.read(rec)
		if err != nil {
			o.logger.Warn("token-usage observer: parse failed", "session", rec.ID, "err", err)
			continue
		}
		if !ok {
			continue
		}
		if _, err := o.store.SetSessionTokenUsage(ctx, rec.ID, usage, o.clock()); err != nil {
			o.logger.Warn("token-usage observer: persist failed", "session", rec.ID, "err", err)
		}
	}
	return nil
}

// shouldParse decides — from the session row alone, with NO file I/O — whether this
// session is worth (re)parsing this tick, so a large transcript is only read when
// there is a reason to.
//
//   - Only claude-code sessions with a materialized workspace have a transcript AO
//     can read; everything else is skipped (graceful n/a).
//   - A live (running) session keeps accumulating turns, so it refreshes every tick.
//   - A terminated/suspended session is parsed ONE more time to capture its final
//     turns, then left finalized: we still owe a parse exactly when the last parse
//     predates the session's last activity (its terminate/suspend stamp). A restore
//     bumps activity forward again, which re-arms this naturally. A never-parsed
//     terminated session has a zero TokensUpdatedAt, which is before any real
//     activity time, so it is parsed once.
func shouldParse(rec domain.SessionRecord) bool {
	if rec.Harness != domain.HarnessClaudeCode || rec.Metadata.WorkspacePath == "" {
		return false
	}
	if !rec.IsTerminated && !rec.IsSuspended {
		return true
	}
	return rec.TokensUpdatedAt.Before(rec.Activity.LastActivityAt)
}
