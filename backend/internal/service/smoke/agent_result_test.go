package smoke

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// seedPlayedCase authors one case and has the user play it fully - a verdict, a
// note and an attached screenshot - so every test below starts from the state
// that actually matters: a case a person has already invested in.
func seedPlayedCase(ctx context.Context, t *testing.T) (*Service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	store.sessions["w1"] = domain.SessionRecord{ID: "w1", ProjectID: "proj"}
	svc := newTestService(t, store, nil)
	if _, err := svc.Author(ctx, "", "w1", []domain.SmokeAuthoredCase{
		{ID: "played", Name: "Played case", Steps: []string{"open it"}},
		{ID: "draft", Name: "Draft case"},
	}); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := svc.SetVerdict(ctx, "w1", "played", domain.SmokePass, "looked right to me", ""); err != nil {
		t.Fatalf("set verdict: %v", err)
	}
	if _, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "human.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
	}); err != nil {
		t.Fatalf("attach user evidence: %v", err)
	}
	return svc, store
}

// TestRecordAgentResultLeavesTheUsersResultAlone is the load-bearing property of
// the whole two-results design: a machine writing its answer must not disturb a
// single field of the person's. They answer different questions - "did the steps
// run" versus "does this work for a person" - and merging them would let a case
// read confirmed with nobody having touched the app.
func TestRecordAgentResultLeavesTheUsersResultAlone(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	check, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{
		Verdict: domain.SmokeFail,
		Note:    "step 2 timed out",
		SHA:     "abc123def456",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	// The user's half, untouched - including the OPPOSITE verdict.
	if check.Verdict != domain.SmokePass {
		t.Errorf("user verdict = %q, want pass (the machine overwrote it)", check.Verdict)
	}
	if check.Note != "looked right to me" {
		t.Errorf("user note = %q, want the user's own", check.Note)
	}
	if check.DecidedAt == nil {
		t.Error("user decidedAt was cleared by a machine write")
	}
	if len(check.Evidence) != 1 || check.Evidence[0].Filename != "human.png" {
		t.Errorf("user evidence = %+v, want only the file the user attached", check.Evidence)
	}
	// The machine's half, recorded.
	if check.AgentVerdict != domain.SmokeFail || check.AgentNote != "step 2 timed out" {
		t.Errorf("agent result = %q/%q, want fail/step 2 timed out", check.AgentVerdict, check.AgentNote)
	}
	if check.AgentSHA != "abc123def456" {
		t.Errorf("agent sha = %q, want the commit it ran against", check.AgentSHA)
	}
	if check.AgentRanAt == nil {
		t.Error("agent ranAt not stamped")
	}
	// And the authored content is untouched: record cannot rewrite the case.
	if check.Name != "Played case" || len(check.Steps) != 1 {
		t.Errorf("authored content changed: name=%q steps=%v", check.Name, check.Steps)
	}
}

// TestAgentEvidenceStaysOutOfTheUsersList: evidence is what you go back to when
// you distrust a verdict, so a machine's screenshot and a person's must never sit
// in one indistinguishable list.
func TestAgentEvidenceStaysOutOfTheUsersList(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	agentEv, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "machine.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
		Source: domain.SmokeEvidenceAgent,
	})
	if err != nil {
		t.Fatalf("attach agent evidence: %v", err)
	}
	if agentEv.Source != domain.SmokeEvidenceAgent {
		t.Errorf("stored source = %q, want agent", agentEv.Source)
	}

	res, err := svc.List(ctx, "w1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	check := findCheck(t, res, "played")
	if len(check.Evidence) != 1 || check.Evidence[0].Filename != "human.png" {
		t.Errorf("user evidence = %+v, want only human.png", check.Evidence)
	}
	if len(check.AgentEvidence) != 1 || check.AgentEvidence[0].Filename != "machine.png" {
		t.Errorf("agent evidence = %+v, want only machine.png", check.AgentEvidence)
	}
	for _, ev := range check.Evidence {
		if ev.Source != domain.SmokeEvidenceUser {
			t.Errorf("user list carries a %q-sourced file", ev.Source)
		}
	}
}

// TestResetKeepsTheMachinesResult: Reset is the USER re-playing a case. It clears
// what they recorded and nothing else - the machine's result and artifacts are
// not theirs to drop, and wiping them here would merge the two results by the
// back door.
func TestResetKeepsTheMachinesResult(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)
	if _, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "machine.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
		Source: domain.SmokeEvidenceAgent,
	}); err != nil {
		t.Fatalf("attach agent evidence: %v", err)
	}
	if _, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{Verdict: domain.SmokePass, SHA: "deadbeef"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	check, err := svc.Reset(ctx, "w1", "played")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if check.Verdict != domain.SmokePending || check.Note != "" || len(check.Evidence) != 0 {
		t.Errorf("reset left the user's result behind: %q/%q/%d", check.Verdict, check.Note, len(check.Evidence))
	}
	if check.AgentVerdict != domain.SmokePass || check.AgentSHA != "deadbeef" {
		t.Errorf("reset destroyed the machine's result: %q/%q", check.AgentVerdict, check.AgentSHA)
	}
	if len(check.AgentEvidence) != 1 {
		t.Errorf("reset destroyed the machine's evidence: %+v", check.AgentEvidence)
	}
}

// TestRecordAgentResultEvidenceOnly: a case about paint, focus, timing or feel is
// the human's alone forever. A machine can still earn its keep by driving to the
// state and capturing what it saw, so a record with no verdict is legitimate -
// but only when it actually carries evidence, or it says nothing at all.
func TestRecordAgentResultEvidenceOnly(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	_, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty record err = %v, want ErrInvalid", err)
	}

	if _, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "machine.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
		Source: domain.SmokeEvidenceAgent,
	}); err != nil {
		t.Fatalf("attach agent evidence: %v", err)
	}
	check, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{Note: "here is the screen"})
	if err != nil {
		t.Fatalf("evidence-only record: %v", err)
	}
	if check.AgentVerdict != "" {
		t.Errorf("agent verdict = %q, want empty (it did not judge)", check.AgentVerdict)
	}
	if check.AgentRanAt == nil {
		t.Error("an evidence-only record must still say a machine ran it")
	}
}

func TestRecordAgentResultRejectsAnUnknownVerdict(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)
	_, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{Verdict: "green"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRecordAgentResultRejectsAForeignCase(t *testing.T) {
	ctx := context.Background()
	svc, store := seedPlayedCase(ctx, t)
	store.sessions["w2"] = domain.SessionRecord{ID: "w2", ProjectID: "proj"}
	_, err := svc.RecordAgentResult(ctx, "w2", "played", domain.SmokeAgentResult{Verdict: domain.SmokePass})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func findCheck(t *testing.T, res SessionSmoke, id string) domain.SmokeCheck {
	t.Helper()
	for _, c := range res.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("case %q missing from %+v", id, res.Checks)
	return domain.SmokeCheck{}
}

// TestReRunningACaseAddsARunInsteadOfDestroyingTheLastOne is the behaviour this
// whole shape exists for. The human hit the failure it fixes: a case re-run on a
// newer commit gave the OPPOSITE result, the earlier verdict was overwritten out
// of existence, and both rounds' screenshots ended up in one strip under the
// newer verdict - so the only surviving trace of the inversion was a sentence
// qa wrote in a note, because the structure had nowhere to put it.
func TestReRunningACaseAddsARunInsteadOfDestroyingTheLastOne(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	// Round 1 at the old commit: a capture, then a failure.
	if _, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "clipped.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
		Source: domain.SmokeEvidenceAgent,
	}); err != nil {
		t.Fatalf("attach round 1 evidence: %v", err)
	}
	if _, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{
		Verdict: domain.SmokeFail, Note: "clipped at 320px", SHA: "d44ad432c",
	}); err != nil {
		t.Fatalf("record round 1: %v", err)
	}

	// Round 2 at the new commit: a capture, then the opposite result.
	if _, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "clean.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
		Source: domain.SmokeEvidenceAgent,
	}); err != nil {
		t.Fatalf("attach round 2 evidence: %v", err)
	}
	check, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{
		Verdict: domain.SmokePass, Note: "renders clean", SHA: "9f10c22a1",
	})
	if err != nil {
		t.Fatalf("record round 2: %v", err)
	}

	if len(check.Runs) != 2 {
		t.Fatalf("runs = %d, want both rounds - a re-run must not destroy the result before it", len(check.Runs))
	}
	if check.Runs[0].Verdict != domain.SmokeFail || check.Runs[0].SHA != "d44ad432c" {
		t.Errorf("run 1 = %q at %q, want the earlier failure with its own commit", check.Runs[0].Verdict, check.Runs[0].SHA)
	}
	if check.Runs[1].Verdict != domain.SmokePass || check.Runs[1].SHA != "9f10c22a1" {
		t.Errorf("run 2 = %q at %q", check.Runs[1].Verdict, check.Runs[1].SHA)
	}
	if check.AgentVerdict != domain.SmokePass {
		t.Errorf("the case's current machine result = %q, want the latest run's pass", check.AgentVerdict)
	}

	// Each round's capture stayed with the verdict it belonged to.
	round1 := check.RunEvidence(check.Runs[0].ID)
	round2 := check.RunEvidence(check.Runs[1].ID)
	if len(round1) != 1 || round1[0].Filename != "clipped.png" {
		t.Errorf("run 1 evidence = %+v, want the capture from the round that failed", round1)
	}
	if len(round2) != 1 || round2[0].Filename != "clean.png" {
		t.Errorf("run 2 evidence = %+v, want the capture from the round that passed", round2)
	}
	// And the user's own screenshot is in neither: the user's lane has no runs.
	if len(check.UnknownRunEvidence()) != 0 {
		t.Errorf("unknown-run evidence = %+v, want none", check.UnknownRunEvidence())
	}
	if len(check.Evidence) != 1 || check.Evidence[0].Filename != "human.png" {
		t.Errorf("user evidence = %+v, want only the file the user attached", check.Evidence)
	}
}

// TestAnEvidenceOnlyRecordNeedsEvidenceFromTHISRun: "I ran it and captured this,
// I am not the one who can judge it" is a complete record, and it is a claim
// about what THIS round saw. An earlier round's screenshots cannot stand in for
// it - they are not what this run looked at, and letting them count would make
// an empty record say nothing at all while looking like it said something.
func TestAnEvidenceOnlyRecordNeedsEvidenceFromTHISRun(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	if _, err := svc.AttachEvidence(ctx, "w1", "played", EvidenceUpload{
		Filename: "round1.png", Mime: "image/png", Reader: strings.NewReader("PNG"),
		Source: domain.SmokeEvidenceAgent,
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{
		Note: "the header renders at 320px, see the shot",
	}); err != nil {
		t.Fatalf("evidence-only record with this run's evidence: %v", err)
	}

	// A second record with no verdict and nothing captured this time.
	_, err := svc.RecordAgentResult(ctx, "w1", "played", domain.SmokeAgentResult{Note: "still fine"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid: an empty record leaning on an earlier round's captures says nothing about this one", err)
	}
	if !strings.Contains(err.Error(), "this run") {
		t.Errorf("error %q does not say the evidence has to come from this run", err)
	}
}

// A SKIP HAS TO SAY WHY.
//
// It is the only machine verdict that answers nothing about the app - it says
// "I could not run this one" - so unaccompanied it is indistinguishable from the
// case nobody got to, which is the exact ambiguity a recorded result is supposed
// to end. Refusing it here rather than in the CLI is deliberate: the desktop app
// and a direct API call reach this too.
func TestRecordAgentResultRejectsAReasonlessSkip(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)

	_, err := svc.RecordAgentResult(ctx, "w1", "draft", domain.SmokeAgentResult{Verdict: domain.SmokeSkip})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a skip with no reason = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "what you tried") {
		t.Fatalf("the refusal does not say the reason must come from an attempt: %v", err)
	}
	// Whitespace is not a reason either.
	if _, err := svc.RecordAgentResult(ctx, "w1", "draft", domain.SmokeAgentResult{Verdict: domain.SmokeSkip, Note: "   "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a blank reason = %v, want ErrInvalid", err)
	}

	got, err := svc.RecordAgentResult(ctx, "w1", "draft", domain.SmokeAgentResult{
		Verdict: domain.SmokeSkip,
		Note:    "tried a 1.2s ao sim drag on the row; the context menu never opened, so nothing was exercised",
	})
	if err != nil {
		t.Fatalf("a skip WITH its reason: %v", err)
	}
	if got.AgentVerdict != domain.SmokeSkip || !strings.Contains(got.AgentNote, "never opened") {
		t.Fatalf("the declared skip did not land: %+v", got)
	}
	// Declared undriveable is a RECORDED run, which is what takes the case out of
	// the handback gap - the whole point of making it sayable.
	if !got.MachineDrove() {
		t.Fatal("a declared skip did not count as a recorded run")
	}
}

// pass and fail carry no such requirement, and neither does the evidence-only
// record: they answer the case's question, and #268 already governs what they
// have to cite.
func TestRecordAgentResultDoesNotDemandANoteFromEveryVerdict(t *testing.T) {
	ctx := context.Background()
	svc, _ := seedPlayedCase(ctx, t)
	if _, err := svc.RecordAgentResult(ctx, "w1", "draft", domain.SmokeAgentResult{Verdict: domain.SmokePass}); err != nil {
		t.Fatalf("a noteless pass was refused: %v", err)
	}
}
