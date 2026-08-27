package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestSmokeAgentResultAndUserResultAreDisjointColumns exercises the migration
// and the two write statements against real SQLite: writing one result must not
// move a byte of the other, in either direction.
func TestSmokeAgentResultAndUserResultAreDisjointColumns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "smk")
	rec, err := s.CreateSession(ctx, sampleRecord("smk"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID,
		[]domain.SmokeAuthoredCase{{ID: "a", Seq: 1, Name: "case a", Steps: []string{"s1"}}}, domain.SmokeAuthor{}, now); err != nil {
		t.Fatalf("author: %v", err)
	}

	// A fresh case carries neither result.
	check, _, err := s.GetSmokeCheck(ctx, "a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if check.Verdict != domain.SmokePending || check.AgentVerdict != "" || check.AgentRanAt != nil {
		t.Fatalf("fresh case = %q/%q/%v, want pending + no machine result", check.Verdict, check.AgentVerdict, check.AgentRanAt)
	}

	if ok, err := s.SetSmokeAgentResult(ctx, "a", domain.SmokeAgentResult{
		Verdict: domain.SmokePass, Note: "ran clean", SHA: "abc123",
	}, now, now); err != nil || !ok {
		t.Fatalf("set agent result: ok=%v err=%v", ok, err)
	}
	check, _, _ = s.GetSmokeCheck(ctx, "a")
	if check.Verdict != domain.SmokePending || check.Note != "" || check.DecidedAt != nil {
		t.Errorf("the machine's write reached the user's columns: %q/%q/%v", check.Verdict, check.Note, check.DecidedAt)
	}
	if check.AgentVerdict != domain.SmokePass || check.AgentNote != "ran clean" || check.AgentSHA != "abc123" || check.AgentRanAt == nil {
		t.Errorf("agent result not stored: %+v", check)
	}

	if ok, err := s.SetSmokeVerdict(ctx, "a", domain.SmokeFail, "felt laggy", now, now); err != nil || !ok {
		t.Fatalf("set verdict: ok=%v err=%v", ok, err)
	}
	check, _, _ = s.GetSmokeCheck(ctx, "a")
	if check.AgentVerdict != domain.SmokePass || check.AgentNote != "ran clean" {
		t.Errorf("the user's write reached the machine's columns: %q/%q", check.AgentVerdict, check.AgentNote)
	}
	if check.Verdict != domain.SmokeFail || check.Note != "felt laggy" {
		t.Errorf("user result not stored: %q/%q", check.Verdict, check.Note)
	}
}

// TestResetSmokeCheckKeepsAgentEvidence: Reset clears the user's attachments and
// only the user's. Before provenance existed this swept the case's whole
// evidence set, which would now take the machine's artifacts with it.
func TestResetSmokeCheckKeepsAgentEvidence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "smk")
	rec, err := s.CreateSession(ctx, sampleRecord("smk"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID,
		[]domain.SmokeAuthoredCase{{ID: "a", Seq: 1, Name: "case a"}}, domain.SmokeAuthor{}, now); err != nil {
		t.Fatalf("author: %v", err)
	}
	for _, ev := range []domain.SmokeEvidence{
		{ID: "ev_user", CheckID: "a", SessionID: rec.ID, Kind: "image", Filename: "human.png", Mime: "image/png", CreatedAt: now, Source: domain.SmokeEvidenceUser},
		{ID: "ev_agent", CheckID: "a", SessionID: rec.ID, Kind: "image", Filename: "machine.png", Mime: "image/png", CreatedAt: now, Source: domain.SmokeEvidenceAgent},
	} {
		if err := s.InsertSmokeEvidence(ctx, ev); err != nil {
			t.Fatalf("insert %s: %v", ev.ID, err)
		}
	}

	check, _, _ := s.GetSmokeCheck(ctx, "a")
	if len(check.Evidence) != 1 || len(check.AgentEvidence) != 1 {
		t.Fatalf("split lists = %d user / %d agent, want 1 each", len(check.Evidence), len(check.AgentEvidence))
	}

	if ok, err := s.ResetSmokeCheck(ctx, "a", now); err != nil || !ok {
		t.Fatalf("reset: ok=%v err=%v", ok, err)
	}
	check, _, _ = s.GetSmokeCheck(ctx, "a")
	if len(check.Evidence) != 0 {
		t.Errorf("reset left the user's evidence: %+v", check.Evidence)
	}
	if len(check.AgentEvidence) != 1 || check.AgentEvidence[0].ID != "ev_agent" {
		t.Errorf("reset destroyed the machine's evidence: %+v", check.AgentEvidence)
	}
}

// TestEvidenceDefaultsToTheUser: every row that predates provenance was attached
// by a person through the Tests tab, and an unset source must read that way
// rather than as an unlabelled third thing.
func TestEvidenceDefaultsToTheUser(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "smk")
	rec, err := s.CreateSession(ctx, sampleRecord("smk"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID,
		[]domain.SmokeAuthoredCase{{ID: "a", Seq: 1, Name: "case a"}}, domain.SmokeAuthor{}, now); err != nil {
		t.Fatalf("author: %v", err)
	}
	if err := s.InsertSmokeEvidence(ctx, domain.SmokeEvidence{
		ID: "ev_old", CheckID: "a", SessionID: rec.ID, Kind: "image", Mime: "image/png", CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	check, _, _ := s.GetSmokeCheck(ctx, "a")
	if len(check.Evidence) != 1 || check.Evidence[0].Source != domain.SmokeEvidenceUser {
		t.Fatalf("source-less evidence = %+v, want it in the user's list", check)
	}
	if len(check.AgentEvidence) != 0 {
		t.Errorf("source-less evidence landed in the machine's list: %+v", check.AgentEvidence)
	}
}

// TestRetiredSmokeCheckSurvivesAReAuthor is retire's whole contract at the SQL
// level: a retired row is neither dropped for being absent from the payload nor
// rewritten, so a checklist that has legitimately shrunk stays shrunk and the
// results it kept stay readable.
func TestRetiredSmokeCheckSurvivesAReAuthor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "smk")
	rec, err := s.CreateSession(ctx, sampleRecord("smk"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID, []domain.SmokeAuthoredCase{
		{ID: "a", Seq: 1, Name: "case a"},
		{ID: "b", Seq: 2, Name: "case b"},
	}, domain.SmokeAuthor{}, now); err != nil {
		t.Fatalf("author: %v", err)
	}
	if ok, err := s.SetSmokeVerdict(ctx, "a", domain.SmokeFail, "flashed Unknown", now, now); err != nil || !ok {
		t.Fatalf("set verdict: ok=%v err=%v", ok, err)
	}
	if err := s.InsertSmokeEvidence(ctx, domain.SmokeEvidence{
		ID: "ev1", CheckID: "a", SessionID: rec.ID, Kind: "image", Mime: "image/png", CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}
	if ok, err := s.RetireSmokeCheck(ctx, "a", "now covered by TestA", now, now); err != nil || !ok {
		t.Fatalf("retire: ok=%v err=%v", ok, err)
	}

	// A second retire must not overwrite the first reason and date.
	if ok, err := s.RetireSmokeCheck(ctx, "a", "a different reason", now.Add(time.Hour), now.Add(time.Hour)); err != nil || ok {
		t.Fatalf("second retire: ok=%v err=%v, want ok=false", ok, err)
	}

	later := now.Add(time.Minute)
	checks, removed, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID,
		[]domain.SmokeAuthoredCase{{ID: "b", Seq: 1, Name: "case b"}}, domain.SmokeAuthor{}, later)
	if err != nil {
		t.Fatalf("re-author: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want nothing (the retired case is not the payload's to drop)", removed)
	}
	if len(checks) != 2 {
		t.Fatalf("checks = %d, want the active case plus the retired one", len(checks))
	}
	if checks[0].ID != "b" || checks[1].ID != "a" {
		t.Errorf("order = %s,%s, want the retired case last", checks[0].ID, checks[1].ID)
	}
	retired := checks[1]
	if retired.RetiredAt == nil || retired.RetiredReason != "now covered by TestA" {
		t.Errorf("retired marks = %v/%q", retired.RetiredAt, retired.RetiredReason)
	}
	if retired.Verdict != domain.SmokeFail || retired.Note != "flashed Unknown" || len(retired.Evidence) != 1 {
		t.Errorf("the retired case lost its trace: %+v", retired)
	}
	if retired.Name != "case a" || retired.Seq != 1 {
		t.Errorf("the re-author rewrote the retired case: %q seq=%d", retired.Name, retired.Seq)
	}
}

// TestSetSmokeAgentResultSkipsARetiredCase: the frozen rule is enforced in the
// statement itself, not only by the service's read-then-write.
func TestSetSmokeAgentResultSkipsARetiredCase(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "smk")
	rec, err := s.CreateSession(ctx, sampleRecord("smk"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID,
		[]domain.SmokeAuthoredCase{{ID: "a", Seq: 1, Name: "case a"}}, domain.SmokeAuthor{}, now); err != nil {
		t.Fatalf("author: %v", err)
	}
	if ok, err := s.RetireSmokeCheck(ctx, "a", "covered", now, now); err != nil || !ok {
		t.Fatalf("retire: ok=%v err=%v", ok, err)
	}
	ok, err := s.SetSmokeAgentResult(ctx, "a", domain.SmokeAgentResult{Verdict: domain.SmokePass}, now, now)
	if err != nil {
		t.Fatalf("set agent result: %v", err)
	}
	if ok {
		t.Fatal("a retired case accepted a machine result")
	}
	check, _, _ := s.GetSmokeCheck(ctx, "a")
	if check.AgentVerdict != "" {
		t.Errorf("agent verdict = %q, want empty", check.AgentVerdict)
	}
}
