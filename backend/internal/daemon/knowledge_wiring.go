package daemon

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/knowledgestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startKnowledgeMigration moves the docs the old data-dir-relative knowledge
// store stranded under <dataDir>/knowledge into cfg.KnowledgeDir, once, in the
// background (it walks each project's git history, which can take seconds). A
// later boot finds nothing left and returns at once. The returned channel
// closes when it is done.
func startKnowledgeMigration(ctx context.Context, cfg config.Config, store *sqlite.Store, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	if cfg.KnowledgeDir == "" || cfg.DataDir == "" {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		report, err := knowledgestore.MigrateStranded(ctx, knowledgestore.MigrateOptions{
			StrandedRoot: filepath.Join(cfg.DataDir, "knowledge"),
			StoreRoot:    cfg.KnowledgeDir,
			RepoPath: func(projectID string) (string, error) {
				rec, ok, err := store.GetProject(ctx, projectID)
				if err != nil || !ok {
					return "", err
				}
				return rec.Path, nil
			},
		})
		for _, e := range report.Entries {
			switch e.Action {
			case knowledgestore.ActionMove:
				log.Info("knowledge store migration: moved stranded doc", "project", e.Project, "from", e.From, "to", e.To)
			case knowledgestore.ActionLeave:
				log.Warn("knowledge store migration: left stranded path in place", "project", e.Project, "path", e.From, "reason", e.Reason)
			}
		}
		if n := len(report.Entries); n > 0 {
			log.Info("knowledge store migration finished",
				"moved", report.Count(knowledgestore.ActionMove),
				"droppedCommitted", report.Count(knowledgestore.ActionDropCommitted),
				"droppedDuplicate", report.Count(knowledgestore.ActionDropDuplicate),
				"left", report.Count(knowledgestore.ActionLeave))
		}
		if err != nil {
			log.Warn("knowledge store migration: partial failure", "err", err)
		}
	}()
	return done
}
