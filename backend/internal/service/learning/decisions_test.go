package learning_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/apply"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type decisionsRig struct {
	st   *sqlite.Store
	svc  *learning.Service
	home string
	mem  string
}

func newDecisionsRig(t *testing.T) decisionsRig {
	t.Helper()
	st, svc, _ := setup(t, true)
	home := t.TempDir()
	mem := filepath.Join(home, ".claude", "projects", "-repo-p", "memory")
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	svc = svc.WithDecide(context.Background(), st, &fakeDecider{}).WithRules(context.Background(), st, nil).
		WithDecisions(st, apply.Roots{Home: home, History: filepath.Join(t.TempDir(), "history")},
			func(context.Context) []redact.Value {
				return []redact.Value{{Value: "uat-secret-pass", Placeholder: "[account:x]", Kind: "account"}}
			})
	return decisionsRig{st: st, svc: svc, home: home, mem: mem}
}

// propose stores one pending proposal as decide would.
func (r decisionsRig) propose(t *testing.T, p domain.LearnProposal) int64 {
	t.Helper()
	p.ProjectID, p.TaskKey, p.Status = "p", "solo:x", domain.LearnProposalPending
	if p.Scope == "" {
		p.Scope = "project:p"
	}
	if err := r.st.CommitDecide(context.Background(), domain.LearnDecideResult{TaskKey: "solo:x", ProjectID: "p", Outcome: domain.LearnOutcomeMerged,
		Proposals: []domain.LearnProposal{p}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	all, _ := r.svc.Proposals(context.Background(), "p", true)
	return all[0].ID
}

func memoryProposal(mem string) domain.LearnProposal {
	return domain.LearnProposal{Action: domain.LearnProposeCreateMemory, TargetPath: filepath.Join(mem, "feedback_dev_build.md"),
		Title: "Dev build only", NewContent: "---\nname: feedback-dev-build\ndescription: \"Test with the Dev build\"\nmetadata:\n  type: feedback\n---\n\nTest with the Dev build.\n",
		IndexLine: "- [Dev build only](feedback_dev_build.md) - Test with the Dev build", Diff: "--- /dev/null\n+++ b/x\n@@ -0,0 +1,1 @@\n+x\n"}
}

func TestApprove_WritesTheMemoryAndSettles(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	id := r.propose(t, memoryProposal(r.mem))
	p, err := r.svc.Approve(ctx, id, "", "")
	if err != nil || p.Status != domain.LearnProposalApplied || p.AppliedSHA256 == "" {
		t.Fatalf("approve = %+v, %v", p, err)
	}
	if b, _ := os.ReadFile(filepath.Join(r.mem, "MEMORY.md")); !strings.Contains(string(b), "- [Dev build only](feedback_dev_build.md)") {
		t.Errorf("MEMORY.md = %q", b)
	}
	if _, err := r.svc.Approve(ctx, id, "", ""); !errors.Is(err, learning.ErrProposalNotPending) {
		t.Errorf("a second approve: %v", err)
	}
}

func TestApprove_EditIsGatedAndWritten(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	id := r.propose(t, memoryProposal(r.mem))
	bad := "---\nname: feedback-dev-build\n---\n\nLog in with uat-secret-pass.\n"
	if _, err := r.svc.Approve(ctx, id, bad, ""); !errors.Is(err, learning.ErrInvalidDecision) || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("a sensitive edit must be refused: %v", err)
	}
	edit := "---\nname: feedback-dev-build\ndescription: Dev build: always\n---\n\nTest with the Dev build — never staging.\n"
	p, err := r.svc.Approve(ctx, id, edit, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(r.mem, "feedback_dev_build.md"))
	if string(b) != p.NewContent || !strings.Contains(string(b), `description: "Dev build: always"`) || strings.Contains(string(b), "—") {
		t.Errorf("written = %q (the edit, with its description quoted and no em dash)", b)
	}
}

func TestApprove_StaleWhenTheFileChanged(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	target := filepath.Join(r.mem, "feedback_old.md")
	if err := os.WriteFile(target, []byte("changed by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := r.propose(t, domain.LearnProposal{Action: domain.LearnProposeUpdateMemory, TargetPath: target, Title: "t",
		BaseSHA256: "0000", NewContent: "new\n"})
	if _, err := r.svc.Approve(ctx, id, "", ""); !errors.Is(err, learning.ErrProposalStale) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "changed by hand\n" {
		t.Error("a changed file is never overwritten")
	}
	p, _, _ := r.svc.Proposal(ctx, id)
	if p.Status != domain.LearnProposalStale {
		t.Errorf("status = %s", p.Status)
	}
}

func TestConflict_SidesAndProtectedRule(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	pinned, err := r.svc.Protect(ctx, learning.ProtectRequest{Text: "Drive simulators through scripts.", Patterns: []string{`\bao sim tap\b`}})
	if err != nil {
		t.Fatal(err)
	}
	conflict := domain.LearnProposal{Action: domain.LearnProposeConflict, TargetPath: "rule:protected-" + strconv.FormatInt(pinned.ID, 10), Title: "t",
		NewContent: "Workers may use ao sim without asking."}
	id := r.propose(t, conflict)
	if _, err := r.svc.Approve(ctx, id, "", ""); !errors.Is(err, learning.ErrInvalidDecision) {
		t.Errorf("a conflict needs a side: %v", err)
	}
	p, err := r.svc.Approve(ctx, id, "", domain.LearnWordsWin)
	if err != nil || p.Resolution != domain.LearnWordsWin || p.Status != domain.LearnProposalApplied {
		t.Fatalf("words win = %+v %v", p, err)
	}
	rules, _ := r.svc.ProtectedRules(ctx, "")
	if rules[0].Text != "Workers may use ao sim without asking." || rules[0].Patterns[0] != `\bao sim tap\b` {
		t.Errorf("the pinned rule takes the newer words and keeps its patterns: %+v", rules[0])
	}

	conflict.TargetPath = "rule:abc-0"
	keep := r.propose(t, conflict)
	if p, err := r.svc.Approve(ctx, keep, "", domain.LearnKeepRule); err != nil || p.Status != domain.LearnProposalRejected || p.RejectReason != "kept the rule" {
		t.Errorf("keep the rule = %+v %v", p, err)
	}
}

func TestRejectAndSnooze(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	id := r.propose(t, memoryProposal(r.mem))
	if _, err := r.svc.Snooze(ctx, id, time.Now().Add(-time.Hour)); !errors.Is(err, learning.ErrInvalidDecision) {
		t.Errorf("snooze into the past: %v", err)
	}
	until := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	if p, err := r.svc.Snooze(ctx, id, until); err != nil || !p.SnoozedUntil.Equal(until) {
		t.Errorf("snooze = %+v %v", p, err)
	}
	if p, _, _ := r.svc.Proposal(ctx, id); !p.SnoozedUntil.Equal(until) || p.Status != domain.LearnProposalPending {
		t.Errorf("stored = %+v", p)
	}
	p, err := r.svc.Reject(ctx, id, "only that week")
	if err != nil || p.Status != domain.LearnProposalRejected || p.RejectReason != "only that week" {
		t.Errorf("reject = %+v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(r.mem, "feedback_dev_build.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a rejected proposal writes nothing")
	}
}
