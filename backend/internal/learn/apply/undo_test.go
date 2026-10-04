package apply

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// applied applies p and returns it as the store keeps it after approval.
func applied(t *testing.T, r Roots, p domain.LearnProposal) domain.LearnProposal {
	t.Helper()
	w, err := Apply(r, p, "", now)
	if err != nil {
		t.Fatal(err)
	}
	p.Status, p.AppliedSHA256, p.AppliedBefore, p.AppliedIndexLine, p.DecidedAt = domain.LearnProposalApplied, w.SHA256, w.Before, w.IndexLine, now
	return p
}

func newMemory(mem string) domain.LearnProposal {
	return domain.LearnProposal{Action: domain.LearnProposeCreateMemory, TargetPath: filepath.Join(mem, "feedback_new.md"),
		NewContent: "---\nname: feedback-new\n---\n\nBody.\n", IndexLine: "- [New](feedback_new.md) - new"}
}

func TestUndo_NewMemoryRemovesTheFileAndOnlyItsIndexLine(t *testing.T) {
	r, mem := setup(t)
	p := applied(t, r, newMemory(mem))
	st, err := Inspect(r, p)
	if err != nil || st.Changed || !st.Exists || !st.IndexLinePresent {
		t.Fatalf("inspect right after apply = %+v, %v", st, err)
	}
	if _, err := Undo(r, p, "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.TargetPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("the memory file must be gone")
	}
	if got := read(t, filepath.Join(mem, "MEMORY.md")); got != "- [Old](feedback_old.md) - old\n" {
		t.Errorf("MEMORY.md = %q, want only the line AO added taken out", got)
	}
}

func TestUndo_AnIndexLeftEmptyIsRemoved(t *testing.T) {
	r, mem := setup(t)
	if err := os.Remove(filepath.Join(mem, "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	p := applied(t, r, newMemory(mem))
	if _, err := Undo(r, p, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mem, "MEMORY.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a MEMORY.md AO created for the line must not stay behind empty")
	}
}

func TestUndo_AHandEditNeedsTheConfirmedToken(t *testing.T) {
	r, mem := setup(t)
	p := applied(t, r, newMemory(mem))
	if err := os.WriteFile(p.TargetPath, []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Undo(r, p, "", now)
	var changed *ChangedError
	if !errors.As(err, &changed) || !changed.State.Changed || changed.State.Content != "edited by hand\n" {
		t.Fatalf("undo of a hand-edited memory must refuse with its state: %v", err)
	}
	if read(t, p.TargetPath) != "edited by hand\n" {
		t.Fatal("nothing may be removed before the person confirms")
	}
	token := changed.State.Token
	// Changed again after the review: the old token no longer confirms it.
	if err := os.WriteFile(p.TargetPath, []byte("edited twice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(r, p, token, now); !errors.As(err, &changed) {
		t.Fatalf("a token of an earlier state must not confirm this one: %v", err)
	}
	if _, err := Undo(r, p, changed.State.Token, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.TargetPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("confirmed: the memory file must be gone")
	}
	var backups int
	_ = filepath.Walk(r.History, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			backups++
		}
		return nil
	})
	if backups < 2 {
		t.Errorf("the hand edit and the index must be backed up before removal, got %d backups", backups)
	}
}

func TestUndo_AMissingIndexLineCountsAsAChange(t *testing.T) {
	r, mem := setup(t)
	p := applied(t, r, newMemory(mem))
	if err := os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- [Old](feedback_old.md) - old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := Inspect(r, p)
	if !st.Changed || st.IndexLinePresent {
		t.Fatalf("state = %+v", st)
	}
}

func TestUndo_UpdateRestoresTheEarlierVersion(t *testing.T) {
	r, mem := setup(t)
	target := filepath.Join(mem, "feedback_old.md")
	if err := os.WriteFile(target, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := applied(t, r, domain.LearnProposal{Action: domain.LearnProposeUpdateMemory, TargetPath: target, BaseSHA256: sha([]byte("v1\n")), NewContent: "v2\n"})
	if _, err := Undo(r, p, "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if read(t, target) != "v1\n" {
		t.Errorf("file = %q, want the version the approve replaced", read(t, target))
	}
}

func TestUndo_UpdateAppliedBeforeTheVersionWasKeptReadsTheBackup(t *testing.T) {
	r, mem := setup(t)
	target := filepath.Join(mem, "feedback_old.md")
	if err := os.WriteFile(target, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := applied(t, r, domain.LearnProposal{Action: domain.LearnProposeUpdateMemory, TargetPath: target, BaseSHA256: sha([]byte("v1\n")), NewContent: "v2\n"})
	p.AppliedBefore = ""
	p.DecidedAt = now.Add(300 * time.Millisecond) // a store keeping a coarser clock
	if _, err := Undo(r, p, "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if read(t, target) != "v1\n" {
		t.Errorf("file = %q, want the backup made at approval", read(t, target))
	}
	p.DecidedAt = now.Add(time.Hour)
	if _, err := Undo(r, p, "", now); !errors.Is(err, ErrNothingToRestore) {
		t.Errorf("no backup at decided_at: %v", err)
	}
}

func TestRewrite_EditsWhatWasWrittenUnderTheSameGuard(t *testing.T) {
	r, mem := setup(t)
	p := applied(t, r, newMemory(mem))
	sum, err := Rewrite(r, p, "---\nname: feedback-new\n---\n\nBetter body.\n", "", now)
	if err != nil || sum == "" {
		t.Fatalf("rewrite = %q, %v", sum, err)
	}
	p.AppliedSHA256 = sum
	if err := os.WriteFile(p.TargetPath, []byte("someone else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var changed *ChangedError
	if _, err := Rewrite(r, p, "mine\n", "", now); !errors.As(err, &changed) {
		t.Fatalf("an edit over someone else's change needs confirming: %v", err)
	}
	if _, err := Rewrite(r, p, "mine\n", changed.State.Token, now); err != nil {
		t.Fatal(err)
	}
	if read(t, p.TargetPath) != "mine\n" {
		t.Error("confirmed edit must be written")
	}
}
