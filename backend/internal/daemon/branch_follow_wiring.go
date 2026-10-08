package daemon

// This file wires the branch-follow loop: a session whose branch is renamed in
// its worktree is moved onto the new name (see observe/branchfollow).

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/branchfollow"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func startBranchFollow(ctx context.Context, store *sqlite.Store, svc *sessionsvc.Service, reg *looptelemetry.Registry, logger *slog.Logger) <-chan struct{} {
	rec := reg.Register(looptelemetry.Spec{
		Name:        "branch-follow",
		Display:     "Branch follow",
		Description: "Moves a session onto its branch's new name after the branch is renamed in its worktree.",
		Interval:    branchfollow.DefaultTickInterval,
	})
	return branchfollow.New(store, svc, branchfollow.Config{Logger: logger, OnTick: rec.Tick}).Start(ctx)
}
