package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reclaimer"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// startReclaimer launches the auto-reclaim poll loop. The returned channel
// closes when the loop exits (ctx cancel), mirroring the reaper's contract.
func startReclaimer(ctx context.Context, sessions *sessionsvc.Service, settings *reclaimsettings.Store, log *slog.Logger) <-chan struct{} {
	return reclaimer.New(sessions, settings, reclaimer.Config{Logger: log}).Start(ctx)
}
