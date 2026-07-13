package daemon

// This file wires the per-session token-telemetry observer into daemon startup. On
// a gentle timer it reads each claude-code session's harness transcript, sums the
// real per-message token usage, and persists the totals on the session row (see
// observe/tokenusage) so the board can show a token/cost chip and flag a runaway
// session. It is purely additive: it never touches session lifecycle and cannot
// block teardown (it only reads transcript files off the lifecycle path).

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/tokenusage"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startTokenUsageObserver launches the token-usage observer and returns a channel
// that closes when its loop exits. claudecode.ReadSessionUsage is the transcript
// reader; it reports "no telemetry" for non-claude-code agents, so the observer is
// harness-agnostic and simply skips them (graceful n/a — no chip).
func startTokenUsageObserver(ctx context.Context, store *sqlite.Store, logger *slog.Logger) <-chan struct{} {
	observer := tokenusage.New(store, claudecode.ReadSessionUsage, tokenusage.Config{Logger: logger})
	return observer.Start(ctx)
}
