package releasetrain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// BoardTTL bounds how long a cached Board is served with no write, because a
// budget crossing turns Clean into Unknown without one.
const BoardTTL = 30 * time.Second

// ErrNotModified means the caller's If-None-Match names the current Board.
var ErrNotModified = errors.New("release trains: board not modified")

// BoardResponse is the body of GET /release-trains/board. Board is absent
// until a valid config has loaded; after a failed edit it is the last good one.
type BoardResponse struct {
	Config ConfigReport `json:"config"`
	Board  *rt.Board    `json:"board,omitempty"`
}

// BoardStore is what the Board read needs from storage.
type BoardStore interface {
	LoadReleaseSnapshot(ctx context.Context) (rt.Snapshot, []rt.Resource, error)
	LoadReleaseResources(ctx context.Context) ([]rt.Resource, error)
}

// BoardDeps wires a BoardService.
type BoardDeps struct {
	Config *ConfigLoader
	Store  BoardStore
	Clock  func() time.Time
}

// BoardService reconciles the stored observations into the Board and caches
// the serialized result until a row, the config or BoardTTL moves.
type BoardService struct {
	d       BoardDeps
	onBuild func()

	mu     sync.Mutex
	cached *cachedBoard
}

type cachedBoard struct {
	key  boardKey
	at   time.Time
	body []byte
	etag string
}

type boardKey struct {
	state   ConfigState
	message string
	config  *rt.Config
	rows    string
}

// NewBoardService returns a BoardService.
func NewBoardService(d BoardDeps) *BoardService { return &BoardService{d: d} }

// Board returns the serialized BoardResponse and its ETag, or ErrNotModified
// with the ETag when ifNoneMatch already names it.
func (s *BoardService) Board(ctx context.Context, ifNoneMatch string) ([]byte, string, error) {
	cfg := s.d.Config.Load()
	rows, err := s.d.Store.LoadReleaseResources(ctx)
	if err != nil {
		return nil, "", err
	}
	key := boardKey{state: cfg.State, message: cfg.Message, config: cfg.Config, rows: rowsFingerprint(rows)}
	now := s.d.Clock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.cached; c == nil || c.key != key || now.Sub(c.at) >= BoardTTL || now.Before(c.at) {
		body, err := s.build(ctx, cfg, now)
		if err != nil {
			return nil, "", err
		}
		sum := sha256.Sum256(body)
		s.cached = &cachedBoard{key: key, at: now, body: body, etag: `"` + hex.EncodeToString(sum[:12]) + `"`}
	}
	if etagMatches(ifNoneMatch, s.cached.etag) {
		return nil, s.cached.etag, ErrNotModified
	}
	return s.cached.body, s.cached.etag, nil
}

func (s *BoardService) build(ctx context.Context, cfg ConfigStatus, now time.Time) ([]byte, error) {
	if s.onBuild != nil {
		s.onBuild()
	}
	resp := BoardResponse{Config: ConfigReport{State: cfg.State, Message: cfg.Message, Path: cfg.Path}}
	if cfg.Config != nil {
		snap, _, err := s.d.Store.LoadReleaseSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		b := rt.Reconcile(rt.Inputs{Config: *cfg.Config, Snap: snap}, rt.Env{Now: now, Loc: cfg.Loc, AgeOut: cfg.Config.AgeOutDays})
		resp.Board = &b
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("release trains: encode board: %w", err)
	}
	return body, nil
}

func rowsFingerprint(rows []rt.Resource) string {
	h := sha256.New()
	for _, r := range rows {
		fmt.Fprintf(h, "%s\x00%s\x00%d\n", r.Kind, r.Scope, r.Generation)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}
