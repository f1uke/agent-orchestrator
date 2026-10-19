package releasetrain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// BoardStore is what the Board and the human-state commands need from storage.
type BoardStore interface {
	LoadReleaseSnapshot(ctx context.Context) (rt.Snapshot, []rt.Resource, error)
	LoadReleaseResources(ctx context.Context) ([]rt.Resource, error)
	LoadReleaseHumanState(ctx context.Context) (rt.HumanState, error)
	SetReleaseRuleSetting(ctx context.Context, id rt.RuleID, enabled *bool, at time.Time) error
	HideReleaseHit(ctx context.Context, id rt.HitID, h rt.Hide) error
	UnhideReleaseHits(ctx context.Context, ids []rt.HitID) ([]rt.HitID, error)
	ListReleaseLlbPlans(ctx context.Context) ([]rt.LlbPlan, error)
	PutReleaseLlbPlan(ctx context.Context, p rt.LlbPlan, ifVersion int) (rt.LlbPlan, error)
	MarkReleaseCardNoMR(ctx context.Context, key string, m rt.NoMRMark) error
	UnmarkReleaseCardNoMR(ctx context.Context, key string) (bool, error)
}

// BoardDeps wires a BoardService.
type BoardDeps struct {
	Config *ConfigLoader
	Store  BoardStore
	Fixes  FixStore
	// Executors are the effect kinds that really apply; every other is a dry run.
	Executors Executors
	Clock     func() time.Time
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
	key   boardKey
	at    time.Time
	board *rt.Board
	body  []byte
	etag  string
}

type boardKey struct {
	state   ConfigState
	message string
	config  *rt.Config
	rows    string
	human   string
}

// NewBoardService returns a BoardService.
func NewBoardService(d BoardDeps) *BoardService { return &BoardService{d: d} }

// Board returns the serialized BoardResponse and its ETag, or ErrNotModified
// with the ETag when ifNoneMatch already names it.
func (s *BoardService) Board(ctx context.Context, ifNoneMatch string) ([]byte, string, error) {
	c, err := s.current(ctx)
	if err != nil {
		return nil, "", err
	}
	if etagMatches(ifNoneMatch, c.etag) {
		return nil, c.etag, ErrNotModified
	}
	return c.body, c.etag, nil
}

// current returns the cached Board, rebuilt when a row, the human state, the
// config or BoardTTL moved.
func (s *BoardService) current(ctx context.Context) (*cachedBoard, error) {
	cfg := s.d.Config.Load()
	rows, err := s.d.Store.LoadReleaseResources(ctx)
	if err != nil {
		return nil, err
	}
	human, err := s.d.Store.LoadReleaseHumanState(ctx)
	if err != nil {
		return nil, err
	}
	key := boardKey{state: cfg.State, message: cfg.Message, config: cfg.Config, rows: rowsFingerprint(rows), human: humanFingerprint(human)}
	now := s.d.Clock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.cached; c == nil || c.key != key || now.Sub(c.at) >= BoardTTL || now.Before(c.at) {
		board, body, err := s.build(ctx, cfg, human, now)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		s.cached = &cachedBoard{key: key, at: now, board: board, body: body, etag: `"` + hex.EncodeToString(sum[:12]) + `"`}
	}
	return s.cached, nil
}

func (s *BoardService) build(ctx context.Context, cfg ConfigStatus, human rt.HumanState, now time.Time) (*rt.Board, []byte, error) {
	if s.onBuild != nil {
		s.onBuild()
	}
	resp := BoardResponse{Config: ConfigReport{State: cfg.State, Message: cfg.Message, Path: cfg.Path}}
	if cfg.Config != nil {
		snap, _, err := s.d.Store.LoadReleaseSnapshot(ctx)
		if err != nil {
			return nil, nil, err
		}
		b := rt.Reconcile(rt.Inputs{Config: *cfg.Config, Snap: snap, Human: human}, rt.Env{Now: now, Loc: cfg.Loc, AgeOut: cfg.Config.AgeOutDays})
		resp.Board = &b
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return nil, nil, fmt.Errorf("release trains: encode board: %w", err)
	}
	return resp.Board, body, nil
}

func rowsFingerprint(rows []rt.Resource) string {
	h := sha256.New()
	for _, r := range rows {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\n", r.Kind, r.Scope, r.Generation)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func humanFingerprint(h rt.HumanState) string {
	rules := make([]string, 0, len(h.RuleSettings))
	for id, on := range h.RuleSettings {
		rules = append(rules, fmt.Sprintf("%s=%v", id, on))
	}
	hides := make([]string, 0, len(h.Hides))
	for id, hide := range h.Hides {
		hides = append(hides, fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", id, hide.Fingerprint, hide.Severity, hide.Reason, hide.HiddenAt.Format(time.RFC3339Nano)))
	}
	sort.Strings(rules)
	sort.Strings(hides)
	plans := make([]string, len(h.Plans))
	for i, p := range h.Plans {
		plans[i] = fmt.Sprintf("%s@%d", p.ID, p.Version)
	}
	fixes := make([]string, 0, len(h.OpenFixes))
	for hit, f := range h.OpenFixes {
		fixes = append(fixes, fmt.Sprintf("%s\x00%s@%s", hit, f.ID, f.State))
	}
	sort.Strings(fixes)
	noMR := make([]string, 0, len(h.NoMR))
	for key, m := range h.NoMR {
		noMR = append(noMR, fmt.Sprintf("%s\x00%s\x00%s\x00%s", key, m.Note, m.MarkedBy, m.MarkedAt.Format(time.RFC3339Nano)))
	}
	sort.Strings(noMR)
	sum := sha256.Sum256([]byte(strings.Join(rules, "\n") + "\n\n" + strings.Join(hides, "\n") + "\n\n" + strings.Join(plans, "\n") +
		"\n\n" + strings.Join(fixes, "\n") + "\n\n" + strings.Join(noMR, "\n")))
	return hex.EncodeToString(sum[:])
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
