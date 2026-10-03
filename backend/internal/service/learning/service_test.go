package learning_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func setup(t *testing.T, learnOn bool) (*sqlite.Store, *learning.Service, domain.SessionRecord) {
	t.Helper()
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/repo/p", RegisteredAt: time.Now().UTC(), Config: domain.ProjectConfig{LearnFromSessions: learnOn}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{WorkspacePath: "/wt/p"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := func(p string) bool {
		return strings.HasPrefix(p, "/claude/projects/") && strings.HasSuffix(p, ".jsonl")
	}
	return st, learning.New(st, valid), rec
}

func TestRecordTranscriptRef_ConfinesThePath(t *testing.T) {
	_, svc, rec := setup(t, true)
	err := svc.RecordTranscriptRef(context.Background(), rec.ID, domain.HookTranscriptRef{TranscriptPath: "/etc/passwd"})
	if !errors.Is(err, learning.ErrInvalidTranscriptPath) {
		t.Fatalf("err = %v, want ErrInvalidTranscriptPath", err)
	}
}

func TestRecordTranscriptRef_UnknownSession(t *testing.T) {
	_, svc, _ := setup(t, true)
	err := svc.RecordTranscriptRef(context.Background(), "nope", domain.HookTranscriptRef{TranscriptPath: "/claude/projects/-wt/a.jsonl"})
	if !errors.Is(err, ports.ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}

// The path is always recorded (it is not content); the prompt fingerprint only
// for a project that learns from sessions.
func TestRecordTranscriptRef_FingerprintsOnlyWhenLearning(t *testing.T) {
	for _, on := range []bool{true, false} {
		st, svc, rec := setup(t, on)
		ctx := context.Background()
		if err := svc.RecordTranscriptRef(ctx, rec.ID, domain.HookTranscriptRef{
			TranscriptPath: "/claude/projects/-wt/a.jsonl", ClaudeSessionID: "c1", PromptSHA256: "ab", PromptBytes: 2,
		}); err != nil {
			t.Fatal(err)
		}
		refs, _ := st.ListTranscriptRefs(ctx, "p")
		if len(refs) != 1 {
			t.Errorf("learning=%v: refs = %d, want 1", on, len(refs))
		}
		counts, _ := st.LearnCounts(ctx, "p", time.Now().Add(time.Hour))
		want := 0
		if on {
			want = 1
		}
		if counts.Prompts != want {
			t.Errorf("learning=%v: prompts = %d, want %d", on, counts.Prompts, want)
		}
	}
}

func TestForget_RefusesWhileLearningIsOn(t *testing.T) {
	_, svc, _ := setup(t, true)
	if _, err := svc.Forget(context.Background(), "p"); !errors.Is(err, learning.ErrStillLearning) {
		t.Fatalf("err = %v, want ErrStillLearning", err)
	}
	_, off, _ := setup(t, false)
	if _, err := off.Forget(context.Background(), "p"); err != nil {
		t.Fatalf("forget with learning off: %v", err)
	}
	if _, err := off.Forget(context.Background(), "missing"); !errors.Is(err, learning.ErrUnknownProject) {
		t.Fatalf("err = %v, want ErrUnknownProject", err)
	}
}

func TestStatus_ListsLearningProjects(t *testing.T) {
	st, svc, rec := setup(t, true)
	ctx := context.Background()
	now := time.Now().UTC()
	cursor := domain.LearnCursor{TranscriptPath: "/claude/projects/-wt/a.jsonl", ProjectID: "p", SessionID: rec.ID, ByteOffset: 10, FileSize: 10, FileMTime: now, HumanTurns: 3, MachineTurns: 2}
	if _, err := st.CommitLearnPass(ctx, cursor, []domain.LearnExcerpt{{
		ProjectID: "p", SessionID: rec.ID, TranscriptPath: cursor.TranscriptPath, TurnUUID: "t1", TurnAt: now,
		SourceClass: domain.LearnSourceTyped, HumanText: "x", CreatedAt: now,
	}}, nil, now); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Status(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("status = %+v err=%v", got, err)
	}
	p := got[0]
	if !p.Enabled || p.Transcripts != 1 || p.Excerpts != 1 || p.HumanTurns != 3 || p.MachineTurns != 2 || p.BySourceClass[domain.LearnSourceTyped] != 1 {
		t.Errorf("status = %+v", p)
	}
}
