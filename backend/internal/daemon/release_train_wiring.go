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
	avatars    *rtsvc.AvatarStore
}

func (r releaseTrains) Avatar(accountID string) ([]byte, error) { return r.avatars.Get(accountID) }

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
	avatars := rtsvc.NewAvatarStore(filepath.Join(dataDir, "release-train", "avatars"))
	clock := func() time.Time { return time.Now().UTC() }
	poller := rtsvc.NewPoller(rtsvc.PollerDeps{
		Config: config,
		Store:  store,
		Sources: []rtsvc.Source{
			gitlabsrc.New(gitlabsrc.Options{Tokens: gitlabTokenSource}),
			jirasrc.New(jirasrc.Options{Avatars: avatars}),
			confluencesrc.New(confluencesrc.Options{}),
		},
		Clock:  clock,
		Rand:   rand.Float64, //nolint:gosec // backoff jitter, not security
		Logger: logger,
	})
	board := rtsvc.NewBoardService(rtsvc.BoardDeps{Config: config, Store: store, Fixes: store, Clock: clock})
	board.UsePlanTargetExecutor()
	pass := func(ctx context.Context) error {
		if err := poller.Poll(ctx); err != nil {
			return err
		}
		return board.SupersedeFixes(ctx)
	}
	done := observe.StartPollLoop(ctx, rtsvc.Tick, pass, logger, "release trains", rec.Tick)
	return releaseTrains{Poller: poller, BoardService: board, configPath: path, avatars: avatars}, done
}
