package releasetrain

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// Tick is how often the poll loop runs a pass.
const Tick = 15 * time.Second

const (
	maxPagesPerPass = 3
	unplannedTTL    = 24 * time.Hour
	authPause       = 10 * time.Minute
	reprobeEvery    = 5 * time.Minute
	backoffBase     = 30 * time.Second
)

// HostLimits bounds one host per pass.
type HostLimits struct {
	Requests    int
	Concurrency int
}

// DefaultLimits are the per-pass budgets from the architecture: GitLab 40/4, Atlassian 20/2.
var DefaultLimits = map[string]HostLimits{
	"gitlab": {Requests: 40, Concurrency: 4},
	"jira":   {Requests: 20, Concurrency: 2},
}

// Store is what the poller needs from storage.
type Store interface {
	LoadReleaseSnapshot(ctx context.Context) (rt.Snapshot, []rt.Resource, error)
	PlanReleaseResources(ctx context.Context, specs []rt.ResourceSpec, now time.Time) error
	WriteReleaseSnapshot(ctx context.Context, spec rt.ResourceSpec, items []rt.Item, at time.Time) error
	WriteReleasePage(ctx context.Context, spec rt.ResourceSpec, items []rt.Item, cursor []byte, complete *time.Time, at time.Time) error
	MarkReleaseFailure(ctx context.Context, spec rt.ResourceSpec, f rt.Failure) error
	DeleteUnplannedReleaseResources(ctx context.Context, before time.Time) (int64, error)
}

// PollerDeps wires a Poller.
type PollerDeps struct {
	Config  *ConfigLoader
	Store   Store
	Sources []Source
	Clock   func() time.Time
	Rand    func() float64
	Limits  map[string]HostLimits
	Logger  *slog.Logger
}

// Poller runs poll passes. Its only memory between passes is the credential
// probe schedule; backoff and pauses live in the rows.
type Poller struct {
	d PollerDeps

	mu       sync.Mutex
	probes   map[string]*probeState
	lastPass PassStats
}

type probeState struct {
	ok     bool
	next   time.Time
	logged bool
}

// PassStats is what the last pass did, per host.
type PassStats struct {
	At       *time.Time     `json:"at,omitempty"`
	Requests map[string]int `json:"requests"`
}

// NewPoller returns a Poller.
func NewPoller(d PollerDeps) *Poller {
	if d.Limits == nil {
		d.Limits = DefaultLimits
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Poller{d: d, probes: map[string]*probeState{}}
}

type planned struct {
	src  Source
	spec rt.ResourceSpec
	row  rt.Resource
}

// Poll runs one pass: plan, prune, then fetch due resources within each host's budget.
func (p *Poller) Poll(ctx context.Context) error {
	cfg := p.d.Config.Load()
	if cfg.Config == nil {
		return nil
	}
	now := p.d.Clock().UTC()
	snap, _, err := p.d.Store.LoadReleaseSnapshot(ctx)
	if err != nil {
		return err
	}
	view := rt.NewPlanView(*cfg.Config, snap, rt.Env{Now: now, Loc: cfg.Loc, AgeOut: cfg.Config.AgeOutDays})
	var all []planned
	var specs []rt.ResourceSpec
	for _, src := range p.d.Sources {
		for _, s := range src.Plan(view) {
			if info, ok := rt.LookupKind(s.Kind); ok {
				s.Mode, s.Version = info.Mode, info.Version
				if s.Budget == 0 {
					s.Budget = info.Budget
				}
			}
			all = append(all, planned{src: src, spec: s})
			specs = append(specs, s)
		}
	}
	if err := p.d.Store.PlanReleaseResources(ctx, specs, now); err != nil {
		return err
	}
	if _, err := p.d.Store.DeleteUnplannedReleaseResources(ctx, now.Add(-unplannedTTL)); err != nil {
		return err
	}
	_, rows, err := p.d.Store.LoadReleaseSnapshot(ctx)
	if err != nil {
		return err
	}
	byKey := map[string]rt.Resource{}
	for _, r := range rows {
		byKey[string(r.Kind)+" "+string(r.Scope)] = r
	}
	hosts := map[string][]planned{}
	var order []string
	for _, pl := range all {
		pl.row = byKey[specKey(pl.spec)]
		h := pl.src.System()
		if _, ok := hosts[h]; !ok {
			order = append(order, h)
		}
		hosts[h] = append(hosts[h], pl)
	}

	stats := PassStats{At: &now, Requests: map[string]int{}}
	var wg sync.WaitGroup
	var statsMu sync.Mutex
	for _, h := range order {
		wg.Add(1)
		go func(host string, res []planned) {
			defer wg.Done()
			n := p.pollHost(ctx, host, res, view, now)
			statsMu.Lock()
			stats.Requests[host] = n
			statsMu.Unlock()
		}(h, hosts[h])
	}
	wg.Wait()
	p.mu.Lock()
	p.lastPass = stats
	p.mu.Unlock()
	return ctx.Err()
}

// LastPass reports what the most recent pass did.
func (p *Poller) LastPass() PassStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastPass
}

func (p *Poller) pollHost(ctx context.Context, host string, res []planned, view rt.PlanView, now time.Time) int {
	if !p.credentialsOK(ctx, host, res, now) {
		return 0
	}
	var pausedUntil time.Time
	for _, pl := range res {
		if (pl.row.ErrorClass == rt.ErrRateLimited || pl.row.ErrorClass == rt.ErrAuth) && pl.row.NextAttemptAt != nil && pl.row.NextAttemptAt.After(pausedUntil) {
			pausedUntil = *pl.row.NextAttemptAt
		}
	}
	if now.Before(pausedUntil) {
		return 0
	}
	due := dueOrder(res, now)
	limits := p.d.Limits[host]
	if limits.Concurrency < 1 {
		limits.Concurrency = 1
	}
	var used atomic.Int64
	hostCtx, pause := context.WithCancel(withRequestCounter(ctx, &used))
	defer pause()
	sem := make(chan struct{}, limits.Concurrency)
	var wg sync.WaitGroup
	var pauseOnce sync.Once
	for _, pl := range due {
		sem <- struct{}{}
		if hostCtx.Err() != nil || (limits.Requests > 0 && int(used.Load()) >= limits.Requests) {
			<-sem
			break
		}
		wg.Add(1)
		go func(pl planned) {
			defer wg.Done()
			defer func() { <-sem }()
			if fe := p.fetchOne(hostCtx, pl, view, now, limits.Requests, &used); fe != nil && pausesHost(fe.Class) {
				pauseOnce.Do(func() {
					pause()
					p.pauseHost(ctx, res, pl, fe, now)
				})
			}
		}(pl)
	}
	wg.Wait()
	return int(used.Load())
}

func pausesHost(c rt.ErrorClass) bool { return c == rt.ErrRateLimited || c == rt.ErrAuth }

func dueOrder(res []planned, now time.Time) []planned {
	due := make([]planned, 0, len(res))
	for _, pl := range res {
		r := pl.row
		switch {
		case r.NextAttemptAt != nil:
			if now.Before(*r.NextAttemptAt) {
				continue
			}
		case r.CompleteAt != nil && now.Before(r.CompleteAt.Add(pl.spec.Budget/2)):
			continue
		}
		due = append(due, pl)
	}
	sort.SliceStable(due, func(i, j int) bool {
		a, b := due[i].row.CompleteAt, due[j].row.CompleteAt
		switch {
		case a == nil || b == nil:
			return a == nil && b != nil
		case !a.Equal(*b):
			return a.Before(*b)
		}
		return specKey(due[i].spec) < specKey(due[j].spec)
	})
	return due
}

func (p *Poller) credentialsOK(ctx context.Context, host string, res []planned, now time.Time) bool {
	p.mu.Lock()
	st := p.probes[host]
	if st == nil {
		st = &probeState{}
		p.probes[host] = st
	}
	probe := !now.Before(st.next)
	p.mu.Unlock()
	if !probe {
		return st.ok
	}
	ok, err := res[0].src.Credentials(ctx)
	p.mu.Lock()
	st.ok, st.next = ok && err == nil, now.Add(reprobeEvery)
	firstMiss := !st.ok && !st.logged
	if st.ok {
		st.logged = false
	} else {
		st.logged = true
	}
	p.mu.Unlock()
	if st.ok {
		return true
	}
	if firstMiss {
		p.d.Logger.Warn("release trains: no usable credentials, sources unconfigured until they appear", "host", host, "err", err)
	}
	for _, pl := range res {
		f := rt.Failure{At: now, Failures: pl.row.Failures, Class: rt.ErrUnconfigured, Message: "no " + host + " credentials", NextAttemptAt: now.Add(reprobeEvery)}
		if err := p.d.Store.MarkReleaseFailure(ctx, pl.spec, f); err != nil {
			p.d.Logger.Warn("release trains: record unconfigured", "resource", specKey(pl.spec), "err", err)
		}
	}
	return false
}

func (p *Poller) fetchOne(ctx context.Context, pl planned, view rt.PlanView, now time.Time, budget int, used *atomic.Int64) *FetchError {
	cursor := pl.row.Cursor
	pages := 1
	if pl.spec.Mode == rt.Incremental {
		pages = maxPagesPerPass
	}
	for page := 0; page < pages; page++ {
		if page > 0 && budget > 0 && int(used.Load()) >= budget {
			return nil
		}
		batch, err := pl.src.Fetch(ctx, pl.spec, view, cursor)
		if err != nil && ctx.Err() != nil {
			return cancelledByPause(err)
		}
		if err == nil && pl.spec.Mode != rt.Incremental && !batch.Complete {
			err = &FetchError{Class: rt.ErrTruncated, Err: errIncompleteBatch}
		}
		if err != nil {
			fe := classify(err)
			p.fail(ctx, pl, fe, now)
			return fe
		}
		if pl.spec.Mode != rt.Incremental {
			p.write(pl, p.d.Store.WriteReleaseSnapshot(ctx, pl.spec, batch.Items, now))
			return nil
		}
		var complete *time.Time
		if batch.Complete {
			complete = &now
		}
		if err := p.d.Store.WriteReleasePage(ctx, pl.spec, batch.Items, batch.Cursor, complete, now); err != nil {
			p.write(pl, err)
			return nil
		}
		cursor = batch.Cursor
		if batch.Complete {
			return nil
		}
	}
	return nil
}

// cancelledByPause drops a fetch another fetch's host pause cut short; that
// pause already wrote this resource's backoff.
func cancelledByPause(error) *FetchError { return nil }

func (p *Poller) write(pl planned, err error) {
	if err != nil {
		p.d.Logger.Error("release trains: store write failed", "resource", specKey(pl.spec), "err", err)
	}
}

func (p *Poller) fail(ctx context.Context, pl planned, fe *FetchError, now time.Time) {
	failures := pl.row.Failures + 1
	next := now.Add(backoff(pl.spec.Budget, failures, p.d.Rand()))
	switch fe.Class {
	case rt.ErrRateLimited:
		if until := now.Add(fe.RetryAfter); next.Before(until) {
			next = until
		}
	case rt.ErrAuth:
		next = now.Add(authPause)
	}
	msg := sanitize(fe.Err.Error())
	p.d.Logger.Warn("release trains: fetch failed", "resource", specKey(pl.spec), "class", fe.Class, "err", msg)
	p.write(pl, p.d.Store.MarkReleaseFailure(ctx, pl.spec, rt.Failure{At: now, Failures: failures, Class: fe.Class, Message: msg, NextAttemptAt: next}))
	fe.RetryAfter = next.Sub(now)
}

func (p *Poller) pauseHost(ctx context.Context, res []planned, cause planned, fe *FetchError, now time.Time) {
	until := now.Add(fe.RetryAfter)
	msg := sanitize(fe.Err.Error())
	for _, pl := range res {
		if specKey(pl.spec) == specKey(cause.spec) {
			continue
		}
		f := rt.Failure{At: now, Failures: pl.row.Failures, Class: fe.Class, Message: "host paused: " + msg, NextAttemptAt: until}
		p.write(pl, p.d.Store.MarkReleaseFailure(ctx, pl.spec, f))
	}
}

// backoff is min(4 x budget, 30s x 2^(failures-1)) with +-20% jitter; jitter is in [0, 1].
func backoff(budget time.Duration, failures int, jitter float64) time.Duration {
	d := time.Duration(float64(backoffBase) * math.Pow(2, float64(failures-1)))
	if limit := 4 * budget; limit > 0 && d > limit {
		d = limit
	}
	return time.Duration(float64(d) * (0.8 + 0.4*jitter))
}
