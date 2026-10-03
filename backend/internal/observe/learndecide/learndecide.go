// Package learndecide is the background loop that turns a finished task's
// drafts into proposals. For each task with open drafts that is ready - its
// sessions ended (or a long-lived one reached the daily cut, or an
// orchestrator's day is over) - it asks the strong model what agents should
// durably learn, asks it again adversarially, applies the gates no model can
// talk past (internal/learn/decide), and stores the proposals with the diff AO
// computed. Nothing is applied: the person approves every proposal.
//
// Model runs share learning's daily budget. A task whose run failed backs off,
// and a pass ends after three failed runs in a row.
package learndecide

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/decide"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/skills"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
)

// Timing and bounds.
const (
	DefaultTickInterval = 30 * time.Minute
	parallel            = 3
	stopAfterFailures   = 3
	failureBackoff      = time.Hour
	maxBackoff          = 24 * time.Hour
	maxPlans            = 3
	planBytes           = 4 << 10
)

// Store is the persistence decide needs.
type Store interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error)
	ListAllLearnDrafts(ctx context.Context) ([]domain.LearnDraft, error)
	ListSkillProposals(ctx context.Context) ([]domain.LearnProposal, error)
	ListLearnRuleSources(ctx context.Context) ([]domain.LearnRuleSource, error)
	ListLearnRuleChunks(ctx context.Context) (map[string]domain.LearnRuleChunk, error)
	ListLearnProtectedRules(ctx context.Context) ([]domain.LearnProtectedRule, error)
	StartLearnJob(ctx context.Context, job domain.LearnJob) (int64, error)
	FailLearnJob(ctx context.Context, job domain.LearnJob) error
	FinishLearnJob(ctx context.Context, job domain.LearnJob) error
	CommitDecide(ctx context.Context, res domain.LearnDecideResult, now time.Time) error
	LearnSpendSince(ctx context.Context, since time.Time) (float64, error)
}

// Dirs are where decide reads skills, rule files and plans.
type Dirs struct {
	Home         string
	DataDir      string
	Learned      string
	KnowledgeDir string
}

// Config holds the loop's knobs; zero values use production defaults.
type Config struct {
	Tick   time.Duration
	Clock  func() time.Time
	Logger *slog.Logger
	OnTick func()
	// Dictionary returns the exact values no proposal may contain.
	Dictionary func(projects []domain.ProjectRecord) []redact.Value
}

// Progress is the state of the current or last pass.
type Progress struct {
	Running    bool
	Manual     bool
	StartedAt  time.Time
	FinishedAt time.Time
	Tasks      int
	Failed     int
	Proposals  int
	Dropped    int
	CostUSD    float64
	BudgetUSD  float64
	StopReason string
	LastError  string
}

// Observer is the decide loop.
type Observer struct {
	store    Store
	runner   llm.Runner
	settings func() learnsettings.Settings
	dirs     Dirs
	cfg      Config
	clock    func() time.Time
	logger   *slog.Logger

	runMu    sync.Mutex // one pass at a time
	commitMu sync.Mutex // proposals are settled against the store one task at a time
	stateMu  sync.Mutex
	progress Progress
	backoff  map[string]taskBackoff
}

type taskBackoff struct {
	until    time.Time
	failures int
}

// New builds the loop.
func New(store Store, runner llm.Runner, settings func() learnsettings.Settings, dirs Dirs, cfg Config) *Observer {
	o := &Observer{store: store, runner: runner, settings: settings, dirs: dirs, cfg: cfg, clock: cfg.Clock, logger: cfg.Logger, backoff: map[string]taskBackoff{}}
	if o.cfg.Tick <= 0 {
		o.cfg.Tick = DefaultTickInterval
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

// Start launches the loop.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.cfg.Tick, o.Poll, o.logger, "learn-decide", o.cfg.OnTick)
}

// Progress reports the current or last pass.
func (o *Observer) Progress() Progress {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()
	return o.progress
}

// ErrBusy refuses a manual run while another pass is running.
var ErrBusy = errors.New("a decide run is already in progress")

// RunOpts scopes a manual run.
type RunOpts struct {
	Project string
	// Task decides one task now, ready or not.
	Task      string
	BudgetUSD float64
}

// RunNow starts a manual run under its own budget and returns at once.
func (o *Observer) RunNow(ctx context.Context, opts RunOpts) error {
	if opts.BudgetUSD <= 0 {
		return errors.New("budget must be positive")
	}
	if !o.runMu.TryLock() {
		return ErrBusy
	}
	go func() {
		defer o.runMu.Unlock()
		o.pass(ctx, true, opts)
	}()
	return nil
}

// Run is RunNow that waits: for the backfill run and tests.
func (o *Observer) Run(ctx context.Context, opts RunOpts) {
	o.runMu.Lock()
	defer o.runMu.Unlock()
	o.pass(ctx, true, opts)
}

// Poll is one background pass within what is left of the daily budget.
func (o *Observer) Poll(ctx context.Context) error {
	if !o.runMu.TryLock() {
		return nil
	}
	defer o.runMu.Unlock()
	now := o.clock()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	spent, err := o.store.LearnSpendSince(ctx, dayStart.UTC())
	if err != nil {
		return err
	}
	left := o.settings().DailyBudgetUSD - spent
	if left <= 0 {
		return nil
	}
	o.pass(ctx, false, RunOpts{BudgetUSD: left})
	return nil
}

func (o *Observer) setProgress(fn func(p *Progress)) {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()
	fn(&o.progress)
}

// task is one task with open drafts.
type task struct {
	key     string
	project domain.ProjectRecord
	drafts  []domain.LearnDraft
	newest  time.Time
	outcome domain.LearnOutcome
	plans   []decide.Plan
	prs     []string
}

// shared is what every task of a pass reads.
type shared struct {
	projects  []domain.ProjectRecord
	drafts    []domain.LearnDraft
	sources   []domain.LearnRuleSource
	chunks    map[string]domain.LearnRuleChunk
	protected []domain.LearnProtectedRule
	skills    []skills.Skill
	redactor  *redact.Redactor
}

func (o *Observer) pass(ctx context.Context, manual bool, opts RunOpts) {
	o.setProgress(func(p *Progress) {
		*p = Progress{Running: true, Manual: manual, StartedAt: o.clock(), BudgetUSD: opts.BudgetUSD}
	})
	stop := ""
	defer func() {
		o.setProgress(func(p *Progress) { p.Running, p.FinishedAt, p.StopReason = false, o.clock(), stop })
	}()
	sh, err := o.load(ctx)
	if err != nil {
		stop = err.Error()
		return
	}
	tasks := o.readyTasks(ctx, sh, opts)
	var (
		mu       sync.Mutex
		spent    float64
		failures int
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, parallel)
	for _, t := range tasks {
		mu.Lock()
		switch {
		case stop != "":
		case ctx.Err() != nil:
			stop = "stopped"
		case spent >= opts.BudgetUSD:
			stop = "budget reached"
		case failures >= stopAfterFailures:
			stop = fmt.Sprintf("stopped after %d failed runs in a row", failures)
		}
		halt := stop != ""
		mu.Unlock()
		if halt {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(t task) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := o.decideTask(ctx, sh, t)
			mu.Lock()
			defer mu.Unlock()
			spent += res.cost
			if err != nil {
				failures++
				b := o.backoff[t.key]
				b.failures++
				wait := min(failureBackoff<<(b.failures-1), maxBackoff)
				b.until = o.clock().Add(wait)
				o.backoff[t.key] = b
				o.logger.Warn("learn-decide: task failed", "task", t.key, "err", err)
				o.setProgress(func(p *Progress) { p.Failed++; p.CostUSD = spent; p.LastError = err.Error() })
				return
			}
			failures = 0
			delete(o.backoff, t.key)
			o.setProgress(func(p *Progress) {
				p.Tasks++
				p.Proposals += res.kept
				p.Dropped += res.dropped
				p.CostUSD = spent
			})
		}(t)
	}
	wg.Wait()
}

func (o *Observer) load(ctx context.Context) (shared, error) {
	var sh shared
	var err error
	if sh.projects, err = o.store.ListProjects(ctx); err != nil {
		return sh, err
	}
	if sh.drafts, err = o.store.ListAllLearnDrafts(ctx); err != nil {
		return sh, err
	}
	if sh.sources, err = o.store.ListLearnRuleSources(ctx); err != nil {
		return sh, err
	}
	if sh.chunks, err = o.store.ListLearnRuleChunks(ctx); err != nil {
		return sh, err
	}
	if sh.protected, err = o.store.ListLearnProtectedRules(ctx); err != nil {
		return sh, err
	}
	repos := map[string]string{}
	for _, p := range sh.projects {
		if p.Config.LearnFromSessions && p.ArchivedAt.IsZero() {
			repos[p.ID] = p.Path
		}
	}
	sh.skills = skills.Index(skills.Dirs{Home: o.dirs.Home, DataDir: o.dirs.DataDir, Learned: o.dirs.Learned, Repos: repos})
	var dict []redact.Value
	if o.cfg.Dictionary != nil {
		dict = o.cfg.Dictionary(sh.projects)
	}
	sh.redactor = redact.New(dict)
	return sh, nil
}

// readyTasks groups the open drafts of learning projects by task and keeps
// the ones ready to decide (or the one task asked for).
func (o *Observer) readyTasks(ctx context.Context, sh shared, opts RunOpts) []task {
	byID := map[string]domain.ProjectRecord{}
	for _, p := range sh.projects {
		if p.Config.LearnFromSessions && p.ArchivedAt.IsZero() && (opts.Project == "" || opts.Project == p.ID) {
			byID[p.ID] = p
		}
	}
	tasks := map[string]*task{}
	var keys []string
	for _, d := range sh.drafts {
		p, ok := byID[string(d.ProjectID)]
		if !ok || d.Status != domain.LearnDraftOpen || (opts.Task != "" && d.TaskKey != opts.Task) {
			continue
		}
		t := tasks[d.TaskKey]
		if t == nil {
			t = &task{key: d.TaskKey, project: p}
			tasks[d.TaskKey] = t
			keys = append(keys, d.TaskKey)
		}
		t.drafts = append(t.drafts, d)
		if d.CreatedAt.After(t.newest) {
			t.newest = d.CreatedAt
		}
	}
	sort.Strings(keys)
	now := o.clock()
	var out []task
	for _, k := range keys {
		t := tasks[k]
		if b, ok := o.backoff[k]; ok && now.Before(b.until) && opts.Task == "" {
			continue
		}
		facts, branches := o.sessionFacts(ctx, t)
		r := decide.Assess(k, facts, t.newest, now)
		if !r.Ready && opts.Task == "" {
			continue
		}
		t.outcome = r.Outcome
		for _, f := range facts {
			for _, pr := range f.PRs {
				t.prs = append(t.prs, "#"+strconv.Itoa(pr.Number)+" "+pr.URL)
			}
		}
		t.plans = o.plans(t.project.ID, branches)
		out = append(out, *t)
	}
	return out
}

func (o *Observer) sessionFacts(ctx context.Context, t *task) ([]decide.SessionFacts, []string) {
	var recs []domain.SessionRecord
	switch {
	case decide.CrewID(t.key) != "":
		all, err := o.store.ListSessions(ctx, domain.ProjectID(t.project.ID))
		if err == nil {
			for _, r := range all {
				if string(r.CrewID) == decide.CrewID(t.key) {
					recs = append(recs, r)
				}
			}
		}
	case decide.SoloSession(t.key) != "":
		if r, ok, err := o.store.GetSession(ctx, decide.SoloSession(t.key)); err == nil && ok {
			recs = append(recs, r)
		}
	}
	facts := make([]decide.SessionFacts, 0, len(recs))
	var branches []string
	for _, r := range recs {
		prs, _ := o.store.ListPRsBySession(ctx, r.ID)
		facts = append(facts, decide.SessionFacts{Session: r, PRs: prs})
		if r.Metadata.Branch != "" {
			branches = append(branches, r.Metadata.Branch)
		}
	}
	return facts, branches
}

// plans reads the knowledge-store plans of the task's branches: files named
// after the branch's last segment or the whole branch with / as -.
func (o *Observer) plans(project string, branches []string) []decide.Plan {
	dir := filepath.Join(o.dirs.KnowledgeDir, project, "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []decide.Plan
	for _, b := range branches {
		prefixes := []string{strings.ReplaceAll(b, "/", "-") + "--", filepath.Base(b) + "--"}
		for _, e := range entries {
			if len(out) == maxPlans {
				return out
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".md") || (!strings.HasPrefix(name, prefixes[0]) && !strings.HasPrefix(name, prefixes[1])) {
				continue
			}
			bs, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			text := string(bs)
			if len(text) > planBytes {
				text = text[:planBytes]
			}
			out = append(out, decide.Plan{Path: filepath.Join(dir, name), Text: text})
		}
	}
	return out
}

type result struct {
	cost    float64
	kept    int
	dropped int
}

// decideTask runs decide and the verifier for one task and commits the result.
func (o *Observer) decideTask(ctx context.Context, sh shared, t task) (result, error) {
	var res result
	pid := domain.ProjectID(t.project.ID)
	proposals, err := o.store.ListSkillProposals(ctx)
	if err != nil {
		return res, err
	}
	var others []domain.LearnDraft
	for _, d := range sh.drafts {
		if d.TaskKey != t.key {
			others = append(others, d)
		}
	}
	corpus := rules.Corpus(sh.sources, sh.chunks, pid)
	shaped, err := decide.Build(decide.Context{
		TaskKey: t.key, ProjectID: pid, Outcome: t.outcome, PRs: t.prs, Plans: t.plans, Drafts: t.drafts,
		Others: others, Corpus: corpus, Protected: sh.protected, Skills: sh.skills, RuleFiles: o.ruleFiles(pid),
		Proposals: proposals,
	})
	if err != nil {
		return res, err
	}
	s := o.settings()
	out, cost, err := o.call(ctx, t, domain.LearnJobDecide, s, decide.SystemPrompt, decide.Schema, shaped.JSON, func(j *domain.LearnJob) {
		j.Turns = len(t.drafts)
	})
	res.cost += cost
	if err != nil {
		return res, err
	}
	answer, err := decide.ParseDecide(out)
	if err != nil {
		return res, err
	}
	env := decide.Env{
		ProjectID: pid, TaskKey: t.key, Outcome: t.outcome, Home: o.dirs.Home, Learned: o.dirs.Learned,
		KnowledgeDir: o.dirs.KnowledgeDir, Skills: sh.skills, Shaped: shaped, Protected: sh.protected,
		Proposals: proposals, Redactor: sh.redactor, Read: readFile,
	}
	cands, _ := decide.Prepare(env, answer)
	vin, err := decide.VerifierInput(env, corpus, cands)
	if err != nil {
		return res, err
	}
	if vin != "" {
		vout, vcost, err := o.call(ctx, t, domain.LearnJobVerify, s, decide.VerifierPrompt, decide.VerifierSchema, vin, nil)
		res.cost += vcost
		if err != nil {
			return res, err
		}
		reviews, err := decide.ParseVerifier(vout)
		if err != nil {
			return res, err
		}
		decide.ApplyReviews(env, cands, reviews)
	}

	o.commitMu.Lock()
	defer o.commitMu.Unlock()
	// Settle against the proposals as they are now: another task of this pass
	// may have just proposed on the same target.
	if env.Proposals, err = o.store.ListSkillProposals(ctx); err != nil {
		return res, err
	}
	decide.Finalize(env, cands)
	commit := domain.LearnDecideResult{TaskKey: t.key, ProjectID: pid, Outcome: t.outcome}
	consumed := map[int64]bool{}
	own := map[int64]bool{}
	for _, d := range t.drafts {
		own[d.ID] = true
	}
	for _, c := range cands {
		commit.Proposals = append(commit.Proposals, c.Proposal)
		if c.Drop != "" {
			res.dropped++
			continue
		}
		res.kept++
		for _, id := range c.Proposal.EvidenceIDs {
			if own[id] {
				consumed[id] = true
			}
		}
	}
	for _, d := range t.drafts {
		if consumed[d.ID] {
			commit.Consumed = append(commit.Consumed, d.ID)
		} else {
			commit.Dropped = append(commit.Dropped, d.ID)
		}
	}
	return res, o.store.CommitDecide(ctx, commit, o.clock().UTC())
}

// call runs one sealed model call, recorded as a job.
func (o *Observer) call(ctx context.Context, t task, kind domain.LearnJobKind, s learnsettings.Settings, system, schema, input string, prep func(*domain.LearnJob)) ([]byte, float64, error) {
	job := domain.LearnJob{ProjectID: domain.ProjectID(t.project.ID), Kind: kind, TaskKey: t.key, Model: s.DecideModel, StartedAt: o.clock().UTC()}
	if prep != nil {
		prep(&job)
	}
	id, err := o.store.StartLearnJob(ctx, job)
	if err != nil {
		return nil, 0, err
	}
	job.ID = id
	out, runErr := o.runner.Run(ctx, llm.Request{Model: s.DecideModel, Effort: s.DecideEffort, SystemPrompt: system, Schema: []byte(schema), Input: input})
	job.FinishedAt = o.clock().UTC()
	if runErr != nil {
		job.Error = runErr.Error()
		var le *llm.Error
		if errors.As(runErr, &le) {
			job.StderrTail, job.CostUSD = le.StderrTail, le.CostUSD
		}
		if err := o.store.FailLearnJob(ctx, job); err != nil {
			o.logger.Warn("learn-decide: record failed run", "err", err)
		}
		return nil, job.CostUSD, runErr
	}
	job.CostUSD, job.InputTokens, job.OutputTokens, job.DurationMS = out.CostUSD, out.InputTokens, out.OutputTokens, out.DurationMS
	if err := o.store.FinishLearnJob(ctx, job); err != nil {
		return nil, job.CostUSD, err
	}
	return out.Output, job.CostUSD, nil
}

// ruleFiles are the files a lesson may be added to, with their headings.
func (o *Observer) ruleFiles(project domain.ProjectID) []decide.RuleFile {
	var out []decide.RuleFile
	for _, f := range []struct{ path, scope string }{
		{filepath.Join(o.dirs.Home, ".claude", "CLAUDE.md"), "global"},
		{filepath.Join(o.dirs.KnowledgeDir, string(project), "INDEX.md"), "project:" + string(project)},
	} {
		text, ok, err := readFile(f.path)
		if err != nil || !ok {
			continue
		}
		out = append(out, decide.RuleFile{Path: f.path, Scope: f.scope, Headings: decide.Headings(text)})
	}
	return out
}

func readFile(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}
