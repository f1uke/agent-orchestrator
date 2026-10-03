package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func sampleExcerpt(project, path, turn string, at time.Time) domain.LearnExcerpt {
	return domain.LearnExcerpt{
		ProjectID:      domain.ProjectID(project),
		SessionID:      "mer-1",
		TranscriptPath: path,
		TurnUUID:       turn,
		TurnAt:         at,
		SourceClass:    domain.LearnSourceTyped,
		Before:         domain.LearnWindow{AgentText: "should I?", Actions: []string{"Bash(Check status)"}},
		HumanText:      "yes, " + turn,
		After:          domain.LearnWindow{AgentText: "ok"},
		Redactions:     map[string]int{"email": 1},
		CreatedAt:      at,
	}
}

// A pass lands its excerpts and its cursor together, and replaying the same
// pass - what a crash between two passes amounts to - stores nothing twice.
func TestCommitLearnPass_IsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	cursor := domain.LearnCursor{
		TranscriptPath: "/p/a.jsonl", ProjectID: "mer", SessionID: "mer-1",
		Attribution: domain.LearnAttributionPinned, ByteOffset: 120, FileSize: 120, FileMTime: now,
		HumanTurns: 2, MachineTurns: 1,
	}
	excerpts := []domain.LearnExcerpt{sampleExcerpt("mer", "/p/a.jsonl", "t1", now), sampleExcerpt("mer", "/p/a.jsonl", "t2", now.Add(time.Second))}

	n, err := s.CommitLearnPass(ctx, cursor, excerpts, nil, now)
	if err != nil || n != 2 {
		t.Fatalf("first commit: n=%d err=%v", n, err)
	}
	n, err = s.CommitLearnPass(ctx, cursor, excerpts, nil, now)
	if err != nil || n != 0 {
		t.Fatalf("replayed commit stored %d new turns (err=%v), want 0", n, err)
	}

	got, ok, err := s.GetLearnCursor(ctx, "/p/a.jsonl")
	if err != nil || !ok || got.ByteOffset != 120 || got.HumanTurns != 2 || got.Attribution != domain.LearnAttributionPinned {
		t.Fatalf("cursor = %+v ok=%v err=%v", got, ok, err)
	}
	rows, err := s.ListLearnExcerpts(ctx, "mer", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("excerpts = %d err=%v", len(rows), err)
	}
	if rows[0].TurnUUID != "t2" || rows[0].Before.Actions[0] != "Bash(Check status)" || rows[0].Redactions["email"] != 1 {
		t.Errorf("newest excerpt = %+v", rows[0])
	}
}

func TestSetLearnCursorError_KeepsThePosition(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	cursor := domain.LearnCursor{TranscriptPath: "/p/a.jsonl", ProjectID: "mer", SessionID: "mer-1", ByteOffset: 50, FileSize: 50}
	if _, err := s.CommitLearnPass(ctx, cursor, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLearnCursorError(ctx, cursor, "read: boom", now); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetLearnCursor(ctx, "/p/a.jsonl")
	if got.ByteOffset != 50 || got.LastError != "read: boom" {
		t.Fatalf("cursor = %+v", got)
	}
	// The next good pass clears the error.
	if _, err := s.CommitLearnPass(ctx, cursor, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.GetLearnCursor(ctx, "/p/a.jsonl"); got.LastError != "" {
		t.Errorf("error survived a good pass: %q", got.LastError)
	}
}

// The canary: every prompt the hook saw should be found in a transcript. One
// typed turn accounts for one prompt, so the same text typed twice is two.
func TestPromptFingerprints_AreMatchedOneTurnAtATime(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for i := 0; i < 2; i++ {
		if err := s.RecordPromptFingerprint(ctx, domain.PromptFingerprint{SessionID: rec.ID, ProjectID: "mer", SHA256: "aa", Bytes: 2, SubmittedAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Now().UTC()
	if c, _ := s.LearnCounts(ctx, "mer", cutoff); c.Prompts != 2 || c.UnmatchedPrompts != 2 {
		t.Fatalf("before capture: %+v", c)
	}
	cursor := domain.LearnCursor{TranscriptPath: "/p/a.jsonl", ProjectID: "mer", SessionID: rec.ID}
	if _, err := s.CommitLearnPass(ctx, cursor, nil, []domain.PromptMatch{{SessionID: rec.ID, SHA256: "aa"}}, cutoff); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.LearnCounts(ctx, "mer", cutoff); c.UnmatchedPrompts != 1 {
		t.Fatalf("one typed turn must account for one prompt: %+v", c)
	}
}

func TestDeliveredFingerprints_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.RecordDeliveredFingerprint(ctx, domain.DeliveredFingerprint{SessionID: rec.ID, ProjectID: "mer", SHA256: "bb", Bytes: 4, Trigger: "send", Author: domain.DeliveryAuthorHuman, DeliveredAt: now}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListDeliveredFingerprints(ctx, rec.ID)
	if err != nil || len(got) != 1 || got[0].Author != domain.DeliveryAuthorHuman || got[0].Trigger != "send" {
		t.Fatalf("delivered = %+v err=%v", got, err)
	}
}

func TestRecordTranscriptRef_KeepsAKnownNativeID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	if err := s.RecordTranscriptRef(ctx, domain.TranscriptRef{SessionID: rec.ID, Path: "/p/a.jsonl", ClaudeSessionID: "c-1", FirstSeenAt: t0, LastSeenAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTranscriptRef(ctx, domain.TranscriptRef{SessionID: rec.ID, Path: "/p/a.jsonl", FirstSeenAt: t0.Add(time.Minute), LastSeenAt: t0.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	refs, err := s.ListTranscriptRefs(ctx, "mer")
	if err != nil || len(refs) != 1 {
		t.Fatalf("refs = %+v err=%v", refs, err)
	}
	if refs[0].ClaudeSessionID != "c-1" || !refs[0].FirstSeenAt.Equal(t0) || !refs[0].LastSeenAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("ref = %+v", refs[0])
	}
}

func TestForgetLearning_DeletesOnlyThatProject(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	seedProject(t, s, "keep")
	now := time.Now().UTC().Truncate(time.Second)
	for _, p := range []string{"mer", "keep"} {
		cursor := domain.LearnCursor{TranscriptPath: "/p/" + p + ".jsonl", ProjectID: domain.ProjectID(p), SessionID: "x"}
		if _, err := s.CommitLearnPass(ctx, cursor, []domain.LearnExcerpt{sampleExcerpt(p, "/p/"+p+".jsonl", "t1", now)}, nil, now); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.ForgetLearning(ctx, "mer")
	if err != nil || n != 1 {
		t.Fatalf("forget: n=%d err=%v", n, err)
	}
	if rows, _ := s.ListLearnExcerpts(ctx, "mer", 10); len(rows) != 0 {
		t.Errorf("mer still has %d excerpts", len(rows))
	}
	if _, ok, _ := s.GetLearnCursor(ctx, "/p/mer.jsonl"); ok {
		t.Error("mer's cursor survived; capture would resume past turns it no longer has")
	}
	if rows, _ := s.ListLearnExcerpts(ctx, "keep", 10); len(rows) != 1 {
		t.Errorf("another project's excerpts were touched: %d", len(rows))
	}
}
