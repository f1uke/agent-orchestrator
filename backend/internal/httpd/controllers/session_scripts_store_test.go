package controllers

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionViewCarriesTheScriptsStoreSummary(t *testing.T) {
	if v := sessionView(domain.Session{}); v.ScriptsStore != nil {
		t.Fatalf("a session without a store worktree carries %+v", v.ScriptsStore)
	}
	w := &domain.ScriptsStoreWorktree{
		State: domain.ScriptsStoreHeld, HeldReason: domain.HoldPublishConflict,
		HeldFiles:   []string{"projects/nter/login.yaml", "projects/nter/draft.yaml"},
		Uncommitted: []string{"projects/nter/draft.yaml"}, Unpublished: 3,
	}
	got := sessionView(domain.Session{ScriptsStore: w}).ScriptsStore
	want := &SessionScriptsStore{
		Uncommitted: 1, Unpublished: 3, HeldReason: domain.HoldPublishConflict,
		Files: []string{"projects/nter/draft.yaml", "projects/nter/login.yaml"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summary = %+v, want %+v (uncommitted files first, each file once)", got, want)
	}
}
