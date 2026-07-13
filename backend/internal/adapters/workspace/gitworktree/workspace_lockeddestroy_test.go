package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// lockWorktree marks a managed worktree with `git worktree lock`, mirroring the
// state an external tool leaves behind (e.g. a `{"owner":"..."}` reason). A
// locked worktree makes `git worktree remove` refuse and `git worktree prune`
// skip it, so AO teardown must unlock it first.
func lockWorktree(t *testing.T, git, repo, path string) {
	t.Helper()
	run(t, git, "-C", repo, "worktree", "lock", "--reason", `{"owner":"external-tool"}`, path)
}

// TestForceDestroyUnlocksMissingLockedWorktree reproduces the exact live
// orchestrator state: the worktree directory was already removed from disk but
// git still has it registered AND locked (prune skips locked worktrees).
// ForceDestroy must unlock so the registration is cleared and the branch can be
// re-checked-out on the next restore.
func TestForceDestroyUnlocksMissingLockedWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-missing", Branch: "ao/missing-orchestrator"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	lockWorktree(t, git, repo, info.Path)
	// Wipe the directory while the registration + lock remain (the state left by
	// AO's own os.RemoveAll fallback running against a lock it could not remove).
	if err := os.RemoveAll(info.Path); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatalf("ForceDestroy missing+locked worktree: %v", err)
	}
	records, err := ws.listRecords(ctx, repo)
	if err != nil {
		t.Fatalf("listRecords: %v", err)
	}
	if _, ok := findWorktree(records, info.Path); ok {
		t.Fatalf("worktree %q still registered after ForceDestroy", info.Path)
	}

	// The branch must be re-checkout-able (restore path): add it back cleanly.
	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("Restore after ForceDestroy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restored.Path, "README.md")); err != nil {
		t.Fatalf("restored worktree missing tracked files: %v", err)
	}
}

// TestDestroyLockedDirtyStillRefused guards the contract that unlocking must NOT
// force-remove uncommitted work: a locked AND dirty worktree still returns
// ErrWorkspaceDirty (only the lock obstacle is cleared, not the dirty guard).
func TestDestroyLockedDirtyStillRefused(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-dirty", Branch: "ao/dirty-orchestrator"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "wip.txt"), []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatalf("write wip: %v", err)
	}
	lockWorktree(t, git, repo, info.Path)

	if err := ws.Destroy(ctx, info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("Destroy locked+dirty error = %v, want ports.ErrWorkspaceDirty", err)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "wip.txt")); err != nil {
		t.Fatalf("dirty worktree work was removed by Destroy: %v", err)
	}
}
