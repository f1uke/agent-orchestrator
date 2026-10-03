package learning_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learnrules"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type idleRefresher struct{ budgets []float64 }

func (r *idleRefresher) RunNow(_ context.Context, budget float64) error {
	r.budgets = append(r.budgets, budget)
	return nil
}
func (r *idleRefresher) Progress() learnrules.Progress { return learnrules.Progress{Sources: 2} }

// seedCorpus stores one global and one project source with a rule each.
func seedCorpus(t *testing.T, st *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, c := range []domain.LearnRuleChunk{
		{Hash: "aaaaaaaaaaaaaaaa", Model: "m", Atoms: []domain.LearnRuleAtom{{Text: "Drive the simulator only through Maestro scripts.", Quote: "q", Tags: []string{"simulator"}}}, CostUSD: 0.01, CreatedAt: now},
		{Hash: "bbbbbbbbbbbbbbbb", Model: "m", Atoms: []domain.LearnRuleAtom{{Text: "Write commit messages in English.", Quote: "q"}}, CreatedAt: now},
	} {
		if err := st.InsertLearnRuleChunk(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []domain.LearnRuleSource{
		{Key: "g", Scope: domain.LearnRuleGlobal, Kind: domain.LearnRuleSourceClaudeMD, Label: "~/.claude/CLAUDE.md", ContentHash: "x", Chunks: []domain.LearnRuleChunkRef{{Hash: "aaaaaaaaaaaaaaaa"}}, RefreshedAt: now},
		{Key: "p", Scope: domain.LearnRuleProject, ProjectID: "p", Kind: domain.LearnRuleSourceAgentsMD, Label: "AGENTS.md", ContentHash: "y", Chunks: []domain.LearnRuleChunkRef{{Hash: "bbbbbbbbbbbbbbbb"}, {Hash: "missing"}}, RefreshedAt: now, Error: "1 of 2 chunks not atomized: budget reached"},
	} {
		if err := st.UpsertLearnRuleSource(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRules_ListSearchAndSources(t *testing.T) {
	st, svc, _ := setup(t, true)
	seedCorpus(t, st)
	refresher := &idleRefresher{}
	svc = svc.WithRules(context.Background(), st, refresher)
	ctx := context.Background()

	all, err := svc.Rules(ctx, "p", "", 0)
	if err != nil || len(all) != 2 {
		t.Fatalf("rules = %+v, err %v", all, err)
	}
	if globalOnly, _ := svc.Rules(ctx, "", "", 0); len(globalOnly) != 1 {
		t.Errorf("no project lists the global rules only, got %d", len(globalOnly))
	}
	hits, _ := svc.Rules(ctx, "p", "can I tap the simulator by hand", 5)
	if len(hits) != 1 || hits[0].ID != rules.RuleID("aaaaaaaaaaaaaaaa", 0) || hits[0].Score <= 0 {
		t.Errorf("search = %+v", hits)
	}
	if _, err := svc.Rules(ctx, "nope", "", 0); !errors.Is(err, learning.ErrUnknownProject) {
		t.Errorf("unknown project: %v", err)
	}
	st2, err := svc.RuleSources(ctx, "p")
	if err != nil || len(st2.Sources) != 2 || st2.Progress.Sources != 2 {
		t.Fatalf("sources = %+v, err %v", st2, err)
	}
	if st2.Sources[1].Rules != 1 || st2.Sources[1].Error == "" {
		t.Errorf("a source counts only its atomized chunks and keeps its error: %+v", st2.Sources[1])
	}
	if err := svc.RefreshRules(ctx, 0); !errors.Is(err, learning.ErrInvalidBudget) {
		t.Errorf("zero budget: %v", err)
	}
	if err := svc.RefreshRules(ctx, 1); err != nil || len(refresher.budgets) != 1 {
		t.Errorf("refresh: %v %v", err, refresher.budgets)
	}
}

func TestProtect_ValidatesCopiesAndChecks(t *testing.T) {
	st, svc, _ := setup(t, true)
	seedCorpus(t, st)
	svc = svc.WithRules(context.Background(), st, nil)
	ctx := context.Background()

	if _, err := svc.Protect(ctx, learning.ProtectRequest{Text: "x", Patterns: []string{"a("}}); !errors.Is(err, learning.ErrInvalidProtectedRule) {
		t.Errorf("a pattern that does not compile must be refused: %v", err)
	}
	if _, err := svc.Protect(ctx, learning.ProtectRequest{}); !errors.Is(err, learning.ErrInvalidProtectedRule) {
		t.Errorf("empty rule: %v", err)
	}
	if _, err := svc.Protect(ctx, learning.ProtectRequest{From: "nope-0"}); !errors.Is(err, learning.ErrUnknownRule) {
		t.Errorf("unknown corpus rule: %v", err)
	}
	sim, err := svc.Protect(ctx, learning.ProtectRequest{From: rules.RuleID("aaaaaaaaaaaaaaaa", 0), Patterns: []string{`\bao sim tap\b`}, Note: "decision 4"})
	if err != nil || sim.Text != "Drive the simulator only through Maestro scripts." || sim.ID == 0 {
		t.Fatalf("protect from the corpus = %+v, %v", sim, err)
	}
	if _, err := svc.Protect(ctx, learning.ProtectRequest{ProjectID: "p", Text: "No real ticket ids.", Patterns: []string{`STAR-\d+`}}); err != nil {
		t.Fatal(err)
	}
	hits, err := svc.CheckForbidden(ctx, "", "Then ao sim tap the button for STAR-1.")
	if err != nil || len(hits) != 1 || hits[0].RuleID != sim.ID {
		t.Errorf("outside the project only the global rule applies: %+v %v", hits, err)
	}
	if hits, _ := svc.CheckForbidden(ctx, "p", "Then ao sim tap the button for STAR-1."); len(hits) != 2 {
		t.Errorf("in the project both apply: %+v", hits)
	}
	if list, _ := svc.ProtectedRules(ctx, ""); len(list) != 2 || list[0].Note != "decision 4" || list[0].Patterns[0] != `\bao sim tap\b` {
		t.Errorf("protected = %+v", list)
	}
	if err := svc.Unprotect(ctx, sim.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Unprotect(ctx, sim.ID); !errors.Is(err, learning.ErrUnknownRule) {
		t.Errorf("unprotect twice: %v", err)
	}
}

func TestRules_UnavailableWithoutTheCorpus(t *testing.T) {
	_, svc, _ := setup(t, true)
	if _, err := svc.Rules(context.Background(), "p", "", 0); !errors.Is(err, learning.ErrRulesUnavailable) {
		t.Errorf("err = %v", err)
	}
	if err := svc.RefreshRules(context.Background(), 1); !errors.Is(err, learning.ErrRulesUnavailable) {
		t.Errorf("err = %v", err)
	}
}
