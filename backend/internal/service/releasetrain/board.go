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

// MaxCachedBoards bounds how many people's Boards are kept; past it the cache starts over.
const MaxCachedBoards = 32

// ErrNotModified means the caller's If-None-Match names the current Board.
var ErrNotModified = errors.New("release trains: board not modified")

// BoardResponse is the body of GET /release-trains/board. Board is absent
// until a valid config has loaded; after a failed edit it is the last good one.
type BoardResponse struct {
	Config ConfigReport `json:"config"`
	// People is the state of the optional people file that joins Jira and GitLab accounts.
	People PeopleReport `json:"people"`
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
	// People is optional; without it no accounts are linked by hand.
	People *PeopleLoader
	Store  BoardStore
	Fixes  FixStore
	// Executors are the effect kinds that really apply; every other is a dry run.
	Executors Executors
	Clock     func() time.Time
	// HideFileDetails leaves the config and people files' paths and messages
	// out of the response, keeping their states, for viewers who must not
	// learn the host's file system.
	HideFileDetails bool
}

// BoardService reconciles the stored observations into the Board and caches
// the serialized result of each person's view until a row, the config, the
// people file or BoardTTL moves.
type BoardService struct {
	d       BoardDeps
	onBuild func()

	mu     sync.Mutex
	cached map[string]*cachedBoard
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
	people  string
}

// NewBoardService returns a BoardService.
func NewBoardService(d BoardDeps) *BoardService {
	return &BoardService{d: d, cached: map[string]*cachedBoard{}}
}

// Board returns the serialized BoardResponse and its ETag, or ErrNotModified
// with the ETag when ifNoneMatch already names it. Its Mine is person's: a
// person id as the Board's people list gives it, or empty for the monitor's own.
func (s *BoardService) Board(ctx context.Context, ifNoneMatch, person string) ([]byte, string, error) {
	c, err := s.current(ctx, person)
	if err != nil {
		return nil, "", err
	}
	if etagMatches(ifNoneMatch, c.etag) {
		return nil, c.etag, ErrNotModified
	}
	return c.body, c.etag, nil
}

// current returns person's cached Board, rebuilt when a row, the human state,
// the config, the people file or BoardTTL moved.
func (s *BoardService) current(ctx context.Context, person string) (*cachedBoard, error) {
	cfg := s.d.Config.Load()
	people := PeopleStatus{PeopleReport: PeopleReport{State: PeopleAbsent}}
	if s.d.People != nil {
		people = s.d.People.Load()
	}
	rows, err := s.d.Store.LoadReleaseResources(ctx)
	if err != nil {
		return nil, err
	}
	human, err := s.d.Store.LoadReleaseHumanState(ctx)
	if err != nil {
		return nil, err
	}
	key := boardKey{state: cfg.State, message: cfg.Message, config: cfg.Config, rows: rowsFingerprint(rows), human: humanFingerprint(human), people: peopleFingerprint(people)}
	now := s.d.Clock()

	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cached[person]
	if !ok || c.key != key || now.Sub(c.at) >= BoardTTL || now.Before(c.at) {
		board, body, err := s.build(ctx, cfg, people, human, person, now)
		if err != nil {
			return nil, err
		}
		if !ok && len(s.cached) >= MaxCachedBoards {
			clear(s.cached)
		}
		sum := sha256.Sum256(body)
		c = &cachedBoard{key: key, at: now, board: board, body: body, etag: `"` + hex.EncodeToString(sum[:12]) + `"`}
		s.cached[person] = c
	}
	return c, nil
}

func (s *BoardService) build(ctx context.Context, cfg ConfigStatus, people PeopleStatus, human rt.HumanState, person string, now time.Time) (*rt.Board, []byte, error) {
	if s.onBuild != nil {
		s.onBuild()
	}
	resp := BoardResponse{Config: ConfigReport{State: cfg.State, Message: cfg.Message, Path: cfg.Path}, People: people.PeopleReport}
	if s.d.HideFileDetails {
		resp.Config = ConfigReport{State: cfg.State}
		resp.People = PeopleReport{State: people.State}
	}
	if cfg.Config != nil {
		snap, _, err := s.d.Store.LoadReleaseSnapshot(ctx)
		if err != nil {
			return nil, nil, err
		}
		b := rt.Reconcile(rt.Inputs{Config: *cfg.Config, Snap: snap, Human: human, Links: people.Links, Viewer: person}, rt.Env{Now: now, Loc: cfg.Loc, AgeOut: cfg.Config.AgeOutDays})
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
