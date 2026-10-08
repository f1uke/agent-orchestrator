package integration

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/branchfollow"
	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// A worker renames its branch in its worktree (git branch -m). Within one pass
// of the follow loop, with no restart, the session records the new name, PR
// discovery keys on it, Files measures it without a "branch missing" notice, and
// a restore brings the session back on it in the worktree it already had.
func TestSessionFollowsABranchRenamedInItsWorktree(t *testing.T) {
	st := newCrewStack(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	proj, _, err := st.store.GetProject(ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	proj.RepoOriginURL = scmTestOriginURL
	if err := st.store.UpsertProject(ctx, proj); err != nil {
		t.Fatal(err)
	}
	rec, err := st.mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Branch: "feature/ABC-1-fix", Prompt: "fix it",
		TaskSize: domain.TaskSizeMechanical,
	})
	if err != nil {
		t.Fatal(err)
	}
	worktree := rec.Metadata.WorkspacePath
	gitIn := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", worktree}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitIn("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "work")
	gitIn("branch", "-m", "feature/ABC-2-fix")

	if err := branchfollow.New(st.store, st.svc, branchfollow.Config{Logger: quiet}).Poll(ctx); err != nil {
		t.Fatal(err)
	}
	got, _, err := st.store.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Branch != "feature/ABC-2-fix" {
		t.Fatalf("recorded branch = %q, want feature/ABC-2-fix", got.Metadata.Branch)
	}

	// The observer's live-worktree candidate is switched off, so the record is
	// the only thing that can attribute a PR from the new branch.
	provider := newCannedSCMProvider()
	provider.detected["feature/ABC-2-fix"] = detectedPR("https://github.com/octocat/hello/pull/7", 7, "feature/ABC-2-fix", "main", gitIn("rev-parse", "HEAD"))
	observer := scmobserve.New(provider, st.store, st.lcm, scmobserve.Config{
		Tick: time.Hour, Logger: quiet, WorktreeBranch: func(string) string { return "" },
	})
	if err := observer.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	prs, err := st.store.ListPRsBySession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 7 {
		t.Fatalf("PRs attributed to the session = %+v, want #7 from feature/ABC-2-fix", prs)
	}

	changes, err := st.svc.WorkspaceChanges(ctx, rec.ID, sessionsvc.WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if changes.Branch != "feature/ABC-2-fix" || changes.BranchMissing || changes.HeadState != sessionsvc.HeadOnBranch {
		t.Fatalf("Files scope = branch %q missing %v head %q, want feature/ABC-2-fix measured with HEAD on it",
			changes.Branch, changes.BranchMissing, changes.HeadState)
	}

	if err := st.lcm.MarkTerminated(ctx, rec.ID, domain.TerminationCauseDaemonShutdown); err != nil {
		t.Fatal(err)
	}
	restored, err := st.mgr.Restore(ctx, rec.ID)
	if err != nil {
		t.Fatalf("restore after the rename: %v", err)
	}
	if restored.Metadata.Branch != "feature/ABC-2-fix" || restored.Metadata.WorkspacePath != worktree {
		t.Fatalf("restored on branch %q at %q, want feature/ABC-2-fix at %q",
			restored.Metadata.Branch, restored.Metadata.WorkspacePath, worktree)
	}
	if head := gitIn("branch", "--show-current"); head != "feature/ABC-2-fix" {
		t.Fatalf("restored worktree is on %q, want feature/ABC-2-fix", head)
	}
}
