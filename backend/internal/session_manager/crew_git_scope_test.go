package sessionmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The scope is what makes the guard safe to carry in an environment, so the two
// ways it can silently STOP APPLYING - or start applying where it must not - are
// worth their own tests. Both mutations below leave every other test in this
// package green, which is the whole reason they are here: a guard that has
// quietly turned itself off looks exactly like one that works.

// TestQAPreCommitHook_ScopeMatchesThroughASymlink pins the `physical` resolution
// in the hook. `ao.crewWorktree` is whatever path the daemon had in hand, and a
// worktree can be named through a symlink - on macOS a temp dir is `/var/...`
// for the caller and `/private/var/...` once git has resolved it. Comparing the
// two as strings would find them different and hand the commit straight through,
// so the guard would be OFF in exactly the environment it ships into, with no
// other test in this package noticing.
func TestQAPreCommitHook_ScopeMatchesThroughASymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := hookRepo(t)

	// The same worktree, named the far side of a symlink.
	link := filepath.Join(t.TempDir(), "worktree-by-another-name")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	env := crewGitEnv(domain.CrewRoleQA, t.TempDir(), link)

	write(t, repo, "internal/app.go", "package app // dev is mid-change\n")
	git(t, repo, nil, "add", "-A")
	if out, err := commit(t, repo, env, "reached through a symlinked scope"); err == nil {
		t.Fatalf("the guard did not apply to its own worktree named through a symlink - it is off:\n%s", out)
	}
}

// TestQAPreCommitHook_ChainsToTheProjectsOwnHookOutsideTheScope. Stepping aside
// in another repository must mean handing over, not swallowing. qa's hooks path
// replaces that repository's for every git the session runs, so if the
// out-of-scope branch simply exited 0 the repo's OWN pre-commit - its formatter,
// its lint gate, its secret scan - would silently stop running everywhere except
// the task's worktree, and nothing would report it.
func TestQAPreCommitHook_ChainsToTheProjectsOwnHookOutsideTheScope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	// Scoped to the task's worktree...
	env := crewGitEnv(domain.CrewRoleQA, t.TempDir(), hookRepo(t))
	// ...and run against a different repository that has a hook of its own.
	other := hookRepo(t)

	marker := filepath.Join(other, "project-hook-ran")
	projectHook := filepath.Join(other, ".git", "hooks", "pre-commit")
	write(t, other, filepath.Join(".git", "hooks", "pre-commit"), "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(projectHook, 0o755); err != nil {
		t.Fatal(err)
	}

	// A non-test path: the one qa's guard would refuse, were it in scope.
	write(t, other, "f.txt", "a temp repo's own file\n")
	git(t, other, nil, "add", "-A")
	if out, err := commit(t, other, env, "not qa's business"); err != nil {
		t.Fatalf("qa's guard reached a repository that is not the task's:\n%s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the other repository's own pre-commit hook did not run: %v", err)
	}
}
