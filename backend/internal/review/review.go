// Package review holds the core code-review logic: triggering a reviewer over a
// worker's worktree, recording review runs, and accepting submitted results.
//
// It is independent of any transport. The daemon's HTTP service
// (internal/service/review) is a thin boundary over this engine today, and the
// same engine can back an in-process CLI trigger later without going through the
// API. Transport-specific concerns (DTOs, error→status mapping) stay in the
// service/controller layers; the orchestration and run-id generation live here.
package review

import (
	stdctx "context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptoverrides"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

// ErrInvalid and ErrNotFound let the transport layer map failures to 422/404.
var (
	ErrInvalid  = errors.New("review: invalid input")
	ErrNotFound = errors.New("review: not found")
)

// Store is the persistence surface the engine needs. *sqlite.Store satisfies it
// in production; tests use a fake.
type Store interface {
	UpsertReview(ctx stdctx.Context, r domain.Review) error
	GetReviewBySession(ctx stdctx.Context, id domain.SessionID) (domain.Review, bool, error)
	InsertReviewRun(ctx stdctx.Context, r domain.ReviewRun) error
	UpdateReviewRunResult(ctx stdctx.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string) (bool, error)
	SupersedeReviewRun(ctx stdctx.Context, id, body string) (bool, error)
	SupersedeStaleRunningReviewRuns(ctx stdctx.Context, sessionID domain.SessionID, prURL, targetSHA, body string) (int64, error)
	FailRunningReviewRunsBySession(ctx stdctx.Context, sessionID domain.SessionID, body string) (int64, error)
	GetReviewRun(ctx stdctx.Context, id string) (domain.ReviewRun, bool, error)
	GetReviewRunBySessionPRAndSHA(ctx stdctx.Context, id domain.SessionID, prURL, targetSHA string) (domain.ReviewRun, bool, error)
	ListReviewRunsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	ListSessionIDsWithRunningReviewRuns(ctx stdctx.Context) ([]domain.SessionID, error)
}

// Sessions resolves the worker session under review.
type Sessions interface {
	GetSession(ctx stdctx.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// PRs resolves the PR a worker owns.
type PRs interface {
	ListPRsBySession(ctx stdctx.Context, id domain.SessionID) ([]domain.PullRequest, error)
}

// Projects resolves the per-project reviewer config.
type Projects interface {
	GetProject(ctx stdctx.Context, id string) (domain.ProjectRecord, bool, error)
}

// Deps wires the engine.
type Deps struct {
	Store    Store
	Sessions Sessions
	PRs      PRs
	Projects Projects
	Launcher Launcher

	// PromptOverrides returns the current global per-kind base overrides, read at
	// trigger time so an edit takes effect on the next reviewer (re)launch. Nil
	// defaults to the built-in reviewer base — the safe default for a bare Engine.
	PromptOverrides func() promptoverrides.Overrides

	// Clock and NewID are injectable for deterministic tests.
	Clock func() time.Time
	NewID func() string
}

// Engine is the core code-review engine.
type Engine struct {
	store           Store
	sessions        Sessions
	prs             PRs
	projects        Projects
	launcher        Launcher
	promptOverrides func() promptoverrides.Overrides
	clock           func() time.Time
	newID           func() string

	// triggerMu guards triggerLocks; triggerLocks holds one mutex per worker
	// session so concurrent Trigger calls for the same worker serialise (see
	// lockWorker). Distinct workers never contend.
	triggerMu    sync.Mutex
	triggerLocks map[domain.SessionID]*sync.Mutex
}

// New wires an Engine from its dependencies, defaulting the clock and id source.
func New(d Deps) *Engine {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	newID := d.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	return &Engine{
		store:           d.Store,
		sessions:        d.Sessions,
		prs:             d.PRs,
		projects:        d.Projects,
		launcher:        d.Launcher,
		promptOverrides: d.PromptOverrides,
		clock:           clock,
		newID:           newID,
		triggerLocks:    make(map[domain.SessionID]*sync.Mutex),
	}
}

// lockWorker serialises Trigger calls for a single worker session and returns
// the unlock func. Without it, two concurrent triggers for the same worker can
// both pass the per-commit idempotency check and each spawn a reviewer against
// the same deterministic handle, leaving two running runs for one commit (#242).
//
// The per-worker mutex is created on first use and kept for the lifetime of the
// engine; the entry is a single pointer, so the unbounded-by-session-count map
// is a negligible, bounded-in-practice cost.
func (e *Engine) lockWorker(id domain.SessionID) func() {
	e.triggerMu.Lock()
	mu, ok := e.triggerLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		e.triggerLocks[id] = mu
	}
	e.triggerMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// TriggerResult is the outcome of a trigger: the (new or existing) run, the live
// reviewer pane's handle so the UI can attach its terminal, and whether a new
// pass was started (false when an existing run for the same commit was reused).
type TriggerResult struct {
	Run              domain.ReviewRun
	ReviewerHandleID string
	Created          bool
	Reviews          []PRReviewState
	CreatedRuns      []domain.ReviewRun
}

// SessionReviews is a worker's review state: the live reviewer handle plus its
// recorded passes, newest first.
type SessionReviews struct {
	ReviewerHandleID string
	Runs             []domain.ReviewRun
	Reviews          []PRReviewState
}

// Trigger starts reviews for every PR on the worker session that needs review.
// It reuses running/up-to-date runs, retries failed/current changes-requested
// heads, and uses one reviewer pane for every new run in the batch.
func (e *Engine) Trigger(ctx stdctx.Context, workerID domain.SessionID) (TriggerResult, error) {
	if workerID == "" {
		return TriggerResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}

	// Serialise concurrent triggers for this worker so the idempotency check
	// below (and the reviewer spawn that follows it) can't be raced into a
	// double-spawn. Held across the spawn deliberately: the loser then re-reads
	// the freshly-recorded run and short-circuits to Created:false.
	unlock := e.lockWorker(workerID)
	defer unlock()

	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}
	if !ok {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	if worker.IsTerminated {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q is terminated", ErrInvalid, workerID)
	}
	if worker.Metadata.WorkspacePath == "" {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q has no workspace to review", ErrInvalid, workerID)
	}

	prs, err := e.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}
	if len(prs) == 0 {
		return TriggerResult{}, fmt.Errorf("%w: worker %q has no PR to review", ErrInvalid, workerID)
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}
	reviews := Plan(prs, runs)

	reviewRow, hasReview, err := e.store.GetReviewBySession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}

	// Probe the reviewer pane's agent liveness once, up front. If the pane is
	// gone (its claude-code exited out of band), any run still 'running' is
	// orphaned — no live reviewer will ever complete it — so fail those runs and
	// re-plan. That unsticks the board and lets this trigger create fresh runs
	// instead of skipping the stuck PRs. The result is reused for the reuse-vs-
	// spawn decision below, so the pane is probed only once.
	reviewerAlive := false
	if hasReview && reviewRow.ReviewerHandleID != "" {
		reviewerAlive, err = e.launcher.Alive(ctx, reviewRow.ReviewerHandleID)
		if err != nil {
			return TriggerResult{}, err
		}
		if !reviewerAlive {
			if _, err := e.store.FailRunningReviewRunsBySession(ctx, workerID, "reviewer pane exited before the review completed"); err != nil {
				return TriggerResult{}, err
			}
			runs, err = e.store.ListReviewRunsBySession(ctx, workerID)
			if err != nil {
				return TriggerResult{}, err
			}
			reviews = Plan(prs, runs)
		}
	}

	projCfg, err := e.projectConfig(ctx, worker)
	if err != nil {
		return TriggerResult{}, err
	}
	harness := projCfg.ResolveReviewerHarness(worker.Harness)

	now := e.clock()
	reviewRow, err = e.upsertReview(ctx, worker, harness, reviewRow.ReviewerHandleID, now)
	if err != nil {
		return TriggerResult{}, err
	}

	var created []domain.ReviewRun
	batchID := ""
	for _, reviewState := range reviews {
		if reviewState.Status != ReviewStateNeedsReview && reviewState.Status != ReviewStateChangesRequested {
			continue
		}
		if reviewState.LatestRun != nil && reviewState.LatestRun.Status != domain.ReviewRunFailed && reviewState.LatestRun.Status != domain.ReviewRunRunning && reviewState.LatestRun.Verdict == domain.VerdictNone {
			superseded, err := e.store.SupersedeReviewRun(ctx, reviewState.LatestRun.ID, "superseded by a new review trigger")
			if err != nil {
				return TriggerResult{}, err
			}
			if !superseded {
				if latest, ok, err := e.store.GetReviewRun(ctx, reviewState.LatestRun.ID); err != nil {
					return TriggerResult{}, err
				} else if ok {
					reviews = replaceReviewLatestRun(reviews, reviewState.PRURL, reviewState.TargetSHA, latest)
					continue
				}
			}
		}
		if _, err := e.store.SupersedeStaleRunningReviewRuns(ctx, workerID, reviewState.PRURL, reviewState.TargetSHA, "superseded by a review trigger for a newer commit"); err != nil {
			return TriggerResult{}, err
		}
		if batchID == "" {
			batchID = e.newID()
		}
		run := domain.ReviewRun{
			ID:        e.newID(),
			ReviewID:  reviewRow.ID,
			SessionID: workerID,
			BatchID:   batchID,
			Harness:   harness,
			PRURL:     reviewState.PRURL,
			TargetSHA: reviewState.TargetSHA,
			Status:    domain.ReviewRunRunning,
			Verdict:   domain.VerdictNone,
			CreatedAt: now,
		}
		if err := e.store.InsertReviewRun(ctx, run); err != nil {
			if errors.Is(err, domain.ErrDuplicateReviewRun) {
				if existing, ok, getErr := e.store.GetReviewRunBySessionPRAndSHA(ctx, workerID, reviewState.PRURL, reviewState.TargetSHA); getErr != nil {
					return TriggerResult{}, getErr
				} else if ok {
					reviews = replaceReviewLatestRun(reviews, reviewState.PRURL, reviewState.TargetSHA, existing)
					continue
				}
			}
			return TriggerResult{}, err
		}
		created = append(created, run)
		reviews = replaceReviewLatestRun(reviews, reviewState.PRURL, reviewState.TargetSHA, run)
	}
	if len(created) == 0 {
		return TriggerResult{Run: firstReusableRun(reviews), ReviewerHandleID: reviewRow.ReviewerHandleID, Created: false, Reviews: reviews}, nil
	}

	failRuns := func(start int, err error) error {
		for _, run := range created[start:] {
			if _, updateErr := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, err.Error(), ""); updateErr != nil {
				return updateErr
			}
		}
		return err
	}

	handleID := ""
	queue := reviewQueue(created)
	// Resolve the reviewer's effective global base (override else default) and the
	// project's per-project addition once; reviewTexts appends the review-only
	// floor + confidentiality guard when it assembles the system prompt.
	spec := reviewLaunchSpec(worker, harness, created[0], queue, 0)
	spec.ReviewerBase = e.reviewerBase()
	spec.ReviewerAddition = projCfg.SystemPromptAdditions.Reviewer
	// A fresh per-launch native session id keyed on this batch's first run, so a
	// relaunched reviewer never reuses a prior pass's `claude --session-id` and
	// collides with its transcript. Only consumed on the Spawn (relaunch) path;
	// the live-pane Notify path keeps the running claude's original id.
	spec.AgentSessionID = reviewerAgentSessionID(workerID, created[0].ID)
	// Reuse the up-front liveness probe: an agent-alive pane is re-notified;
	// otherwise a fresh reviewer is spawned (which destroys any stale pane first).
	if reviewerAlive && reviewRow.ReviewerHandleID != "" {
		handleID = reviewRow.ReviewerHandleID
	}
	if handleID == "" {
		h, err := e.launcher.Spawn(ctx, spec)
		if err != nil {
			return TriggerResult{}, failRuns(0, fmt.Errorf("launch reviewer: %w", err))
		}
		handleID = h
	} else {
		if err := e.launcher.Notify(ctx, handleID, spec); err != nil {
			return TriggerResult{}, failRuns(0, fmt.Errorf("notify reviewer: %w", err))
		}
	}
	reviewRow, err = e.upsertReview(ctx, worker, harness, handleID, now)
	if err != nil {
		return TriggerResult{}, err
	}
	for i := range created {
		created[i].ReviewID = reviewRow.ID
	}
	return TriggerResult{Run: created[0], ReviewerHandleID: handleID, Created: true, Reviews: reviews, CreatedRuns: created}, nil
}

func reviewLaunchSpec(worker domain.SessionRecord, harness domain.ReviewerHarness, run domain.ReviewRun, queue []ports.ReviewTask, index int) LaunchSpec {
	return LaunchSpec{
		RunID:         run.ID,
		WorkerID:      worker.ID,
		Harness:       harness,
		WorkspacePath: worker.Metadata.WorkspacePath,
		PRURL:         run.PRURL,
		TargetSHA:     run.TargetSHA,
		ReviewQueue:   queue,
		ReviewIndex:   index,
	}
}

func reviewQueue(runs []domain.ReviewRun) []ports.ReviewTask {
	queue := make([]ports.ReviewTask, 0, len(runs))
	for _, run := range runs {
		queue = append(queue, ports.ReviewTask{
			RunID:     run.ID,
			PRURL:     run.PRURL,
			TargetSHA: run.TargetSHA,
		})
	}
	return queue
}

func replaceReviewLatestRun(reviews []PRReviewState, prURL, targetSHA string, run domain.ReviewRun) []PRReviewState {
	for i := range reviews {
		if reviews[i].PRURL == prURL && reviews[i].TargetSHA == targetSHA {
			reviews[i].LatestRun = &run
			if run.Status == domain.ReviewRunRunning {
				reviews[i].Status = ReviewStateRunning
			}
			break
		}
	}
	return reviews
}

func firstReusableRun(reviews []PRReviewState) domain.ReviewRun {
	// Legacy compatibility only: in the multi-PR model the authoritative state
	// is Reviews. When no run is created, this field is just a best-effort
	// non-empty run for older clients.
	for _, review := range reviews {
		if review.LatestRun != nil {
			return *review.LatestRun
		}
	}
	return domain.ReviewRun{}
}

// ReconcileOrphanedRuns fails running review runs whose reviewer pane is no
// longer alive. Called on daemon boot so a review left "running" when a reviewer
// died (or the daemon restarted without it) unsticks the board automatically,
// instead of waiting for the next manual trigger. A run whose pane genuinely
// survived is left alone so an in-flight reviewer can still submit. Best-effort:
// a per-session probe error is skipped (never fails a run on an ambiguous probe).
// Returns the number of runs failed.
func (e *Engine) ReconcileOrphanedRuns(ctx stdctx.Context) (int, error) {
	ids, err := e.store.ListSessionIDsWithRunningReviewRuns(ctx)
	if err != nil {
		return 0, err
	}
	failed := 0
	for _, id := range ids {
		alive := false
		if reviewRow, ok, err := e.store.GetReviewBySession(ctx, id); err != nil {
			return failed, err
		} else if ok && reviewRow.ReviewerHandleID != "" {
			alive, err = e.launcher.Alive(ctx, reviewRow.ReviewerHandleID)
			if err != nil {
				// Ambiguous probe: do not fail the run on uncertainty.
				continue
			}
		}
		if alive {
			continue
		}
		n, err := e.store.FailRunningReviewRunsBySession(ctx, id, "reviewer pane not alive at daemon start")
		if err != nil {
			return failed, err
		}
		failed += int(n)
	}
	return failed, nil
}

// Reset fails every still-running review run for a worker, clearing a board stuck
// on "Reviewing…" when a reviewer died out of band and left an orphaned run. A
// failed run drops out of the per-commit idempotency index, so the next trigger
// can start a fresh review. Returns the number of runs failed.
func (e *Engine) Reset(ctx stdctx.Context, workerID domain.SessionID) (int64, error) {
	if workerID == "" {
		return 0, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	return e.store.FailRunningReviewRunsBySession(ctx, workerID, "review reset by operator")
}

// List returns a worker's review state: the live reviewer handle and its passes.
func (e *Engine) List(ctx stdctx.Context, workerID domain.SessionID) (SessionReviews, error) {
	if workerID == "" {
		return SessionReviews{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	var handle string
	if review, ok, err := e.store.GetReviewBySession(ctx, workerID); err != nil {
		return SessionReviews{}, err
	} else if ok {
		handle = review.ReviewerHandleID
	}
	prs, err := e.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	return SessionReviews{ReviewerHandleID: handle, Runs: runs, Reviews: Plan(prs, runs)}, nil
}

// projectConfig loads the worker's project config once, nil-safe: no projects
// port (or an unregistered project) yields the zero config. Both the reviewer
// harness (ResolveReviewerHarness) and the reviewer prompt addition
// (SystemPromptAdditions.Reviewer) read from it, so Trigger fetches the project
// a single time.
func (e *Engine) projectConfig(ctx stdctx.Context, worker domain.SessionRecord) (domain.ProjectConfig, error) {
	if e.projects == nil {
		return domain.ProjectConfig{}, nil
	}
	proj, ok, err := e.projects.GetProject(ctx, string(worker.ProjectID))
	if err != nil {
		return domain.ProjectConfig{}, err
	}
	if !ok {
		return domain.ProjectConfig{}, nil
	}
	return proj.Config, nil
}

// reviewerBase returns the effective global reviewer base: the stored override
// when set, otherwise the built-in default. A nil promptOverrides (bare Engine
// or wiring that omits the store) falls back to the default — the safe default.
func (e *Engine) reviewerBase() string {
	base := prompts.DefaultBase(prompts.KindReviewer)
	if e.promptOverrides != nil {
		if ov, ok := e.promptOverrides().Base[prompts.KindReviewer]; ok {
			base = ov
		}
	}
	return base
}

func (e *Engine) upsertReview(ctx stdctx.Context, worker domain.SessionRecord, harness domain.ReviewerHarness, handleID string, now time.Time) (domain.Review, error) {
	existing, ok, err := e.store.GetReviewBySession(ctx, worker.ID)
	if err != nil {
		return domain.Review{}, err
	}
	review := domain.Review{
		ID:               e.newID(),
		SessionID:        worker.ID,
		ProjectID:        worker.ProjectID,
		Harness:          harness,
		PRURL:            "",
		ReviewerHandleID: handleID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if ok {
		// Reuse the existing row's identity and creation time; UpsertReview
		// refreshes harness/pr_url/reviewer_handle_id/updated_at.
		review.ID = existing.ID
		review.CreatedAt = existing.CreatedAt
	}
	if err := e.store.UpsertReview(ctx, review); err != nil {
		return domain.Review{}, err
	}
	return review, nil
}
