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
)

func TestWorkspaceIntegrationCreateRestoreDestroy(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.Path != filepath.Join(ws.managedRoot, "proj", "feature", "one") || info.Branch != cfg.Branch || info.SessionID != cfg.SessionID || info.ProjectID != cfg.ProjectID {
		t.Fatalf("info = %#v", info)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "README.md")); err != nil {
		t.Fatalf("created worktree missing seed file: %v", err)
	}

	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore registered: %v", err)
	}
	if restored.Path != info.Path || restored.Branch != cfg.Branch {
		t.Fatalf("restored = %#v", restored)
	}

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after destroy stat err = %v, want not exist", err)
	}

	restored, err = ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore after destroy: %v", err)
	}
	if restored.Path != info.Path || restored.Branch != cfg.Branch {
		t.Fatalf("restored after destroy = %#v", restored)
	}
	if err := ws.Destroy(ctx, restored); err != nil {
		t.Fatalf("destroy restored: %v", err)
	}
}

// TestWorkspaceIntegrationDestroyUnlocksLockedWorktree pins the fix for the
// orchestrator-restart-500 bug: a `git worktree lock` on a managed worktree used
// to wedge Destroy (git refuses to remove it and prune skips it, so Destroy
// returned a non-dirty "still registered" error, which Kill/Restart surfaced as
// a 500). AO owns worktrees under managedRoot, so Destroy now unlocks a stale
// lock and tears a clean worktree down. The dirty-worktree guard is unaffected
// (TestDestroyLockedDirtyStillRefused).
func TestWorkspaceIntegrationDestroyUnlocksLockedWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/lock"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	runGit(t, git, repo, "worktree", "lock", info.Path)

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy locked worktree: %v", err)
	}
	if _, statErr := os.Stat(info.Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("path after destroy stat err = %v, want not exist", statErr)
	}
	records, err := ws.listRecords(ctx, repo)
	if err != nil {
		t.Fatalf("listRecords: %v", err)
	}
	if _, ok := findWorktree(records, info.Path); ok {
		t.Fatalf("worktree %q still registered after Destroy", info.Path)
	}
}

// TestWorkspaceIntegrationDestroyDirtyWorktree proves the two halves of the
// dirty-teardown contract against real git:
//
//  1. A worktree whose only untracked files are covered by a self-ignoring
//     .gitignore (the shape agent adapters install for their hook files) is
//     clean in git's eyes, so Destroy succeeds without --force.
//  2. Real uncommitted work makes Destroy refuse with ports.ErrWorkspaceDirty
//     and preserves the worktree.
func TestWorkspaceIntegrationDestroyDirtyWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/dirty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// AO-managed hook files behind a self-ignoring .gitignore: invisible to git
	// status, so they must not block teardown.
	hookDir := filepath.Join(info.Path, ".codex")
	if err := os.MkdirAll(hookDir, 0o750); err != nil {
		t.Fatalf("mkdir hook dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hooks.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write hooks.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, ".gitignore"), []byte(".gitignore\nhooks.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	// Real agent work must keep blocking teardown, typed as ErrWorkspaceDirty.
	wip := filepath.Join(info.Path, "wip.txt")
	if err := os.WriteFile(wip, []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatalf("write wip: %v", err)
	}
	err = ws.Destroy(ctx, info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy dirty error = %v, want ports.ErrWorkspaceDirty", err)
	}
	if _, statErr := os.Stat(wip); statErr != nil {
		t.Fatalf("dirty worktree was not preserved: %v", statErr)
	}

	// With the real work gone, only the ignored AO files remain — git considers
	// the worktree clean and Destroy succeeds without --force.
	if err := os.Remove(wip); err != nil {
		t.Fatalf("remove wip: %v", err)
	}
	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy with ignored-only files: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after destroy stat err = %v, want not exist", err)
	}
}

// TestWorkspaceIntegrationCreateInRemotelessRepo guards the BRANCH_NOT_FETCHED
// regression: a repo with no remote configured must still spawn worktrees for
// new branches by basing them on the local default-branch head
// (refs/heads/main) once no origin/* candidate resolves.
func TestWorkspaceIntegrationCreateInRemotelessRepo(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	run(t, git, "init", repo)
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, repo, "add", "README.md")
	runGit(t, git, repo, "commit", "-m", "seed")
	runGit(t, git, repo, "branch", "-M", "main")

	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/remoteless"})
	if err != nil {
		t.Fatalf("create in remoteless repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "README.md")); err != nil {
		t.Fatalf("created worktree missing seed file: %v", err)
	}
	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	return git
}

func setupOriginClone(t *testing.T, git, tmp string) string {
	t.Helper()
	origin := filepath.Join(tmp, "origin.git")
	seed := filepath.Join(tmp, "seed")
	repo := filepath.Join(tmp, "repo")
	run(t, git, "init", "--bare", origin)
	run(t, git, "init", seed)
	runGit(t, git, seed, "config", "user.email", "ao@example.com")
	runGit(t, git, seed, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, seed, "add", "README.md")
	runGit(t, git, seed, "commit", "-m", "seed")
	runGit(t, git, seed, "branch", "-M", "main")
	runGit(t, git, seed, "remote", "add", "origin", origin)
	runGit(t, git, seed, "push", "-u", "origin", "main")
	run(t, git, "clone", origin, repo)
	// A clone does not copy the seed's local identity, and CI runners have no
	// global git identity to fall back on, so commit/commit-tree in this repo's
	// worktrees would fail with "empty ident name". Set it on the clone; worktrees
	// inherit the common dir config.
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")
	runGit(t, git, repo, "checkout", "main")
	return repo
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	run(t, git, append([]string{"-C", dir}, args...)...)
}

func run(t *testing.T, binary string, args ...string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", binary, strings.Join(args, " "), err, out)
	}
}

// TestWorkspaceIntegrationUncommittedFiles proves, against real git, that the
// list a person is shown before discarding is exactly the set that blocked the
// teardown - and that it is a LIST, with each file's fate named.
//
// Three shapes against real git: a tracked edit, a brand-new file, and build
// output a rebuild reproduces. Only the last is absent from the answer, because
// it is the only one nothing is lost by. (statusWord's full code table is unit
// tested next door.)
func TestWorkspaceIntegrationUncommittedFiles(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{
		Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo},
		ArtifactPatterns: func() []string { return []string{"derivedDataPath"} },
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/listing"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A clean tree loses nothing, and must say so rather than refusing a kill.
	files, err := ws.UncommittedFiles(ctx, info)
	if err != nil {
		t.Fatalf("UncommittedFiles on a clean tree: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("clean worktree reported %+v; a kill would be refused over nothing", files)
	}

	seeded := filepath.Join(info.Path, "README.md")
	if _, statErr := os.Stat(seeded); statErr != nil {
		t.Fatalf("fixture repo has no README.md to modify: %v", statErr)
	}
	if err := os.WriteFile(seeded, []byte("edited by the agent\n"), 0o600); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "NewFile.swift"), []byte("struct New {}\n"), 0o600); err != nil {
		t.Fatalf("write new file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(info.Path, "derivedDataPath"), 0o750); err != nil {
		t.Fatalf("mkdir build output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "derivedDataPath", "cache.bin"), []byte("rebuildable\n"), 0o600); err != nil {
		t.Fatalf("write build output: %v", err)
	}

	files, err = ws.UncommittedFiles(ctx, info)
	if err != nil {
		t.Fatalf("UncommittedFiles: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Status
	}
	if got["README.md"] != ports.UncommittedModified {
		t.Fatalf("tracked edit = %q, want %q; files=%+v", got["README.md"], ports.UncommittedModified, files)
	}
	if got["NewFile.swift"] != ports.UncommittedUntracked {
		t.Fatalf("new file = %q, want %q; files=%+v", got["NewFile.swift"], ports.UncommittedUntracked, files)
	}
	if _, listed := got["derivedDataPath/"]; listed {
		t.Fatalf("regenerable build output was listed as work to lose: %+v", files)
	}

	// And the list agrees with the refusal it explains: the same tree makes the
	// non-force Destroy refuse. Two answers to one question is how they drift.
	if err := ws.Destroy(ctx, info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("Destroy = %v, want ErrWorkspaceDirty over the same files UncommittedFiles listed", err)
	}

	// A worktree that is already gone loses nothing either: a reclaimed session
	// keeps its path for ever, and must not refuse a kill because of it.
	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatalf("force destroy: %v", err)
	}
	files, err = ws.UncommittedFiles(ctx, info)
	if err != nil || len(files) != 0 {
		t.Fatalf("absent worktree = %+v, %v; want an empty list and no error", files, err)
	}
}
