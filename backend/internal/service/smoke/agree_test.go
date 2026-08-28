package smoke

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// seedRunCase authors one case and records `verdicts` against it as successive
// machine runs, returning the service and the run ids in the order recorded.
func seedRunCase(ctx context.Context, t *testing.T, verdicts ...domain.SmokeVerdict) (*Service, *fakeStore, []string) {
	t.Helper()
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, nil)
	if _, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{
		{ID: "c1", Name: "The case", Steps: []string{"open it"}},
	}); err != nil {
		t.Fatalf("author: %v", err)
	}
	ids := make([]string, 0, len(verdicts))
	for _, v := range verdicts {
		check, err := svc.RecordAgentResult(ctx, "w1", "c1", domain.SmokeAgentResult{Verdict: v, Note: "qa saw it", SHA: "abc1234def"})
		if err != nil {
			t.Fatalf("record agent result %q: %v", v, err)
		}
		run, ok := check.LatestRun()
		if !ok {
			t.Fatalf("record agent result %q: no recorded run", v)
		}
		ids = append(ids, run.ID)
	}
	return svc, store, ids
}

// TestAgreeingWritesTheUsersLaneAndNeverTheMachines is the property the whole
// "agree" button exists under. Confirming qa's conclusion must be a HUMAN act
// recorded in the HUMAN's lane: the machine may not gain a way to fill in the
// verdict, because "0 of 7 verified" only means "a person looked" for as long as
// nothing but a person can move it.
//
// So this asserts BOTH halves: the user's verdict lands exactly as a
// hand-pressed Pass lands it, and the machine's lane is byte-identical
// afterwards - same run count, same run verdict, no new run opened by the act of
// agreeing.
func TestAgreeingWritesTheUsersLaneAndNeverTheMachines(t *testing.T) {
	ctx := context.Background()
	svc, _, runs := seedRunCase(ctx, t, domain.SmokePass)
	before, err := svc.getCheck(ctx, "c1")
	if err != nil {
		t.Fatalf("get check: %v", err)
	}

	after, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokePass, "agreed", runs[0])
	if err != nil {
		t.Fatalf("agree: %v", err)
	}

	// The user's lane: a real, complete verdict of theirs.
	if after.Verdict != domain.SmokePass {
		t.Fatalf("user verdict = %q, want pass", after.Verdict)
	}
	if after.DecidedAt == nil {
		t.Fatal("decidedAt is nil: an agreed verdict must be stamped exactly like a hand-pressed one")
	}
	if after.Note != "agreed" {
		t.Fatalf("note = %q, want the user's own note", after.Note)
	}
	if after.AgreedRunID != runs[0] {
		t.Fatalf("agreedRunId = %q, want %q", after.AgreedRunID, runs[0])
	}

	// The machine's lane: untouched. Agreeing records nothing on qa's behalf.
	if len(after.Runs) != len(before.Runs) {
		t.Fatalf("runs = %d, want %d: agreeing must not open or close a run", len(after.Runs), len(before.Runs))
	}
	for i, run := range after.Runs {
		was := before.Runs[i]
		if run.ID != was.ID || run.Verdict != was.Verdict || run.Note != was.Note || run.SessionID != was.SessionID {
			t.Fatalf("run %d changed: %+v -> %+v", i, was, run)
		}
	}
	if after.AgentVerdict != before.AgentVerdict {
		t.Fatalf("agentVerdict = %q, want %q unchanged", after.AgentVerdict, before.AgentVerdict)
	}
}

// TestAgreeingCountsAsAPersonPlayingTheCase pins the signal the restriction
// protects: a case only leaves "pending" because someone acted, and an agreed
// case is played on exactly the same terms as a hand-judged one - including for
// the guard that refuses to destroy a played case.
func TestAgreeingCountsAsAPersonPlayingTheCase(t *testing.T) {
	ctx := context.Background()
	svc, _, runs := seedRunCase(ctx, t, domain.SmokePass)

	// Before: the machine has judged it, and that alone does NOT make it played.
	check, err := svc.getCheck(ctx, "c1")
	if err != nil {
		t.Fatalf("get check: %v", err)
	}
	if played(check) {
		t.Fatal("a machine result alone made the case count as played")
	}

	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokePass, "", runs[0]); err != nil {
		t.Fatalf("agree: %v", err)
	}
	check, err = svc.getCheck(ctx, "c1")
	if err != nil {
		t.Fatalf("get check: %v", err)
	}
	if !played(check) {
		t.Fatal("an agreed verdict did not count as the user playing the case")
	}
	// And it is protected like any other result the user recorded.
	if _, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{{ID: "other", Name: "Other"}}); !errors.Is(err, ErrResultsAtRisk) {
		t.Fatalf("re-author dropping an agreed case: err = %v, want ErrResultsAtRisk", err)
	}
}

// TestAgreeingWithASkipIsRefused settles the case the design had to decide.
// qa's skip means "I could not run this one, nothing was exercised"; the user's
// skip means "this check does not apply". They are different claims, so there is
// nothing to agree with - and a one-click agreement here would put words in the
// user's mouth they never said. The Tests tab therefore offers no button, and
// this makes it a RULE rather than a UI omission.
func TestAgreeingWithASkipIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, _, runs := seedRunCase(ctx, t, domain.SmokeSkip)

	_, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokeSkip, "", runs[0])
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("agree with a skip: err = %v, want ErrInvalid", err)
	}
	// Skipping on the user's own authority is untouched.
	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokeSkip, "does not apply here", ""); err != nil {
		t.Fatalf("plain skip: %v", err)
	}
}

// TestAgreeingWithAnEvidenceOnlyRunIsRefused keeps the evidence-only outcome
// from degrading into a disguised pass. A run that deliberately did not judge has
// no verdict to confirm; agreeing with it could only ever mean "pass", asserted
// on qa's behalf over a conclusion qa explicitly declined to reach.
func TestAgreeingWithAnEvidenceOnlyRunIsRefused(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, nil)
	if _, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{{ID: "c1", Name: "The case"}}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.AttachEvidence(ctx, "w1", "c1", EvidenceUpload{
		Filename: "shot.png", Mime: "image/png", Reader: strings.NewReader("PNG"), Source: domain.SmokeEvidenceAgent,
	}); err != nil {
		t.Fatalf("attach agent evidence: %v", err)
	}
	check, err := svc.RecordAgentResult(ctx, "w1", "c1", domain.SmokeAgentResult{SHA: "abc1234def"})
	if err != nil {
		t.Fatalf("record evidence-only result: %v", err)
	}
	if len(check.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(check.Runs))
	}

	for _, verdict := range []domain.SmokeVerdict{domain.SmokePass, domain.SmokeFail} {
		if _, err := svc.SetVerdict(ctx, "w1", "c1", verdict, "", check.Runs[0].ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("agree %q with an evidence-only run: err = %v, want ErrInvalid", verdict, err)
		}
	}
}

// TestAgreementMustMatchTheRunItNames is what stops "agreed with qa" from being
// a claim nobody can check. The named run must be on this case, must have
// concluded, and must have said the same thing.
func TestAgreementMustMatchTheRunItNames(t *testing.T) {
	ctx := context.Background()
	svc, _, runs := seedRunCase(ctx, t, domain.SmokeFail)

	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokePass, "", runs[0]); !errors.Is(err, ErrInvalid) {
		t.Fatalf("agree pass with a failing run: err = %v, want ErrInvalid", err)
	}
	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokeFail, "", "run_somewhere_else_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("agree with a foreign run: err = %v, want ErrNotFound", err)
	}
	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokeFail, "matches", runs[0]); err != nil {
		t.Fatalf("agree fail with a failing run: %v", err)
	}
}

// TestAgreeingNamesWhichRunWasAgreedWith is the run-history case: a case can have
// failed at one commit and passed at another, so "the user agreed with qa" is
// ambiguous the moment two runs disagree. The stored id resolves it, and the
// verdict must match the run it names rather than the case's latest result.
func TestAgreeingNamesWhichRunWasAgreedWith(t *testing.T) {
	ctx := context.Background()
	svc, _, runs := seedRunCase(ctx, t, domain.SmokeFail, domain.SmokePass)

	after, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokePass, "", runs[1])
	if err != nil {
		t.Fatalf("agree with the latest run: %v", err)
	}
	if after.AgreedRunID != runs[1] {
		t.Fatalf("agreedRunId = %q, want the run agreed with (%q)", after.AgreedRunID, runs[1])
	}
	if got := agreedRunLabel(after); got != "agreed with qa's run 2" {
		t.Fatalf("report label = %q, want it to name run 2", got)
	}
	// The superseded run is still there, still saying what it said.
	if after.Runs[0].Verdict != domain.SmokeFail {
		t.Fatalf("run 1 verdict = %q, want fail preserved", after.Runs[0].Verdict)
	}
	// And agreeing "pass" against the run that FAILED stays refused, even though
	// the case's current machine result is a pass.
	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokePass, "", runs[0]); !errors.Is(err, ErrInvalid) {
		t.Fatalf("agree pass with the earlier failing run: err = %v, want ErrInvalid", err)
	}
}

// TestChangingTheVerdictByHandDropsTheAgreement: once the user overrides their
// own agreed verdict, the row must stop claiming they agreed with a run. A stale
// agreement standing over a verdict the user has since changed by hand is a
// false statement about how they decided.
func TestChangingTheVerdictByHandDropsTheAgreement(t *testing.T) {
	ctx := context.Background()
	svc, _, runs := seedRunCase(ctx, t, domain.SmokePass)
	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokePass, "", runs[0]); err != nil {
		t.Fatalf("agree: %v", err)
	}

	after, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokeFail, "actually it is broken", "")
	if err != nil {
		t.Fatalf("override by hand: %v", err)
	}
	if after.AgreedRunID != "" {
		t.Fatalf("agreedRunId = %q, want cleared by a hand-made verdict", after.AgreedRunID)
	}

	// Reset clears it too - the case goes back to nobody having decided.
	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokePass, "", runs[0]); err != nil {
		t.Fatalf("agree again: %v", err)
	}
	reset, err := svc.Reset(ctx, "w1", "c1")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if reset.AgreedRunID != "" || reset.Verdict != domain.SmokePending {
		t.Fatalf("after reset: verdict %q, agreedRunId %q; want pending and empty", reset.Verdict, reset.AgreedRunID)
	}
}

// TestTheReportSaysTheUserAgreedRatherThanDerived: the provenance must survive
// the boundary to the worker. The verdict it reads is the user's either way -
// an agreement is not a weaker result - but "they confirmed your run 2" and
// "they played it and reached the same answer" are different amounts of
// independent evidence, and the icon alone cannot tell them apart.
func TestTheReportSaysTheUserAgreedRatherThanDerived(t *testing.T) {
	ctx := context.Background()
	svc, store, runs := seedRunCase(ctx, t, domain.SmokeFail, domain.SmokePass)
	if _, err := svc.AddCases(ctx, "", "w1", []domain.SmokeAuthoredCase{{ID: "c2", Name: "Judged by hand"}}); err != nil {
		t.Fatalf("add case: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, "w1", "c1", domain.SmokePass, "", runs[1]); err != nil {
		t.Fatalf("agree: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, "w1", "c2", domain.SmokePass, "played it myself", ""); err != nil {
		t.Fatalf("hand verdict: %v", err)
	}

	checks, err := store.ListSmokeChecksBySession(ctx, "w1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	summary := composeSummary("w1", checks)
	if !strings.Contains(summary, "agreed with qa's run 2") {
		t.Fatalf("report does not name the run the user agreed with:\n%s", summary)
	}
	// Both are the user's, and both count. An agreement is not reported as
	// something less than a verdict.
	if !strings.Contains(summary, "2 of 2 checked · 2 pass, 0 fail") {
		t.Fatalf("report does not count the agreed case as checked:\n%s", summary)
	}
	// And the hand-made one claims no agreement it did not make.
	if strings.Count(summary, "agreed with qa's run") != 1 {
		t.Fatalf("an independently reached verdict claims an agreement:\n%s", summary)
	}
}
