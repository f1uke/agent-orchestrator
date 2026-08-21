// Package crewrun owns the BRACKET a crew member puts around a build, a test
// suite or a device pass - `ao crew run --start` … `ao crew run --end` - and the
// verdict the tree-write detector reaches about it.
//
// One mechanism, two consumers. The detector needs the bracket (a write-counter
// reading at each end, equal means nothing moved), and while a bracket is open
// it is also the only thing that can tell the board "qa is running a build"
// rather than the much weaker "qa is awake": domain.ActivityState cannot tell a
// build from an agent reading a file. Building the bracket for the detector gets
// the status for free, with nothing to keep in step.
package crewrun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/treewatch"
)

// ErrInvalid and ErrNotFound are the service sentinels the HTTP controller maps
// to 422 and 404.
var (
	ErrInvalid  = errors.New("crewrun: invalid request")
	ErrNotFound = errors.New("crewrun: not found")
)

// historyDepth is how many runs the Tests tab is shown.
const historyDepth = 20

// Store is the persistence surface this service owns.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	InsertCrewRun(ctx context.Context, run domain.CrewRun) error
	EndCrewRun(ctx context.Context, run domain.CrewRun) (domain.CrewRun, bool, error)
	GetCrewRun(ctx context.Context, id string) (domain.CrewRun, bool, error)
	ListCrewRunsForSession(ctx context.Context, id domain.SessionID, limit int) ([]domain.CrewRun, error)
	OpenCrewRunForSession(ctx context.Context, id domain.SessionID) (domain.CrewRun, bool, error)
	ConsecutiveCrewRunDiscards(ctx context.Context, id domain.SessionID) (int, error)
	OpenCrewRunsForCrewmates(ctx context.Context, crewID, self domain.SessionID) ([]domain.CrewRun, error)
	AbandonOpenCrewRuns(ctx context.Context, now time.Time) (int, error)
}

// Watcher is the tree-write detector. *treewatch.Registry satisfies it.
type Watcher interface {
	Attach(ctx context.Context, root string) (*treewatch.Lease, error)
}

// Manager is the surface the HTTP controller depends on.
type Manager interface {
	Start(ctx context.Context, sessionID domain.SessionID, in StartInput) (StartResult, error)
	End(ctx context.Context, sessionID domain.SessionID, in EndInput) (EndResult, error)
	List(ctx context.Context, sessionID domain.SessionID) ([]domain.CrewRun, error)
}

// StartInput is what the member says about the run it is about to make.
type StartInput struct {
	Kind  domain.CrewRunKind
	Label string
}

// StartResult is the open run plus the one thing the member has to be told
// before it begins: whether anything is actually watching.
type StartResult struct {
	Run domain.CrewRun `json:"run"`
	// Certified is false when the detector could not be established. The run may
	// still go ahead - refusing to let qa build would be worse - but its result
	// will be marked uncertified rather than passed off as verified.
	Certified bool `json:"certified"`
	// SupersededRunID names a run this start had to abandon because it was left
	// open (an agent that never called --end, a daemon that went away). It is
	// reported rather than swallowed: a run thrown away silently is no better
	// than a mixed result reported as clean.
	SupersededRunID string `json:"supersededRunId,omitempty"`
	// CrewmateRun is a run the OTHER member of this task already has open in the
	// same worktree. ADVISORY, and nothing here waits or refuses on it: this run
	// is allowed to start, and whether its result survives is the detector's call
	// as always.
	//
	// It is worth saying at all because of one case the design left open and this
	// does not close: two concurrent `xcodebuild` runs against ONE shared
	// DerivedData. The shared cache is the whole reason a crew has one worktree,
	// and Xcode's own locking either handles two builders or it does not - which
	// is unverified. Naming it is what lets a member choose to wait, and lets a
	// human recognise the failure if it ever happens.
	CrewmateRun *domain.CrewRun `json:"crewmateRun,omitempty"`
}

// EndInput closes a bracket. RunID is optional: with it empty the session's open
// run is closed, which is what the CLI does.
type EndInput struct {
	RunID  string
	Result domain.CrewRunResult
}

// EndResult is the closed run and what the member should do about it.
type EndResult struct {
	Run domain.CrewRun `json:"run"`
	// Retry says the run was discarded and an automatic re-run is still within
	// the cap, so the member should simply go again.
	Retry bool `json:"retry"`
	// Attempt is this run's position in the current discard streak, and
	// MaxAttempts is domain.CappedRepeat.
	Attempt     int `json:"attempt"`
	MaxAttempts int `json:"maxAttempts"`
	// Escalated says the cap is spent: the member must stop re-running and the
	// task parks at NEEDS YOU for a human to decide.
	Escalated bool `json:"escalated"`
}

// Options configures a Service.
type Options struct {
	Store     Store
	Watcher   Watcher
	GitBinary string
	Logger    *slog.Logger
	Now       func() time.Time
}

// Service implements Manager.
type Service struct {
	store   Store
	watcher Watcher
	git     string
	logger  *slog.Logger
	now     func() time.Time

	// leases holds the live watch for each OPEN run. It is in memory on purpose:
	// a lease cannot outlive the process that holds it, so a run whose lease is
	// missing at --end (the daemon restarted) is one nothing was watching, and it
	// ends uncertified. That is the honest answer, and it falls out of the shape
	// rather than needing a check.
	mu     sync.Mutex
	leases map[string]*treewatch.Lease

	// sessionMu serialises Start/End per SESSION. Without it a start is a
	// check-then-act - read the open run, supersede it, insert a new one - so two
	// that arrive together can both find no open run and both insert, leaving one
	// of them open forever and the board claiming a build is running that nothing
	// is running. The review engine's lockWorker is the same shape for the same
	// reason.
	sessionsMu sync.Mutex
	sessionMu  map[domain.SessionID]*sync.Mutex
}

// New builds the service. A nil Watcher is legal and makes every run
// uncertified, which is what a build without the detector should look like.
func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.GitBinary == "" {
		opts.GitBinary = "git"
	}
	return &Service{
		store:     opts.Store,
		watcher:   opts.Watcher,
		git:       opts.GitBinary,
		logger:    opts.Logger,
		now:       opts.Now,
		leases:    map[string]*treewatch.Lease{},
		sessionMu: map[domain.SessionID]*sync.Mutex{},
	}
}

// lockSession serialises this session's brackets and returns the unlock.
func (s *Service) lockSession(id domain.SessionID) func() {
	s.sessionsMu.Lock()
	if s.sessionMu == nil {
		s.sessionMu = map[domain.SessionID]*sync.Mutex{}
	}
	mu, ok := s.sessionMu[id]
	if !ok {
		mu = &sync.Mutex{}
		s.sessionMu[id] = mu
	}
	s.sessionsMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// Start opens a bracket: attach the detector to the session's worktree, read the
// write generation, and record the run.
func (s *Service) Start(ctx context.Context, sessionID domain.SessionID, in StartInput) (StartResult, error) {
	if !in.Kind.Valid() {
		return StartResult{}, fmt.Errorf("%w: kind must be build, test or device", ErrInvalid)
	}
	defer s.lockSession(sessionID)()
	rec, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return StartResult{}, err
	}
	if !ok {
		return StartResult{}, fmt.Errorf("%w: session %s", ErrNotFound, sessionID)
	}
	worktree := strings.TrimSpace(rec.Metadata.WorkspacePath)
	if worktree == "" {
		return StartResult{}, fmt.Errorf("%w: session %s has no workspace to watch", ErrInvalid, sessionID)
	}

	superseded, err := s.supersedeOpenRun(ctx, sessionID)
	if err != nil {
		return StartResult{}, err
	}

	streak, err := s.store.ConsecutiveCrewRunDiscards(ctx, sessionID)
	if err != nil {
		return StartResult{}, err
	}

	now := s.now()
	run := domain.CrewRun{
		ID:           uuid.NewString(),
		SessionID:    sessionID,
		ProjectID:    rec.ProjectID,
		CrewID:       rec.CrewID,
		Role:         rec.CrewRole,
		WorktreePath: worktree,
		Kind:         in.Kind,
		Label:        strings.TrimSpace(in.Label),
		Attempt:      streak + 1,
		Detector:     domain.CrewRunDetectorLive,
		StartedAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	lease := s.attach(ctx, worktree, &run)
	if err := s.store.InsertCrewRun(ctx, run); err != nil {
		if lease != nil {
			lease.Release()
		}
		return StartResult{}, err
	}
	if lease != nil {
		s.mu.Lock()
		s.leases[run.ID] = lease
		s.mu.Unlock()
	}
	return StartResult{
		Run:             run,
		Certified:       run.Detector == domain.CrewRunDetectorLive,
		SupersededRunID: superseded,
		CrewmateRun:     s.crewmateRun(ctx, rec),
	}, nil
}

// crewmateRun is the other member's open run, if it has one. Best effort by
// design: this is advice, so failing to fetch it must not fail a build.
func (s *Service) crewmateRun(ctx context.Context, rec domain.SessionRecord) *domain.CrewRun {
	if rec.CrewID == "" {
		return nil
	}
	runs, err := s.store.OpenCrewRunsForCrewmates(ctx, rec.CrewID, rec.ID)
	if err != nil || len(runs) == 0 {
		return nil
	}
	return &runs[0]
}

// attach establishes the detector and stamps the run's starting generation. It
// never fails the start: a build that cannot be watched still has to be allowed
// to run, and the honesty lives in the DETECTOR field rather than in a refusal.
func (s *Service) attach(ctx context.Context, worktree string, run *domain.CrewRun) *treewatch.Lease {
	down := func(reason string) *treewatch.Lease {
		run.Detector = domain.CrewRunDetectorDown
		run.DetectorReason = reason
		s.logger.Warn("crew run: starting without a tree-write detector",
			"sessionID", run.SessionID, "worktree", worktree, "reason", reason)
		return nil
	}
	if s.watcher == nil {
		return down("no tree-write detector is configured on this daemon")
	}
	lease, err := s.watcher.Attach(ctx, worktree)
	if err != nil {
		return down(fmt.Sprintf("could not watch %s: %v", worktree, err))
	}
	if reason, isDown := lease.Down(); isDown {
		lease.Release()
		return down(reason)
	}
	run.GenAtStart = lease.StartGeneration()
	return lease
}

// supersedeOpenRun closes a bracket the member never closed. It can only end
// UNCERTIFIED: nobody read the counter at the moment that run finished, so there
// is no reading that could make it trusted.
func (s *Service) supersedeOpenRun(ctx context.Context, sessionID domain.SessionID) (string, error) {
	open, ok, err := s.store.OpenCrewRunForSession(ctx, sessionID)
	if err != nil || !ok {
		return "", err
	}
	now := s.now()
	open.EndedAt = &now
	open.UpdatedAt = now
	open.Outcome = domain.CrewRunUncertified
	open.Detector = domain.CrewRunDetectorDown
	open.DetectorReason = "a new run started before this one was ended, so its result was never read"
	if _, _, err := s.store.EndCrewRun(ctx, open); err != nil {
		return "", err
	}
	s.releaseLease(open.ID)
	s.logger.Warn("crew run: superseded a run that was never ended",
		"sessionID", sessionID, "runID", open.ID)
	return open.ID, nil
}

// End closes a bracket and decides whether its result can be trusted.
func (s *Service) End(ctx context.Context, sessionID domain.SessionID, in EndInput) (EndResult, error) {
	if in.Result != domain.CrewRunResultNone && !in.Result.Valid() {
		return EndResult{}, fmt.Errorf("%w: result must be pass or fail", ErrInvalid)
	}
	defer s.lockSession(sessionID)()
	run, err := s.openRun(ctx, sessionID, in.RunID)
	if err != nil {
		return EndResult{}, err
	}

	s.mu.Lock()
	lease := s.leases[run.ID]
	delete(s.leases, run.ID)
	s.mu.Unlock()

	now := s.now()
	run.EndedAt = &now
	run.UpdatedAt = now
	run.Result = in.Result
	run.HeadSHA = s.headSHA(ctx, run.WorktreePath)
	s.decide(run.ID, lease, &run)
	if lease != nil {
		lease.Release()
	}

	stored, ok, err := s.store.EndCrewRun(ctx, run)
	if err != nil {
		return EndResult{}, err
	}
	if !ok {
		return EndResult{}, fmt.Errorf("%w: run %s has already ended", ErrInvalid, run.ID)
	}

	streak, err := s.store.ConsecutiveCrewRunDiscards(ctx, sessionID)
	if err != nil {
		return EndResult{}, err
	}
	res := EndResult{Run: stored, Attempt: stored.Attempt, MaxAttempts: domain.CappedRepeat}
	if stored.Outcome == domain.CrewRunDiscarded {
		res.Escalated = streak >= domain.CappedRepeat
		res.Retry = !res.Escalated
	}
	return res, nil
}

// decide is the whole verdict, and it has exactly three answers.
//
// A missing or down detector never yields "trusted". It yields UNCERTIFIED,
// which is a different statement from both pass and fail: the run happened, and
// nothing can vouch for it. Silently degrading to a cheaper check here is the one
// thing this package must not do - a detector that misses launders a mixed
// result as a clean one, and an absent detector that pretends to be present is
// the same failure with fewer moving parts.
func (s *Service) decide(runID string, lease *treewatch.Lease, run *domain.CrewRun) {
	if run.Detector == domain.CrewRunDetectorDown {
		run.Outcome = domain.CrewRunUncertified
		return
	}
	if lease == nil {
		run.Outcome = domain.CrewRunUncertified
		run.Detector = domain.CrewRunDetectorDown
		run.DetectorReason = "the daemon restarted while this run was open, so nothing watched the tree"
		s.logger.Warn("crew run: ended with no live detector", "runID", runID)
		return
	}
	end, err := lease.Generation()
	if err != nil {
		run.Outcome = domain.CrewRunUncertified
		run.Detector = domain.CrewRunDetectorDown
		run.DetectorReason = err.Error()
		return
	}
	run.GenAtEnd = end
	if end == run.GenAtStart {
		run.Outcome = domain.CrewRunTrusted
		return
	}
	run.Outcome = domain.CrewRunDiscarded
	run.ChangedPaths = lease.Changed()
}

func (s *Service) openRun(ctx context.Context, sessionID domain.SessionID, runID string) (domain.CrewRun, error) {
	if runID = strings.TrimSpace(runID); runID != "" {
		run, ok, err := s.store.GetCrewRun(ctx, runID)
		if err != nil {
			return domain.CrewRun{}, err
		}
		if !ok || run.SessionID != sessionID {
			return domain.CrewRun{}, fmt.Errorf("%w: run %s", ErrNotFound, runID)
		}
		if !run.Open() {
			return domain.CrewRun{}, fmt.Errorf("%w: run %s has already ended", ErrInvalid, runID)
		}
		return run, nil
	}
	run, ok, err := s.store.OpenCrewRunForSession(ctx, sessionID)
	if err != nil {
		return domain.CrewRun{}, err
	}
	if !ok {
		return domain.CrewRun{}, fmt.Errorf("%w: %s has no run open; start one with `ao crew run --start`", ErrInvalid, sessionID)
	}
	return run, nil
}

func (s *Service) releaseLease(runID string) {
	s.mu.Lock()
	lease := s.leases[runID]
	delete(s.leases, runID)
	s.mu.Unlock()
	if lease != nil {
		lease.Release()
	}
}

// headSHA records which commit the run measured. Best effort: a tree with no
// resolvable HEAD is not a reason to fail a bracket.
func (s *Service) headSHA(ctx context.Context, worktree string) string {
	if worktree == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, s.git, "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// List returns the session's runs, newest first.
func (s *Service) List(ctx context.Context, sessionID domain.SessionID) ([]domain.CrewRun, error) {
	return s.store.ListCrewRunsForSession(ctx, sessionID, historyDepth)
}

// ReconcileOpenRuns closes brackets left open by a previous process. Called once
// at daemon start: the leases those runs depended on died with that process, so
// leaving them open would have the board show a build running that nothing is
// running, and would let an --end from a stale agent certify a tree nobody
// watched.
func (s *Service) ReconcileOpenRuns(ctx context.Context) error {
	closed, err := s.store.AbandonOpenCrewRuns(ctx, s.now())
	if err != nil {
		return err
	}
	if closed > 0 {
		s.logger.Info("crew run: closed runs left open by a previous daemon as uncertified", "runs", closed)
	}
	return nil
}
