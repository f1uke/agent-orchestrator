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

// THE ENFORCEMENT. dev's prompt saying "the checklist is qa's" is a request, and
// a brief that asks dev for smoke cases beats a request - that is exactly what
// happened in both real crew runs. This is the layer a brief cannot argue with.
func TestAuthor_RefusesCrewDevWhileAQAExists(t *testing.T) {
	svc := newTestService(t, crewStore(t, true), nil)

	_, err := svc.Author(context.Background(), "mer-1", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}})
	if !errors.Is(err, ErrQAOwnsChecklist) {
		t.Fatalf("a crew dev authored a checklist while its qa was alive: err = %v", err)
	}
	// It NAMES the qa, under the session sigil, so dev can hand the work over
	// instead of guessing who owns it.
	if !strings.Contains(err.Error(), "@mer-2") {
		t.Fatalf("the refusal does not name the qa: %v", err)
	}
	if !strings.Contains(err.Error(), "ao send --crew qa") {
		t.Fatalf("the refusal does not say how to hand it over: %v", err)
	}
}

// The predicate is A QA EXISTS, not "this is a crew dev". Under lazy creation a
// standard task runs dev-alone until the trigger fires, and during that window
// dev authoring the checklist is CORRECT and must keep working.
func TestAuthor_CrewDevWithNoQAStillAuthors(t *testing.T) {
	store := crewStore(t, false)
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "mer-1", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}}); err != nil {
		t.Fatalf("a dev with no qa must still author its own checklist: %v", err)
	}
	if len(store.lastCases) != 1 {
		t.Fatalf("cases written = %d, want 1", len(store.lastCases))
	}
}

// THE WINDOW, walked end to end. Under lazy creation a `standard` task IS a solo
// session until dev touches a runtime surface - dev carries no crew columns at
// all - so the same call has to be allowed before the join and refused after it.
// This is the interaction lazy creation reopens, so it is asserted rather than
// reasoned about.
func TestAuthor_TheRefusalStartsTheMomentAQAAppears(t *testing.T) {
	store := newFakeStore()
	// Before: a standard task, one agent, no crew columns anywhere.
	store.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard,
	}
	svc := newTestService(t, store, nil)
	if _, err := svc.Author(context.Background(), "mer-1", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}}); err != nil {
		t.Fatalf("a standard task with no qa yet could not author its checklist: %v", err)
	}

	// The trigger fires: dev gains its crew columns and a qa appears beside it.
	dev := store.sessions["mer-1"]
	dev.CrewID, dev.CrewRole = "mer-1", domain.CrewRoleDev
	store.sessions["mer-1"] = dev
	store.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleQA, CrewJoinReason: domain.CrewJoinSim,
	}

	// After: the same call, from the same caller, about the same task.
	_, err := svc.Author(context.Background(), "mer-1", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}})
	if !errors.Is(err, ErrQAOwnsChecklist) {
		t.Fatalf("dev still owns the checklist after its qa arrived: err = %v", err)
	}
}

// A TERMINATED qa is not a qa. Its rows survive teardown, and reading them as
// "a qa exists" would leave dev unable to author on a task whose qa is gone.
func TestAuthor_TerminatedQADoesNotOwnTheChecklist(t *testing.T) {
	store := crewStore(t, true)
	qa := store.sessions["mer-2"]
	qa.IsTerminated = true
	store.sessions["mer-2"] = qa
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "mer-1", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}}); err != nil {
		t.Fatalf("a terminated qa must not block dev: %v", err)
	}
}

// qa is the OWNER, and it authors against the crew id - which is dev's session
// id, the same target dev was just refused for. Only the caller differs.
func TestAuthor_QAAuthorsAgainstTheCrewID(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "mer-2", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}}); err != nil {
		t.Fatalf("qa was refused its own checklist: %v", err)
	}
	if len(store.lastCases) != 1 {
		t.Fatalf("cases written = %d, want 1", len(store.lastCases))
	}
}

// THE PRESERVATION GUARD. A solo worker is what almost every session is: it has
// no crew and no qa, so the refusal can never fire on it and `ao smoke set`
// behaves exactly as it did before this change.
func TestAuthor_SoloWorkerIsUntouched(t *testing.T) {
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj", Kind: domain.KindWorker}
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "w1", "w1", []domain.SmokeAuthoredCase{{Name: "Build passes"}}); err != nil {
		t.Fatalf("a solo worker was refused its own checklist: %v", err)
	}
	if len(store.lastCases) != 1 {
		t.Fatalf("cases written = %d, want 1", len(store.lastCases))
	}
}

// A caller AO cannot identify (an unset AO_SESSION_ID, the desktop app, an
// older `ao`) is not refused: the rule needs a known crew dev, and refusing on
// an unknown caller would break authoring for everyone the refusal is not for.
func TestAuthor_UnknownCallerIsNotRefused(t *testing.T) {
	store := crewStore(t, true)
	svc := newTestService(t, store, nil)

	if _, err := svc.Author(context.Background(), "", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}}); err != nil {
		t.Fatalf("an unidentified caller must not be refused: %v", err)
	}
	if _, err := svc.Author(context.Background(), "ghost", "mer-1", []domain.SmokeAuthoredCase{{Name: "Board still paints"}}); err != nil {
		t.Fatalf("a caller with no session row must not be refused: %v", err)
	}
}
