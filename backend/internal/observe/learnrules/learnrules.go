// Package learnrules keeps learning's standing-rules corpus current: it reads
// the rules agents are already told (Sources), splits each into chunks, has a
// model atomize every chunk it has not seen before (internal/learn/rules), and
// caches the result by content hash. An unchanged source costs nothing; an
// edited one re-atomizes only the chunks the edit touched.
//
// Model runs share learning's daily budget with collect. A chunk whose run
// failed backs off (doubling, up to a day), so a chunk the model cannot handle
// does not spend the budget every hour.
package learnrules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
)

// Timing and bounds.
const (
	DefaultTickInterval = time.Hour
	// parallel is how many chunks are atomized at once.
	parallel = 4
	// stopAfterFailures ends a pass after this many failed runs in a row.
	stopAfterFailures = 3
	failureBackoff    = time.Hour
	maxBackoff        = 24 * time.Hour
	// keepUnused is how long a chunk no source refers to stays cached. Its
	// row is also the record of what its run cost, so it must outlive the
	// day's budget; and an edit that is reverted finds it again.
	keepUnused = 48 * time.Hour
)

// Source is one source's current text.
type Source struct {
	Key       string
	Scope     domain.LearnRuleScope
	ProjectID domain.ProjectID
	Kind      domain.LearnRuleSourceKind
	Label     string
	Text      string
	// Deterministic splits the text with rules.Blocks instead of a model.
	Deterministic bool
	// Memory is a Claude Code memory file: one chunk, one rule, no model.
	Memory bool
}

// Lister returns every source the corpus should hold now.
type Lister func(ctx context.Context) ([]Source, error)

// Store is the persistence the loop needs.
type Store interface {
	ListLearnRuleSources(ctx context.Context) ([]domain.LearnRuleSource, error)
	UpsertLearnRuleSource(ctx context.Context, src domain.LearnRuleSource) error
	DeleteLearnRuleSource(ctx context.Context, key string) error
	LearnRuleChunkHashes(ctx context.Context) (map[string]time.Time, error)
	InsertLearnRuleChunk(ctx context.Context, c domain.LearnRuleChunk) error
	DeleteLearnRuleChunk(ctx context.Context, hash string) error
	LearnSpendSince(ctx context.Context, since time.Time) (float64, error)
}

// Config holds the loop's knobs; zero values use production defaults.
type Config struct {
	Tick   time.Duration
	Clock  func() time.Time
	Logger *slog.Logger
	OnTick func()
}

// Progress is the state of the current or last pass, for status.
type Progress struct {
	Running    bool
	Manual     bool
	StartedAt  time.Time
	FinishedAt time.Time
	Sources    int
	Atomized   int
	Failed     int
	CostUSD    float64
	BudgetUSD  float64
	StopReason string
	LastError  string
}

// Observer is the refresh loop.
type Observer struct {
	store    Store
	runner   llm.Runner
	list     Lister
	settings func() learnsettings.Settings
	tick     time.Duration
	clock    func() time.Time
	logger   *slog.Logger
	onTick   func()

	runMu    sync.Mutex // one pass at a time
	stateMu  sync.Mutex
	progress Progress
	// backoff holds, per chunk hash, when a failed chunk may be tried again and
	// how many times in a row it failed. It is in memory on purpose: a daemon
	// restart is a fair moment to try again.
	backoff map[string]chunkBackoff
}

type chunkBackoff struct {
	until    time.Time
	failures int
}

// New builds the loop.
func New(store Store, runner llm.Runner, list Lister, settings func() learnsettings.Settings, cfg Config) *Observer {
	o := &Observer{store: store, runner: runner, list: list, settings: settings, tick: cfg.Tick, clock: cfg.Clock,
		logger: cfg.Logger, onTick: cfg.OnTick, backoff: map[string]chunkBackoff{}}
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

// Start launches the loop and returns a channel that closes when it exits.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartPollLoop(ctx, o.tick, o.Poll, o.logger, "learn-rules", o.onTick)
}

// Progress reports the current or last pass.
func (o *Observer) Progress() Progress {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()
	return o.progress
}

// ErrBusy refuses a manual refresh while another pass is running.
var ErrBusy = errors.New("a rules refresh is already in progress")

// RunNow starts a refresh at once, spending at most budgetUSD on model runs.
// It returns at once; the pass continues on ctx.
func (o *Observer) RunNow(ctx context.Context, budgetUSD float64) error {
	if budgetUSD <= 0 {
		return errors.New("budget must be positive")
	}
	if !o.runMu.TryLock() {
		return ErrBusy
	}
	go func() {
		defer o.runMu.Unlock()
		o.pass(ctx, true, budgetUSD)
	}()
	return nil
}

// Poll is one background pass within what is left of the daily budget. Sources
// are still listed, recorded and pruned when the budget is spent; only new
// model runs wait for tomorrow.
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
	// Sources are still recorded on a spent day; only model runs wait, so the
	// budget left is never shown below zero.
	o.pass(ctx, false, max(o.settings().DailyBudgetUSD-spent, 0))
	return nil
}

func (o *Observer) setProgress(fn func(p *Progress)) {
	o.stateMu.Lock()
	defer o.stateMu.Unlock()
	fn(&o.progress)
}

// pending is a chunk that needs a model run, and the sources waiting on it.
type pending struct {
	chunk  rules.Chunk
	source string
}

// pass runs one refresh. The caller holds runMu.
func (o *Observer) pass(ctx context.Context, manual bool, budget float64) {
	start := o.clock()
	o.setProgress(func(p *Progress) {
		*p = Progress{Running: true, Manual: manual, StartedAt: start, BudgetUSD: budget}
	})
	stop := ""
	defer func() {
		o.setProgress(func(p *Progress) {
			p.Running = false
			p.FinishedAt = o.clock()
			p.StopReason = stop
		})
	}()

	sources, err := o.list(ctx)
	if err != nil {
		stop = "could not list sources: " + err.Error()
		return
	}
	existing, err := o.store.ListLearnRuleSources(ctx)
	if err != nil {
		stop = err.Error()
		return
	}
	times, err := o.store.LearnRuleChunkHashes(ctx)
	if err != nil {
		stop = err.Error()
		return
	}
	cached := make(map[string]bool, len(times))
	for h := range times {
		cached[h] = true
	}
	prev := map[string]domain.LearnRuleSource{}
	for _, s := range existing {
		prev[s.Key] = s
	}
	o.setProgress(func(p *Progress) { p.Sources = len(sources) })

	// Split every source; deterministic chunks go straight into the cache,
	// model chunks are queued once per hash.
	split := map[string][]rules.Chunk{}
	var queue []pending
	queued := map[string]bool{}
	for _, src := range sources {
		ch := contentHash(src.Text)
		if p, ok := prev[src.Key]; ok && p.ContentHash == ch && p.Error == "" && allCached(p.Chunks, cached) && p.Label == src.Label {
			continue
		}
		var chunks []rules.Chunk
		switch {
		case src.Memory:
			chunks = []rules.Chunk{rules.Whole(src.Text, rules.MemoryVersion)}
		case src.Deterministic:
			chunks = rules.Chunks(src.Text, rules.BlocksVersion)
		default:
			chunks = rules.Chunks(src.Text, rules.Version)
		}
		split[src.Key] = chunks
		for _, c := range chunks {
			if cached[c.Hash] || queued[c.Hash] {
				continue
			}
			if src.Deterministic || src.Memory {
				atoms := rules.Blocks(c.Text())
				if src.Memory {
					atoms = rules.MemoryAtoms(c.Text())
				}
				if err := o.store.InsertLearnRuleChunk(ctx, domain.LearnRuleChunk{Hash: c.Hash, Atoms: toDomain(atoms), CreatedAt: o.clock().UTC()}); err != nil {
					o.logger.Warn("learn-rules: cache split chunk failed", "source", src.Key, "err", err)
					continue
				}
				cached[c.Hash] = true
				continue
			}
			queued[c.Hash] = true
			queue = append(queue, pending{chunk: c, source: src.Label})
		}
	}

	lastErr := map[string]string{}
	stop = o.atomize(ctx, queue, budget, cached, lastErr)

	// Record every changed source, with an error naming what is still missing.
	keep := map[string]bool{}
	for _, src := range sources {
		keep[src.Key] = true
		chunks, changed := split[src.Key]
		if !changed {
			continue
		}
		rec := domain.LearnRuleSource{Key: src.Key, Scope: src.Scope, ProjectID: src.ProjectID, Kind: src.Kind, Label: src.Label,
			ContentHash: contentHash(src.Text), RefreshedAt: o.clock().UTC()}
		missing, why := 0, ""
		for _, c := range chunks {
			rec.Chunks = append(rec.Chunks, domain.LearnRuleChunkRef{Hash: c.Hash, Heading: c.Heading()})
			if !cached[c.Hash] {
				missing++
				if e := lastErr[c.Hash]; e != "" {
					why = e
				}
			}
		}
		if missing > 0 {
			if why == "" {
				why = stop
			}
			if why == "" {
				why = "waiting to be atomized"
			}
			rec.Error = fmt.Sprintf("%d of %d chunks not atomized: %s", missing, len(chunks), why)
		}
		if err := o.store.UpsertLearnRuleSource(ctx, rec); err != nil {
			o.logger.Warn("learn-rules: record source failed", "source", src.Key, "err", err)
		}
	}
	for key := range prev {
		if !keep[key] {
			if err := o.store.DeleteLearnRuleSource(ctx, key); err != nil {
				o.logger.Warn("learn-rules: delete source failed", "source", key, "err", err)
			}
		}
	}
	o.prune(ctx)
}

// atomize runs the queued chunks, a few at a time, until the queue is empty,
// the budget is spent or runs keep failing. It returns why it stopped early.
func (o *Observer) atomize(ctx context.Context, queue []pending, budget float64, cached map[string]bool, lastErr map[string]string) string {
	if len(queue) == 0 {
		return ""
	}
	model := o.settings().RulesModel
	var (
		mu             sync.Mutex
		spent          float64
		failuresInARow int
		stop           string
		wg             sync.WaitGroup
	)
	sem := make(chan struct{}, parallel)
	for _, p := range queue {
		mu.Lock()
		halt := stop
		switch {
		case halt != "":
		case ctx.Err() != nil:
			stop, halt = "stopped", "stopped"
		case spent >= budget:
			stop, halt = "budget reached", "budget reached"
		case failuresInARow >= stopAfterFailures:
			stop, halt = fmt.Sprintf("stopped after %d failed runs in a row", failuresInARow), "failures"
		}
		b, backingOff := o.backoff[p.chunk.Hash]
		backingOff = backingOff && o.clock().Before(b.until)
		if halt == "" && backingOff {
			lastErr[p.chunk.Hash] = "backing off after a failed run"
		}
		mu.Unlock()
		if halt != "" {
			break
		}
		if backingOff {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(p pending) {
			defer wg.Done()
			defer func() { <-sem }()
			cost, err := o.atomizeOne(ctx, p, model)
			mu.Lock()
			defer mu.Unlock()
			spent += cost
			if err != nil {
				failuresInARow++
				lastErr[p.chunk.Hash] = err.Error()
				b := o.backoff[p.chunk.Hash]
				b.failures++
				wait := failureBackoff << (b.failures - 1)
				if wait > maxBackoff || wait <= 0 {
					wait = maxBackoff
				}
				b.until = o.clock().Add(wait)
				o.backoff[p.chunk.Hash] = b
				o.logger.Warn("learn-rules: atomize failed", "source", p.source, "err", err)
				o.setProgress(func(pr *Progress) { pr.Failed++; pr.CostUSD = spent; pr.LastError = err.Error() })
				return
			}
			failuresInARow = 0
			cached[p.chunk.Hash] = true
			delete(o.backoff, p.chunk.Hash)
			o.setProgress(func(pr *Progress) { pr.Atomized++; pr.CostUSD = spent })
		}(p)
	}
	wg.Wait()
	return stop
}

// atomizeOne runs one chunk and caches its atoms. It returns what the run cost
// even when it failed.
func (o *Observer) atomizeOne(ctx context.Context, p pending, model string) (float64, error) {
	input, err := rules.Input(p.source, p.chunk)
	if err != nil {
		return 0, err
	}
	out, err := o.runner.Run(ctx, llm.Request{Model: model, SystemPrompt: rules.SystemPrompt, Schema: []byte(rules.Schema), Input: input})
	if err != nil {
		var le *llm.Error
		if errors.As(err, &le) {
			return le.CostUSD, fmt.Errorf("%w: %s", err, le.StderrTail)
		}
		return 0, err
	}
	atoms, rejected, err := rules.Atoms(out.Output, p.chunk)
	if err != nil {
		return out.CostUSD, err
	}
	err = o.store.InsertLearnRuleChunk(ctx, domain.LearnRuleChunk{
		Hash: p.chunk.Hash, Model: model, Atoms: toDomain(atoms), Rejected: len(rejected), CostUSD: out.CostUSD,
		InputTokens: out.InputTokens, OutputTokens: out.OutputTokens, DurationMS: out.DurationMS, CreatedAt: o.clock().UTC(),
	})
	return out.CostUSD, err
}

// prune drops cached chunks no source has referred to for keepUnused.
func (o *Observer) prune(ctx context.Context) {
	sources, err := o.store.ListLearnRuleSources(ctx)
	if err != nil {
		return
	}
	cached, err := o.store.LearnRuleChunkHashes(ctx)
	if err != nil {
		return
	}
	cutoff := o.clock().Add(-keepUnused)
	used := map[string]bool{}
	for _, s := range sources {
		for _, c := range s.Chunks {
			used[c.Hash] = true
		}
	}
	for h, at := range cached {
		if !used[h] && at.Before(cutoff) {
			if err := o.store.DeleteLearnRuleChunk(ctx, h); err != nil {
				o.logger.Warn("learn-rules: prune chunk failed", "err", err)
			}
		}
	}
}

func allCached(chunks []domain.LearnRuleChunkRef, cached map[string]bool) bool {
	for _, c := range chunks {
		if !cached[c.Hash] {
			return false
		}
	}
	return true
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func toDomain(atoms []rules.Atom) []domain.LearnRuleAtom {
	out := make([]domain.LearnRuleAtom, 0, len(atoms))
	for _, a := range atoms {
		out = append(out, domain.LearnRuleAtom{Text: a.Text, Quote: a.Quote, Tags: a.Tags, Heading: a.Heading})
	}
	return out
}
