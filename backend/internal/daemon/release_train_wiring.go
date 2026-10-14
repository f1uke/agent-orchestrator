package daemon

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/confluencesrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/gitlabsrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/jirasrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type releaseTrains struct {
	*rtsvc.Poller
	*rtsvc.BoardService
	configPath string
}

func (r releaseTrains) InitConfig() (string, error) {
	return r.configPath, rtsvc.WriteExampleConfig(r.configPath)
}

func startReleaseTrains(ctx context.Context, store *sqlite.Store, dataDir string, reg *looptelemetry.Registry, logger *slog.Logger) (releaseTrains, <-chan struct{}) {
	rec := reg.Register(looptelemetry.Spec{
		Name:        "release-trains",
		Display:     "Release trains",
		Description: "Reads the mobile release trains from GitLab, Jira and Confluence, read-only, as release-train.json describes.",
		Interval:    rtsvc.Tick,
	})
	path := filepath.Join(dataDir, rtsvc.ConfigFileName)
	config := rtsvc.NewConfigLoader(path)
	clock := func() time.Time { return time.Now().UTC() }
	poller := rtsvc.NewPoller(rtsvc.PollerDeps{
		Config: config,
		Store:  store,
		Sources: []rtsvc.Source{
			gitlabsrc.New(gitlabsrc.Options{Tokens: gitlabTokenSource}),
			jirasrc.New(jirasrc.Options{}),
			confluencesrc.New(confluencesrc.Options{}),
		},
		Clock:  clock,
		Rand:   rand.Float64, //nolint:gosec // backoff jitter, not security
		Logger: logger,
	})
	board := rtsvc.NewBoardService(rtsvc.BoardDeps{Config: config, Store: store, Clock: clock})
	done := observe.StartPollLoop(ctx, rtsvc.Tick, poller.Poll, logger, "release trains", rec.Tick)
	return releaseTrains{Poller: poller, BoardService: board, configPath: path}, done
}
