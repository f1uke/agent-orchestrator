package daemon

// This file wires learning's collect stage into daemon startup: a background
// loop that hands captured, redacted human turns to a model through the human's
// own `claude` CLI and keeps the candidate lessons that survive the grounding
// checks (see observe/learncollect and docs/architecture.md, "Learning
// capture"). It only ever reads excerpts of projects that learn from sessions,
// and it stops for the day at the budget in learning-settings.json.

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learncollect"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learnrules"
	learningsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startLearnCollect launches the collect loop. It returns the loop and its
// settings for the API, and a channel that closes when the loop exits. When the
// settings cannot be opened the stage is left off: the returned loop and
// settings are nil and the channel is already closed.
func startLearnCollect(ctx context.Context, store *sqlite.Store, dataDir string, reg *looptelemetry.Registry, logger *slog.Logger) (*learncollect.Observer, *learnsettings.Store, <-chan struct{}) {
	settings, err := learnsettings.NewStore(dataDir)
	if err != nil {
		logger.Warn("learn-collect: settings unavailable; collect is off", "err", err)
		done := make(chan struct{})
		close(done)
		return nil, nil, done
	}
	rec := reg.Register(looptelemetry.Spec{
		Name:        "learn-collect",
		Display:     "Learning collect",
		Description: "Asks a model which of your captured turns teach something durable, within the daily budget.",
		Interval:    learncollect.DefaultTickInterval,
	})
	runner := llm.ClaudeCLI{Binary: claudecode.ResolveClaudeBinary}
	observer := learncollect.New(store, runner, settings.Get, learncollect.Config{Logger: logger, OnTick: rec.Tick})
	done := observer.Start(ctx)
	return observer, settings, done
}

// learningService builds the learning API service, with the collect stage and
// the rules corpus when they are running. A nil loop must not reach the
// service as a typed nil in a non-nil interface, so each is only attached when
// present.
func learningService(ctx context.Context, store *sqlite.Store, collector *learncollect.Observer, settings *learnsettings.Store, rules *learnrules.Observer) *learningsvc.Service {
	svc := learningsvc.New(store, claudecode.IsTranscriptPath)
	if rules != nil {
		svc = svc.WithRules(ctx, store, rules)
	}
	if collector == nil || settings == nil {
		return svc
	}
	return svc.WithCollect(ctx, collector, settings)
}
