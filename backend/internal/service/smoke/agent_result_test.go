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
	if _, err := svc.SetVerdict(ctx, "w1", "played", domain.SmokePass, "looked right to me"); err != nil {
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
