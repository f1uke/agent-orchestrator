package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reclaimer"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// startReclaimer launches the auto-reclaim poll loop. The returned channel
// closes when the loop exits (ctx cancel), mirroring the reaper's contract.
func startReclaimer(ctx context.Context, sessions *sessionsvc.Service, settings *reclaimsettings.Store, reg *looptelemetry.Registry, log *slog.Logger) <-chan struct{} {
	rec := reg.Register(looptelemetry.Spec{
		Name:        "reclaimer",
		Display:     "Session auto-reclaim",
		Description: "Tears down finished worker sessions (tmux + worktree) past their grace period.",
		Interval:    reclaimer.DefaultTickInterval,
	})
	return reclaimer.New(sessions, settings, reclaimer.Config{Logger: log, OnTick: rec.Tick}).Start(ctx)
}
