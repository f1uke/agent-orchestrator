package sessionmanager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// renamedBranchFixture is a worker whose branch was renamed after spawn, against
// a REAL git worktree: spawned on feature/chat-logout-storm, its worktree lives
// at .../feature/chat-logout-storm, and the branch is now hotfix/chat-logout-storm.
// The managed path derived from the CURRENT branch is .../hotfix/chat-logout-storm,
// which is not where the session's worktree is.
type renamedBranchFixture struct {
	m       *Manager
	st      *fakeStore
	rt      *fakeRuntime
	git     string
	path    string // the worktree the session owns
	derived string // the path the current branch name would derive
}

const (
	spawnBranch   = "feature/chat-logout-storm"
	renamedBranch = "hotfix/chat-logout-storm"
	inFlightWork  = "uncommitted agent work\n"
)

func newRenamedBranchFixture(t *testing.T) renamedBranchFixture {
	t.Helper()
	git := requireGitBinary(t)
	tmp := t.TempDir()
	repo := seedGitRepo(t, git, filepath.Join(tmp, "repo"))
	managed := filepath.Join(tmp, "managed")

	realWS, err := gitworktree.New(gitworktree.Options{
		Binary:       git,
		ManagedRoot:  managed,
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

	info, err := realWS.Create(ctx, ports.WorkspaceConfig{ProjectID: "mer", SessionID: "mer-1", Branch: spawnBranch})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "in-flight.txt"), []byte(inFlightWork), 0o600); err != nil {
		t.Fatalf("write uncommitted work: %v", err)
	}
	// The agent renames its branch from inside its worktree.
	gitOutput(t, git, info.Path, "branch", "-m", renamedBranch)

	resolvedManaged, err := filepath.EvalSymlinks(managed)
	if err != nil {
		t.Fatalf("resolve managed root: %v", err)
	}
	return renamedBranchFixture{
		m: m, st: st, rt: rt, git: git, path: info.Path,
		derived: filepath.Join(resolvedManaged, "mer", "hotfix", "chat-logout-storm"),
	}
}

func (f renamedBranchFixture) record() domain.SessionRecord {
	return domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{
			WorkspacePath: f.path, Branch: renamedBranch,
			AgentSessionID: "agent-x", RuntimeHandleID: "h1",
		},
	}
}

// assertResumedInOwnWorktree: the session came back in the worktree it owns -
// same cwd (Claude Code keys its transcripts by cwd), on the renamed branch,
// with the uncommitted work intact - and nothing was materialised at the path
// the new branch name would derive.
func (f renamedBranchFixture) assertResumedInOwnWorktree(t *testing.T, rec domain.SessionRecord) {
	t.Helper()
	if rec.Metadata.WorkspacePath != f.path {
		t.Fatalf("workspace path = %q, want the session's own worktree %q", rec.Metadata.WorkspacePath, f.path)
	}
	if rec.Metadata.Branch != renamedBranch {
		t.Fatalf("branch = %q, want %q", rec.Metadata.Branch, renamedBranch)
	}
	if f.rt.lastCfg.WorkspacePath != f.path {
		t.Fatalf("agent launched in %q, want %q (a different cwd loses the conversation)", f.rt.lastCfg.WorkspacePath, f.path)
	}
	if got := gitOutput(t, f.git, f.path, "rev-parse", "--abbrev-ref", "HEAD"); got != renamedBranch {
		t.Fatalf("worktree HEAD = %q, want %q", got, renamedBranch)
	}
	assertFileBytes(t, filepath.Join(f.path, "in-flight.txt"), inFlightWork)
	if _, err := os.Stat(f.derived); !os.IsNotExist(err) {
		t.Fatalf("restore materialised the branch-derived path %q (stat err = %v); it must reuse %q", f.derived, err, f.path)
	}
}

// TestRestore_RenamedBranchReusesOwnWorktree reproduces the field failure:
// restoring a terminated worker whose branch was renamed after spawn failed with
// BRANCH_CHECKED_OUT_ELSEWHERE, the "other" worktree being the session's own,
// because Restore re-derived the worktree path from the current branch name
// instead of using the path the session recorded.
func TestRestore_RenamedBranchReusesOwnWorktree(t *testing.T) {
	f := newRenamedBranchFixture(t)
	rec := f.record()
	rec.IsTerminated = true
	rec.Activity = domain.Activity{State: domain.ActivityExited}
	f.st.sessions["mer-1"] = rec

	out, err := f.m.Restore(ctx, "mer-1")
	if err != nil {
		t.Fatalf("Restore after a branch rename: %v", err)
	}
	f.assertResumedInOwnWorktree(t, out)
}

// TestRestore_RenamedBranchRecreatesRemovedWorktreeAtRecordedPath: when the
// worktree itself is gone (a kill or shutdown teardown removed it), it is
// recreated where the session recorded it, not at the branch-derived path, so
// the agent's cwd - and its resumable conversation - does not move.
func TestRestore_RenamedBranchRecreatesRemovedWorktreeAtRecordedPath(t *testing.T) {
	f := newRenamedBranchFixture(t)
	repo := f.st.projects["mer"].Path
	gitOutput(t, f.git, repo, "worktree", "remove", "--force", f.path)
	rec := f.record()
	rec.IsTerminated = true
	rec.Activity = domain.Activity{State: domain.ActivityExited}
	f.st.sessions["mer-1"] = rec

	out, err := f.m.Restore(ctx, "mer-1")
	if err != nil {
		t.Fatalf("Restore after a branch rename and worktree removal: %v", err)
	}
	if out.Metadata.WorkspacePath != f.path || f.rt.lastCfg.WorkspacePath != f.path {
		t.Fatalf("restored into %q (agent cwd %q), want the recorded path %q", out.Metadata.WorkspacePath, f.rt.lastCfg.WorkspacePath, f.path)
	}
	if got := gitOutput(t, f.git, f.path, "rev-parse", "--abbrev-ref", "HEAD"); got != renamedBranch {
		t.Fatalf("worktree HEAD = %q, want %q", got, renamedBranch)
	}
	if _, err := os.Stat(f.derived); !os.IsNotExist(err) {
		t.Fatalf("restore materialised the branch-derived path %q (stat err = %v)", f.derived, err)
	}
}

// TestResume_RenamedBranchReusesOwnWorktree: resuming a suspended session goes
// through the same workspace restore, so it must not re-derive the path either.
func TestResume_RenamedBranchReusesOwnWorktree(t *testing.T) {
	f := newRenamedBranchFixture(t)
	rec := f.record()
	rec.IsSuspended = true
	f.st.sessions["mer-1"] = rec

	out, err := f.m.Resume(ctx, "mer-1", domain.WokenByView)
	if err != nil {
		t.Fatalf("Resume after a branch rename: %v", err)
	}
	f.assertResumedInOwnWorktree(t, out)
}

// TestRestoreAll_RenamedBranchReusesOwnWorktree: the boot-time restore of a
// shutdown-saved session has its own workspace.Restore call and must use the
// recorded path too.
func TestRestoreAll_RenamedBranchReusesOwnWorktree(t *testing.T) {
	f := newRenamedBranchFixture(t)
	rec := f.record()
	rec.IsTerminated = true
	rec.Activity = domain.Activity{State: domain.ActivityExited}
	f.st.sessions["mer-1"] = rec
	f.st.worktrees["mer-1"] = []domain.SessionWorktreeRecord{{
		SessionID: "mer-1", RepoName: domain.RootWorkspaceRepoName, Branch: renamedBranch, WorktreePath: f.path, State: "removed",
	}}

	if err := f.m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	if f.rt.created != 1 {
		t.Fatalf("RestoreAll relaunched %d session(s), want 1 (the workspace restore failed and the session was skipped)", f.rt.created)
	}
	f.assertResumedInOwnWorktree(t, f.st.sessions["mer-1"])
}
