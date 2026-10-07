package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestTestinyEvidenceLog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tny")
	task, err := s.CreateSession(ctx, sampleRecord("tny"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	other, err := s.CreateSession(ctx, sampleRecord("tny"))
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	for _, l := range []domain.TestinyRunLink{
		{SessionID: task.ID, RunID: 191, CreatedAt: t0},
		{SessionID: task.ID, RunID: 192, CreatedAt: t0},
		{SessionID: other.ID, RunID: 191, CreatedAt: t0},
	} {
		if err := s.InsertTestinyRunLink(ctx, l); err != nil {
			t.Fatalf("link %v: %v", l, err)
		}
	}

	uploaded := func(sid domain.SessionID, run domain.TestinyRunID, file string, c int64) domain.TestinyEvidenceEntry {
		return domain.TestinyEvidenceEntry{SessionID: sid, RunID: run, Event: domain.TestinyEvidenceUploaded, File: file, CaseID: c, DriveID: "1" + file, SetBy: "tny-2", CreatedAt: t0}
	}
	linked := func(sid domain.SessionID, run domain.TestinyRunID, file string, c, comment int64, at time.Time) domain.TestinyEvidenceEntry {
		return domain.TestinyEvidenceEntry{SessionID: sid, RunID: run, Event: domain.TestinyEvidenceLinked, File: file, CaseID: c, DriveID: "1" + file, CommentID: comment, SetBy: "tny-2", CreatedAt: at}
	}
	first := linked(task.ID, 191, "TC-1 pass.png", 1, 2601, t0)
	byOther := linked(other.ID, 191, "TC-2 pass.png", 2, 2602, t0.Add(time.Minute))
	if err := s.AppendTestinyEvidence(ctx, []domain.TestinyEvidenceEntry{
		uploaded(task.ID, 191, "README.md", 0),
		uploaded(task.ID, 191, "TC-1 pass.png", 1),
		first,
		linked(task.ID, 192, "TC-9 pass.png", 9, 2700, t0),
		byOther,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := s.TestinyEvidenceLinked(ctx, 191)
	if err != nil {
		t.Fatal(err)
	}
	if want := []domain.TestinyEvidenceEntry{first, byOther}; !reflect.DeepEqual(got, want) {
		t.Fatalf("linked in 191 =\n%+v\nwant every task's links, uploads left out\n%+v", got, want)
	}

	// A batch with one bad row writes none of it.
	bad := linked(task.ID, 191, "TC-3 pass.png", 3, 0, t0)
	if err := s.AppendTestinyEvidence(ctx, []domain.TestinyEvidenceEntry{linked(task.ID, 191, "TC-4 pass.png", 4, 2604, t0), bad}); err == nil {
		t.Fatal("a linked row with no comment id was accepted")
	}
	if got, _ := s.TestinyEvidenceLinked(ctx, 191); len(got) != 2 {
		t.Fatalf("a refused batch left %d linked rows, want 2", len(got))
	}
}
