package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReplaceSmokeChecksPreservesResultsByID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "smk")
	rec, err := s.CreateSession(ctx, sampleRecord("smk"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	// Author two cases.
	cases := []domain.SmokeAuthoredCase{
		{ID: "a", Seq: 1, Name: "case a", Why: "why a", Steps: []string{"s1", "s2"}, Expected: "exp a", PRNum: 36, FileRef: "a.go:1"},
		{ID: "b", Seq: 2, Name: "case b", Steps: []string{"s"}, Expected: "exp b"},
	}
	if _, _, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID, cases, now); err != nil {
		t.Fatalf("author: %v", err)
	}

	// The user plays case a: pass + note + one evidence blob.
	if ok, err := s.SetSmokeVerdict(ctx, "a", domain.SmokePass, "looks good", now, now); err != nil || !ok {
		t.Fatalf("set verdict: ok=%v err=%v", ok, err)
	}
	if err := s.InsertSmokeEvidence(ctx, domain.SmokeEvidence{
		ID: "ev1", CheckID: "a", SessionID: rec.ID, Kind: "image", Filename: "shot.png", Mime: "image/png", SizeBytes: 123, CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert evidence: %v", err)
	}

	// Re-author: keep a (edited name), drop b, add c.
	later := now.Add(time.Minute)
	reauthored := []domain.SmokeAuthoredCase{
		{ID: "a", Seq: 1, Name: "case a (edited)", Why: "why a2", Steps: []string{"s1"}, Expected: "exp a2", PRNum: 40, FileRef: "a.go:9"},
		{ID: "c", Seq: 2, Name: "case c", Steps: []string{"z"}, Expected: "exp c"},
	}
	checks, removed, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID, reauthored, later)
	if err != nil {
		t.Fatalf("re-author: %v", err)
	}
	if len(removed) != 1 || removed[0] != "b" {
		t.Fatalf("removed = %v, want [b]", removed)
	}
	if len(checks) != 2 {
		t.Fatalf("checks len = %d, want 2", len(checks))
	}

	byID := map[string]domain.SmokeCheck{}
	for _, c := range checks {
		byID[c.ID] = c
	}
	a, ok := byID["a"]
	if !ok {
		t.Fatal("case a missing after re-author")
	}
	// Authored fields rewritten…
	if a.Name != "case a (edited)" || a.Expected != "exp a2" || a.PRNum != 40 || a.FileRef != "a.go:9" {
		t.Fatalf("case a authored fields not rewritten: %+v", a)
	}
	if len(a.Steps) != 1 || a.Steps[0] != "s1" {
		t.Fatalf("case a steps = %v, want [s1]", a.Steps)
	}
	// …but the user's play results preserved.
	if a.Verdict != domain.SmokePass || a.Note != "looks good" {
		t.Fatalf("case a lost its verdict/note: verdict=%q note=%q", a.Verdict, a.Note)
	}
	if a.DecidedAt == nil {
		t.Fatal("case a lost decided_at")
	}
	if len(a.Evidence) != 1 || a.Evidence[0].ID != "ev1" {
		t.Fatalf("case a lost evidence: %+v", a.Evidence)
	}

	c, ok := byID["c"]
	if !ok {
		t.Fatal("new case c missing")
	}
	if c.Verdict != domain.SmokePending || len(c.Evidence) != 0 {
		t.Fatalf("new case c should start pending & empty: %+v", c)
	}

	if _, ok := byID["b"]; ok {
		t.Fatal("case b should have been removed")
	}
}

func TestResetSmokeCheckClearsVerdictAndEvidence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "smk")
	rec, err := s.CreateSession(ctx, sampleRecord("smk"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID, []domain.SmokeAuthoredCase{{ID: "a", Seq: 1, Name: "case a"}}, now); err != nil {
		t.Fatalf("author: %v", err)
	}
	if _, err := s.SetSmokeVerdict(ctx, "a", domain.SmokeFail, "broken", now, now); err != nil {
		t.Fatalf("verdict: %v", err)
	}
	if err := s.InsertSmokeEvidence(ctx, domain.SmokeEvidence{ID: "ev1", CheckID: "a", SessionID: rec.ID, Kind: "image", CreatedAt: now}); err != nil {
		t.Fatalf("evidence: %v", err)
	}

	if ok, err := s.ResetSmokeCheck(ctx, "a", now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("reset: ok=%v err=%v", ok, err)
	}
	got, ok, err := s.GetSmokeCheck(ctx, "a")
	if err != nil || !ok {
		t.Fatalf("get after reset: ok=%v err=%v", ok, err)
	}
	if got.Verdict != domain.SmokePending || got.Note != "" || got.DecidedAt != nil {
		t.Fatalf("reset did not clear result: %+v", got)
	}
	if len(got.Evidence) != 0 {
		t.Fatalf("reset did not delete evidence: %+v", got.Evidence)
	}
}

func TestMarkSmokeReportedStampsSessionRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "smk")
	rec, err := s.CreateSession(ctx, sampleRecord("smk"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.ReplaceSmokeChecks(ctx, rec.ID, rec.ProjectID, []domain.SmokeAuthoredCase{
		{ID: "a", Seq: 1, Name: "a"}, {ID: "b", Seq: 2, Name: "b"},
	}, now); err != nil {
		t.Fatalf("author: %v", err)
	}
	n, err := s.MarkSmokeReported(ctx, rec.ID, now, now)
	if err != nil {
		t.Fatalf("mark reported: %v", err)
	}
	if n != 2 {
		t.Fatalf("marked %d rows, want 2", n)
	}
	checks, err := s.ListSmokeChecksBySession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range checks {
		if c.ReportedAt == nil {
			t.Fatalf("check %s not stamped reported_at", c.ID)
		}
	}
}
