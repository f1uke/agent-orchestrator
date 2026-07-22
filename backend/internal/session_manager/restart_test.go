package sessionmanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestRestart_DirtyWorktreeRelaunchesOnFirstCall pins the fix for the
// first-click-restart failure. Restart used to be Kill+Restore, and Kill bails
// out on ErrWorkspaceDirty AFTER destroying the runtime but BEFORE marking the
// session terminated — so the agent was already dead while Restore still saw a
// live record and refused with ErrNotRestorable. Every real worker has
// uncommitted changes, so that was the common case, and nothing flips the
// session to terminated afterwards (Reconcile only runs at daemon boot), which
// left the session permanently unrestartable with a dead terminal.
//
// Restart is now a runtime-only recycle: it never asks the workspace to tear
// down, so a dirty worktree is simply not a question it can fail on.
func TestRestart_DirtyWorktreeRelaunchesOnFirstCall(t *testing.T) {
	m, st, rt, ws := newManager()
	// Any workspace teardown during a restart is a bug: fail it the way a real
	// dirty worktree does, so a regression re-introduces the original 409.
	ws.destroyErr = ports.ErrWorkspaceDirty
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	rec, err := m.Restart(ctx, "mer-1")
	if err != nil {
		t.Fatalf("first-click Restart on a dirty worktree: %v", err)
	}
	if ws.destroyed != 0 {
		t.Fatalf("restart tore the workspace down %d time(s); it must never touch the worktree (uncommitted work lives there)", ws.destroyed)
	}
	if rt.destroyed != 1 || rt.created != 1 {
		t.Fatalf("restart should recycle the runtime exactly once: destroyed=%d created=%d, want 1/1", rt.destroyed, rt.created)
	}
	if rec.ID != "mer-1" {
		t.Fatalf("restarted id = %q, want mer-1 (restart keeps the same session)", rec.ID)
	}
	if rec.IsTerminated {
		t.Fatal("restarted session must be live, not terminated")
	}
	if rec.Metadata.WorkspacePath != "/ws/mer-1" || rec.Metadata.Branch != "b" {
		t.Fatalf("restart moved the session: workspace=%q branch=%q, want /ws/mer-1 and b", rec.Metadata.WorkspacePath, rec.Metadata.Branch)
	}
	if !slices.Contains(rt.lastCfg.Argv, "resume") {
		t.Fatalf("relaunch argv = %v, want the agent's resume command (the conversation must continue)", rt.lastCfg.Argv)
	}
}

// TestRestart_LeavesRestoreMarkersAndReviewerAlone: the kill leg used to delete
// the session's boot-restore markers and reap its reviewer pane, both of which
// are teardown-only concerns. A restarted session never stops existing, so both
// must survive it.
func TestRestart_LeavesRestoreMarkersAndReviewerAlone(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{{
		SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: "b", WorktreePath: "/ws/mer-1", State: "active",
	}}
	reviewerReaped := 0
	m.SetReviewerReaper(func(context.Context, domain.SessionID) error { reviewerReaped++; return nil })

	if _, err := m.Restart(ctx, "mer-1"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(st.worktrees["mer-1"]) == 0 {
		t.Fatal("restart deleted the session's restore markers; the session is still live and must keep them")
	}
	if reviewerReaped != 0 {
		t.Fatalf("restart reaped the reviewer pane %d time(s); the worker is coming right back", reviewerReaped)
	}
}

// TestRestart_IncompleteHandleRefusesWithoutKillingTheAgent: a live session
// whose spawn failed before the workspace landed has nowhere to relaunch into.
// Restart used to route through Kill, so an impossible restart still destroyed
// the running agent before surfacing the error. It now refuses up front and
// leaves the session exactly as it found it.
func TestRestart_IncompleteHandleRefusesWithoutKillingTheAgent(t *testing.T) {
	m, st, rt, ws := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"}, // no workspace path, no branch
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if _, err := m.Restart(ctx, "mer-1"); !errors.Is(err, ErrIncompleteHandle) {
		t.Fatalf("Restart with an incomplete handle = %v, want ErrIncompleteHandle", err)
	}
	if rt.destroyed != 0 || ws.destroyed != 0 {
		t.Fatalf("a refused restart must not tear anything down, got runtime=%d workspace=%d", rt.destroyed, ws.destroyed)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("a refused restart must leave the session live, not terminate it")
	}
}

// TestRestart_RealDirtyWorktreePreservesUncommittedWork is the
// non-negotiable invariant, proven against a REAL git worktree rather than a
// fake: a worker's uncommitted changes are the whole value of its worktree, so a
// first-click Restart must leave every uncommitted file byte-for-byte intact,
// on the same branch, at the same path, under the same session id, and resume
// the conversation.
func TestRestart_RealDirtyWorktreePreservesUncommittedWork(t *testing.T) {
	git := requireGitBinary(t)
	tmp := t.TempDir()
	repo := seedGitRepo(t, git, filepath.Join(tmp, "repo"))

	realWS, err := gitworktree.New(gitworktree.Options{
		Binary:       git,
		ManagedRoot:  filepath.Join(tmp, "managed"),
		RepoResolver: gitworktree.StaticRepoResolver{"mer": repo},
	})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}

	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: repo, Config: testRoleAgents()}
	rt := &fakeRuntime{}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: realWS, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	info, err := realWS.Create(ctx, ports.WorkspaceConfig{ProjectID: "mer", SessionID: "mer-1", Branch: "feature/dirty"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	// The in-progress work: one untracked file and one modified tracked file.
	const untracked = "in-flight analysis\nline two\n"
	const modified = "seed\nedited by the agent\n"
	if err := os.WriteFile(filepath.Join(info.Path, "work-in-progress.txt"), []byte(untracked), 0o600); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "README.md"), []byte(modified), 0o600); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}

	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{
			WorkspacePath: info.Path, Branch: info.Branch,
			AgentSessionID: "agent-x", RuntimeHandleID: "h1",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	rec, err := m.Restart(ctx, "mer-1")
	if err != nil {
		t.Fatalf("first-click Restart on a real dirty worktree: %v", err)
	}

	// The invariant: uncommitted work survives byte-for-byte.
	assertFileBytes(t, filepath.Join(info.Path, "work-in-progress.txt"), untracked)
	assertFileBytes(t, filepath.Join(info.Path, "README.md"), modified)

	// Same session, same worktree, same branch.
	if rec.ID != "mer-1" {
		t.Fatalf("session id = %q, want mer-1", rec.ID)
	}
	if rec.Metadata.WorkspacePath != info.Path {
		t.Fatalf("workspace path = %q, want %q", rec.Metadata.WorkspacePath, info.Path)
	}
	if rec.Metadata.Branch != "feature/dirty" {
		t.Fatalf("branch = %q, want feature/dirty", rec.Metadata.Branch)
	}
	if rec.IsTerminated {
		t.Fatal("restarted session must be live")
	}
	// The worktree is still git's, still on its branch, still dirty.
	if got := gitOutput(t, git, info.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature/dirty" {
		t.Fatalf("worktree HEAD = %q, want feature/dirty", got)
	}
	if status := gitOutput(t, git, info.Path, "status", "--porcelain"); status == "" {
		t.Fatal("worktree is clean after restart; the agent's uncommitted work was discarded")
	}
	// And the conversation resumes rather than starting over.
	if !slices.Contains(rt.lastCfg.Argv, "resume") {
		t.Fatalf("relaunch argv = %v, want the agent's resume command", rt.lastCfg.Argv)
	}
	if rt.lastCfg.WorkspacePath != info.Path {
		t.Fatalf("relaunched in %q, want the original worktree %q", rt.lastCfg.WorkspacePath, info.Path)
	}
}

func assertFileBytes(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatalf("read %s after restart: %v", filepath.Base(path), err)
	}
	if string(got) != want {
		t.Fatalf("%s after restart = %q, want %q (uncommitted work must survive byte-for-byte)", filepath.Base(path), got, want)
	}
}

func requireGitBinary(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	return git
}

// seedGitRepo creates a one-commit repo on main at dir and returns its path.
func seedGitRepo(t *testing.T, git, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGitIn(t, git, dir, "init")
	runGitIn(t, git, dir, "config", "user.email", "ao@example.com")
	runGitIn(t, git, dir, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	runGitIn(t, git, dir, "add", "README.md")
	runGitIn(t, git, dir, "commit", "-m", "seed")
	runGitIn(t, git, dir, "branch", "-M", "main")
	return dir
}

func runGitIn(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, args...) //nolint:gosec // test-owned git invocation
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitOutput(t *testing.T, git, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, args...) //nolint:gosec // test-owned git invocation
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
