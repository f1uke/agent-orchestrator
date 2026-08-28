package smoke

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func ptr[T any](v T) *T { return &v }

// THE PROPERTY THE WHOLE FEATURE RESTS ON: an author reaches only the cases it
// names.
//
// This is why shared authorship is a write-path change and not a permission
// change. Lifting the old dev refusal on `Author` alone would have made the
// second writer destructive - `Author` sets the WHOLE list, so whoever calls it
// second erases the other member's cases. Attribution records who destroyed
// something; it does not prevent it.
func TestAddCases_LeavesCasesItDoesNotNameAlone(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	if _, err := svc.Author(ctx, "mer-1", "mer-1", []domain.SmokeAuthoredCase{
		{ID: "dev-a", Name: "The lease is released on hand-off"},
		{ID: "dev-b", Name: "The branch still builds"},
	}); err != nil {
		t.Fatalf("dev could not author: %v", err)
	}

	res, err := svc.AddCases(ctx, "mer-2", "mer-1", []domain.SmokeAuthoredCase{{ID: "qa-a", Name: "Drag scrolls the list"}})
	if err != nil {
		t.Fatalf("qa could not add: %v", err)
	}
	if len(res.Checks) != 3 {
		t.Fatalf("checks = %d, want 3 - qa's add erased dev's cases", len(res.Checks))
	}
	// And the new case APPENDS rather than renumbering: a "CHECK N" the user has
	// already been shown must not come back meaning something else.
	byID := map[string]domain.SmokeCheck{}
	for _, c := range res.Checks {
		byID[c.ID] = c
	}
	if byID["dev-a"].Seq != 1 || byID["dev-b"].Seq != 2 || byID["qa-a"].Seq != 3 {
		t.Fatalf("seqs = %d/%d/%d, want 1/2/3", byID["dev-a"].Seq, byID["dev-b"].Seq, byID["qa-a"].Seq)
	}
}

// Re-adding an id EDITS that case and keeps everything the user recorded on it.
func TestAddCases_ReAddingAnIDKeepsTheUsersResults(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	if _, err := svc.Author(ctx, "mer-1", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints"}}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, "mer-1", "c1", domain.SmokeFail, "the header flickers", ""); err != nil {
		t.Fatalf("set verdict: %v", err)
	}

	res, err := svc.AddCases(ctx, "mer-2", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints on a cold start"}})
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	got := res.Checks[0]
	if got.Name != "Board still paints on a cold start" {
		t.Fatalf("the case was not edited: %q", got.Name)
	}
	if got.Verdict != domain.SmokeFail || got.Note != "the header flickers" {
		t.Fatalf("the user's result was destroyed by an edit: %q/%q", got.Verdict, got.Note)
	}
	if got.AuthoredByRole != domain.CrewRoleQA {
		t.Fatalf("the edit is not attributed to its author: %q", got.AuthoredByRole)
	}
}

// A partial edit leaves the fields it does not name EXACTLY as they were. That
// is the point of a separate verb: re-sending a whole case to change one field
// would silently overwrite whatever the other author sharpened in the meantime,
// and the loss would look like nothing having happened.
func TestEditCase_TouchesOnlyTheFieldsItNames(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	if _, err := svc.Author(ctx, "mer-2", "mer-1", []domain.SmokeAuthoredCase{{
		ID: "c1", Name: "Drag scrolls the list", Why: "it has broken twice",
		Steps: []string{"Open the tab.", "Drag up."}, Expected: "the list follows the finger",
	}}); err != nil {
		t.Fatalf("author: %v", err)
	}

	got, err := svc.EditCase(ctx, "mer-1", "mer-1", "c1", domain.SmokeCasePatch{PRNum: ptr(264)})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got.PRNum != 264 {
		t.Fatalf("prNum = %d, want 264", got.PRNum)
	}
	if got.Name != "Drag scrolls the list" || got.Why != "it has broken twice" ||
		got.Expected != "the list follows the finger" || len(got.Steps) != 2 {
		t.Fatalf("a one-field edit rewrote the rest of the case: %+v", got)
	}
	if got.AuthoredByRole != domain.CrewRoleDev {
		t.Fatalf("the edit is not attributed to dev: %q", got.AuthoredByRole)
	}
}

// An edit cannot reach the human's lane. Nothing in the patch names those
// fields, and this asserts it rather than trusting the shape.
func TestEditCase_CannotReachTheUsersResult(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	if _, err := svc.Author(ctx, "mer-1", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints"}}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, "mer-1", "c1", domain.SmokePass, "looked right", ""); err != nil {
		t.Fatalf("set verdict: %v", err)
	}

	got, err := svc.EditCase(ctx, "mer-2", "mer-1", "c1", domain.SmokeCasePatch{Name: ptr("Board still paints on a cold start")})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got.Verdict != domain.SmokePass || got.Note != "looked right" || got.DecidedAt == nil {
		t.Fatalf("an edit reached the user's result: %q/%q/%v", got.Verdict, got.Note, got.DecidedAt)
	}
}

func TestEditCase_RefusesAnEmptyPatchAndABlankName(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()
	if _, err := svc.Author(ctx, "mer-1", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints"}}); err != nil {
		t.Fatalf("author: %v", err)
	}

	if _, err := svc.EditCase(ctx, "mer-1", "mer-1", "c1", domain.SmokeCasePatch{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an edit that names no field must be a usage error: %v", err)
	}
	if _, err := svc.EditCase(ctx, "mer-1", "mer-1", "c1", domain.SmokeCasePatch{Name: ptr("   ")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a case must not be left without a name: %v", err)
	}
}

// THE GUARD THE HUMAN KEPT. A case nobody has played comes off freely - that is
// the capability they asked for. A case they HAVE played is retired, with a
// reason, never silently deleted: their verdict, note and evidence are the one
// part of a checklist AO cannot regenerate.
func TestRemoveCase_UnplayedGoesPlayedIsRefused(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	if _, err := svc.Author(ctx, "mer-2", "mer-1", []domain.SmokeAuthoredCase{
		{ID: "untouched", Name: "Nobody has played this"},
		{ID: "played", Name: "The user judged this one"},
	}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, "mer-1", "played", domain.SmokeFail, "the header flickers", ""); err != nil {
		t.Fatalf("set verdict: %v", err)
	}

	res, err := svc.RemoveCase(ctx, "mer-1", "mer-1", "untouched")
	if err != nil {
		t.Fatalf("an unplayed case must be removable: %v", err)
	}
	if len(res.Checks) != 1 {
		t.Fatalf("checks after remove = %d, want 1", len(res.Checks))
	}

	_, err = svc.RemoveCase(ctx, "mer-1", "mer-1", "played")
	if !errors.Is(err, ErrResultsAtRisk) {
		t.Fatalf("removing a played case must be refused: %v", err)
	}
	// It says the one thing that gets the caller unstuck.
	if !strings.Contains(err.Error(), "ao smoke retire") {
		t.Fatalf("the refusal does not point at retire: %v", err)
	}
	// And the case is still there, with everything the user recorded on it.
	after, err := svc.List(ctx, "mer-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after.Checks) != 1 || after.Checks[0].Verdict != domain.SmokeFail || after.Checks[0].Note != "the header flickers" {
		t.Fatalf("the refused removal damaged the case: %+v", after.Checks)
	}
}

// A NOTE ALONE counts as played, and so does evidence. The verdict is the
// obvious half; the other two are the ones a narrow guard would miss.
func TestRemoveCase_ANoteAloneProtectsACase(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()
	if _, err := svc.Author(ctx, "mer-1", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints"}}); err != nil {
		t.Fatalf("author: %v", err)
	}
	// A note with no verdict: the user started judging and wrote down what they saw.
	check := store.checks["c1"]
	check.Note = "half of it looked wrong, coming back to this"
	store.checks["c1"] = check

	if _, err := svc.RemoveCase(ctx, "mer-1", "mer-1", "c1"); !errors.Is(err, ErrResultsAtRisk) {
		t.Fatalf("a case carrying the user's note must not be removable: %v", err)
	}
}

// A retired case is frozen against the per-case verbs too, not just `set`.
func TestPerCaseWrites_RefuseARetiredCase(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()
	if _, err := svc.Author(ctx, "mer-1", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints"}}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.Retire(ctx, "mer-1", "c1", "now covered by TestBoardPaints"); err != nil {
		t.Fatalf("retire: %v", err)
	}

	if _, err := svc.EditCase(ctx, "mer-2", "mer-1", "c1", domain.SmokeCasePatch{PRNum: ptr(1)}); !errors.Is(err, ErrCaseRetired) {
		t.Fatalf("editing a retired case must be refused: %v", err)
	}
	if _, err := svc.RemoveCase(ctx, "mer-2", "mer-1", "c1"); !errors.Is(err, ErrCaseRetired) {
		t.Fatalf("removing a retired case must be refused: %v", err)
	}
	if _, err := svc.AddCases(ctx, "mer-2", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints"}}); !errors.Is(err, ErrCaseRetired) {
		t.Fatalf("re-adding a retired id must be refused rather than reviving it: %v", err)
	}
}

// STAND-DOWN: the answer an empty checklist could not previously give.
func TestStandDown_RecordsTheClaimWithItsAuthor(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)

	res, err := svc.StandDown(context.Background(), "mer-2", "mer-1", "pure refactor; behaviour covered by TestReplaceSmokeChecks")
	if err != nil {
		t.Fatalf("stand down: %v", err)
	}
	if res.StandDown == nil {
		t.Fatal("standing down recorded nothing - the tab would still just look empty")
	}
	if res.StandDown.ByRole != domain.CrewRoleQA || res.StandDown.By != "mer-2" {
		t.Fatalf("the claim is not attributed: %+v", res.StandDown)
	}
	if !strings.Contains(res.StandDown.Reason, "pure refactor") {
		t.Fatalf("the reason was lost: %q", res.StandDown.Reason)
	}
}

func TestStandDown_RequiresAReason(t *testing.T) {
	svc := newTestService(t, crewStore(t, true), nil)
	if _, err := svc.StandDown(context.Background(), "mer-1", "mer-1", "  "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("\"nothing to check\" with no account of what was looked at is not an answer: %v", err)
	}
}

// The claim cannot stand beside the thing that disproves it.
func TestStandDown_RefusedWhileCasesAreStillOnTheList(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()
	if _, err := svc.Author(ctx, "mer-1", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints"}}); err != nil {
		t.Fatalf("author: %v", err)
	}

	_, err := svc.StandDown(ctx, "mer-1", "mer-1", "nothing here")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("standing down over a live checklist must be refused: %v", err)
	}
	if !strings.Contains(err.Error(), "c1") {
		t.Fatalf("the refusal does not name what is in the way: %v", err)
	}

	// Retiring the last case empties the list in the sense that matters: nothing
	// is left for the user to play.
	if _, err := svc.Retire(ctx, "mer-1", "c1", "now covered by TestBoardPaints"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := svc.StandDown(ctx, "mer-1", "mer-1", "everything left is covered by tests"); err != nil {
		t.Fatalf("an all-retired checklist must accept a stand-down: %v", err)
	}
}

// Adding a case RETRACTS the claim. "There is something after all" is the honest
// undo, and it is the only one - a stand-down sitting above a list of things to
// play would be worse than none.
func TestStandDown_IsRetractedByAuthoringACase(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()
	if _, err := svc.StandDown(ctx, "mer-2", "mer-1", "no runtime surface"); err != nil {
		t.Fatalf("stand down: %v", err)
	}

	res, err := svc.AddCases(ctx, "mer-1", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Actually, look at the header"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.StandDown != nil {
		t.Fatalf("a case was added and the stand-down survived beside it: %+v", res.StandDown)
	}
}

// The checklist cap is shared by both write paths, so adding one case at a time
// cannot walk past the ceiling `set` enforces in a single call.
func TestAddCases_RespectsTheChecklistCap(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	full := make([]domain.SmokeAuthoredCase, 0, maxChecklistCases)
	for i := 0; i < maxChecklistCases; i++ {
		full = append(full, domain.SmokeAuthoredCase{ID: "c" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Name: "case"})
	}
	if _, err := svc.Author(ctx, "mer-1", "mer-1", full); err != nil {
		t.Fatalf("author a full checklist: %v", err)
	}
	if _, err := svc.AddCases(ctx, "mer-2", "mer-1", []domain.SmokeAuthoredCase{{ID: "one-too-many", Name: "case"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("adding past the cap must be refused: %v", err)
	}
}
