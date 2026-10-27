package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/releaseweb"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/confluencesrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/gitlabsrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/jirasrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	sqlitestore "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// ReleaseWebAssetsEnv names the directory of the web bundle when --assets is
// not given.
const ReleaseWebAssetsEnv = "AO_RELEASE_WEB_ASSETS"

// ReleaseWebOptions configures RunReleaseWeb.
type ReleaseWebOptions struct {
	// DataDir is the AO data dir: release-train.json and the app's ao.db sit in
	// it, and the web's own directory sits beside it (releaseweb.DirFor).
	DataDir string
	// Port is where the web listens on 127.0.0.1; releaseweb.DefaultPort when 0.
	Port int
	// AssetsDir is the web bundle; see ResolveReleaseWebAssets.
	AssetsDir string
	Logger    *slog.Logger
}

// ResolveReleaseWebAssets picks the web bundle: the flag, else
// $AO_RELEASE_WEB_ASSETS, else release-web/ beside the ao binary. It fails
// with the flag's name when the directory is missing.
func ResolveReleaseWebAssets(flag string, executable func() (string, error)) (string, error) {
	dir := flag
	if dir == "" {
		dir = os.Getenv(ReleaseWebAssetsEnv)
	}
	if dir == "" {
		exe, err := executable()
		if err != nil {
			return "", fmt.Errorf("release web: find the ao binary to locate the web bundle; pass --assets DIR: %w", err)
		}
		dir = filepath.Join(filepath.Dir(exe), "release-web")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("release web: no web bundle at %s; pass --assets DIR or set %s", dir, ReleaseWebAssetsEnv)
	}
	return dir, nil
}

// RunReleaseWeb runs the team web until ctx is cancelled. It owns a SQLite
// store and a poller of its own, so it works whether or not the daemon runs.
//
// Cost: the daemon polls GitLab, Jira and Confluence on rtsvc.Tick, and so does
// this program. Running both doubles the read load on those hosts; the web
// polls no faster than the app. The human's plans, hides, rule toggles and
// No-MR marks are read from the app's database and never written.
func RunReleaseWeb(ctx context.Context, o ReleaseWebOptions) error {
	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}
	paths := releaseweb.Paths{Dir: releaseweb.DirFor(o.DataDir)}
	if err := paths.Ensure(); err != nil {
		return err
	}
	if _, err := releaseweb.ReadKey(paths); err != nil {
		if errors.Is(err, releaseweb.ErrNoKey) {
			return fmt.Errorf("release web: no key yet; run `ao release-web rotate-key` first")
		}
		return err
	}
	store, err := sqlite.Open(paths.DBDir())
	if err != nil {
		return fmt.Errorf("release web: open its database: %w", err)
	}
	defer func() { _ = store.Close() }()

	runCtx, cancel := context.WithCancel(ctx)
	board, loopDone := startReleaseWebBoard(runCtx, store, o.DataDir, paths, logger)
	port := o.Port
	if port == 0 {
		port = releaseweb.DefaultPort
	}
	err = releaseweb.Serve(runCtx, releaseweb.Options{
		Paths:  paths,
		Board:  board,
		Assets: os.DirFS(o.AssetsDir),
		Addr:   net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Logger: logger,
	})
	cancel()
	<-loopDone
	return err
}

// releaseWebBoard adapts the board service and avatar store to
// releaseweb.BoardSource.
type releaseWebBoard struct {
	board   *rtsvc.BoardService
	avatars *rtsvc.AvatarStore
}

func (b releaseWebBoard) Board(ctx context.Context, ifNoneMatch, _ string) ([]byte, string, error) {
	return b.board.Board(ctx, ifNoneMatch)
}

func (b releaseWebBoard) Avatar(accountID string) ([]byte, error) { return b.avatars.Get(accountID) }

func startReleaseWebBoard(ctx context.Context, web *sqlite.Store, dataDir string, paths releaseweb.Paths, logger *slog.Logger) (releaseWebBoard, <-chan struct{}) {
	config := rtsvc.NewConfigLoader(filepath.Join(dataDir, rtsvc.ConfigFileName))
	avatars := rtsvc.NewAvatarStore(filepath.Join(paths.Dir, "avatars"))
	clock := func() time.Time { return time.Now().UTC() }
	poller := rtsvc.NewPoller(rtsvc.PollerDeps{
		Config: config,
		Store:  web,
		Sources: []rtsvc.Source{
			gitlabsrc.New(gitlabsrc.Options{Tokens: gitlabTokenSource}),
			jirasrc.New(jirasrc.Options{Avatars: avatars}),
			confluencesrc.New(confluencesrc.Options{}),
		},
		Clock:  clock,
		Rand:   rand.Float64, //nolint:gosec // backoff jitter, not security
		Logger: logger,
	})
	store := newReleaseWebStore(web, &appHumanState{path: filepath.Join(dataDir, "ao.db"), log: logger})
	svc := rtsvc.NewBoardService(rtsvc.BoardDeps{Config: config, Store: store, Fixes: web, Clock: clock})
	done := observe.StartPollLoop(ctx, rtsvc.Tick, poller.Poll, logger, "release web", nil)
	return releaseWebBoard{board: svc, avatars: avatars}, done
}

// ErrReleaseWebReadOnly is what every write on the team web's store returns.
var ErrReleaseWebReadOnly = errors.New("release web: read-only")

// humanStateReader is where the human's state comes from.
type humanStateReader interface {
	LoadReleaseHumanState(ctx context.Context) (rt.HumanState, error)
}

// releaseWebStore is the BoardStore of the team web: the snapshot and the
// resources come from the web's own database, the human state from the app's,
// and every write is refused.
type releaseWebStore struct {
	web   *sqlite.Store
	human humanStateReader
}

var _ rtsvc.BoardStore = releaseWebStore{}

func newReleaseWebStore(web *sqlite.Store, human humanStateReader) releaseWebStore {
	return releaseWebStore{web: web, human: human}
}

func (s releaseWebStore) LoadReleaseSnapshot(ctx context.Context) (rt.Snapshot, []rt.Resource, error) {
	return s.web.LoadReleaseSnapshot(ctx)
}

func (s releaseWebStore) LoadReleaseResources(ctx context.Context) ([]rt.Resource, error) {
	return s.web.LoadReleaseResources(ctx)
}

func (s releaseWebStore) LoadReleaseHumanState(ctx context.Context) (rt.HumanState, error) {
	return s.human.LoadReleaseHumanState(ctx)
}

func (releaseWebStore) SetReleaseRuleSetting(context.Context, rt.RuleID, *bool, time.Time) error {
	return ErrReleaseWebReadOnly
}

func (releaseWebStore) HideReleaseHit(context.Context, rt.HitID, rt.Hide) error {
	return ErrReleaseWebReadOnly
}

func (releaseWebStore) UnhideReleaseHits(context.Context, []rt.HitID) ([]rt.HitID, error) {
	return nil, ErrReleaseWebReadOnly
}

func (s releaseWebStore) ListReleaseLlbPlans(ctx context.Context) ([]rt.LlbPlan, error) {
	human, err := s.human.LoadReleaseHumanState(ctx)
	return human.Plans, err
}

func (releaseWebStore) PutReleaseLlbPlan(context.Context, rt.LlbPlan, int) (rt.LlbPlan, error) {
	return rt.LlbPlan{}, ErrReleaseWebReadOnly
}

func (releaseWebStore) MarkReleaseCardNoMR(context.Context, string, rt.NoMRMark) error {
	return ErrReleaseWebReadOnly
}

func (releaseWebStore) UnmarkReleaseCardNoMR(context.Context, string) (bool, error) {
	return false, ErrReleaseWebReadOnly
}

// appHumanState reads the human state from the app's ao.db through a read-only
// connection: no migration, no write. The file may appear after the web
// started, so it is opened on first use. While it is absent or unreadable the
// state is empty, and the cause is logged once until a read succeeds again.
type appHumanState struct {
	path string
	log  *slog.Logger

	mu     sync.Mutex
	store  *sqlitestore.Store
	warned bool
}

func (a *appHumanState) LoadReleaseHumanState(ctx context.Context) (rt.HumanState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	store, err := a.open()
	if err != nil {
		a.warn(err)
		return emptyHumanState(), nil
	}
	human, err := store.LoadReleaseHumanState(ctx)
	if err != nil {
		a.warn(err)
		return emptyHumanState(), nil
	}
	a.warned = false
	return human, nil
}

func (a *appHumanState) open() (*sqlitestore.Store, error) {
	if a.store != nil {
		return a.store, nil
	}
	if _, err := os.Stat(a.path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("the app database %s does not exist yet", a.path)
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+a.path+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	a.store = sqlitestore.NewStore(db, db)
	return a.store, nil
}

func (a *appHumanState) warn(err error) {
	if a.warned {
		return
	}
	a.warned = true
	a.log.Warn("release web: the human's plans, hides and marks are not shown", "err", err)
}

func emptyHumanState() rt.HumanState {
	return rt.HumanState{
		RuleSettings: map[rt.RuleID]bool{},
		Hides:        map[rt.HitID]rt.Hide{},
		OpenFixes:    map[rt.HitID]rt.FixRef{},
		NoMR:         map[string]rt.NoMRMark{},
	}
}
