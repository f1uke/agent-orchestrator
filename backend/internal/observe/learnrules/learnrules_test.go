package learnrules_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/llm"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learnrules"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// fakeRunner answers with one rule quoting the first non-empty line of the
// text it is given, so the grounding check passes.
type fakeRunner struct {
	mu     sync.Mutex
	inputs []string
	fail   func(text string) bool
}

func (f *fakeRunner) Run(_ context.Context, req llm.Request) (llm.Result, error) {
	var in struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(req.Input), &in)
	f.mu.Lock()
	f.inputs = append(f.inputs, in.Text)
	fail := f.fail != nil && f.fail(in.Text)
	f.mu.Unlock()
	if fail {
		return llm.Result{}, &llm.Error{Err: errors.New("claude exited 1"), StderrTail: "not logged in", CostUSD: 0.002}
	}
	first := ""
	for _, l := range strings.Split(in.Text, "\n") {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "#") {
			first = strings.TrimSpace(l)
			break
		}
	}
	b, _ := json.Marshal(map[string]any{"rules": []map[string]any{{"text": "Rule: " + first, "quote": first, "tags": []string{"t"}}}})
	return llm.Result{Output: b, CostUSD: 0.01, InputTokens: 100, OutputTokens: 20, DurationMS: 5}, nil
}

func (f *fakeRunner) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inputs)
}

type rig struct {
	t       *testing.T
	store   *sqlite.Store
	runner  *fakeRunner
	now     time.Time
	budget  float64
	sources []learnrules.Source
	obs     *learnrules.Observer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	r := &rig{t: t, store: st, runner: &fakeRunner{}, now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), budget: 2}
	if err := st.UpsertProject(context.Background(), domain.ProjectRecord{ID: "nter", Path: "/repo/nter", RegisteredAt: r.now}); err != nil {
		t.Fatal(err)
	}
	r.obs = learnrules.New(st, r.runner, func(context.Context) ([]learnrules.Source, error) { return r.sources, nil },
		func() learnsettings.Settings {
			s := learnsettings.Default()
			s.DailyBudgetUSD = r.budget
			return s
		}, learnrules.Config{Clock: func() time.Time { return r.now }})
	return r
}

func (r *rig) poll() {
	r.t.Helper()
	if err := r.obs.Poll(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) corpus(project domain.ProjectID) []domain.LearnRule {
	r.t.Helper()
	srcs, err := r.store.ListLearnRuleSources(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	chunks, err := r.store.ListLearnRuleChunks(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return rules.Corpus(srcs, chunks, project)
}

func (r *rig) sourceErrors() map[string]string {
	r.t.Helper()
	srcs, _ := r.store.ListLearnRuleSources(context.Background())
	out := map[string]string{}
	for _, s := range srcs {
		out[s.Key] = s.Error
	}
	return out
}

func global(key, text string) learnrules.Source {
	return learnrules.Source{Key: key, Scope: domain.LearnRuleGlobal, Kind: domain.LearnRuleSourceClaudeMD, Label: key, Text: text}
}

func TestPoll_AtomizesOnceAndOnlyWhatChanged(t *testing.T) {
	r := newRig(t)
	index := learnrules.Source{Key: "idx", Scope: domain.LearnRuleProject, ProjectID: "nter", Kind: domain.LearnRuleSourceKnowledgeIndex,
		Label: "INDEX", Text: "# Index\n\n- [a](a.md) - always squash before merge\n- [b](b.md) - read only the named entries\n", Deterministic: true}
	r.sources = []learnrules.Source{
		global("claude", "# Rules\n\nDrive simulators only through scripts.\n"),
		// The same text in a second file is atomized once.
		global("copy", "# Rules\n\nDrive simulators only through scripts.\n"),
		index,
	}
	r.poll()
	if r.runner.calls() != 1 {
		t.Fatalf("model calls = %d, want 1: a shared chunk once, the INDEX never", r.runner.calls())
	}
	var texts []string
	for _, rule := range r.corpus("nter") {
		texts = append(texts, rule.Text)
	}
	if got := strings.Join(texts, " | "); got != "Rule: Drive simulators only through scripts. | - [a](a.md) - always squash before merge | - [b](b.md) - read only the named entries" {
		t.Errorf("corpus = %s", got)
	}
	r.poll()
	if r.runner.calls() != 1 {
		t.Errorf("an unchanged corpus must cost nothing, calls = %d", r.runner.calls())
	}
	// An edit re-atomizes; a removed source and its no-longer-used chunk go.
	r.sources = []learnrules.Source{global("claude", "# Rules\n\nPaste passwords, never type them.\n")}
	r.poll()
	if r.runner.calls() != 2 {
		t.Errorf("an edit must re-atomize once, calls = %d", r.runner.calls())
	}
	got := r.corpus("nter")
	if len(got) != 1 || got[0].Text != "Rule: Paste passwords, never type them." {
		t.Errorf("corpus after edit = %+v", got)
	}
	if spent, _ := r.store.LearnSpendSince(context.Background(), r.now.Add(-time.Hour)); spent < 0.019 || spent > 0.021 {
		t.Errorf("spend = %v, want both runs counted in learning's budget, the replaced one too", spent)
	}
	if hashes, _ := r.store.LearnRuleChunkHashes(context.Background()); len(hashes) != 3 {
		t.Errorf("cached chunks = %d; an unused chunk stays two days, its row is the record of its cost", len(hashes))
	}
	r.now = r.now.Add(49 * time.Hour)
	r.poll()
	if hashes, _ := r.store.LearnRuleChunkHashes(context.Background()); len(hashes) != 1 {
		t.Errorf("cached chunks = %d, want the unused ones pruned after two days", len(hashes))
	}
}

func TestPoll_FailureIsRecordedKeepsOldRulesAndBacksOff(t *testing.T) {
	r := newRig(t)
	r.sources = []learnrules.Source{global("claude", "# A\n\nFirst rule.\n")}
	r.poll()
	r.runner.fail = func(text string) bool { return strings.Contains(text, "Second") }
	r.sources = []learnrules.Source{global("claude", "# A\n\nFirst rule.\n\nSecond rule.\n")}
	r.poll()
	if e := r.sourceErrors()["claude"]; !strings.Contains(e, "1 of 1 chunks not atomized") || !strings.Contains(e, "not logged in") {
		t.Errorf("source error = %q; it must say what is missing and why", e)
	}
	if p := r.obs.Progress(); p.Failed != 1 || !strings.Contains(p.LastError, "not logged in") {
		t.Errorf("progress = %+v", p)
	}
	calls := r.runner.calls()
	r.now = r.now.Add(30 * time.Minute)
	r.poll()
	if r.runner.calls() != calls {
		t.Error("a failed chunk must back off instead of being retried every pass")
	}
	r.runner.fail = nil
	r.now = r.now.Add(time.Hour)
	r.poll()
	if r.runner.calls() != calls+1 || r.sourceErrors()["claude"] != "" {
		t.Errorf("after the backoff the chunk is retried and the error clears: calls %d, error %q", r.runner.calls(), r.sourceErrors()["claude"])
	}
}

func TestPoll_BudgetSpentRecordsSourcesWithoutRunning(t *testing.T) {
	r := newRig(t)
	r.budget = 0
	r.sources = []learnrules.Source{global("claude", "# A\n\nFirst rule.\n")}
	r.poll()
	if r.runner.calls() != 0 {
		t.Fatal("no budget, no model runs")
	}
	if e := r.sourceErrors()["claude"]; !strings.Contains(e, "budget reached") {
		t.Errorf("source error = %q", e)
	}
	if err := r.obs.RunNow(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.runner.calls() == 0 || r.obs.Progress().Running {
		if time.Now().After(deadline) {
			t.Fatal("manual refresh did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := r.sourceErrors()["claude"]; e != "" {
		t.Errorf("a manual refresh runs under its own budget; error = %q", e)
	}
}

func TestFiles_ListsOnlyWhileAProjectLearns(t *testing.T) {
	home, data, repo := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".claude", "CLAUDE.md"), "global rules")
	write(filepath.Join(data, "elsewhere", "pdf", "SKILL.md"), "pdf skill")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(data, "elsewhere", "pdf"), filepath.Join(home, ".claude", "skills", "pdf")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(data, "skills", "using-ao", "SKILL.md"), "ao skill")
	write(filepath.Join(repo, "CLAUDE.md"), "repo rules")
	write(filepath.Join(repo, ".claude", "skills", "deploy", "SKILL.md"), "deploy skill")
	write(filepath.Join(home, ".ao", "knowledge", "nter", "INDEX.md"), "- entry")
	mem := filepath.Join(home, ".claude", "projects", "-repo", "memory")
	write(filepath.Join(mem, "MEMORY.md"), "- [QA](feedback_qa.md) - hand work to qa")
	write(filepath.Join(mem, "feedback_qa.md"), "---\nname: feedback-qa\ndescription: \"Hand finished work to qa\"\n---\n\nHand finished work to qa.\n")
	learning := false
	f := learnrules.Files{
		Home: home, DataDir: data, KnowledgeDir: filepath.Join(home, ".ao", "knowledge"),
		MemoryDir: func(string) (string, error) { return mem, nil },
		Projects: func(context.Context) ([]domain.ProjectRecord, error) {
			return []domain.ProjectRecord{
				{ID: "nter", Path: repo, Config: domain.ProjectConfig{LearnFromSessions: learning}},
				{ID: "off", Path: repo},
			}, nil
		},
		Prompts: func(_ context.Context, id domain.ProjectID) (map[string]string, error) {
			return map[string]string{"worker": "worker prompt for " + string(id), "orchestrator": ""}, nil
		},
	}
	got, err := f.List(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("no project learns: sources = %v, err = %v", got, err)
	}
	learning = true
	got, err = f.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range got {
		keys = append(keys, s.Key)
		if s.Kind == domain.LearnRuleSourceKnowledgeIndex != s.Deterministic || s.Kind == domain.LearnRuleSourceMemory != s.Memory {
			t.Errorf("%s: only the knowledge INDEX and memory files are split without a model", s.Key)
		}
	}
	want := []string{
		"global::claude_md:~/.claude/CLAUDE.md",
		"global::skill:~/.claude/skills/pdf/SKILL.md",
		"global::skill:" + filepath.Join(data, "skills", "using-ao", "SKILL.md"),
		"project:nter:claude_md:" + filepath.Join(repo, "CLAUDE.md"),
		"project:nter:skill:" + filepath.Join(repo, ".claude", "skills", "deploy", "SKILL.md"),
		"project:nter:knowledge_index:~/.ao/knowledge/nter/INDEX.md",
		"project:nter:memory:~/.claude/projects/-repo/memory/feedback_qa.md",
		"project:nter:ao_prompt:AO worker prompt (nter)",
	}
	if strings.Join(keys, "\n") != strings.Join(want, "\n") {
		t.Errorf("keys =\n%s\nwant\n%s", strings.Join(keys, "\n"), strings.Join(want, "\n"))
	}
}

func TestPoll_AMemoryFileIsOneRuleWithoutAModel(t *testing.T) {
	r := newRig(t)
	r.sources = []learnrules.Source{{Key: "m", Scope: domain.LearnRuleProject, ProjectID: "nter", Kind: domain.LearnRuleSourceMemory, Label: "mem", Memory: true,
		Text: "---\nname: feedback-qa\ndescription: \"Hand finished work to qa\"\n---\n\n# Handoff\n\nHand finished work to qa.\n\n## Why\n\nThe person asked.\n"}}
	r.poll()
	got := r.corpus("nter")
	if r.runner.calls() != 0 || len(got) != 1 {
		t.Fatalf("calls %d, rules %+v", r.runner.calls(), got)
	}
	if got[0].Heading != "feedback-qa" || got[0].Quote != "Hand finished work to qa" || !strings.Contains(got[0].Text, "The person asked.") {
		t.Errorf("rule = %+v", got[0])
	}
}
