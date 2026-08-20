package smoke

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestRetireKeepsEverythingAndRecordsWhy: retire is NOT delete. The case leaves
// the checklist and leaves a trace - its name, its steps, the verdict, note and
// evidence the user recorded, and the reason it went. That trace is the point:
// "retired 3, now covered by tests" is worth far more than three cases quietly
// disappearing.
func TestRetireKeepsEverythingAndRecordsWhy(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	check, err := svc.Retire(ctx, "w1", "played", "now covered by TestPlayed")
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if check.RetiredAt == nil || check.RetiredReason != "now covered by TestPlayed" {
		t.Fatalf("retired marks = %v/%q, want a date and the reason", check.RetiredAt, check.RetiredReason)
	}
	if check.Verdict != domain.SmokePass || check.Note != "looked right to me" {
		t.Errorf("retire destroyed the user's result: %q/%q", check.Verdict, check.Note)
	}
	if len(check.Evidence) != 1 || check.Evidence[0].Filename != "human.png" {
		t.Errorf("retire destroyed the user's evidence: %+v", check.Evidence)
	}
	if check.Name != "Played case" || len(check.Steps) != 1 {
		t.Errorf("retire destroyed the case itself: name=%q steps=%v", check.Name, check.Steps)
	}

	// And it is still readable - the checklist shrank auditably, not silently.
	res, err := svc.List(ctx, "w1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if findCheck(t, res, "played").RetiredReason == "" {
		t.Error("the retired case dropped out of the list entirely")
	}
}

// TestRetireRequiresAReason: without one there is no trace, and a reasonless
// retire is just a delete wearing a different name.
func TestRetireRequiresAReason(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)
	for _, reason := range []string{"", "   "} {
		if _, err := svc.Retire(ctx, "w1", "played", reason); !errors.Is(err, ErrInvalid) {
			t.Errorf("retire(%q) err = %v, want ErrInvalid", reason, err)
		}
	}
	if got := mustCheck(t, svc, "played"); got.Retired() {
		t.Error("a reasonless retire went through anyway")
	}
}

// TestRetireIsTheWayPastTheResultsAtRiskRefusal is the whole reason this verb
// exists: #221's guard is correct and stays, but it left no legitimate way to
// remove a case the user had already played. Retire is that way, and it works by
// making the case invisible to Author rather than by weakening the guard.
func TestRetireIsTheWayPastTheResultsAtRiskRefusal(t *testing.T) {
	ctx := context.Background()
	svc, store := seedPlayedCase(ctx, t)
	shrunk := []domain.SmokeAuthoredCase{{ID: "draft", Name: "Draft case"}}

	// Before: the guard fires, exactly as #221 made it.
	_, err := svc.Author(ctx, "w1", shrunk)
	if !errors.Is(err, ErrResultsAtRisk) {
		t.Fatalf("author err = %v, want ErrResultsAtRisk", err)
	}
	// and it points at the way out.
	for _, want := range []string{"ao smoke retire", "--reason"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}

	if _, err := svc.Retire(ctx, "w1", "played", "now covered by TestPlayed"); err != nil {
		t.Fatalf("retire: %v", err)
	}

	// After: the same payload is accepted, and the case is still there with
	// everything the user recorded on it.
	res, err := svc.Author(ctx, "w1", shrunk)
	if err != nil {
		t.Fatalf("author after retire: %v", err)
	}
	if len(res.Checks) != 2 {
		t.Fatalf("checks = %d, want the active case plus the retired one", len(res.Checks))
	}
	retired := findCheck(t, res, "played")
	if !retired.Retired() || retired.Verdict != domain.SmokePass || len(retired.Evidence) != 1 {
		t.Errorf("the retired case lost its trace: %+v", retired)
	}
	if _, ok := store.evidence[retired.Evidence[0].ID]; !ok {
		t.Error("the retired case's evidence row was deleted")
	}
	// Active cases are last in the list ordering, retired after them.
	if res.Checks[len(res.Checks)-1].ID != "played" {
		t.Errorf("retired case is not sorted last: %v", res.Checks)
	}
}

// TestAuthorRefusesToReviveARetiredCase: an agent that re-sends its whole
// checklist every round must not be able to resurrect what it retired last
// round, silently and with the old results attached to new steps.
func TestAuthorRefusesToReviveARetiredCase(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)
	if _, err := svc.Retire(ctx, "w1", "played", "now covered by TestPlayed"); err != nil {
		t.Fatalf("retire: %v", err)
	}

	_, err := svc.Author(ctx, "w1", []domain.SmokeAuthoredCase{
		{ID: "played", Name: "Played case, rewritten"},
		{ID: "draft", Name: "Draft case"},
	})
	if !errors.Is(err, ErrCaseRetired) {
		t.Fatalf("author err = %v, want ErrCaseRetired", err)
	}
	for _, want := range []string{"Played case", "now covered by TestPlayed", "NEW id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
	if got := mustCheck(t, svc, "played"); got.Name != "Played case" {
		t.Errorf("the refused payload was applied anyway: %q", got.Name)
	}
}

// TestRetiredCaseIsFrozen: one rule, every write. A trace nothing can edit is
// what makes the trace worth reading.
func TestRetiredCaseIsFrozen(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		write func(svc *Service) error
	}{
		{"verdict", func(svc *Service) error {
			_, err := svc.SetVerdict(ctx, "w1", "played", domain.SmokeFail, "")
			return err
		}},
		{"reset", func(svc *Service) error {
			_, err := svc.Reset(ctx, "w1", "played")
			return err
		}},
		{"agent result", func(svc *Service) error {
			_, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{Verdict: domain.SmokePass})
			return err
		}},
		{"evidence", func(svc *Service) error {
			_, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
				Filename: "late.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
			})
			return err
		}},
		{"retire again", func(svc *Service) error {
			_, err := svc.Retire(ctx, "w1", "played", "a different reason")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := seedPlayedCase(ctx, t)
			if _, err := svc.Retire(ctx, "w1", "played", "now covered by TestPlayed"); err != nil {
				t.Fatalf("retire: %v", err)
			}
			if err := tc.write(svc); !errors.Is(err, ErrCaseRetired) {
				t.Fatalf("%s on a retired case err = %v, want ErrCaseRetired", tc.name, err)
			}
			got := mustCheck(t, svc, "played")
			if got.RetiredReason != "now covered by TestPlayed" || got.Verdict != domain.SmokePass || len(got.Evidence) != 1 {
				t.Errorf("the frozen case moved: %+v", got)
			}
		})
	}
}

// TestAuthorStillRefusesADroppedPlayedCaseThatWasNeverRetired pins #221's
// behaviour where it must not change: retire is an extra door, not a hole in the
// wall beside it.
func TestAuthorStillRefusesADroppedPlayedCaseThatWasNeverRetired(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)
	_, err := svc.Author(ctx, "w1", []domain.SmokeAuthoredCase{{ID: "draft", Name: "Draft case"}})
	if !errors.Is(err, ErrResultsAtRisk) {
		t.Fatalf("author err = %v, want ErrResultsAtRisk", err)
	}
	if got := mustCheck(t, svc, "played"); got.Verdict != domain.SmokePass {
		t.Errorf("the played case was disturbed: %+v", got)
	}
}

func mustCheck(t *testing.T, svc *Service, id string) domain.SmokeCheck {
	t.Helper()
	res, err := svc.List(context.Background(), "w1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return findCheck(t, res, id)
}

// TestReportAndJiraLeaveRetiredCasesOut: a retired case is no longer part of what
// the user was asked to play, so an old verdict of its must not be reported as
// this checklist's result - but the fact that the checklist shrank has to stay
// visible, or retiring becomes the silent delete it exists to replace.
func TestReportAndJiraLeaveRetiredCasesOut(t *testing.T) {
	ctx := context.Background()
	svc, store := seedPlayedCase(ctx, t)
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	if _, err := svc.SetVerdict(ctx, "w1", "draft", domain.SmokeFail, "still broken"); err != nil {
		t.Fatalf("set verdict: %v", err)
	}
	if _, err := svc.Retire(ctx, "w1", "played", "now covered by TestPlayed"); err != nil {
		t.Fatalf("retire: %v", err)
	}

	checks, err := store.ListSmokeChecksBySession(ctx, "w1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	summary := composeSummary("w1", checks)
	if !strings.Contains(summary, "1 of 1 checked") {
		t.Errorf("summary counts the retired case: %s", summary)
	}
	if strings.Contains(summary, "Played case") {
		t.Errorf("summary lists the retired case as a result: %s", summary)
	}
	if !strings.Contains(summary, "1 retired") {
		t.Errorf("summary hides that the checklist shrank: %s", summary)
	}
	if got := runChecks(checks); len(got) != 1 || got[0].ID != "draft" {
		t.Errorf("Jira rows = %+v, want only the active decided case", got)
	}
}
