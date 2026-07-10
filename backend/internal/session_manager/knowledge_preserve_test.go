package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/knowledgestore"
)

// managerWithDataDir builds a Manager whose knowledge store lives under dataDir,
// mirroring newManager() but exposing the store root the teardown safety net
// writes into.
func managerWithDataDir(dataDir string) (*Manager, *fakeStore) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		DataDir: dataDir, LookPath: lookPath,
	})
	return m, st
}

func writeWorktreeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestKill_PreservesWorkerPlanningDocs verifies the belt-and-suspenders net:
// killing a worker copies stray planning docs out of the worktree into the
// project's private knowledge store before the worktree is torn down.
func TestKill_PreservesWorkerPlanningDocs(t *testing.T) {
	dataDir := t.TempDir()
	wt := t.TempDir()
	writeWorktreeFile(t, filepath.Join(wt, "PLAN.md"), "the plan")
	writeWorktreeFile(t, filepath.Join(wt, "docs", "plans", "auth.md"), "auth design")
	writeWorktreeFile(t, filepath.Join(wt, "README.md"), "ignore me")

	m, st := managerWithDataDir(dataDir)
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: wt, Branch: "feat/topic", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if _, err := m.Kill(context.Background(), "mer-1"); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	plansDir := knowledgestore.PlansDir(dataDir, "mer")
	for _, name := range []string{"feat-topic--PLAN.md", "feat-topic--docs-plans-auth.md"} {
		if _, err := os.Stat(filepath.Join(plansDir, name)); err != nil {
			t.Fatalf("expected preserved %q in the knowledge store: %v", name, err)
		}
	}
	// The unrelated README must not be preserved.
	if entries, _ := os.ReadDir(plansDir); len(entries) != 2 {
		t.Fatalf("want exactly 2 preserved docs, got %d", len(entries))
	}
}

// TestKill_SkipsOrchestratorKnowledge confirms only workers are scanned: an
// orchestrator's worktree docs are never copied into the plans store (the
// orchestrator curates the store, it does not seed plans from a worktree).
func TestKill_SkipsOrchestratorKnowledge(t *testing.T) {
	dataDir := t.TempDir()
	wt := t.TempDir()
	writeWorktreeFile(t, filepath.Join(wt, "PLAN.md"), "orchestrator scratch")

	m, st := managerWithDataDir(dataDir)
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{WorkspacePath: wt, Branch: "ao/mer/root", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if _, err := m.Kill(context.Background(), "mer-1"); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := os.Stat(knowledgestore.PlansDir(dataDir, "mer")); !os.IsNotExist(err) {
		t.Fatalf("orchestrator teardown must not create a plans store, stat err = %v", err)
	}
}

// TestSaveAndTeardownAll_PreservesWorkerPlanningDocs verifies the shutdown/crash
// teardown path also runs the safety net before the worktree is force-removed.
func TestSaveAndTeardownAll_PreservesWorkerPlanningDocs(t *testing.T) {
	dataDir := t.TempDir()
	wt := t.TempDir()
	writeWorktreeFile(t, filepath.Join(wt, "implementation-plan.md"), "shutdown plan")

	m, st := managerWithDataDir(dataDir)
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: wt, Branch: "feat/x", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.SaveAndTeardownAll(context.Background()); err != nil {
		t.Fatalf("SaveAndTeardownAll: %v", err)
	}
	if _, err := os.Stat(filepath.Join(knowledgestore.PlansDir(dataDir, "mer"), "feat-x--implementation-plan.md")); err != nil {
		t.Fatalf("expected preserved plan after shutdown teardown: %v", err)
	}
}
