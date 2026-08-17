package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reclaimer"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimlog"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// startReclaimer launches the auto-reclaim poll loop. The returned channel
// closes when the loop exits (ctx cancel), mirroring the reaper's contract.
func startReclaimer(ctx context.Context, sessions *sessionsvc.Service, settings *reclaimsettings.Store, audit *reclaimlog.Writer, worktreeRoot string, reg *looptelemetry.Registry, log *slog.Logger) <-chan struct{} {
	rec := reg.Register(looptelemetry.Spec{
		Name:        "reclaimer",
		Display:     "Session auto-reclaim",
		Description: "Tears down finished worker sessions (tmux + worktree) past their grace period.",
		Interval:    reclaimer.DefaultTickInterval,
	})
	log.Info("auto-reclaim configured", "settings", settings.Get(), "log", audit.Path())
	return reclaimer.New(sessions, settings, reclaimer.Config{
		Logger:       log,
		Audit:        audit,
		WorktreeRoot: worktreeRoot,
		Paths:        sessions,
		OnTick:       rec.Tick,
	}).Start(ctx)
}
