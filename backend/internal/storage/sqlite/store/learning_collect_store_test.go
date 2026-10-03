package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func seedExcerpts(t *testing.T, s interface {
	CommitLearnPass(context.Context, domain.LearnCursor, []domain.LearnExcerpt, []domain.PromptMatch, time.Time) (int, error)
}, project, session string, turns []time.Time) {
	t.Helper()
	excerpts := make([]domain.LearnExcerpt, 0, len(turns))
	for i, at := range turns {
		e := sampleExcerpt(project, "/p/"+session+".jsonl", session+"-t"+string(rune('a'+i)), at)
		e.SessionID = domain.SessionID(session)
		excerpts = append(excerpts, e)
	}
	cursor := domain.LearnCursor{TranscriptPath: "/p/" + session + ".jsonl", ProjectID: domain.ProjectID(project), SessionID: domain.SessionID(session)}
	if _, err := s.CommitLearnPass(context.Background(), cursor, excerpts, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestListCollectCandidates_SpansPerSession(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	seedExcerpts(t, s, "mer", "mer-1", []time.Time{t0, t0.Add(time.Hour)})
	seedExcerpts(t, s, "mer", "mer-2", []time.Time{t0.Add(2 * time.Hour)})

	got, err := s.ListCollectCandidates(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SessionID != "mer-1" || got[0].Turns != 2 ||
		!got[0].OldestAt.Equal(t0) || !got[0].NewestAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("candidates = %+v", got)
	}
}

func TestCommitLearnJob_StoresDraftsAndMarksTurnsCollected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	seedExcerpts(t, s, "mer", "mer-1", []time.Time{t0, t0.Add(time.Minute)})
	turns, err := s.ListUncollectedExcerpts(ctx, "mer", "mer-1", 10)
	if err != nil || len(turns) != 2 {
		t.Fatalf("turns = %d err=%v", len(turns), err)
	}

	now := t0.Add(time.Hour)
	run := func(drafts []domain.LearnDraft, ids []int64) int {
		t.Helper()
		id, err := s.StartLearnJob(ctx, domain.LearnJob{ProjectID: "mer", SessionID: "mer-1", Model: "m", Turns: len(ids), StartedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		n, err := s.CommitLearnJob(ctx, domain.LearnJob{ID: id, CostUSD: 0.05, FinishedAt: now}, drafts, ids, now)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	draft := func(stmt string, sup int64) domain.LearnDraft {
		return domain.LearnDraft{ProjectID: "mer", SessionID: "mer-1", TaskKey: "solo:mer-1", Kind: domain.LearnDraftRule,
			Statement: stmt, Quote: "q", AnchorExcerptID: turns[0].ID, EvidenceExcerptIDs: []int64{turns[0].ID}, SupersedesID: sup,
			About: domain.LearnAboutAgentPractice}
	}
	if n := run([]domain.LearnDraft{draft("Drive simulators only through scripts.", 0)}, []int64{turns[0].ID, turns[1].ID}); n != 1 {
		t.Fatalf("first job stored %d drafts", n)
	}
	if left, _ := s.ListUncollectedExcerpts(ctx, "mer", "mer-1", 10); len(left) != 0 {
		t.Errorf("%d turns still uncollected", len(left))
	}
	open, _ := s.OpenDrafts(ctx, "mer-1")
	if len(open) != 1 {
		t.Fatalf("open drafts = %d", len(open))
	}
	// The same statement again (case and spacing aside) is not stored twice; a
	// later draft that takes the first one back reverses it.
	if n := run([]domain.LearnDraft{draft("drive simulators  only through scripts.", 0), draft("Taps are fine for one-off checks.", open[0].ID)}, nil); n != 1 {
		t.Fatalf("second job stored %d drafts, want 1", n)
	}
	all, _ := s.ListLearnDrafts(ctx, "mer", 10)
	statuses := map[string]domain.LearnDraftStatus{}
	for _, d := range all {
		statuses[d.Statement] = d.Status
		if d.About != domain.LearnAboutAgentPractice {
			t.Errorf("draft %q about = %q, want the tag it was stored with", d.Statement, d.About)
		}
	}
	if statuses["Drive simulators only through scripts."] != domain.LearnDraftReversed || statuses["Taps are fine for one-off checks."] != domain.LearnDraftOpen {
		t.Errorf("statuses = %v", statuses)
	}
	if spent, _ := s.LearnSpendSince(ctx, t0); spent < 0.099 || spent > 0.101 {
		t.Errorf("spend = %v, want 0.10", spent)
	}
	counts, _ := s.LearnCollectCounts(ctx, "mer")
	if counts.Uncollected != 0 || counts.Drafts[domain.LearnDraftOpen] != 1 || counts.Drafts[domain.LearnDraftReversed] != 1 {
		t.Errorf("counts = %+v", counts)
	}
	if n, _ := s.ForgetLearning(ctx, "mer"); n != 2 {
		t.Errorf("forget deleted %d turns", n)
	}
	if all, _ := s.ListLearnDrafts(ctx, "mer", 10); len(all) != 0 {
		t.Errorf("forget left %d drafts", len(all))
	}
}

func TestLearnJobs_FailAndAbandon(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	failed, _ := s.StartLearnJob(ctx, domain.LearnJob{ProjectID: "mer", SessionID: "mer-1", StartedAt: now})
	if err := s.FailLearnJob(ctx, domain.LearnJob{ID: failed, Error: "claude: not logged in", StderrTail: "auth", FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartLearnJob(ctx, domain.LearnJob{ProjectID: "mer", SessionID: "mer-1", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.AbandonRunningLearnJobs(ctx, now); n != 1 {
		t.Errorf("abandoned %d, want the one still running", n)
	}
	last, ok, _ := s.LastFailedLearnJob(ctx, "mer")
	if !ok || last.Error != "claude: not logged in" {
		t.Errorf("last failed = %+v ok=%v", last, ok)
	}
	recent, _ := s.RecentLearnJobs(ctx, "mer-1", 5)
	if len(recent) != 2 || recent[0].State != domain.LearnJobAbandoned || recent[1].State != domain.LearnJobFailed {
		t.Errorf("recent = %+v", recent)
	}
}
