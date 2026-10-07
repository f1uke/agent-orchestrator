package store_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func sampleStoreWorktree(owner domain.SessionID, project domain.ProjectID, at time.Time) domain.ScriptsStoreWorktree {
	return domain.ScriptsStoreWorktree{
		SessionID: owner, ProjectID: project, Store: "/store", Path: "/data/store-worktrees/store/" + string(owner),
		Branch: "ao/" + string(owner), BaseBranch: "main", State: domain.ScriptsStoreActive,
		CreatedAt: at, UpdatedAt: at,
	}
}

func TestScriptsStoreWorktreeRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ssw")
	rec, err := s.CreateSession(ctx, sampleRecord("ssw"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, ok, err := s.GetScriptsStoreWorktree(ctx, rec.ID); err != nil || ok {
		t.Fatalf("get before insert = ok %v, err %v; want absent", ok, err)
	}
	created := time.Now().UTC().Truncate(time.Second)
	w := sampleStoreWorktree(rec.ID, rec.ProjectID, created)
	if err := s.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		t.Fatalf("insert: %v", err)
	}

	later := created.Add(time.Minute)
	w.State, w.HeldReason, w.HeldFiles = domain.ScriptsStoreHeld, domain.HoldPublishConflict, []string{"projects/nter/a.yaml"}
	w.Uncommitted, w.Unpublished = []string{"projects/nter/b.yaml"}, 2
	w.CreatedAt, w.UpdatedAt, w.PublishedAt = later, later, later
	if err := s.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, ok, err := s.GetScriptsStoreWorktree(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("get = ok %v, err %v", ok, err)
	}
	want := w
	want.CreatedAt = created
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v (created_at kept from the first write)", got, want)
	}

	held, err := s.ListScriptsStoreWorktreesInStates(ctx, domain.ScriptsStoreHeld)
	if err != nil || len(held) != 1 || held[0].SessionID != rec.ID {
		t.Fatalf("list held = %+v, %v; want the one row", held, err)
	}
	active, err := s.ListScriptsStoreWorktreesInStates(ctx, domain.ScriptsStoreActive)
	if err != nil || len(active) != 0 {
		t.Fatalf("list active = %+v, %v; want none", active, err)
	}
}

func TestScriptsStoreWorktreeOutlivesItsSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "sso")
	rec, err := s.CreateSession(ctx, sampleRecord("sso"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	w := sampleStoreWorktree(rec.ID, rec.ProjectID, now)
	w.State, w.HeldReason = domain.ScriptsStoreHeld, domain.HoldUncommitted
	if err := s.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.PurgeSession(ctx, rec.ID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, ok, err := s.GetScriptsStoreWorktree(ctx, rec.ID); err != nil || !ok {
		t.Fatalf("held row after purge = ok %v, err %v; want kept, it names work nobody published", ok, err)
	}
}

func TestScriptsStoreWorktreeChangeFansOutOwnerUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ssc")
	rec, err := s.CreateSession(ctx, sampleRecord("ssc"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	head, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatalf("latest seq: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	w := sampleStoreWorktree(rec.ID, rec.ProjectID, now)
	if err := s.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		t.Fatalf("insert: %v", err)
	}
	w.UpdatedAt = now.Add(time.Second)
	if err := s.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		t.Fatalf("rewrite without visible change: %v", err)
	}
	w.Unpublished = 1
	if err := s.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		t.Fatalf("unpublished commit: %v", err)
	}
	w.Uncommitted = []string{"x.yaml"}
	if err := s.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		t.Fatalf("uncommitted file: %v", err)
	}
	w.State = domain.ScriptsStoreRemoved
	if err := s.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		t.Fatalf("removed: %v", err)
	}

	events, err := s.EventsAfter(ctx, head, 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var states []string
	for _, e := range events {
		var p struct {
			ID    string `json:"id"`
			State string `json:"scriptsStoreState"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("payload %s: %v", e.Payload, err)
		}
		if e.Type != "session_updated" || p.ID != string(rec.ID) {
			t.Fatalf("event %s %s does not address the owner %s", e.Type, e.Payload, rec.ID)
		}
		states = append(states, p.State)
	}
	want := []string{"active", "active", "active", "removed"}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("events = %v, want %v (none for a rewrite that changes nothing the card draws)", states, want)
	}
}
