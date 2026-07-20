package sessionmanager

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// newSyncMgr builds a manager over a project whose default branch is
// deliberately NOT "main"/"master", so a sync that hardcoded a branch name would
// be caught rather than accidentally passing.
func newSyncMgr(t *testing.T, defaultBranch string) (*Manager, *fakeWorkspace, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{DefaultBranch: defaultBranch}}
	ws := &fakeWorkspace{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	return m, ws, st
}

// TestSpawnOrchestratorSyncsWorktreeToProjectDefaultBranch is the wiring half of
// the staleness fix. An orchestrator branch is released but never deleted when a
// session is retired, so a "new" orchestrator re-checks-out the SAME frozen
// commit its predecessor sat on. Creating the worktree is therefore not enough —
// the manager must ask the workspace to bring it up to the project's default
// branch on every spawn.
func TestSpawnOrchestratorSyncsWorktreeToProjectDefaultBranch(t *testing.T) {
	m, ws, _ := newSyncMgr(t, "main-fluke")

	rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode})
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.syncCalls) != 1 {
		t.Fatalf("SyncToBase calls = %d, want 1 — a spawned orchestrator must be brought current", len(ws.syncCalls))
	}
	got := ws.syncCalls[0]
	if got.baseBranch != "main-fluke" {
		t.Fatalf("synced to base %q, want the project's configured default branch main-fluke", got.baseBranch)
	}
	if got.path != rec.Metadata.WorkspacePath {
		t.Fatalf("synced path = %q, want the session's workspace %q", got.path, rec.Metadata.WorkspacePath)
	}
}

// TestSpawnOrchestratorSyncsToPerProjectBranch guards requirement 3: the base
// comes from each project's own config. `main-fluke` here, `develop` there —
// nothing may hardcode a branch name.
func TestSpawnOrchestratorSyncsToPerProjectBranch(t *testing.T) {
	for _, branch := range []string{"main-fluke", "develop", "trunk"} {
		t.Run(branch, func(t *testing.T) {
			m, ws, _ := newSyncMgr(t, branch)
			if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode}); err != nil {
				t.Fatal(err)
			}
			if len(ws.syncCalls) != 1 || ws.syncCalls[0].baseBranch != branch {
				t.Fatalf("sync calls = %#v, want one call against %q", ws.syncCalls, branch)
			}
		})
	}
}

// TestSpawnWorkerDoesNotSyncWorktree guards requirement 6. Worker worktrees are
// cut per-branch ON PURPOSE — a worker's branch carries its work, and fast-
// forwarding it onto the default branch would be wrong even when it is possible.
func TestSpawnWorkerDoesNotSyncWorktree(t *testing.T) {
	m, ws, _ := newSyncMgr(t, "main-fluke")

	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err != nil {
		t.Fatal(err)
	}
	if len(ws.syncCalls) != 0 {
		t.Fatalf("SyncToBase called %d times for a worker, want 0: %#v", len(ws.syncCalls), ws.syncCalls)
	}
}

// TestSpawnOrchestratorSurvivesSyncFailure: a workspace that cannot be brought
// current is a degraded orchestrator, not a dead one. The session must still
// come up — refusing to spawn because a fetch failed would be a worse failure
// than the staleness itself.
func TestSpawnOrchestratorSurvivesSyncFailure(t *testing.T) {
	m, ws, _ := newSyncMgr(t, "main-fluke")
	ws.syncErr = errors.New("fetch exploded")

	rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode})
	if err != nil {
		t.Fatalf("spawn must survive a sync failure, got: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("spawn returned no session")
	}
	if len(ws.syncCalls) != 1 {
		t.Fatalf("SyncToBase calls = %d, want 1 (attempted then tolerated)", len(ws.syncCalls))
	}
}

// TestSpawnOrchestratorSurvivesSkippedSync: the same tolerance for a DELIBERATE
// skip (dirty or diverged worktree). The session comes up on a stale tree
// rather than not at all.
func TestSpawnOrchestratorSurvivesSkippedSync(t *testing.T) {
	m, ws, _ := newSyncMgr(t, "main-fluke")
	ws.syncResult = ports.WorkspaceSyncResult{
		Outcome: ports.WorkspaceSyncSkipped,
		Reason:  ports.WorkspaceSyncReasonDirty,
		FromSHA: "aaaa", ToSHA: "bbbb",
	}

	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode}); err != nil {
		t.Fatalf("spawn must survive a skipped sync, got: %v", err)
	}
	if len(ws.syncCalls) != 1 {
		t.Fatalf("SyncToBase calls = %d, want 1", len(ws.syncCalls))
	}
}

// TestRestoreOrchestratorSyncsWorktree covers the second update point required
// by the brief. A restored orchestrator has been sitting terminated while the
// default branch moved on; restoring it must not hand back the tree exactly as
// it was left.
func TestRestoreOrchestratorSyncsWorktree(t *testing.T) {
	m, ws, st := newSyncMgr(t, "main-fluke")
	seedTerminalKind(st, "mer-1", domain.KindOrchestrator)

	restored, err := m.Restore(ctx, "mer-1")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(ws.syncCalls) != 1 {
		t.Fatalf("SyncToBase calls on restore = %d, want 1 — a restored orchestrator must be brought current", len(ws.syncCalls))
	}
	if got := ws.syncCalls[0].baseBranch; got != "main-fluke" {
		t.Fatalf("restore synced to %q, want main-fluke", got)
	}
	if got := ws.syncCalls[0].path; got != "/ws/mer-1" {
		t.Fatalf("restore synced path %q, want the restored worktree /ws/mer-1", got)
	}
	if restored.ID != "mer-1" {
		t.Fatalf("restored session = %q, want mer-1", restored.ID)
	}
}

// TestRestoreWorkerDoesNotSyncWorktree: requirement 6 again, on the restore path.
func TestRestoreWorkerDoesNotSyncWorktree(t *testing.T) {
	m, ws, st := newSyncMgr(t, "main-fluke")
	seedTerminalKind(st, "mer-1", domain.KindWorker)

	if _, err := m.Restore(ctx, "mer-1"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(ws.syncCalls) != 0 {
		t.Fatalf("SyncToBase called %d times restoring a worker, want 0: %#v", len(ws.syncCalls), ws.syncCalls)
	}
}

func seedTerminalKind(st *fakeStore, id domain.SessionID, kind domain.SessionKind) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: kind, Harness: domain.HarnessClaudeCode,
		Metadata:     domain.SessionMetadata{WorkspacePath: "/ws/" + string(id), Branch: "ao/mer-orchestrator", AgentSessionID: "agent-x"},
		IsTerminated: true,
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
}
