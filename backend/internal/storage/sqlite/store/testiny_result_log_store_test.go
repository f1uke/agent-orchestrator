package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestTestinyResultLog(t *testing.T) {
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
		{SessionID: task.ID, RunID: 632, CreatedAt: t0},
		{SessionID: task.ID, RunID: 633, CreatedAt: t0},
		{SessionID: other.ID, RunID: 632, CreatedAt: t0},
	} {
		if err := s.InsertTestinyRunLink(ctx, l); err != nil {
			t.Fatalf("link %v: %v", l, err)
		}
	}

	entry := func(sid domain.SessionID, run domain.TestinyRunID, c int64, status domain.TestinyCaseStatus, comment, by string, at time.Time) domain.TestinyResultEntry {
		return domain.TestinyResultEntry{
			SessionID: sid, RunID: run, SetBy: by, SHA: "4f2c9e1", CreatedAt: at,
			TestinyResult: domain.TestinyResult{CaseID: c, Status: status, Comment: comment},
		}
	}
	first := []domain.TestinyResultEntry{
		entry(task.ID, 632, 7166, domain.TestinyFailed, "ปุ่มไม่แสดง", "tny-2", t0),
		entry(task.ID, 632, 7167, domain.TestinyPassed, "", "tny-2", t0),
		entry(task.ID, 633, 7166, domain.TestinyPassed, "", "tny-2", t0),
		entry(other.ID, 632, 7166, domain.TestinyBlocked, "no device", "", t0),
	}
	if err := s.AppendTestinyResults(ctx, first); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Two writes in one batch share a time; the later one wins.
	later := t0.Add(time.Minute)
	if err := s.AppendTestinyResults(ctx, []domain.TestinyResultEntry{
		entry(task.ID, 632, 7166, domain.TestinyPassed, "", "", later),
		entry(task.ID, 632, 7166, domain.TestinyNotRun, "", "", later),
	}); err != nil {
		t.Fatalf("append later: %v", err)
	}

	got, err := s.LatestTestinyResults(ctx, task.ID, 632)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	want := []domain.TestinyResultEntry{
		entry(task.ID, 632, 7166, domain.TestinyNotRun, "", "", later),
		entry(task.ID, 632, 7167, domain.TestinyPassed, "", "tny-2", t0),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("latest =\n%+v\nwant\n%+v", got, want)
	}

	// A batch with a row that breaks a constraint writes nothing.
	err = s.AppendTestinyResults(ctx, []domain.TestinyResultEntry{
		entry(task.ID, 633, 9000, domain.TestinyPassed, "", "", later),
		entry(task.ID, 999, 9001, domain.TestinyPassed, "", "", later),
	})
	if err == nil {
		t.Fatal("append to an unlinked run succeeded")
	}
	if got, _ := s.LatestTestinyResults(ctx, task.ID, 633); len(got) != 1 || got[0].CaseID != 7166 {
		t.Fatalf("a failed batch left rows behind: %+v", got)
	}

	if err := s.DeleteTestinyRunLink(ctx, task.ID, 632); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if got, _ := s.LatestTestinyResults(ctx, task.ID, 632); len(got) != 0 {
		t.Fatalf("log outlived its run's link: %+v", got)
	}
	if got, _ := s.LatestTestinyResults(ctx, other.ID, 632); len(got) != 1 {
		t.Fatalf("unlinking one task's run removed another task's log: %+v", got)
	}

	if err := s.PurgeSession(ctx, task.ID); err != nil {
		t.Fatalf("purge task: %v", err)
	}
	if got, _ := s.LatestTestinyResults(ctx, task.ID, 633); len(got) != 0 {
		t.Fatalf("log outlived its task: %+v", got)
	}
}

func TestTestinyStepResultLog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tny")
	task, err := s.CreateSession(ctx, sampleRecord("tny"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	if err := s.InsertTestinyRunLink(ctx, domain.TestinyRunLink{SessionID: task.ID, RunID: 632, CreatedAt: t0}); err != nil {
		t.Fatalf("link: %v", err)
	}
	steps := func(n int, status domain.TestinyCaseStatus) []domain.TestinyStepResult {
		return []domain.TestinyStepResult{{N: n, Status: status}}
	}
	entries := []domain.TestinyResultEntry{
		{SessionID: task.ID, RunID: 632, SetBy: "tny-2", CreatedAt: t0,
			TestinyResult: domain.TestinyResult{CaseID: 7166, Status: domain.TestinyFailed, Comment: "step 2", Steps: steps(2, domain.TestinyFailed)}},
		{SessionID: task.ID, RunID: 632, SetBy: "tny-2", CreatedAt: t0,
			TestinyResult: domain.TestinyResult{CaseID: 7167, Status: domain.TestinyPassed}},
		{SessionID: task.ID, RunID: 632, CreatedAt: t0.Add(time.Minute),
			TestinyResult: domain.TestinyResult{CaseID: 7166, Steps: steps(3, domain.TestinyPassed)}},
	}
	if err := s.AppendTestinyResults(ctx, entries); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := s.TestinyStepResultLog(ctx, task.ID, 632)
	if err != nil {
		t.Fatalf("step log: %v", err)
	}
	if want := []domain.TestinyResultEntry{entries[0], entries[2]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("step log =\n%+v\nwant\n%+v", got, want)
	}

	// A write that set only steps leaves the case's latest status where it was.
	latest, err := s.LatestTestinyResults(ctx, task.ID, 632)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if want := []domain.TestinyResultEntry{entries[0], entries[1]}; !reflect.DeepEqual(latest, want) {
		t.Fatalf("latest =\n%+v\nwant\n%+v", latest, want)
	}
}
