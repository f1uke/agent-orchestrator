package smoke

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// crewStore builds a task worked by a crew: dev owns the checklist's session id
// (the CREW id is dev's id, which is what `$AO_CREW_ID` expands to), and qa is
// the second member on the same crew.
func crewStore(t *testing.T, withQA bool) *fakeStore {
	t.Helper()
	store := newFakeStore()
	store.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleDev,
	}
	if withQA {
		store.sessions["mer-2"] = domain.SessionRecord{
			ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker,
			CrewID: "mer-1", CrewRole: domain.CrewRoleQA,
		}
	}
	return store
}

// THE REVERSAL. This file used to assert the opposite - that a crew's dev was
// REFUSED (`ErrQAOwnsChecklist`) for as long as its task had a qa. The human
// reversed that after watching a real iOS run where qa wrote two cases while
// several places needed checking: dev is the member that knows what the change
// actually touched, and qa has to reconstruct it from the outside.
//
// So the enforcement is gone, and what replaces it is not a weaker permission
// check - it is a different WRITE SHAPE (AddCases/EditCase/RemoveCase, one case
// at a time). Permission is asserted here; the shape is asserted in
// shared_authorship_test.go, which is the half that actually makes two authors
// safe.
func TestAuthor_CrewDevIsNotRefusedWhileAQAExists(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)

	res, err := svc.Author(context.Background(), "mer-1", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}})
	if err != nil {
		t.Fatalf("a crew dev must be able to author cases: %v", err)
	}
	if len(res.Checks) != 1 {
		t.Fatalf("checks written = %d, want 1", len(res.Checks))
	}
	// And the write is ATTRIBUTED, which is the human's stated reason for wanting
	// it: they want to see which cases came from dev and which from qa.
	got := res.Checks[0]
	if got.AuthoredBy != "mer-1" || got.AuthoredByRole != domain.CrewRoleDev {
		t.Fatalf("dev's case is not attributed to dev: by=%q role=%q", got.AuthoredBy, got.AuthoredByRole)
	}
	if got.AuthoredAt == nil {
		t.Fatal("an attributed case must carry when it was written")
	}
}

// qa authors against the crew id - dev's session id, the same target dev writes
// to. Only the CALLER differs, which is why `from` exists at all: the target
// cannot say which member is writing.
func TestAuthor_QAIsAttributedSeparatelyFromDev(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "mer-1", "mer-1", []domain.SmokeAuthoredCase{{ID: "dev-case", Name: "Board still paints"}}); err != nil {
		t.Fatalf("dev could not author: %v", err)
	}
	res, err := svc.AddCases(context.Background(), "mer-2", "mer-1", []domain.SmokeAuthoredCase{{ID: "qa-case", Name: "Drag scrolls the list"}})
	if err != nil {
		t.Fatalf("qa could not add a case: %v", err)
	}

	byID := map[string]domain.SmokeCheck{}
	for _, c := range res.Checks {
		byID[c.ID] = c
	}
	if byID["dev-case"].AuthoredByRole != domain.CrewRoleDev {
		t.Fatalf("dev's case lost its attribution: %q", byID["dev-case"].AuthoredByRole)
	}
	if byID["qa-case"].AuthoredByRole != domain.CrewRoleQA {
		t.Fatalf("qa's case is not attributed to qa: %q", byID["qa-case"].AuthoredByRole)
	}
}

// A solo worker has no crew, so it carries an author id and NO role. That is a
// distinction worth keeping: "dev wrote this" and "the worker wrote this" are
// different statements on a list whose point is telling two authors apart.
func TestAuthor_SoloWorkerIsAttributedWithoutARole(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", Kind: domain.KindWorker}
	svc := newTestService(t, store, nil)

	res, err := svc.Author(context.Background(), "w1", "w1", []domain.SmokeAuthoredCase{{Name: "Build passes"}})
	if err != nil {
		t.Fatalf("a solo worker was refused its own checklist: %v", err)
	}
	if got := res.Checks[0]; got.AuthoredBy != "w1" || got.AuthoredByRole != "" {
		t.Fatalf("solo attribution = %q/%q, want w1 with no role", got.AuthoredBy, got.AuthoredByRole)
	}
}

// A caller AO cannot identify - an unset AO_SESSION_ID, the desktop app, a
// direct API call, an older `ao` - writes with NO author rather than a guessed
// one, and is never refused. An `authoredAt` with nobody's name beside it would
// read as a fact about a person.
func TestAuthor_UnknownCallerWritesWithoutAnAuthor(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)

	for _, from := range []domain.SessionID{"", "ghost"} {
		res, err := svc.Author(context.Background(), from, "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}})
		if err != nil {
			t.Fatalf("an unidentified caller (%q) must not be refused: %v", from, err)
		}
		got := res.Checks[0]
		if got.AuthoredBy != "" || got.AuthoredByRole != "" || got.AuthoredAt != nil {
			t.Fatalf("from=%q was attributed anyway: by=%q role=%q at=%v", from, got.AuthoredBy, got.AuthoredByRole, got.AuthoredAt)
		}
	}
}

// Attribution is stamped by AUTHORING writes only. It answers "who wrote this
// case", which is a different question from "who last touched this row" -
// updated_at already answers the second, and a verdict the human recorded must
// never make the case look like they authored it.
func TestAttribution_IsNotMovedByTheHumansVerdict(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)
	ctx := context.Background()

	res, err := svc.Author(ctx, "mer-2", "mer-1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "Board still paints"}})
	if err != nil {
		t.Fatalf("author: %v", err)
	}
	if res.Checks[0].AuthoredByRole != domain.CrewRoleQA {
		t.Fatalf("precondition: case is not qa's: %q", res.Checks[0].AuthoredByRole)
	}

	after, err := svc.SetVerdict(ctx, "mer-1", "c1", domain.SmokePass, "looked right")
	if err != nil {
		t.Fatalf("set verdict: %v", err)
	}
	if after.AuthoredBy != "mer-2" || after.AuthoredByRole != domain.CrewRoleQA {
		t.Fatalf("the human's verdict moved the case's authorship to %q/%q", after.AuthoredBy, after.AuthoredByRole)
	}
}

// The refusal that is GONE must be gone from the message surface too: a caller
// that still parses for it would read a permission problem into a write that
// succeeded.
func TestAuthor_NoRefusalMentionsChecklistOwnership(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)

	_, err := svc.Author(context.Background(), "mer-1", "mer-1", nil)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("an empty payload is still a usage error: %v", err)
	}
	if strings.Contains(err.Error(), "owns this task's checklist") {
		t.Fatalf("the reversed ownership refusal survives in a message: %v", err)
	}
}
