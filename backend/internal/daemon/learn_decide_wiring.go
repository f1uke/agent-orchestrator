package daemon

// This file wires learning's decide stage into daemon startup: a loop that,
// for each finished task with drafts, asks the strong model (through the
// human's own `claude` CLI) what agents should durably learn, checks it with an
// adversarial second call and the code gates, and stores proposals for the
// human. Nothing is applied here (see observe/learndecide and
// docs/architecture.md).

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learndecide"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startLearnDecide launches the decide loop. Without learning settings or a
// home directory it is left off: the loop is nil and the channel closed.
func startLearnDecide(ctx context.Context, store *sqlite.Store, dataDir string, settings *learnsettings.Store, reg *looptelemetry.Registry, logger *slog.Logger) (*learndecide.Observer, <-chan struct{}) {
	home, err := os.UserHomeDir()
	if settings == nil || err != nil {
		done := make(chan struct{})
		close(done)
		return nil, done
	}
	rec := reg.Register(looptelemetry.Spec{
		Name:        "learn-decide",
		Display:     "Learning decide",
		Description: "Turns a finished task's candidate lessons into proposals for you to approve, within the daily budget.",
		Interval:    learndecide.DefaultTickInterval,
	})
	dirs := learndecide.Dirs{
		Home: home, DataDir: dataDir,
		Learned: filepath.Join(home, ".ao", "learned"),
		// Where the standing prompts tell agents the knowledge store is.
		KnowledgeDir: filepath.Join(home, ".ao", "knowledge"),
	}
	observer := learndecide.New(store, llm.ClaudeCLI{Binary: claudecode.ResolveClaudeBinary}, settings.Get, dirs, learndecide.Config{
		Logger: logger, OnTick: rec.Tick,
		Dictionary: func(projects []domain.ProjectRecord) []redact.Value { return learn.Dictionary(projects, os.Environ()) },
	})
	done := observer.Start(ctx)
	return observer, done
}
