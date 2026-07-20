package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func seedLiveKind(st *fakeStore, id domain.SessionID, kind domain.SessionKind, path string) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: kind, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: path, Branch: "ao/mer-orchestrator", RuntimeHandleID: "h-" + string(id)},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
}

// TestSyncOrchestratorWorkspacesUpdatesLiveOrchestrators is the periodic half of
// the fix. Spawn and restore alone leave a long-lived orchestrator drifting: a
// session that has been up for days was brought current on the day it started
// and has been answering questions from that snapshot ever since. The sweep is
// what makes "always current" true rather than "current at startup".
func TestSyncOrchestratorWorkspacesUpdatesLiveOrchestrators(t *testing.T) {
	m, ws, st := newSyncMgr(t, "main-fluke")
	seedLiveKind(st, "mer-1", domain.KindOrchestrator, "/ws/mer-1")

	if err := m.SyncOrchestratorWorkspaces(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(ws.syncCalls) != 1 {
		t.Fatalf("SyncToBase calls = %d, want 1", len(ws.syncCalls))
	}
	if got := ws.syncCalls[0]; got.path != "/ws/mer-1" || got.baseBranch != "main-fluke" {
		t.Fatalf("sync call = %#v, want /ws/mer-1 against main-fluke", got)
	}
}

// TestSyncOrchestratorWorkspacesSkipsWorkers: requirement 6 on the sweep path.
// A periodic job that touched every worktree would quietly fast-forward workers
// off their own work — far more damaging than the staleness being fixed.
func TestSyncOrchestratorWorkspacesSkipsWorkers(t *testing.T) {
	m, ws, st := newSyncMgr(t, "main-fluke")
	seedLiveKind(st, "mer-1", domain.KindWorker, "/ws/mer-1")
	seedLiveKind(st, "mer-2", domain.KindWorker, "/ws/mer-2")

	if err := m.SyncOrchestratorWorkspaces(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(ws.syncCalls) != 0 {
		t.Fatalf("sweep synced %d worker worktrees, want 0: %#v", len(ws.syncCalls), ws.syncCalls)
	}
}

// TestSyncOrchestratorWorkspacesSkipsTerminated: a terminated session's worktree
// may not even exist. Restore syncs it when it comes back; the sweep must not
// touch it meanwhile.
func TestSyncOrchestratorWorkspacesSkipsTerminated(t *testing.T) {
	m, ws, st := newSyncMgr(t, "main-fluke")
	seedTerminalKind(st, "mer-1", domain.KindOrchestrator)

	if err := m.SyncOrchestratorWorkspaces(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(ws.syncCalls) != 0 {
		t.Fatalf("sweep synced %d terminated sessions, want 0: %#v", len(ws.syncCalls), ws.syncCalls)
	}
}

// TestSyncOrchestratorWorkspacesSkipsPathlessSessions: a TODO or a session whose
// workspace was never materialised has no tree to sync.
func TestSyncOrchestratorWorkspacesSkipsPathlessSessions(t *testing.T) {
	m, ws, st := newSyncMgr(t, "main-fluke")
	seedLiveKind(st, "mer-1", domain.KindOrchestrator, "")

	if err := m.SyncOrchestratorWorkspaces(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(ws.syncCalls) != 0 {
		t.Fatalf("sweep synced %d pathless sessions, want 0: %#v", len(ws.syncCalls), ws.syncCalls)
	}
}

// TestSyncOrchestratorWorkspacesContinuesPastFailure: one unreachable repo must
// not stop the rest of the sweep. A sweep that aborted on the first error would
// silently leave every later orchestrator stale — the same class of bug.
func TestSyncOrchestratorWorkspacesContinuesPastFailure(t *testing.T) {
	m, ws, st := newSyncMgr(t, "main-fluke")
	seedLiveKind(st, "mer-1", domain.KindOrchestrator, "/ws/mer-1")
	seedLiveKind(st, "mer-2", domain.KindOrchestrator, "/ws/mer-2")
	seedLiveKind(st, "mer-3", domain.KindOrchestrator, "/ws/mer-3")
	ws.syncResult = ports.WorkspaceSyncResult{Outcome: ports.WorkspaceSyncSkipped, Reason: ports.WorkspaceSyncReasonDirty}

	if err := m.SyncOrchestratorWorkspaces(ctx); err != nil {
		t.Fatalf("sweep must not fail on a skipped/failed session: %v", err)
	}
	if len(ws.syncCalls) != 3 {
		t.Fatalf("SyncToBase calls = %d, want all 3 attempted: %#v", len(ws.syncCalls), ws.syncCalls)
	}
}
