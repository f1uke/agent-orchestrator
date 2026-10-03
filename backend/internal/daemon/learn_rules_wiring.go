package daemon

// This file wires learning's standing-rules corpus into daemon startup: an
// hourly loop that splits the rules agents are already told (the human's
// CLAUDE.md and skills, each learning project's repo instructions, AO's own
// standing prompt, the knowledge INDEX) into statements through the human's own
// `claude` CLI, caching every chunk by content hash, so a lesson can later be
// checked against them (see observe/learnrules and docs/architecture.md).

import (
	"context"
	"log/slog"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learnrules"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startLearnRules launches the corpus refresh loop. prompts returns AO's
// standing prompts for a project. Without learning settings the loop is left
// off: the returned loop is nil and the channel is already closed.
func startLearnRules(ctx context.Context, store *sqlite.Store, dataDir string, settings *learnsettings.Store,
	prompts func(context.Context, domain.ProjectID) (map[string]string, error), reg *looptelemetry.Registry, logger *slog.Logger,
) (*learnrules.Observer, <-chan struct{}) {
	home, err := os.UserHomeDir()
	if settings == nil || err != nil {
		if err != nil {
			logger.Warn("learn-rules: no home directory; the rules corpus is off", "err", err)
		}
		done := make(chan struct{})
		close(done)
		return nil, done
	}
	rec := reg.Register(looptelemetry.Spec{
		Name:        "learn-rules",
		Display:     "Learning rules",
		Description: "Splits the rules your agents are already told into statements, so a lesson is checked against them.",
		Interval:    learnrules.DefaultTickInterval,
	})
	files := learnrules.Files{Home: home, DataDir: dataDir, Projects: store.ListProjects, Prompts: prompts}
	runner := llm.ClaudeCLI{Binary: claudecode.ResolveClaudeBinary}
	observer := learnrules.New(store, runner, files.List, settings.Get, learnrules.Config{Logger: logger, OnTick: rec.Tick})
	return observer, observer.Start(ctx)
}
