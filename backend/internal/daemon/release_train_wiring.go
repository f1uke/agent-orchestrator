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
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/gitlabsrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/jirasrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type releaseTrains struct {
	*rtsvc.Poller
	configPath string
}

func (r releaseTrains) InitConfig() (string, error) {
	return r.configPath, rtsvc.WriteExampleConfig(r.configPath)
}

func startReleaseTrains(ctx context.Context, store *sqlite.Store, dataDir string, reg *looptelemetry.Registry, logger *slog.Logger) (releaseTrains, <-chan struct{}) {
	rec := reg.Register(looptelemetry.Spec{
		Name:        "release-trains",
		Display:     "Release trains",
		Description: "Reads the mobile release trains from GitLab and Jira, read-only, as release-train.json describes.",
		Interval:    rtsvc.Tick,
	})
	path := filepath.Join(dataDir, rtsvc.ConfigFileName)
	poller := rtsvc.NewPoller(rtsvc.PollerDeps{
		Config: rtsvc.NewConfigLoader(path),
		Store:  store,
		Sources: []rtsvc.Source{
			gitlabsrc.New(gitlabsrc.Options{Tokens: gitlabTokenSource}),
			jirasrc.New(jirasrc.Options{}),
		},
		Clock:  func() time.Time { return time.Now().UTC() },
		Rand:   rand.Float64, //nolint:gosec // backoff jitter, not security
		Logger: logger,
	})
	done := observe.StartPollLoop(ctx, rtsvc.Tick, poller.Poll, logger, "release trains", rec.Tick)
	return releaseTrains{Poller: poller, configPath: path}, done
}
