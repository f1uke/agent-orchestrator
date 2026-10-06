package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func sampleChild(rec domain.SessionRecord, agentID string, at time.Time) domain.SessionChild {
	return domain.SessionChild{
		SessionID: rec.ID, ProjectID: rec.ProjectID, AgentID: agentID, AgentType: "general-purpose",
		Branch: "ao-child/" + string(rec.ID) + "/" + agentID, TargetBranch: "feat/x", BaseSHA: "abc123",
		BaseDirty: []string{"w.txt"}, WorktreePath: "/children/" + agentID, State: domain.ChildRunning,
		CreatedAt: at, UpdatedAt: at,
	}
}

func TestSessionChildRoundTripAndBoardSelection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "chl")
	live, err := s.CreateSession(ctx, sampleRecord("chl"))
	if err != nil {
		t.Fatalf("create live session: %v", err)
	}
	ended, err := s.CreateSession(ctx, sampleRecord("chl"))
	if err != nil {
		t.Fatalf("create ended session: %v", err)
	}
	ended.IsTerminated = true
	if err := s.UpdateSession(ctx, ended); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	if err := s.InsertSessionChild(ctx, sampleChild(live, "a1", now)); err != nil {
		t.Fatalf("insert a1: %v", err)
	}
	merged := sampleChild(ended, "b1", now)
	preserved := sampleChild(ended, "b2", now.Add(time.Second))
	for _, c := range []domain.SessionChild{merged, preserved} {
		if err := s.InsertSessionChild(ctx, c); err != nil {
			t.Fatalf("insert %s: %v", c.AgentID, err)
		}
	}

	got, ok, err := s.GetSessionChild(ctx, live.ID, "a1")
	if err != nil || !ok {
		t.Fatalf("get a1: ok=%v err=%v", ok, err)
	}
	if got.State != domain.ChildRunning || got.BaseSHA != "abc123" || len(got.BaseDirty) != 1 || got.BaseDirty[0] != "w.txt" {
		t.Fatalf("a1 round-tripped wrong: %+v", got)
	}

	finished := now.Add(time.Minute)
	got.State, got.MergedSHA, got.Commits, got.FilesChanged = domain.ChildMerged, "def456", 2, 3
	got.Description, got.StopBlocks, got.NotifiedState = "Write the parser", 1, domain.ChildRunning
	got.UpdatedAt, got.FinishedAt = finished, &finished
	if ok, err := s.UpdateSessionChild(ctx, got); err != nil || !ok {
		t.Fatalf("update a1: ok=%v err=%v", ok, err)
	}
	again, _, _ := s.GetSessionChild(ctx, live.ID, "a1")
	if again.State != domain.ChildMerged || again.MergedSHA != "def456" || again.Commits != 2 || again.FilesChanged != 3 ||
		again.Description != "Write the parser" || again.StopBlocks != 1 || again.NotifiedState != domain.ChildRunning ||
		again.FinishedAt == nil || !again.FinishedAt.Equal(finished) {
		t.Fatalf("a1 after update: %+v", again)
	}

	merged.State = domain.ChildMerged
	merged.UpdatedAt = now
	preserved.State = domain.ChildPreserved
	for _, c := range []domain.SessionChild{merged, preserved} {
		if _, err := s.UpdateSessionChild(ctx, c); err != nil {
			t.Fatalf("update %s: %v", c.AgentID, err)
		}
	}
	board, err := s.ListBoardSessionChildren(ctx)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	var ids []string
	for _, c := range board {
		ids = append(ids, string(c.SessionID)+"/"+c.AgentID)
	}
	want := []string{string(live.ID) + "/a1", string(ended.ID) + "/b2"}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("board children = %v, want %v (live worker's child + ended worker's preserved child only)", ids, want)
	}

	running, err := s.ListSessionChildrenInStates(ctx, domain.ChildMerged, domain.ChildPreserved)
	if err != nil || len(running) != 3 {
		t.Fatalf("children in merged/preserved = %d (err %v), want 3", len(running), err)
	}
}

func TestSessionChildStateChangeFansOutWorkerUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "chc")
	rec, err := s.CreateSession(ctx, sampleRecord("chc"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	head, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatalf("latest seq: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	c := sampleChild(rec, "a1", now)
	if err := s.InsertSessionChild(ctx, c); err != nil {
		t.Fatalf("insert: %v", err)
	}
	c.StopBlocks = 1
	if _, err := s.UpdateSessionChild(ctx, c); err != nil {
		t.Fatalf("update without visible change: %v", err)
	}
	c.State = domain.ChildMerged
	if _, err := s.UpdateSessionChild(ctx, c); err != nil {
		t.Fatalf("update state: %v", err)
	}

	events, err := s.EventsAfter(ctx, head, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var states []string
	for _, e := range events {
		var p struct {
			ID         string `json:"id"`
			ChildState string `json:"childState"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("payload %s: %v", e.Payload, err)
		}
		if e.Type != "session_updated" || p.ID != string(rec.ID) {
			t.Fatalf("event %s %s does not address the worker %s", e.Type, e.Payload, rec.ID)
		}
		states = append(states, p.ChildState)
	}
	if len(states) != 2 || states[0] != "running" || states[1] != "merged" {
		t.Fatalf("child events = %v, want [running merged] (no event for a stop-block counter bump)", states)
	}
}
