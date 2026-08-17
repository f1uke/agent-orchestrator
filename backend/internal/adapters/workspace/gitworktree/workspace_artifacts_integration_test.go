package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
)

// These tests run against a REAL git repository. They are the end-to-end proof
// of the reclaim safety rules — that a worktree holding human work survives,
// that one holding only regenerable build output is reclaimed, and that no
// branch is ever deleted — because those are properties of git's behaviour, not
// of our parsing.

// artifactWorkspace builds a workspace whose artefact patterns are the shipped
// defaults, plus a live worktree on `feature/one` to operate on.
func artifactWorkspace(t *testing.T, patterns []string) (*Workspace, ports.WorkspaceInfo, string) {
	t.Helper()
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{
		Binary:           git,
		ManagedRoot:      filepath.Join(tmp, "managed"),
		RepoResolver:     StaticRepoResolver{"proj": repo},
		ArtifactPatterns: func() []string { return patterns },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	info, err := ws.Create(context.Background(),
		ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return ws, info, repo
}

// branchExists reports whether the repo still has the branch ref.
func branchExists(t *testing.T, git, repo, branch string) bool {
	t.Helper()
	err := exec.Command(git, "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run()
	return err == nil
}

// TestDestroy_ReclaimsAWorktreeDirtiedOnlyByBuildOutput is the fix for the
// measured cause of the disk never being reclaimed: a native-app worktree grows
// an untracked build directory on its first build and is dirty from then on, so
// it was pinned on disk forever however finished its session was.
func TestDestroy_ReclaimsAWorktreeDirtiedOnlyByBuildOutput(t *testing.T) {
	git := requireGit(t)
	ws, info, repo := artifactWorkspace(t, reclaimsettings.DefaultArtifactPatterns)

	// Reproduce the exact shape measured on disk: a large untracked build
	// directory, and a coverage report nested under a TRACKED config directory.
	//
	// The tracked file in fastlane/ matters. git collapses a wholly-untracked
	// directory to its topmost entry, so without it the status line would read
	// `?? fastlane/` — which is correctly NOT an artefact — instead of the
	// `?? fastlane/xcov_report/` the real repos report.
	mustMkdirWithFile(t, filepath.Join(info.Path, "derivedDataPath", "Build", "Products"), "app.o")
	mustMkdirWithFile(t, filepath.Join(info.Path, "fastlane"), "Fastfile")
	runGit(t, git, info.Path, "add", "fastlane/Fastfile")
	runGit(t, git, info.Path, "commit", "-m", "add fastlane config")
	mustMkdirWithFile(t, filepath.Join(info.Path, "fastlane", "xcov_report"), "index.html")

	// Premise: git really does consider this worktree dirty.
	if out, _ := exec.Command(git, "-C", info.Path, "status", "--porcelain").Output(); len(strings.TrimSpace(string(out))) == 0 {
		t.Fatal("premise broken: expected the build output to make the worktree dirty")
	}

	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatalf("a worktree dirtied only by build output must be reclaimable: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still on disk after destroy: %v", err)
	}
	// The branch is kept: that is what makes reclamation recoverable.
	if !branchExists(t, git, repo, "feature/one") {
		t.Fatal("reclaim deleted the branch — it must never do that")
	}
}

// TestDestroy_RefusesAWorktreeWithRealUncommittedWork: uncommitted work is the
// one thing that exists nowhere else, so it always wins over disk.
func TestDestroy_RefusesAWorktreeWithRealUncommittedWork(t *testing.T) {
	git := requireGit(t)
	ws, info, repo := artifactWorkspace(t, reclaimsettings.DefaultArtifactPatterns)

	// A modified TRACKED file, alongside build output that would otherwise be
	// cleared. The presence of the artefacts must not rescue this worktree.
	mustMkdirWithFile(t, filepath.Join(info.Path, "derivedDataPath"), "app.o")
	if err := os.WriteFile(filepath.Join(info.Path, "README.md"), []byte("work in progress\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ws.Destroy(context.Background(), info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("want ErrWorkspaceDirty, got %v", err)
	}
	if _, statErr := os.Stat(info.Path); statErr != nil {
		t.Fatalf("the worktree must be preserved intact: %v", statErr)
	}
	// The user's changes must still be there, and so must the branch.
	body, readErr := os.ReadFile(filepath.Join(info.Path, "README.md"))
	if readErr != nil || !strings.Contains(string(body), "work in progress") {
		t.Fatalf("uncommitted work was damaged: %v %q", readErr, body)
	}
	if !branchExists(t, git, repo, "feature/one") {
		t.Fatal("branch must survive a refusal too")
	}
}

// TestDestroy_RefusesUntrackedWorkThatIsNotBuildOutput: the safety default. An
// untracked file the classifier does not positively recognise is treated as
// human work — a scratch note, a downloaded fixture — and keeps the worktree.
func TestDestroy_RefusesUntrackedWorkThatIsNotBuildOutput(t *testing.T) {
	ws, info, _ := artifactWorkspace(t, reclaimsettings.DefaultArtifactPatterns)

	if err := os.WriteFile(filepath.Join(info.Path, "notes.md"), []byte("do not delete\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := ws.Destroy(context.Background(), info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("want ErrWorkspaceDirty for an untracked non-artefact, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "notes.md")); statErr != nil {
		t.Fatalf("the untracked file must survive: %v", statErr)
	}
}

// TestDestroy_ArtefactClearingOffKeepsThePreviousBehaviour: with the knob off,
// build output blocks reclamation exactly as it did before this feature.
func TestDestroy_ArtefactClearingOffKeepsThePreviousBehaviour(t *testing.T) {
	ws, info, _ := artifactWorkspace(t, nil) // no patterns == feature off

	mustMkdirWithFile(t, filepath.Join(info.Path, "derivedDataPath"), "app.o")

	err := ws.Destroy(context.Background(), info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("with artefact clearing off, build output must still block: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "derivedDataPath")); statErr != nil {
		t.Fatalf("the build output must not have been deleted: %v", statErr)
	}
}

// TestDestroy_KeepsBuildOutputWhenTheWorktreeIsRefusedAnyway: artefacts are only
// cleared when doing so actually unblocks removal. If something else is dirty
// the worktree survives, and it must survive INTACT — deleting the build output
// of a worktree that then stays costs the user a long rebuild for nothing.
func TestDestroy_KeepsBuildOutputWhenTheWorktreeIsRefusedAnyway(t *testing.T) {
	ws, info, _ := artifactWorkspace(t, reclaimsettings.DefaultArtifactPatterns)

	artifact := filepath.Join(info.Path, "derivedDataPath")
	mustMkdirWithFile(t, artifact, "expensive-to-rebuild.o")
	if err := os.WriteFile(filepath.Join(info.Path, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("want ErrWorkspaceDirty, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(artifact, "expensive-to-rebuild.o")); statErr != nil {
		t.Fatalf("build output was deleted from a worktree that survived: %v", statErr)
	}
}

// TestDestroy_RefusesAPausedRebaseEvenWithACleanTree: an interactive rebase
// stopped at an `edit` step leaves a CLEAN working tree while holding its todo
// list and ORIG_HEAD in the worktree's git dir, which removal would destroy.
// `git status` cannot see that, so it is checked separately.
func TestDestroy_RefusesAPausedRebaseEvenWithACleanTree(t *testing.T) {
	git := requireGit(t)
	ws, info, _ := artifactWorkspace(t, reclaimsettings.DefaultArtifactPatterns)

	// Fabricate the paused-rebase marker inside the worktree's own git dir.
	gitDir := gitPathOf(t, git, info.Path, "rebase-merge")
	mustMkdirWithFile(t, gitDir, "git-rebase-todo")

	// Premise: the working tree really is clean, so only the git-dir probe can
	// catch this.
	if out, _ := exec.Command(git, "-C", info.Path, "status", "--porcelain").Output(); len(strings.TrimSpace(string(out))) != 0 {
		t.Fatalf("premise broken: expected a clean tree, got %q", out)
	}

	// clearRegenerableArtifacts must decline to touch anything here. There are
	// no artefacts to clear, so what this pins is the probe itself.
	if op := ws.inProgressOp(context.Background(), info.Path); op != "rebase-merge" {
		t.Fatalf("a paused rebase must be detected, got %q", op)
	}
}

// TestInProgressOp_CleanWorktreeHasNone guards the other direction: an ordinary
// worktree must not be reported as mid-operation, or nothing would ever be
// reclaimed.
func TestInProgressOp_CleanWorktreeHasNone(t *testing.T) {
	ws, info, _ := artifactWorkspace(t, reclaimsettings.DefaultArtifactPatterns)

	if op := ws.inProgressOp(context.Background(), info.Path); op != "" {
		t.Fatalf("a clean worktree has no paused operation, got %q", op)
	}
}

// TestDestroy_ReclaimedWorktreeCommitsStayReachable: reclaim removes a
// directory, never history. Commits made in the worktree must still be
// reachable from the branch afterwards — which is precisely why the branch must
// never be deleted.
func TestDestroy_ReclaimedWorktreeCommitsStayReachable(t *testing.T) {
	git := requireGit(t)
	ws, info, repo := artifactWorkspace(t, reclaimsettings.DefaultArtifactPatterns)

	// Commit real work in the worktree, then leave build output behind.
	if err := os.WriteFile(filepath.Join(info.Path, "shipped.txt"), []byte("real work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, info.Path, "add", "shipped.txt")
	runGit(t, git, info.Path, "commit", "-m", "ship it")
	head := strings.TrimSpace(string(mustOutput(t, git, info.Path, "rev-parse", "HEAD")))
	mustMkdirWithFile(t, filepath.Join(info.Path, "derivedDataPath"), "app.o")

	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	// The commit is still reachable from the kept branch in the main repo.
	if !branchExists(t, git, repo, "feature/one") {
		t.Fatal("branch deleted")
	}
	tip := strings.TrimSpace(string(mustOutput(t, git, repo, "rev-parse", "refs/heads/feature/one")))
	if tip != head {
		t.Fatalf("branch tip = %s, want the commit made in the worktree %s", tip, head)
	}
	// And its content is retrievable.
	blob := mustOutput(t, git, repo, "show", "refs/heads/feature/one:shipped.txt")
	if !strings.Contains(string(blob), "real work") {
		t.Fatalf("committed work is not recoverable, got %q", blob)
	}
}

// --- helpers ---

func mustMkdirWithFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitPathOf(t *testing.T, git, worktree, name string) string {
	t.Helper()
	out := strings.TrimSpace(string(mustOutput(t, git, worktree, "rev-parse", "--git-path", name)))
	if !filepath.IsAbs(out) {
		out = filepath.Join(worktree, out)
	}
	return out
}

func mustOutput(t *testing.T, git, dir string, args ...string) []byte {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command(git, full...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}
