package learndecide_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/decide"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learndecide"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// fakeRunner answers decide with one create_memory citing every draft id in the
// input, and the verifier with grounded unless ungrounded is set.
type fakeRunner struct {
	mu         sync.Mutex
	calls      []llm.Request
	ungrounded bool
}

func (f *fakeRunner) Run(_ context.Context, req llm.Request) (llm.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if req.SystemPrompt == decide.VerifierPrompt {
		b, _ := json.Marshal(map[string]any{"reviews": []map[string]any{{"proposal": "p1", "contradicts_rule": false,
			"contradicted_rules": []string{}, "grounded": !f.ungrounded, "sensitive": false, "notes": "checked"}}})
		return llm.Result{Output: b, CostUSD: 0.05}, nil
	}
	var in struct {
		Drafts []struct {
			ID string `json:"id"`
		} `json:"drafts"`
	}
	_ = json.Unmarshal([]byte(req.Input), &in)
	var ev []string
	for _, d := range in.Drafts {
		ev = append(ev, d.ID)
	}
	b, _ := json.Marshal(map[string]any{"proposals": []map[string]any{{
		"action": "create_memory", "memory_type": "feedback", "name": "verify on device", "description": "Verify a UI change through its script",
		"target": "", "under_heading": "", "scope": "project", "title": "Verify on device", "rationale": "The person said so.",
		"evidence": ev, "rule_verdicts": []any{}, "content": "Run the Maestro script for the screen.", "confidence": 0.9,
	}}})
	return llm.Result{Output: b, CostUSD: 0.2}, nil
}

func (f *fakeRunner) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type rig struct {
	t      *testing.T
	store  *sqlite.Store
	runner *fakeRunner
	now    time.Time
	obs    *learndecide.Observer
	budget float64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	home := t.TempDir()
	r := &rig{t: t, store: st, runner: &fakeRunner{}, now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), budget: 2}
	if err := st.UpsertProject(context.Background(), domain.ProjectRecord{ID: "nter", Path: t.TempDir(), RegisteredAt: r.now,
		Config: domain.ProjectConfig{LearnFromSessions: true}}); err != nil {
		t.Fatal(err)
	}
	r.obs = learndecide.New(st, r.runner, func() learnsettings.Settings {
		s := learnsettings.Default()
		s.DailyBudgetUSD = r.budget
		return s
	}, learndecide.Dirs{Home: home, DataDir: t.TempDir(), KnowledgeDir: home + "/.ao/knowledge",
		MemoryDir: func(string) (string, error) { return home + "/.claude/projects/-repo/memory", nil }},
		learndecide.Config{Clock: func() time.Time { return r.now }})
	return r
}

// session makes a worker session; ended terminates it with reason.
func (r *rig) session(reason string) domain.SessionID {
	r.t.Helper()
	ctx := context.Background()
	rec, err := r.store.CreateSession(ctx, domain.SessionRecord{ProjectID: "nter", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: r.now}, CreatedAt: r.now, UpdatedAt: r.now})
	if err != nil {
		r.t.Fatal(err)
	}
	if reason != "" {
		rec.IsTerminated = true
		rec.Termination = domain.Termination{Source: domain.TerminationSourceAO, Reason: reason, At: r.now.Add(-time.Hour)}
		if err := r.store.UpdateSession(ctx, rec); err != nil {
			r.t.Fatal(err)
		}
	}
	return rec.ID
}

// drafts captures one typed turn per statement for the session and stores a
// draft on each.
func (r *rig) drafts(id domain.SessionID, statements ...string) {
	r.t.Helper()
	ctx := context.Background()
	var ex []domain.LearnExcerpt
	for i, s := range statements {
		at := r.now.Add(-2 * time.Hour).Add(time.Duration(i) * time.Second)
		ex = append(ex, domain.LearnExcerpt{ProjectID: "nter", SessionID: id, TranscriptPath: "/t/" + string(id), TurnUUID: string(id) + s,
			TurnAt: at, SourceClass: domain.LearnSourceTyped, HumanText: s, CreatedAt: at})
	}
	cursor := domain.LearnCursor{TranscriptPath: "/t/" + string(id), ProjectID: "nter", SessionID: id}
	if _, err := r.store.CommitLearnPass(ctx, cursor, ex, nil, r.now); err != nil {
		r.t.Fatal(err)
	}
	turns, err := r.store.ListUncollectedExcerpts(ctx, "nter", id, 100)
	if err != nil {
		r.t.Fatal(err)
	}
	job := domain.LearnJob{ProjectID: "nter", SessionID: id, Model: "m", StartedAt: r.now}
	if job.ID, err = r.store.StartLearnJob(ctx, job); err != nil {
		r.t.Fatal(err)
	}
	var ds []domain.LearnDraft
	for _, tu := range turns {
		ds = append(ds, domain.LearnDraft{ProjectID: "nter", SessionID: id, TaskKey: "solo:" + string(id), Kind: domain.LearnDraftRule,
			Statement: "Lesson: " + tu.HumanText, Quote: tu.HumanText, AnchorExcerptID: tu.ID, EvidenceExcerptIDs: []int64{tu.ID},
			Confidence: 0.8, About: domain.LearnAboutAgentPractice})
	}
	if _, err := r.store.CommitLearnJob(ctx, job, ds, nil, r.now.Add(-2*time.Hour)); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) poll() {
	r.t.Helper()
	if err := r.obs.Poll(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) proposals() []domain.LearnProposal {
	r.t.Helper()
	ps, err := r.store.ListSkillProposals(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return ps
}

func (r *rig) statuses() map[domain.LearnDraftStatus]int {
	r.t.Helper()
	ds, _ := r.store.ListAllLearnDrafts(context.Background())
	out := map[domain.LearnDraftStatus]int{}
	for _, d := range ds {
		out[d.Status]++
	}
	return out
}

func TestPoll_DecidesAFinishedTaskOnce(t *testing.T) {
	r := newRig(t)
	done := r.session(domain.TerminationCauseWorkComplete)
	running := r.session("")
	r.drafts(done, "always run the maestro script to verify a screen")
	r.drafts(running, "write tests first")
	r.poll()
	if r.runner.n() != 2 {
		t.Fatalf("calls = %d, want decide + verify for the finished task only", r.runner.n())
	}
	first := r.runner.calls[0]
	if first.Model != "claude-opus-5-5" || first.Effort != "medium" || first.SystemPrompt != decide.SystemPrompt {
		t.Errorf("decide call = %s/%s", first.Model, first.Effort)
	}
	ps := r.proposals()
	if len(ps) != 1 || ps[0].Status != domain.LearnProposalPending || ps[0].Outcome != domain.LearnOutcomeMerged ||
		!strings.HasPrefix(ps[0].Diff, "--- /dev/null") || ps[0].Action != domain.LearnProposeCreateMemory || ps[0].IndexLine == "" || len(ps[0].EvidenceIDs) != 1 || ps[0].Scope != "project:nter" {
		t.Fatalf("proposals = %+v", ps)
	}
	if st := r.statuses(); st[domain.LearnDraftConsumed] != 1 || st[domain.LearnDraftOpen] != 1 {
		t.Errorf("draft statuses = %v; the evidence is consumed, the running task's draft stays open", st)
	}
	r.poll()
	if r.runner.n() != 2 {
		t.Errorf("a decided task is not decided again: calls = %d", r.runner.n())
	}
	if spent, _ := r.store.LearnSpendSince(context.Background(), r.now.Add(-time.Hour)); spent < 0.249 || spent > 0.251 {
		t.Errorf("spend = %v, want both runs in the daily budget", spent)
	}

	// The running task is decided once a day has passed (daily cut), and its
	// proposal on the same target amends the pending one.
	r.now = r.now.Add(25 * time.Hour)
	r.poll()
	ps = r.proposals()
	if len(ps) != 1 || len(ps[0].EvidenceIDs) != 2 {
		t.Errorf("same target must amend, not duplicate: %+v", ps)
	}
}

func TestPoll_VerifierRefusalIsKeptAsDropped(t *testing.T) {
	r := newRig(t)
	r.runner.ungrounded = true
	r.drafts(r.session(domain.TerminationCauseWorkComplete), "use the script")
	r.poll()
	ps := r.proposals()
	if len(ps) != 1 || ps[0].Status != domain.LearnProposalDropped || !strings.Contains(ps[0].DropReason, "not grounded") {
		t.Fatalf("proposals = %+v", ps)
	}
	if st := r.statuses(); st[domain.LearnDraftDropped] != 1 {
		t.Errorf("draft statuses = %v", st)
	}
}

func TestPoll_NoBudgetNoCalls_ManualTaskRunsAnyway(t *testing.T) {
	r := newRig(t)
	r.budget = 0
	id := r.session("")
	r.drafts(id, "use the script")
	r.poll()
	if r.runner.n() != 0 {
		t.Fatal("no budget, no calls")
	}
	r.obs.Run(context.Background(), learndecide.RunOpts{Task: "solo:" + string(id), BudgetUSD: 1})
	if r.runner.n() != 2 || len(r.proposals()) != 1 {
		t.Errorf("--task decides a task that is not ready: calls %d", r.runner.n())
	}
}
