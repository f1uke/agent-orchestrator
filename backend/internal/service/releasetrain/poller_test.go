package releasetrain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var t0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

type fetchFunc func(spec rt.ResourceSpec, cursor []byte) (Batch, error)

type fakeSource struct {
	system string
	specs  []rt.ResourceSpec
	fetch  fetchFunc

	mu      sync.Mutex
	creds   bool
	probes  int
	calls   []string
	planned int
}

func (f *fakeSource) System() string { return f.system }

func (f *fakeSource) Plan(rt.PlanView) []rt.ResourceSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planned++
	return f.specs
}

func (f *fakeSource) Fetch(ctx context.Context, spec rt.ResourceSpec, _ rt.PlanView, cursor []byte) (Batch, error) {
	CountRequest(ctx)
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf("%s %s %s", spec.Kind, spec.Scope, cursor))
	f.mu.Unlock()
	return f.fetch(spec, cursor)
}

func (f *fakeSource) Credentials(context.Context, rt.PlanView) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes++
	return f.creds, nil
}

func (f *fakeSource) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

type harness struct {
	t       *testing.T
	dataDir string
	store   *sqlite.Store
	now     time.Time
	logs    *bytes.Buffer
	sources []Source
	limits  map[string]HostLimits
}

func newHarness(t *testing.T, sources ...Source) *harness {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(rt.ExampleConfigJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &harness{t: t, dataDir: dir, store: st, now: t0, logs: &bytes.Buffer{}, sources: sources,
		limits: map[string]HostLimits{"gitlab": {Requests: 40, Concurrency: 1}, "jira": {Requests: 20, Concurrency: 1}}}
}

func (h *harness) poller() *Poller {
	return NewPoller(PollerDeps{
		Config:  NewConfigLoader(filepath.Join(h.dataDir, ConfigFileName)),
		Store:   h.store,
		Sources: h.sources,
		Clock:   func() time.Time { return h.now },
		Rand:    func() float64 { return 0.5 },
		Limits:  h.limits,
		Logger:  slog.New(slog.NewTextHandler(h.logs, nil)),
	})
}

func (h *harness) poll(p *Poller) {
	h.t.Helper()
	if err := p.Poll(context.Background()); err != nil {
		h.t.Fatalf("poll: %v", err)
	}
}

func (h *harness) rows() map[rt.Scope]rt.Resource {
	h.t.Helper()
	_, rows, err := h.store.LoadReleaseSnapshot(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[rt.Scope]rt.Resource{}
	for _, r := range rows {
		out[r.Scope] = r
	}
	return out
}

func (h *harness) snapshot() rt.Snapshot {
	h.t.Helper()
	s, _, err := h.store.LoadReleaseSnapshot(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func tagSpec(scope string) rt.ResourceSpec { return rt.SpecFor(rt.KindTags, rt.Scope(scope), "di") }

func mrSpec(scope string) rt.ResourceSpec { return rt.SpecFor(rt.KindMRs, rt.Scope(scope), "di") }

func tagsBatch(t *testing.T, names ...string) Batch {
	t.Helper()
	b := Batch{Complete: true}
	for _, n := range names {
		it, err := rt.EncodeItem(rt.KindTags, rt.TagFact{Project: 1001, Name: n, CommitOn: rt.Day{Y: 2026, M: 10, D: 1}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		b.Items = append(b.Items, it)
	}
	return b
}

func tagNamesOf(s rt.Snapshot, scope string) []string {
	var out []string
	for _, tag := range rt.Items(s, rt.KindTags, rt.Scope(scope)) {
		out = append(out, tag.Name)
	}
	return out
}

func constFetch(t *testing.T) fetchFunc {
	return func(rt.ResourceSpec, []byte) (Batch, error) { return tagsBatch(t, "1.0.0"), nil }
}

func TestPollWithoutConfigDoesNothing(t *testing.T) {
	src := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1")}, fetch: constFetch(t)}
	h := newHarness(t, src)
	if err := os.Remove(filepath.Join(h.dataDir, ConfigFileName)); err != nil {
		t.Fatal(err)
	}
	h.poll(h.poller())
	if src.planned != 0 || len(h.rows()) != 0 {
		t.Fatalf("planned %d rows %d, want nothing without a config", src.planned, len(h.rows()))
	}
}

func TestPollTakesMostStaleFirstWithinTheHostRequestBudget(t *testing.T) {
	src := &fakeSource{system: "gitlab", creds: true, fetch: constFetch(t)}
	for _, s := range []string{"p:a", "p:b", "p:c", "p:d", "p:e"} {
		src.specs = append(src.specs, tagSpec(s))
	}
	h := newHarness(t, src)
	h.limits["gitlab"] = HostLimits{Requests: 3, Concurrency: 1}
	p := h.poller()

	if err := h.store.PlanReleaseResources(context.Background(), src.specs, t0); err != nil {
		t.Fatal(err)
	}
	for scope, age := range map[string]time.Duration{"p:a": 2 * time.Minute, "p:b": 30 * time.Minute, "p:c": 9 * time.Minute, "p:e": 20 * time.Minute} {
		h.now = t0.Add(-age)
		if err := h.store.WriteReleaseSnapshot(context.Background(), tagSpec(scope), nil, h.now); err != nil {
			t.Fatal(err)
		}
	}
	h.now = t0
	h.poll(p)
	if got, want := src.takeCalls(), []string{"gitlab.tags p:d ", "gitlab.tags p:b ", "gitlab.tags p:e "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first pass fetched %v, want the never-complete one then the stalest, three within budget: %v", got, want)
	}
	h.poll(p)
	if got, want := src.takeCalls(), []string{"gitlab.tags p:c "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second pass fetched %v, want %v (p:a is complete 2m ago, under half its 10m budget)", got, want)
	}
}

func mrPages(t *testing.T, pages int) fetchFunc {
	return func(_ rt.ResourceSpec, cursor []byte) (Batch, error) {
		page := 1
		if cursor != nil {
			var c struct{ Page int }
			if err := json.Unmarshal(cursor, &c); err != nil {
				return Batch{}, err
			}
			page = c.Page
		}
		it, err := rt.EncodeItem(rt.KindMRs, rt.MRFact{Project: 1001, IID: page, Title: fmt.Sprint("mr ", page), CreatedOn: rt.Day{Y: 2026, M: 10, D: 1}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := json.Marshal(struct{ Page int }{page + 1})
		return Batch{Items: []rt.Item{it}, Cursor: next, Complete: page >= pages}, nil
	}
}

func mrCount(s rt.Snapshot) int { return len(rt.Items(s, rt.KindMRs, "p:1")) }

func TestPollBackfillsThreePagesPerPassAndCommitsEachPage(t *testing.T) {
	src := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{mrSpec("p:1")}, fetch: mrPages(t, 7)}
	h := newHarness(t, src)
	p := h.poller()

	h.poll(p)
	if got := src.takeCalls(); !reflect.DeepEqual(got, []string{"gitlab.mrs p:1 ", `gitlab.mrs p:1 {"Page":2}`, `gitlab.mrs p:1 {"Page":3}`}) {
		t.Fatalf("pass 1 = %v", got)
	}
	r := h.rows()["p:1"]
	if r.CompleteAt != nil || string(r.Cursor) != `{"Page":4}` || mrCount(h.snapshot()) != 3 {
		t.Fatalf("after pass 1 row = %+v cursor %s items %d; want backfilling at page 4", r, r.Cursor, mrCount(h.snapshot()))
	}
	h.now = h.now.Add(15 * time.Second)
	h.poll(p)
	h.now = h.now.Add(15 * time.Second)
	h.poll(p)
	if got := src.takeCalls(); len(got) != 4 || got[3] != `gitlab.mrs p:1 {"Page":7}` {
		t.Fatalf("passes 2 and 3 = %v, want pages 4-7 then stop on the short page", got)
	}
	r = h.rows()["p:1"]
	if r.CompleteAt == nil || !r.CompleteAt.Equal(h.now) || mrCount(h.snapshot()) != 7 {
		t.Fatalf("after backfill row = %+v items %d", r, mrCount(h.snapshot()))
	}
}

func TestPollResumesFromTheLastCommittedPageAfterAFailure(t *testing.T) {
	src := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{mrSpec("p:1")}}
	pages := mrPages(t, 5)
	src.fetch = func(spec rt.ResourceSpec, cursor []byte) (Batch, error) {
		if string(cursor) == `{"Page":2}` {
			return Batch{}, &FetchError{Class: rt.ErrUnavailable, Err: errors.New("gitlab scm: 502 bad gateway")}
		}
		return pages(spec, cursor)
	}
	h := newHarness(t, src)
	h.poll(h.poller())
	src.takeCalls()
	r := h.rows()["p:1"]
	if string(r.Cursor) != `{"Page":2}` || r.ErrorClass != rt.ErrUnavailable || r.NextAttemptAt == nil || mrCount(h.snapshot()) != 1 {
		t.Fatalf("after the failed page row = %+v cursor %s", r, r.Cursor)
	}

	src.fetch = pages
	h.now = *r.NextAttemptAt
	h.poll(h.poller())
	if got := src.takeCalls(); len(got) == 0 || got[0] != `gitlab.mrs p:1 {"Page":2}` {
		t.Fatalf("a new poller started at %v, want page 2 from the committed cursor", got)
	}
}

func TestRateLimitPausesTheHostAcrossARestart(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1"), tagSpec("p:2")}}
	gl.fetch = func(spec rt.ResourceSpec, _ []byte) (Batch, error) {
		return Batch{}, fmt.Errorf("list tags: %w", &FetchError{Class: rt.ErrRateLimited, RetryAfter: 10 * time.Minute, Err: errors.New("429")})
	}
	jira := &fakeSource{system: "jira", creds: true, specs: []rt.ResourceSpec{rt.SpecFor(rt.KindJiraVersions, "project", "")}}
	jira.fetch = func(rt.ResourceSpec, []byte) (Batch, error) { return Batch{Complete: true}, nil }
	h := newHarness(t, gl, jira)

	h.poll(h.poller())
	if got := gl.takeCalls(); len(got) != 1 {
		t.Fatalf("gitlab fetched %v after a 429; the host must pause", got)
	}
	if len(jira.takeCalls()) != 1 {
		t.Fatal("a GitLab 429 paused Jira")
	}
	rows := h.rows()
	for _, s := range []rt.Scope{"p:1", "p:2"} {
		if r := rows[s]; r.ErrorClass != rt.ErrRateLimited || r.NextAttemptAt == nil || r.NextAttemptAt.Before(t0.Add(10*time.Minute)) {
			t.Fatalf("%s = %+v, want rate_limited until at least Retry-After", s, r)
		}
	}

	gl.fetch = constFetch(t)
	gl.specs = append(gl.specs, tagSpec("p:3"))
	h.now = t0.Add(9 * time.Minute)
	h.poll(h.poller())
	if got := gl.takeCalls(); len(got) != 0 {
		t.Fatalf("a restarted poller fetched %v inside the 429 window, a newly planned resource included", got)
	}
	h.now = t0.Add(13 * time.Minute)
	h.poll(h.poller())
	if got := gl.takeCalls(); len(got) != 3 {
		t.Fatalf("after the window fetched %v, want both", got)
	}
}

func TestAuthFailurePausesTheHostForTenMinutes(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1"), tagSpec("p:2"), rt.SpecFor(rt.KindMilestones, "p:1", "di")}}
	gl.fetch = func(rt.ResourceSpec, []byte) (Batch, error) {
		return Batch{}, &FetchError{Class: rt.ErrAuth, Err: errors.New("gitlab scm: authentication failed: 401 Unauthorized")}
	}
	h := newHarness(t, gl)
	h.poll(h.poller())
	if got := gl.takeCalls(); len(got) != 1 {
		t.Fatalf("fetched %v after a 401", got)
	}
	for s, r := range h.rows() {
		if r.ErrorClass != rt.ErrAuth || !r.NextAttemptAt.Equal(t0.Add(10*time.Minute)) {
			t.Fatalf("%s = %+v, want auth until +10m", s, r)
		}
	}
	h.now = t0.Add(9 * time.Minute)
	h.poll(h.poller())
	if got := gl.takeCalls(); len(got) != 0 {
		t.Fatalf("fetched %v inside the auth pause", got)
	}
}

func TestMissingCredentialsAreUnconfiguredReprobedEveryFiveMinutesAndLoggedOnce(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: false, specs: []rt.ResourceSpec{tagSpec("p:1")}, fetch: constFetch(t)}
	h := newHarness(t, gl)
	p := h.poller()
	h.poll(p)
	h.now = t0.Add(time.Minute)
	h.poll(p)
	if gl.probes != 1 || len(gl.takeCalls()) != 0 {
		t.Fatalf("probes %d before the 5m re-probe", gl.probes)
	}
	r := h.rows()["p:1"]
	if r.ErrorClass != rt.ErrUnconfigured || !r.NextAttemptAt.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("row = %+v", r)
	}
	h.now = t0.Add(5 * time.Minute)
	h.poll(p)
	if gl.probes != 2 || strings.Count(h.logs.String(), "credentials") != 1 {
		t.Fatalf("probes %d, logs:\n%s", gl.probes, h.logs)
	}
	gl.mu.Lock()
	gl.creds = true
	gl.mu.Unlock()
	h.now = t0.Add(10 * time.Minute)
	h.poll(p)
	if r := h.rows()["p:1"]; r.ErrorClass != "" || r.CompleteAt == nil || len(gl.takeCalls()) != 1 {
		t.Fatalf("after credentials appear row = %+v", r)
	}
}

func TestTruncatedFetchKeepsTheLastCompleteItems(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1")}}
	gl.fetch = func(rt.ResourceSpec, []byte) (Batch, error) { return tagsBatch(t, "1.0.0", "1.1.0"), nil }
	h := newHarness(t, gl)
	h.poll(h.poller())

	gl.fetch = func(rt.ResourceSpec, []byte) (Batch, error) {
		return Batch{}, &FetchError{Class: rt.ErrTruncated, Err: errors.New("gitlab scm: incomplete read: tags at 3 pages")}
	}
	h.now = t0.Add(6 * time.Minute)
	h.poll(h.poller())
	r := h.rows()["p:1"]
	if r.ErrorClass != rt.ErrTruncated || !r.CompleteAt.Equal(t0) || len(tagNamesOf(h.snapshot(), "p:1")) != 2 {
		t.Fatalf("truncated row = %+v items %v", r, tagNamesOf(h.snapshot(), "p:1"))
	}

	gl.fetch = func(rt.ResourceSpec, []byte) (Batch, error) {
		b := tagsBatch(t, "1.1.0")
		b.Complete = false
		return b, nil
	}
	h.now = *r.NextAttemptAt
	h.poll(h.poller())
	if got := tagNamesOf(h.snapshot(), "p:1"); len(got) != 2 || h.rows()["p:1"].ErrorClass != rt.ErrTruncated {
		t.Fatalf("an incomplete snapshot batch replaced the items: %v", got)
	}

	gl.fetch = func(rt.ResourceSpec, []byte) (Batch, error) { return tagsBatch(t, "1.1.0"), nil }
	h.now = *h.rows()["p:1"].NextAttemptAt
	h.poll(h.poller())
	if got := tagNamesOf(h.snapshot(), "p:1"); !reflect.DeepEqual(got, []string{"1.1.0"}) {
		t.Fatalf("a complete snapshot kept absent items: %v", got)
	}
}

func TestTwoPassesConverge(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1"), mrSpec("p:1")}}
	pages := mrPages(t, 2)
	gl.fetch = func(spec rt.ResourceSpec, cursor []byte) (Batch, error) {
		if spec.Kind == rt.KindMRs.Name {
			return pages(spec, cursor)
		}
		return tagsBatch(t, "1.0.0"), nil
	}
	h := newHarness(t, gl)
	h.poll(h.poller())
	first := h.rows()
	h.poll(h.poller())
	if !reflect.DeepEqual(first, h.rows()) || len(gl.takeCalls()) != 3 {
		t.Fatalf("a second pass at the same instant changed the rows:\n%+v\n%+v", first, h.rows())
	}
}

func TestBackoffDoublesFromThirtySecondsUpToFourBudgets(t *testing.T) {
	for _, tc := range []struct {
		failures int
		jitter   float64
		want     time.Duration
	}{
		{1, 0.5, 30 * time.Second},
		{2, 0.5, time.Minute},
		{4, 0.5, 4 * time.Minute},
		{6, 0.5, 8 * time.Minute},
		{6, 0, 8*time.Minute - 96*time.Second},
		{6, 1, 8*time.Minute + 96*time.Second},
	} {
		if got := backoff(2*time.Minute, tc.failures, tc.jitter); got != tc.want {
			t.Errorf("backoff(2m, %d, %v) = %v, want %v", tc.failures, tc.jitter, got, tc.want)
		}
	}
}

func TestStoredErrorMessagesAreSanitized(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1")}}
	gl.fetch = func(rt.ResourceSpec, []byte) (Batch, error) {
		return Batch{}, &FetchError{Class: rt.ErrUnavailable,
			Err: errors.New(`gitlab scm: GET https://gitlab.example.com/api/v4/projects/1/tags?private_token=SENTINEL-TOKEN&page=2: ` + strings.Repeat("é", 400))}
	}
	h := newHarness(t, gl)
	h.poll(h.poller())
	msg := h.rows()["p:1"].ErrorMessage
	if strings.Contains(msg, "SENTINEL") || strings.Contains(h.logs.String(), "SENTINEL") || len(msg) > 300 || !strings.Contains(msg, "/tags") {
		t.Fatalf("stored message %q (%d bytes)", msg, len(msg))
	}
}
