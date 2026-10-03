// Package learncollect is the background loop that turns captured human turns
// into drafts: it hands one session's uncollected excerpts at a time to a
// model (internal/learn/llm) and keeps the lessons that survive the grounding
// checks (internal/learn/collect).
//
// It reads excerpts, never transcripts, so the model only ever sees text that
// capture already redacted. It collects a session once the human has gone
// quiet in it - the newest uncollected turn is at least QuietFor old - or once
// the oldest has waited MaxWait, which is what makes a never-ending
// orchestrator session collect at all. Every run is recorded with its cost and,
// on failure, its error and stderr; the background loop stops for the day at
// the daily budget, and a failing session backs off rather than spending the
// budget on the same failure.
//
// A manual run (RunNow) collects everything uncollected under its own budget.
// It is how a backlog - the turns captured before collect existed - is
// processed in one go.
package learncollect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/collect"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
)

// Timing.
const (
	DefaultTickInterval = 10 * time.Minute
	// QuietFor is how long a session's newest uncollected turn must be old
	// before it is collected: corrections come in bursts, and one call over
	// the burst sees a later turn take an earlier one back.
	QuietFor = 15 * time.Minute
	// MaxWait collects a session whose oldest turn has waited this long even
	// while the human is still talking to it.
	MaxWait = 2 * time.Hour
	// failureBackoff is the first wait after a failed run, doubled per
	// consecutive failure up to maxBackoff.
	failureBackoff = 30 * time.Minute
	maxBackoff     = 24 * time.Hour
	// maxTurnsPerSessionPass bounds what one pass reads for one session.
	maxTurnsPerSessionPass = 500
	// stopAfterFailures ends a pass after this many failed runs in a row:
	// they are almost always one cause (a logged-out CLI), and the rest of the
	// pass would only repeat it.
	stopAfterFailures = 3
)

// Store is the persistence collect needs.
type Store interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListCollectCandidates(ctx context.Context, projectID domain.ProjectID) ([]domain.LearnCollectCandidate, error)
	ListUncollectedExcerpts(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID, limit int) ([]domain.LearnExcerpt, error)
	OpenDrafts(ctx context.Context, sessionID domain.SessionID) ([]domain.LearnDraft, error)
	StartLearnJob(ctx context.Context, job domain.LearnJob) (int64, error)
	FailLearnJob(ctx context.Context, job domain.LearnJob) error
	CommitLearnJob(ctx context.Context, job domain.LearnJob, drafts []domain.LearnDraft, excerptIDs []int64, now time.Time) (int, error)
	RecentLearnJobs(ctx context.Context, sessionID domain.SessionID, limit int) ([]domain.LearnJob, error)
	LearnSpendSince(ctx context.Context, since time.Time) (float64, error)
	AbandonRunningLearnJobs(ctx context.Context, now time.Time) (int, error)
}

// Config holds the loop's knobs; zero values use production defaults.
type Config struct {
	Tick   time.Duration
	Clock  func() time.Time
	Logger *slog.Logger
	OnTick func()
}

// Progress is the state of the current or last run, for status.
type Progress struct {
	Running    bool
	Manual     bool
	Project    string
	StartedAt  time.Time
	FinishedAt time.Time
	Jobs       int
	Failed     int
	Drafts     int
	CostUSD    float64
	BudgetUSD  float64
	StopReason string
	LastError  string
}

// Observer is the collect loop.
type Observer struct {
	store    Store
	runner   llm.Runner
	settings func() learnsettings.Settings
	tick     time.Duration
	clock    func() time.Time
	logger   *slog.Logger
	onTick   func()

	runMu    sync.Mutex // one pass at a time, background or manual
	stateMu  sync.Mutex
	progress Progress
}

// New builds the loop.
func New(store Store, runner llm.Runner, settings func() learnsettings.Settings, cfg Config) *Observer {
	o := &Observer{store: store, runner: runner, settings: settings, tick: cfg.Tick, clock: cfg.Clock, logger: cfg.Logger, onTick: cfg.OnTick}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	if o.settings == nil {
		o.settings = learnsettings.Default
	}
	return o
}

// Start abandons the runs the previous daemon left in flight, then launches
// the loop and returns a channel that closes when it exits.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	if n, err := o.store.AbandonRunningLearnJobs(ctx, o.clock().UTC()); err != nil {
		o.logger.Warn("learn-collect: abandon stale runs failed", "err", err)
	} else if n > 0 {
		o.logger.Info("learn-collect: abandoned runs left by the previous daemon", "runs", n)
	}
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "learn-collect", o.onTick)
}

// Progress reports the current or last run.
func (o *Observer) Progress() Progress {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()
	return o.progress
}

// ErrBusy refuses a manual run while another run is in progress.
var ErrBusy = errors.New("a collect run is already in progress")

// RunNow starts a manual run over every uncollected turn of one project (or of
// every learning project when project is ""), spending at most budgetUSD. It
// returns at once; the run continues on ctx, which should outlive the request.
func (o *Observer) RunNow(ctx context.Context, project string, budgetUSD float64) error {
	if budgetUSD <= 0 {
		return errors.New("budget must be positive")
	}
	if !o.runMu.TryLock() {
		return ErrBusy
	}
	go func() {
		defer o.runMu.Unlock()
		o.pass(ctx, passOpts{manual: true, project: project, budget: budgetUSD})
	}()
	return nil
}

// Poll is one background pass: sessions whose human has gone quiet, within
// what is left of the daily budget. It skips when a manual run is going.
func (o *Observer) Poll(ctx context.Context) error {
	if !o.runMu.TryLock() {
		return nil
	}
	defer o.runMu.Unlock()
	s := o.settings()
	now := o.clock()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	spent, err := o.store.LearnSpendSince(ctx, dayStart.UTC())
	if err != nil {
		return err
	}
	left := s.DailyBudgetUSD - spent
	if left <= 0 {
		return nil
	}
	o.pass(ctx, passOpts{budget: left, onlyQuiet: true})
	return nil
}

type passOpts struct {
	manual    bool
	project   string
	budget    float64
	onlyQuiet bool
}

func (o *Observer) setProgress(fn func(p *Progress)) {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()
	fn(&o.progress)
}

// pass runs one collect pass. The caller holds runMu.
func (o *Observer) pass(ctx context.Context, opts passOpts) {
	start := o.clock()
	o.setProgress(func(p *Progress) {
		*p = Progress{Running: true, Manual: opts.manual, Project: opts.project, StartedAt: start, BudgetUSD: opts.budget}
	})
	stop := ""
	defer func() {
		o.setProgress(func(p *Progress) {
			p.Running = false
			p.FinishedAt = o.clock()
			p.StopReason = stop
		})
	}()

	projects, err := o.store.ListProjects(ctx)
	if err != nil {
		stop = "could not list projects: " + err.Error()
		return
	}
	s := o.settings()
	var spent float64
	failuresInARow := 0
	for _, p := range projects {
		if !p.Config.LearnFromSessions || !p.ArchivedAt.IsZero() || (opts.project != "" && opts.project != p.ID) {
			continue
		}
		cands, err := o.store.ListCollectCandidates(ctx, domain.ProjectID(p.ID))
		if err != nil {
			o.logger.Warn("learn-collect: list candidates failed", "project", p.ID, "err", err)
			continue
		}
		for _, c := range cands {
			if ctx.Err() != nil {
				stop = "stopped"
				return
			}
			now := o.clock()
			if opts.onlyQuiet && now.Sub(c.NewestAt) < QuietFor && now.Sub(c.OldestAt) < MaxWait {
				continue
			}
			if wait, backing := o.backoff(ctx, c.SessionID, now); backing {
				o.logger.Debug("learn-collect: session backing off", "session", c.SessionID, "for", wait)
				continue
			}
			res := o.collectSession(ctx, p, c.SessionID, s, opts.budget-spent)
			spent += res.cost
			o.setProgress(func(pr *Progress) {
				pr.Jobs += res.jobs
				pr.Failed += res.failed
				pr.Drafts += res.drafts
				pr.CostUSD = spent
				if res.lastErr != "" {
					pr.LastError = res.lastErr
				}
			})
			if res.failed > 0 && res.jobs == res.failed {
				failuresInARow += res.failed
			} else if res.jobs > 0 {
				failuresInARow = 0
			}
			if failuresInARow >= stopAfterFailures {
				stop = fmt.Sprintf("%d runs failed in a row: %s", failuresInARow, res.lastErr)
				return
			}
			if res.overBudget || spent >= opts.budget {
				stop = fmt.Sprintf("budget reached ($%.2f of $%.2f)", spent, opts.budget)
				return
			}
		}
	}
	stop = "done"
}

// backoff reports whether a session must wait after recent failed runs.
func (o *Observer) backoff(ctx context.Context, id domain.SessionID, now time.Time) (time.Duration, bool) {
	jobs, err := o.store.RecentLearnJobs(ctx, id, 8)
	if err != nil || len(jobs) == 0 || jobs[0].State != domain.LearnJobFailed {
		return 0, false
	}
	failures := 0
	for _, j := range jobs {
		if j.State == domain.LearnJobAbandoned {
			continue
		}
		if j.State != domain.LearnJobFailed {
			break
		}
		failures++
	}
	wait := failureBackoff << (failures - 1)
	if wait > maxBackoff || wait <= 0 {
		wait = maxBackoff
	}
	last := jobs[0].FinishedAt
	if last.IsZero() {
		last = jobs[0].StartedAt
	}
	if remaining := last.Add(wait).Sub(now); remaining > 0 {
		return remaining, true
	}
	return 0, false
}

type sessionResult struct {
	jobs, failed, drafts int
	cost                 float64
	overBudget           bool
	lastErr              string
}

// collectSession runs the batches of one session's uncollected turns while
// budget remains, a page of turns at a time until none are left. A failed
// batch ends the session for this pass: its turns stay uncollected and the
// session backs off.
func (o *Observer) collectSession(ctx context.Context, p domain.ProjectRecord, id domain.SessionID, s learnsettings.Settings, budget float64) sessionResult {
	var res sessionResult
	rec, found, err := o.store.GetSession(ctx, id)
	if err != nil {
		found = false
	}
	session := collect.Session{Project: p.ID, Kind: "worker"}
	if found {
		session.Kind = string(rec.Kind)
		session.Role = string(rec.CrewRole)
		session.Branch = rec.Metadata.Branch
	}
	for ctx.Err() == nil {
		turns, err := o.store.ListUncollectedExcerpts(ctx, domain.ProjectID(p.ID), id, maxTurnsPerSessionPass)
		if err != nil {
			res.lastErr = err.Error()
			return res
		}
		if len(turns) == 0 {
			return res
		}
		for _, batch := range collect.Batches(turns) {
			if res.cost >= budget {
				res.overBudget = true
				return res
			}
			if !o.runBatch(ctx, p, id, rec, found, session, s, batch, &res) {
				return res
			}
		}
	}
	return res
}

// runBatch makes one model call over one batch and stores what survives. It
// reports false when the batch failed and the session should stop.
func (o *Observer) runBatch(ctx context.Context, p domain.ProjectRecord, id domain.SessionID, rec domain.SessionRecord, found bool, session collect.Session, s learnsettings.Settings, batch []domain.LearnExcerpt, res *sessionResult) bool {
	open, err := o.store.OpenDrafts(ctx, id)
	if err != nil {
		res.lastErr = err.Error()
		return false
	}
	input, err := collect.Input(session, batch, open)
	if err != nil {
		res.lastErr = err.Error()
		return false
	}
	job := domain.LearnJob{ProjectID: domain.ProjectID(p.ID), SessionID: id, Model: s.CollectModel, Turns: len(batch), StartedAt: o.clock().UTC()}
	job.ID, err = o.store.StartLearnJob(ctx, job)
	if err != nil {
		res.lastErr = err.Error()
		return false
	}
	res.jobs++
	out, runErr := o.runner.Run(ctx, llm.Request{
		Model: s.CollectModel, Effort: s.CollectEffort, SystemPrompt: collect.SystemPrompt,
		Schema: []byte(collect.Schema), Input: input,
	})
	job.FinishedAt = o.clock().UTC()
	if runErr != nil {
		var le *llm.Error
		if errors.As(runErr, &le) {
			job.StderrTail = le.StderrTail
			job.CostUSD = le.CostUSD
		}
		job.Error = runErr.Error()
		res.cost += job.CostUSD
		o.fail(ctx, job, res)
		return false
	}
	job.CostUSD, job.InputTokens, job.OutputTokens, job.DurationMS = out.CostUSD, out.InputTokens, out.OutputTokens, out.DurationMS
	res.cost += out.CostUSD
	base := domain.LearnDraft{
		ProjectID: domain.ProjectID(p.ID),
		SessionID: id,
		TaskKey:   collect.TaskKey(rec, found, id, batch[0].TurnAt),
	}
	drafts, rejected, err := collect.Drafts(out.Output, base, batch, open)
	if err != nil {
		job.Error = err.Error()
		o.fail(ctx, job, res)
		return false
	}
	job.Rejected = len(rejected)
	ids := make([]int64, 0, len(batch))
	for _, t := range batch {
		ids = append(ids, t.ID)
	}
	n, err := o.store.CommitLearnJob(ctx, job, drafts, ids, o.clock().UTC())
	if err != nil {
		job.Error = err.Error()
		o.fail(ctx, job, res)
		return false
	}
	res.drafts += n
	return true
}

func (o *Observer) fail(ctx context.Context, job domain.LearnJob, res *sessionResult) {
	res.failed++
	res.lastErr = job.Error
	o.logger.Warn("learn-collect: run failed", "session", job.SessionID, "err", job.Error, "stderr", job.StderrTail)
	if err := o.store.FailLearnJob(ctx, job); err != nil {
		o.logger.Warn("learn-collect: record failure failed", "session", job.SessionID, "err", err)
	}
}
