package learning_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

func kinds(t *testing.T, r decisionsRig, id int64) string {
	t.Helper()
	evs, err := r.svc.History(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, string(e.Kind))
	}
	return strings.Join(out, ",")
}

func TestSnoozed_UnsnoozeOrDecideDirectly(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	id := r.propose(t, memoryProposal(r.mem))
	if _, err := r.svc.Unsnooze(ctx, id, app); !errors.Is(err, learning.ErrProposalWrongState) {
		t.Errorf("unsnooze of one not snoozed: %v", err)
	}
	if _, err := r.svc.Snooze(ctx, id, time.Now().Add(90*24*time.Hour-time.Minute), app); err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.Unsnooze(ctx, id, domain.LearnActor{Via: domain.LearnViaCLI, SessionID: "agent-orchestrator-9"})
	if err != nil || !p.SnoozedUntil.IsZero() || p.Status != domain.LearnProposalPending {
		t.Fatalf("unsnooze = %+v, %v", p, err)
	}
	// Snoozed again, and rejected straight from the snooze: what the person meant.
	if _, err := r.svc.Snooze(ctx, id, time.Now().Add(time.Hour), app); err != nil {
		t.Fatal(err)
	}
	if p, err := r.svc.Reject(ctx, id, "do not record this", app); err != nil || p.Status != domain.LearnProposalRejected {
		t.Fatalf("reject a snoozed one = %+v, %v", p, err)
	}
	if got := kinds(t, r, id); got != "snoozed,unsnoozed,snoozed,rejected" {
		t.Errorf("history = %s", got)
	}
	evs, _ := r.svc.History(ctx, id)
	if evs[0].SnoozedUntil.IsZero() || evs[1].Actor != (domain.LearnActor{Via: domain.LearnViaCLI, SessionID: "agent-orchestrator-9"}) || evs[3].Note != "do not record this" {
		t.Errorf("events = %+v", evs)
	}
}

func TestRejected_ReopenThenApprove(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	id := r.propose(t, memoryProposal(r.mem))
	if _, err := r.svc.Reopen(ctx, id, app); !errors.Is(err, learning.ErrProposalWrongState) {
		t.Errorf("reopen of a pending one: %v", err)
	}
	if _, err := r.svc.Reject(ctx, id, "changed my mind later", app); err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.Reopen(ctx, id, app)
	if err != nil || p.Status != domain.LearnProposalPending || p.RejectReason != "" || !p.DecidedAt.IsZero() {
		t.Fatalf("reopen = %+v, %v", p, err)
	}
	if p, err = r.svc.Approve(ctx, id, "", "", app); err != nil || p.Status != domain.LearnProposalApplied {
		t.Fatalf("approve after reopen = %+v, %v", p, err)
	}
	if got := kinds(t, r, id); got != "rejected,reopened,approved" {
		t.Errorf("history = %s", got)
	}
}

func TestReopen_NeverWritesAMemoryTwice(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	first := r.propose(t, memoryProposal(r.mem))
	if _, err := r.svc.Reject(ctx, first, "no", app); err != nil {
		t.Fatal(err)
	}
	// The same memory, proposed again later and approved.
	second := r.propose(t, memoryProposal(r.mem))
	if _, err := r.svc.Approve(ctx, second, "", "", app); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Reopen(ctx, first, app); !errors.Is(err, learning.ErrMemoryExists) || !strings.Contains(err.Error(), "#"+strconv.FormatInt(second, 10)) {
		t.Fatalf("reopening a memory that exists must be refused, naming who wrote it: %v", err)
	}

	// A proposal for the same file waiting: decide that one first.
	other := filepath.Join(r.mem, "feedback_other.md")
	p := memoryProposal(r.mem)
	p.TargetPath, p.IndexLine = other, "- [Other](feedback_other.md) - other"
	a := r.propose(t, p)
	if _, err := r.svc.Reject(ctx, a, "no", app); err != nil {
		t.Fatal(err)
	}
	b := r.propose(t, p)
	if _, err := r.svc.Reopen(ctx, a, app); !errors.Is(err, domain.ErrLearnTargetPending) || !strings.Contains(err.Error(), "#"+strconv.FormatInt(b, 10)) {
		t.Errorf("a second pending proposal for the same file: %v", err)
	}
}

func TestApplied_EditIsGatedAndKept(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	id := r.propose(t, memoryProposal(r.mem))
	p, err := r.svc.Approve(ctx, id, "", "", app)
	if err != nil {
		t.Fatal(err)
	}
	target := p.TargetPath
	bad := strings.Replace(p.NewContent, "Test with the Dev build.", "Log in with uat-secret-pass.", 1)
	if _, err := r.svc.EditApplied(ctx, id, bad, "", app); !errors.Is(err, learning.ErrInvalidDecision) || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("a sensitive edit after approve must be refused: %v", err)
	}
	edit := strings.Replace(p.NewContent, "Test with the Dev build.", "Test with the Dev build — never staging.", 1)
	got, err := r.svc.EditApplied(ctx, id, edit, "", app)
	if err != nil || got.Status != domain.LearnProposalApplied || got.AppliedSHA256 == p.AppliedSHA256 {
		t.Fatalf("edit = %+v, %v", got, err)
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "never staging") || strings.Contains(string(b), "—") || string(b) != got.NewContent {
		t.Errorf("file = %q", b)
	}
	if !strings.Contains(got.Diff, "+Test with the Dev build - never staging.") || !strings.Contains(got.Diff, "MEMORY.md") {
		t.Errorf("the diff must show what is written now, its MEMORY.md line kept:\n%s", got.Diff)
	}
	w, ok, err := r.svc.Written(ctx, got)
	if err != nil || !ok || w.Changed {
		t.Errorf("after an edit through AO the file is what AO wrote: %+v %v", w, err)
	}
	if got := kinds(t, r, id); got != "approved,edited" {
		t.Errorf("history = %s", got)
	}
}

func TestApplied_UndoRemovesTheMemoryAndItsIndexLine(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(r.mem, "MEMORY.md"), []byte("- [Mine](feedback_mine.md) - mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := r.propose(t, memoryProposal(r.mem))
	if _, err := r.svc.Approve(ctx, id, "", "", app); err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.Undo(ctx, id, "", app)
	if err != nil || p.Status != domain.LearnProposalPending || p.AppliedSHA256 != "" {
		t.Fatalf("undo = %+v, %v", p, err)
	}
	if _, err := os.Stat(p.TargetPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("the memory file must be gone")
	}
	if b, _ := os.ReadFile(filepath.Join(r.mem, "MEMORY.md")); string(b) != "- [Mine](feedback_mine.md) - mine\n" {
		t.Errorf("MEMORY.md = %q, want only AO's line gone", b)
	}
	evs, _ := r.svc.History(ctx, id)
	if last := evs[len(evs)-1]; last.Kind != domain.LearnEventUndone || !strings.Contains(last.Note, "MEMORY.md line") {
		t.Errorf("last event = %+v", last)
	}
	// Back in the queue: approving again writes it again.
	if p, err = r.svc.Approve(ctx, id, "", "", app); err != nil || p.Status != domain.LearnProposalApplied {
		t.Fatalf("approve after undo = %+v, %v", p, err)
	}
}

func TestApplied_UndoOfAHandEditedMemoryShowsTheDiffAndNeedsConfirming(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	id := r.propose(t, memoryProposal(r.mem))
	p, err := r.svc.Approve(ctx, id, "", "", app)
	if err != nil {
		t.Fatal(err)
	}
	hand := strings.Replace(p.NewContent, "Test with the Dev build.", "Test with the Dev build, and say so in the MR.", 1)
	if err := os.WriteFile(p.TargetPath, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	w, _, _ := r.svc.Written(ctx, p)
	if !w.Changed || !strings.Contains(w.Diff, "+Test with the Dev build, and say so in the MR.") || w.Token == "" {
		t.Fatalf("written = %+v", w)
	}
	_, err = r.svc.Undo(ctx, id, "", app)
	var changed *learning.ChangedError
	if !errors.As(err, &changed) || changed.Token != w.Token || changed.Diff != w.Diff {
		t.Fatalf("undo of a hand edit must refuse with the diff: %v", err)
	}
	if b, _ := os.ReadFile(p.TargetPath); string(b) != hand {
		t.Fatal("nothing may be touched before confirming")
	}
	if got, _, _ := r.svc.Proposal(ctx, id); got.Status != domain.LearnProposalApplied {
		t.Fatalf("still applied, got %s", got.Status)
	}
	if _, err := r.svc.Undo(ctx, id, changed.Token, app); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.TargetPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("confirmed undo removes the file")
	}
}

func TestApplied_UndoRefusedWhileAnotherProposalForTheFileWaits(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	id := r.propose(t, memoryProposal(r.mem))
	p, err := r.svc.Approve(ctx, id, "", "", app)
	if err != nil {
		t.Fatal(err)
	}
	r.propose(t, domain.LearnProposal{Action: domain.LearnProposeUpdateMemory, TargetPath: p.TargetPath, Title: "amend", NewContent: "x\n"})
	if _, err := r.svc.Undo(ctx, id, "", app); !errors.Is(err, domain.ErrLearnTargetPending) {
		t.Fatalf("undo with a waiting amendment: %v", err)
	}
	if _, err := os.Stat(p.TargetPath); err != nil {
		t.Error("a refused undo touches nothing")
	}
}

func TestSettledConflict_Undo(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	pinned, err := r.svc.Protect(ctx, learning.ProtectRequest{Text: "Drive simulators through scripts.", Patterns: []string{`\bao sim tap\b`}})
	if err != nil {
		t.Fatal(err)
	}
	conflict := domain.LearnProposal{Action: domain.LearnProposeConflict, TargetPath: "rule:protected-" + strconv.FormatInt(pinned.ID, 10), Title: "t",
		NewContent: "Workers may use ao sim without asking."}
	id := r.propose(t, conflict)
	if _, err := r.svc.Approve(ctx, id, "", domain.LearnWordsWin, app); err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.Undo(ctx, id, "", app)
	if err != nil || p.Status != domain.LearnProposalPending || p.Resolution != "" {
		t.Fatalf("undo words win = %+v, %v", p, err)
	}
	rules, _ := r.svc.ProtectedRules(ctx, "")
	if rules[0].Text != "Drive simulators through scripts." {
		t.Errorf("the pinned rule must get its text back: %q", rules[0].Text)
	}

	// Words win again, then the pinned rule is edited elsewhere: undo shows it.
	if _, err := r.svc.Approve(ctx, id, "", domain.LearnWordsWin, app); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.UpdateLearnProtectedRuleText(ctx, pinned.ID, "Edited since.", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err = r.svc.Undo(ctx, id, "", app)
	var changed *learning.ChangedError
	if !errors.As(err, &changed) || !strings.Contains(changed.Diff, "+Edited since.") {
		t.Fatalf("a pinned rule changed since must be confirmed: %v", err)
	}
	if _, err := r.svc.Undo(ctx, id, changed.Token, app); err != nil {
		t.Fatal(err)
	}

	// Keep the rule: undo reopens it.
	conflict.TargetPath = "rule:abc-0"
	keep := r.propose(t, conflict)
	if _, err := r.svc.Approve(ctx, keep, "", domain.LearnKeepRule, app); err != nil {
		t.Fatal(err)
	}
	if p, err := r.svc.Undo(ctx, keep, "", app); err != nil || p.Status != domain.LearnProposalPending {
		t.Fatalf("undo keep the rule = %+v, %v", p, err)
	}
	if got := kinds(t, r, keep); got != "rejected,undone" {
		t.Errorf("history = %s", got)
	}
}

func TestUndo_ChangedFileRestoresTheEarlierVersion(t *testing.T) {
	r := newDecisionsRig(t)
	ctx := context.Background()
	target := filepath.Join(r.mem, "feedback_old.md")
	old := "---\nname: feedback-old\ndescription: old\n---\n\nOld.\n"
	if err := os.WriteFile(target, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	id := r.propose(t, domain.LearnProposal{Action: domain.LearnProposeUpdateMemory, TargetPath: target, Title: "t",
		BaseSHA256: sha(old), NewContent: "---\nname: feedback-old\ndescription: new\n---\n\nNew.\n"})
	if _, err := r.svc.Approve(ctx, id, "", "", app); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Undo(ctx, id, "", app); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != old {
		t.Errorf("file = %q, want the version before the approve", b)
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
