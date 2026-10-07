package session

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// An orchestrator can be marked terminated while its agent runs on in the
// project's orchestrator terminal, a name every orchestrator of the project
// shares. Spawning beside it fails "duplicate session" (testiny-cli-6,
// 2026-10-07), so the live one must be taken back instead.
func endedOrchestrators(st *fakeStore) {
	base := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true, CreatedAt: base, UpdatedAt: base.Add(30 * time.Minute)}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true, CreatedAt: base.Add(time.Minute), UpdatedAt: base.Add(2 * time.Minute)}
}

func TestSpawnOrchestratorNoCleanAdoptsTheLastOrchestratorWhoseAgentStillRuns(t *testing.T) {
	st := newFakeStore()
	endedOrchestrators(st)
	fc := &fakeCommander{adoptStore: st, liveAgents: map[domain.SessionID]bool{"mer-1": true}}
	svc := &Service{manager: fc, store: st}

	got, err := svc.SpawnOrchestrator(context.Background(), "mer", false)
	if err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if got.ID != "mer-1" || got.IsTerminated {
		t.Fatalf("got %s terminated=%v, want the adopted mer-1", got.ID, got.IsTerminated)
	}
	if fc.spawned {
		t.Fatal("a new orchestrator was spawned beside the live one")
	}
	if !slices.Equal(fc.adoptAsked, []domain.SessionID{"mer-1"}) {
		t.Fatalf("adopt asked for %v, want only the last to end (mer-1)", fc.adoptAsked)
	}
}

func TestSpawnOrchestratorNoCleanSpawnsWhenTheLastOrchestratorsAgentIsGone(t *testing.T) {
	st := newFakeStore()
	endedOrchestrators(st)
	fc := &fakeCommander{adoptStore: st}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", false); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if !fc.spawned {
		t.Fatal("no orchestrator was spawned")
	}
}

func TestSpawnOrchestratorCleanRetiresTheLiveOrchestratorItAdopts(t *testing.T) {
	st := newFakeStore()
	endedOrchestrators(st)
	fc := &fakeCommander{adoptStore: st, liveAgents: map[domain.SessionID]bool{"mer-1": true}}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if !slices.Equal(fc.retired, []domain.SessionID{"mer-1"}) || !fc.spawned || fc.killsAtSpawn != 1 {
		t.Fatalf("retired=%v spawned=%v retiredAtSpawn=%d, want mer-1 retired before the spawn", fc.retired, fc.spawned, fc.killsAtSpawn)
	}
}
