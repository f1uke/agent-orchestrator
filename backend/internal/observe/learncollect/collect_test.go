package learncollect_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learncollect"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// fakeRunner answers every call by quoting the first human turn it is given,
// so the grounding check passes, and records what it was asked.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []llm.Request
	fail   error
	cost   float64
	answer func(input string) string
}

func (f *fakeRunner) Run(_ context.Context, req llm.Request) (llm.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.fail != nil {
		return llm.Result{}, &llm.Error{Err: f.fail, StderrTail: "stderr tail", CostUSD: 0.001}
	}
	var in struct {
		Turns []struct {
			ID    string `json:"id"`
			Human string `json:"human"`
		} `json:"turns"`
	}
	_ = json.Unmarshal([]byte(req.Input), &in)
	body := `{"lessons":[]}`
	if f.answer != nil {
		body = f.answer(req.Input)
	} else if len(in.Turns) > 0 {
		b, _ := json.Marshal(map[string]any{"lessons": []map[string]any{{
			"kind": "rule", "statement": "Lesson from " + in.Turns[0].Human, "applies_when": "always", "scope_hint": "project",
			"quote": in.Turns[0].Human, "turn": in.Turns[0].ID, "agent_before": "", "supersedes": "", "confidence": 0.8,
		}}})
		body = string(b)
	}
	return llm.Result{Output: json.RawMessage(body), CostUSD: f.cost, InputTokens: 100, OutputTokens: 10}, nil
}

type rig struct {
	t      *testing.T
	store  *sqlite.Store
	runner *fakeRunner
	now    time.Time
	obs    *learncollect.Observer
	budget float64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	r := &rig{t: t, store: st, runner: &fakeRunner{cost: 0.01}, now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), budget: 2}
	r.obs = learncollect.New(st, r.runner, func() learnsettings.Settings {
		s := learnsettings.Default()
		s.DailyBudgetUSD = r.budget
		return s
	}, learncollect.Config{Clock: func() time.Time { return r.now }})
	return r
}

func (r *rig) project(id string, on bool) {
	r.t.Helper()
	if err := r.store.UpsertProject(context.Background(), domain.ProjectRecord{ID: id, Path: "/repo/" + id, RegisteredAt: r.now, Config: domain.ProjectConfig{LearnFromSessions: on}}); err != nil {
		r.t.Fatal(err)
	}
}

// turns stores captured turns for a session, the newest `age` before now.
func (r *rig) turns(project, session string, age time.Duration, texts ...string) {
	r.t.Helper()
	var ex []domain.LearnExcerpt
	for i, txt := range texts {
		at := r.now.Add(-age).Add(time.Duration(i-len(texts)+1) * time.Second)
		ex = append(ex, domain.LearnExcerpt{
			ProjectID: domain.ProjectID(project), SessionID: domain.SessionID(session), TranscriptPath: "/t/" + session,
			TurnUUID: session + txt, TurnAt: at, SourceClass: domain.LearnSourceTyped, HumanText: txt, CreatedAt: at,
		})
	}
	cursor := domain.LearnCursor{TranscriptPath: "/t/" + session, ProjectID: domain.ProjectID(project), SessionID: domain.SessionID(session)}
	if _, err := r.store.CommitLearnPass(context.Background(), cursor, ex, nil, r.now); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) drafts(project string) []string {
	r.t.Helper()
	ds, err := r.store.ListLearnDrafts(context.Background(), domain.ProjectID(project), 100)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, d := range ds {
		out = append(out, d.Statement)
	}
	return out
}

func TestPoll_CollectsQuietSessionsOfLearningProjectsOnly(t *testing.T) {
	r := newRig(t)
	r.project("on", true)
	r.project("off", false)
	r.turns("on", "on-1", 20*time.Minute, "use scripts")
	r.turns("on", "on-2", 2*time.Minute, "still talking") // not quiet yet
	r.turns("off", "off-1", time.Hour, "customer note")

	if err := r.obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.drafts("on"); len(got) != 1 || got[0] != "Lesson from use scripts" {
		t.Errorf("on drafts = %q", got)
	}
	if got := r.drafts("off"); len(got) != 0 {
		t.Errorf("a project with learning off reached the model: %q", got)
	}
	for _, c := range r.runner.calls {
		if strings.Contains(c.Input, "customer note") || strings.Contains(c.Input, "still talking") {
			t.Errorf("sent a turn it must not have: %s", c.Input)
		}
		if c.Model != "claude-sonnet-5-5" || c.Effort != "low" {
			t.Errorf("model = %s/%s", c.Model, c.Effort)
		}
	}
	// A turn is collected once: a second pass has nothing to do.
	calls := len(r.runner.calls)
	if err := r.obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.runner.calls) != calls {
		t.Errorf("a collected turn was sent again")
	}
}

func TestPoll_StopsAtTheDailyBudget(t *testing.T) {
	r := newRig(t)
	r.project("on", true)
	r.runner.cost = 0.6
	for _, s := range []string{"s1", "s2", "s3", "s4"} {
		r.turns("on", s, time.Hour, "turn of "+s)
	}
	r.budget = 1.0
	if err := r.obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.runner.calls); n != 2 {
		t.Errorf("made %d calls; $1.00 budget at $0.60 a call allows the call that crosses it and no more", n)
	}
	if err := r.obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(r.runner.calls); n != 2 {
		t.Errorf("a second pass the same day spent past the budget (%d calls)", n)
	}
	if p := r.obs.Progress(); !strings.Contains(p.StopReason, "budget") && p.StopReason != "" {
		t.Errorf("progress = %+v", p)
	}
}

func TestPoll_AFailingRunLeavesTurnsForLaterAndBacksOff(t *testing.T) {
	r := newRig(t)
	r.project("on", true)
	r.turns("on", "s1", time.Hour, "keep this")
	r.runner.fail = errors.New("claude reported an error: Not logged in")
	if err := r.obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := r.obs.Progress(); p.Failed != 1 || !strings.Contains(p.LastError, "Not logged in") {
		t.Errorf("progress = %+v", p)
	}
	left, _ := r.store.ListUncollectedExcerpts(context.Background(), "on", "s1", 10)
	if len(left) != 1 {
		t.Fatalf("a failed run must leave its turns uncollected, %d left", len(left))
	}
	r.runner.fail = nil
	r.now = r.now.Add(10 * time.Minute)
	_ = r.obs.Poll(context.Background())
	if len(r.runner.calls) != 1 {
		t.Errorf("retried inside the backoff window")
	}
	r.now = r.now.Add(25 * time.Minute)
	_ = r.obs.Poll(context.Background())
	if got := r.drafts("on"); len(got) != 1 {
		t.Errorf("not collected after the backoff: %q", got)
	}
}

func TestRunNow_CollectsEverythingUnderItsOwnBudget(t *testing.T) {
	r := newRig(t)
	r.project("on", true)
	r.turns("on", "s1", 2*time.Minute, "fresh turn")
	r.budget = 0 // background collect paused
	if err := r.obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.runner.calls) != 0 {
		t.Fatal("a paused background collect made a call")
	}
	if err := r.obs.RunNow(context.Background(), "on", 5); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.obs.Progress().Running || r.obs.Progress().FinishedAt.IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("manual run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.drafts("on"); len(got) != 1 {
		t.Errorf("manual run drafts = %q", got)
	}
	if p := r.obs.Progress(); !p.Manual || p.Jobs != 1 || p.StopReason != "done" {
		t.Errorf("progress = %+v", p)
	}
}

func TestRunNow_FinishesASessionLongerThanOnePage(t *testing.T) {
	r := newRig(t)
	r.project("on", true)
	texts := make([]string, 0, 520)
	for i := 0; i < 520; i++ {
		texts = append(texts, "turn number "+strconv.Itoa(i))
	}
	r.turns("on", "long", time.Hour, texts...)
	r.runner.answer = func(string) string { return `{"lessons":[]}` }
	if err := r.obs.RunNow(context.Background(), "on", 5); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for r.obs.Progress().Running || r.obs.Progress().FinishedAt.IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("manual run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if left, _ := r.store.ListUncollectedExcerpts(context.Background(), "on", "long", 1000); len(left) != 0 {
		t.Errorf("%d turns left uncollected; a manual run must finish the session", len(left))
	}
}
