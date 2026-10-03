package daemon

// This file wires learning capture into daemon startup. For every project with
// learnFromSessions on, a gentle background loop turns the project's Claude Code
// transcripts into redacted excerpts of what the human typed (see
// observe/learncapture and docs/architecture.md, "Learning capture"). It is
// additive like the token-usage observer: it reads transcript files off the
// lifecycle path and never touches a session, and a project with the switch off
// is never opened.

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learncapture"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startLearnCapture launches the capture loop and returns a channel that
// closes when it exits.
func startLearnCapture(ctx context.Context, store *sqlite.Store, reg *looptelemetry.Registry, logger *slog.Logger) <-chan struct{} {
	rec := reg.Register(looptelemetry.Spec{
		Name:        "learn-capture",
		Display:     "Learning capture",
		Description: "Keeps redacted excerpts of what you typed to sessions of projects that learn from sessions.",
		Interval:    learncapture.DefaultTickInterval,
	})
	observer := learncapture.New(store, learncapture.Locator{
		WorkspaceDir: claudecode.WorkspaceTranscriptDir,
		Pinned:       claudecode.PinnedTranscriptPath,
	}, learncapture.Config{Logger: logger, OnTick: rec.Tick})
	return observer.Start(ctx)
}
