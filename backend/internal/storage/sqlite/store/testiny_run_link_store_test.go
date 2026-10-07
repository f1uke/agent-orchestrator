package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestTestinyRunLinks(t *testing.T) {
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
	t0 := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

	mob := domain.TestinyProject{ID: 1, Key: "MOB", Name: "MOBILITY"}
	star := domain.TestinyProject{ID: 3, Key: "STAR", Name: "STAR"}
	for _, l := range []domain.TestinyRunLink{
		{SessionID: task.ID, RunID: 632, Project: mob, LinkedBy: "tny-2", CreatedAt: t0},
		{SessionID: task.ID, RunID: 565, Project: mob, LinkedBy: "", CreatedAt: t0.Add(time.Minute)},
		{SessionID: task.ID, RunID: 700, Project: star, LinkedBy: "", CreatedAt: t0.Add(time.Minute)},
		{SessionID: other.ID, RunID: 632, Project: mob, LinkedBy: "", CreatedAt: t0},
	} {
		if err := s.InsertTestinyRunLink(ctx, l); err != nil {
			t.Fatalf("insert %v: %v", l, err)
		}
	}
	// A re-link is a no-op: the first link's author and time stay, so the
	// order a person saw does not change.
	if err := s.InsertTestinyRunLink(ctx, domain.TestinyRunLink{SessionID: task.ID, RunID: 632, LinkedBy: "", CreatedAt: t0.Add(time.Hour)}); err != nil {
		t.Fatalf("re-link: %v", err)
	}

	got, err := s.ListTestinyRunLinks(ctx, task.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []domain.TestinyRunLink{
		{SessionID: task.ID, RunID: 632, Project: mob, LinkedBy: "tny-2", CreatedAt: t0},
		{SessionID: task.ID, RunID: 565, Project: mob, LinkedBy: "", CreatedAt: t0.Add(time.Minute)},
		{SessionID: task.ID, RunID: 700, Project: star, LinkedBy: "", CreatedAt: t0.Add(time.Minute)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("links =\n%+v\nwant\n%+v", got, want)
	}

	if err := s.DeleteTestinyRunLink(ctx, task.ID, 565); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteTestinyRunLink(ctx, task.ID, 565); err != nil {
		t.Fatalf("delete a missing link: %v", err)
	}
	got, _ = s.ListTestinyRunLinks(ctx, task.ID)
	if len(got) != 2 || got[0].RunID != 632 || got[1].RunID != 700 {
		t.Fatalf("after delete = %+v", got)
	}
	left, _ := s.ListTestinyRunLinks(ctx, other.ID)
	if len(left) != 1 || left[0].RunID != 632 {
		t.Fatalf("the other task's link moved: %+v", left)
	}

	if err := s.PurgeSession(ctx, task.ID); err != nil {
		t.Fatalf("purge task: %v", err)
	}
	if got, _ := s.ListTestinyRunLinks(ctx, task.ID); len(got) != 0 {
		t.Fatalf("links outlived their task: %+v", got)
	}
}

// A link made before AO stored the run's project gets it filled in, on every
// task that holds the run; a link that has its project keeps it.
func TestFillTestinyRunLinkProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tny")
	a, err := s.CreateSession(ctx, sampleRecord("tny"))
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := s.CreateSession(ctx, sampleRecord("tny"))
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	mob := domain.TestinyProject{ID: 1, Key: "MOB", Name: "MOBILITY"}
	star := domain.TestinyProject{ID: 3, Key: "STAR", Name: "STAR"}
	for _, l := range []domain.TestinyRunLink{
		{SessionID: a.ID, RunID: 632, CreatedAt: t0},
		{SessionID: b.ID, RunID: 632, CreatedAt: t0},
		{SessionID: a.ID, RunID: 700, Project: star, CreatedAt: t0.Add(time.Minute)},
	} {
		if err := s.InsertTestinyRunLink(ctx, l); err != nil {
			t.Fatalf("insert %v: %v", l, err)
		}
	}

	if err := s.FillTestinyRunLinkProject(ctx, 632, mob); err != nil {
		t.Fatalf("fill 632: %v", err)
	}
	if err := s.FillTestinyRunLinkProject(ctx, 700, mob); err != nil {
		t.Fatalf("fill 700: %v", err)
	}

	got, _ := s.ListTestinyRunLinks(ctx, a.ID)
	if len(got) != 2 || got[0].Project != mob || got[1].Project != star {
		t.Fatalf("links of a = %+v, want 632 in MOB and 700 kept in STAR", got)
	}
	got, _ = s.ListTestinyRunLinks(ctx, b.ID)
	if len(got) != 1 || got[0].Project != mob {
		t.Fatalf("links of b = %+v, want 632 in MOB", got)
	}
}
