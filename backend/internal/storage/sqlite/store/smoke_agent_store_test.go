package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// recordRun opens and closes a machine run in one call - what `ao smoke record`
// does when it captured nothing - so a test that only cares about the result can
// say so in one line.
func recordRun(t *testing.T, s *sqlite.Store, checkID string, sessionID domain.SessionID, res domain.SmokeAgentResult, at time.Time) (domain.SmokeRun, bool) {
	t.Helper()
	ctx := context.Background()
	run, opened, err := s.OpenSmokeRun(ctx, checkID, sessionID, at)
	if err != nil {
		t.Fatalf("open run: %v", err)
	}
	if !opened {
		return domain.SmokeRun{}, false
	}
	closed, err := s.CloseSmokeRun(ctx, run.ID, res, at, at)
	if err != nil {
		t.Fatalf("close run: %v", err)
	}
	return run, closed
}

// TestSmokeAgentResultAndUserResultAreDisjoint exercises the migration and the
// two write paths against real SQLite: writing one result must not move a byte
// of the other, in either direction. The machine's now lives in its own TABLE,
// so "disjoint" is structural - the statement that writes a run cannot name a
// column on the case row at all.
func TestSmokeAgentResultAndUserResultAreDisjoint(t *testing.T) {
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

	if _, ok := recordRun(t, s, "a", rec.ID, domain.SmokeAgentResult{
		Verdict: domain.SmokePass, Note: "ran clean", SHA: "abc123",
	}, now); !ok {
		t.Fatal("recording a machine run on an active case failed")
	}
	check, _, _ = s.GetSmokeCheck(ctx, "a")
	if check.Verdict != domain.SmokePending || check.Note != "" || check.DecidedAt != nil {
		t.Errorf("the machine's write reached the user's columns: %q/%q/%v", check.Verdict, check.Note, check.DecidedAt)
	}
	if check.AgentVerdict != domain.SmokePass || check.AgentNote != "ran clean" || check.AgentSHA != "abc123" || check.AgentRanAt == nil {
		t.Errorf("agent result not stored: %+v", check)
	}

	if ok, err := s.SetSmokeVerdict(ctx, "a", domain.SmokeFail, "felt laggy", "", now, now); err != nil || !ok {
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
	if ok, err := s.SetSmokeVerdict(ctx, "a", domain.SmokeFail, "flashed Unknown", "", now, now); err != nil || !ok {
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

// TestOpeningARunSkipsARetiredCase: the frozen rule is enforced in the statement
// itself, not only by the service's read-then-write.
func TestOpeningARunSkipsARetiredCase(t *testing.T) {
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
	if _, ok := recordRun(t, s, "a", rec.ID, domain.SmokeAgentResult{Verdict: domain.SmokePass}, now); ok {
		t.Fatal("a retired case accepted a machine run")
	}
	check, _, _ := s.GetSmokeCheck(ctx, "a")
	if len(check.Runs) != 0 || check.AgentVerdict != "" {
		t.Errorf("runs = %d, agent verdict = %q, want none/empty", len(check.Runs), check.AgentVerdict)
	}
}

// TestMachineRunsAccumulateAndEvidenceStaysWithItsRun is the whole point of the
// run table, against real SQLite: a case re-run on a newer commit keeps BOTH
// rounds, and each round's captures stay under the verdict they belong to. The
// shape this replaces destroyed the earlier verdict and pooled its screenshots
// under the newer one, which is how a person came to read a stale image as
// current evidence for a result that contradicted it.
func TestMachineRunsAccumulateAndEvidenceStaysWithItsRun(t *testing.T) {
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

	// Round 1 at the old commit: a capture opens the run, then it fails.
	first, opened, err := s.OpenSmokeRun(ctx, "a", rec.ID, now)
	if err != nil || !opened {
		t.Fatalf("open run 1: opened=%v err=%v", opened, err)
	}
	seedRunEvidence(t, s, "ev1", "a", rec.ID, first.ID, now)
	// A second capture in the same round joins the SAME run rather than starting
	// another: one `ao smoke record` is one run, whatever it uploads.
	again, _, err := s.OpenSmokeRun(ctx, "a", rec.ID, now)
	if err != nil {
		t.Fatalf("re-open run 1: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("a second capture opened run %s, want the round already open (%s)", again.ID, first.ID)
	}
	if ok, err := s.CloseSmokeRun(ctx, first.ID, domain.SmokeAgentResult{
		Verdict: domain.SmokeFail, Note: "clipped at 320px", SHA: "d44ad432c",
	}, now, now); err != nil || !ok {
		t.Fatalf("close run 1: ok=%v err=%v", ok, err)
	}

	// Round 2 at the new commit: the opposite result.
	later := now.Add(time.Hour)
	second, opened, err := s.OpenSmokeRun(ctx, "a", rec.ID, later)
	if err != nil || !opened {
		t.Fatalf("open run 2: opened=%v err=%v", opened, err)
	}
	if second.ID == first.ID || second.Seq != 2 {
		t.Fatalf("run 2 = %s seq %d, want a new row at seq 2 - a re-run must not overwrite the round before it", second.ID, second.Seq)
	}
	seedRunEvidence(t, s, "ev2", "a", rec.ID, second.ID, later)
	if ok, err := s.CloseSmokeRun(ctx, second.ID, domain.SmokeAgentResult{
		Verdict: domain.SmokePass, Note: "renders clean", SHA: "9f10c22a1",
	}, later, later); err != nil || !ok {
		t.Fatalf("close run 2: ok=%v err=%v", ok, err)
	}

	check, _, err := s.GetSmokeCheck(ctx, "a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(check.Runs) != 2 {
		t.Fatalf("runs = %d, want both rounds", len(check.Runs))
	}
	if check.Runs[0].Verdict != domain.SmokeFail || check.Runs[0].SHA != "d44ad432c" {
		t.Errorf("run 1 = %q at %q, want the earlier failure preserved", check.Runs[0].Verdict, check.Runs[0].SHA)
	}
	if check.Runs[1].Verdict != domain.SmokePass || check.Runs[1].SHA != "9f10c22a1" {
		t.Errorf("run 2 = %q at %q", check.Runs[1].Verdict, check.Runs[1].SHA)
	}
	// The four Agent* fields are the LATEST run, derived on read.
	if check.AgentVerdict != domain.SmokePass || check.AgentNote != "renders clean" || check.AgentSHA != "9f10c22a1" || check.AgentRanAt == nil {
		t.Errorf("derived machine result = %q/%q/%q, want the latest run's", check.AgentVerdict, check.AgentNote, check.AgentSHA)
	}
	if got := check.RunEvidence(first.ID); len(got) != 1 || got[0].ID != "ev1" {
		t.Errorf("run 1 evidence = %+v, want only ev1 - the capture from the round that failed", got)
	}
	if got := check.RunEvidence(second.ID); len(got) != 1 || got[0].ID != "ev2" {
		t.Errorf("run 2 evidence = %+v, want only ev2", got)
	}
	if got := check.UnknownRunEvidence(); len(got) != 0 {
		t.Errorf("unknown-run evidence = %+v, want none: every capture here was taken inside a run", got)
	}
}

// TestAnOpenRunIsNotAResult: a round the machine opened, captured into and never
// concluded must not present itself as the case's machine result. It is what a
// crashed or abandoned run leaves behind, and reading it as a verdict would put
// an empty conclusion in front of a person as if it were one.
func TestAnOpenRunIsNotAResult(t *testing.T) {
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
	if _, ok := recordRun(t, s, "a", rec.ID, domain.SmokeAgentResult{Verdict: domain.SmokePass, SHA: "abc123"}, now); !ok {
		t.Fatal("record a run: not opened")
	}
	// A later round that captured and never concluded.
	open, opened, err := s.OpenSmokeRun(ctx, "a", rec.ID, now.Add(time.Hour))
	if err != nil || !opened {
		t.Fatalf("open run 2: opened=%v err=%v", opened, err)
	}
	seedRunEvidence(t, s, "ev1", "a", rec.ID, open.ID, now.Add(time.Hour))

	check, _, _ := s.GetSmokeCheck(ctx, "a")
	if len(check.Runs) != 2 {
		t.Fatalf("runs = %d, want the recorded one plus the open one", len(check.Runs))
	}
	if check.Runs[1].Recorded() {
		t.Error("the open run reads as recorded")
	}
	if check.AgentVerdict != domain.SmokePass || check.AgentSHA != "abc123" {
		t.Errorf("derived result = %q at %q, want the last RECORDED run - an unfinished round is not a result", check.AgentVerdict, check.AgentSHA)
	}
}

// seedRunEvidence records one machine capture inside a run.
func seedRunEvidence(t *testing.T, s *sqlite.Store, id, checkID string, sessionID domain.SessionID, runID string, at time.Time) {
	t.Helper()
	if err := s.InsertSmokeEvidence(context.Background(), domain.SmokeEvidence{
		ID: id, CheckID: checkID, SessionID: sessionID, Kind: "image",
		Filename: id + ".png", Mime: "image/png", SizeBytes: 10,
		CreatedAt: at, Source: domain.SmokeEvidenceAgent, RunID: runID,
	}); err != nil {
		t.Fatalf("seed evidence %s: %v", id, err)
	}
}
