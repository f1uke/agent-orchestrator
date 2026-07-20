package gitworktree

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// setupForkedOrigin builds the shape the real bug lives in: an upstream default
// branch ("main") that lags, and the project's ACTUAL default branch
// ("main-fluke") two commits ahead of it. Both exist on origin and locally. The
// returned repo has main-fluke CHECKED OUT, because that is the normal state of
// a user's main checkout — and it is what makes the orchestrator worktree unable
// to simply check out the default branch itself.
//
// The branch name is deliberately NOT "main"/"master": a sync that hardcodes a
// branch name passes against a default-named branch and fails here.
func setupForkedOrigin(t *testing.T, git, tmp string) (repo, defaultBranch, staleSHA string) {
	t.Helper()
	repo = setupOriginClone(t, git, tmp)
	defaultBranch = "main-fluke"

	// The commit "main" sits at — where a stale orchestrator branch was cut from.
	staleSHA = revParse(t, git, repo, "HEAD")

	runGit(t, git, repo, "checkout", "-b", defaultBranch)
	writeCommit(t, git, repo, "one.txt", "one\n", "advance 1")
	writeCommit(t, git, repo, "two.txt", "two\n", "advance 2")
	runGit(t, git, repo, "push", "-u", "origin", defaultBranch)
	// Leave the default branch checked out in the main checkout.
	return repo, defaultBranch, staleSHA
}

func writeCommit(t *testing.T, git, dir, name, body, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGit(t, git, dir, "add", name)
	runGit(t, git, dir, "commit", "-m", msg)
}

func revParse(t *testing.T, git, dir, ref string) string {
	t.Helper()
	out, err := runCommand(context.Background(), git, "-C", dir, "rev-parse", "--verify", ref)
	if err != nil {
		t.Fatalf("rev-parse %s in %s: %v", ref, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// newOrchestratorWorkspace builds a Workspace plus the orchestrator config that
// session_manager would hand it.
func newOrchestratorWorkspace(t *testing.T, git, tmp, repo, defaultBranch string) (*Workspace, ports.WorkspaceConfig) {
	t.Helper()
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return ws, ports.WorkspaceConfig{
		ProjectID:     "proj",
		Kind:          domain.KindOrchestrator,
		SessionPrefix: "proj",
		Branch:        "ao/proj-orchestrator",
		BaseBranch:    defaultBranch,
	}
}

// TestSyncToBaseFastForwardsStaleOrchestratorBranch is the regression test for
// the reported bug, reproduced exactly: an orchestrator branch that already
// exists at an OLD commit (the live one read "branch: Created from origin/main")
// is re-checked-out by a new orchestrator session and stays frozen there, so the
// orchestrator answers questions from code that is 268 commits out of date.
//
// Whole files that exist on the default branch are absent from that tree — which
// is how the orchestrator came to tell the human a code path "does not exist".
// The assertion below is that shape: a file present on main-fluke and absent
// from the stale commit must be readable in the worktree after the sync.
func TestSyncToBaseFastForwardsStaleOrchestratorBranch(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo, defaultBranch, staleSHA := setupForkedOrigin(t, git, tmp)

	// The orchestrator branch already exists, frozen at the old commit.
	runGit(t, git, repo, "branch", "ao/proj-orchestrator", staleSHA)

	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Precondition: without a sync the worktree really is stale. If this ever
	// stops holding, the test is no longer reproducing the bug.
	if got := revParse(t, git, info.Path, "HEAD"); got != staleSHA {
		t.Fatalf("precondition: worktree HEAD = %s, want the stale commit %s", got, staleSHA)
	}

	res, err := ws.SyncToBase(ctx, info, defaultBranch)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Outcome != ports.WorkspaceSyncUpdated {
		t.Fatalf("outcome = %q (reason %q), want %q", res.Outcome, res.Reason, ports.WorkspaceSyncUpdated)
	}

	want := revParse(t, git, repo, "origin/"+defaultBranch)
	if got := revParse(t, git, info.Path, "HEAD"); got != want {
		t.Fatalf("worktree HEAD = %s, want default-branch head %s", got, want)
	}
	// The file-level harm: content that exists on the default branch must now be
	// visible in the orchestrator's tree.
	if _, err := os.Stat(filepath.Join(info.Path, "two.txt")); err != nil {
		t.Fatalf("file present on %s missing from synced worktree: %v", defaultBranch, err)
	}
	if res.FromSHA != staleSHA || res.ToSHA != want {
		t.Fatalf("result SHAs = %s -> %s, want %s -> %s", res.FromSHA, res.ToSHA, staleSHA, want)
	}
}

// TestSyncToBaseWhileDefaultBranchCheckedOutElsewhere pins the constraint that
// shapes the whole design: the project's default branch is ALREADY checked out
// in the user's main repo checkout, so the orchestrator worktree can never check
// it out itself. The sync must move the orchestrator's OWN branch to that
// commit, and must not disturb the other worktree holding the default branch.
func TestSyncToBaseWhileDefaultBranchCheckedOutElsewhere(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo, defaultBranch, staleSHA := setupForkedOrigin(t, git, tmp)
	runGit(t, git, repo, "branch", "ao/proj-orchestrator", staleSHA)

	// Prove the premise rather than assume it: checking the default branch out a
	// second time is refused by git.
	if out, err := runCommand(context.Background(), git, "-C", repo, "worktree", "add", filepath.Join(tmp, "second"), defaultBranch); err == nil {
		t.Fatalf("expected git to refuse a second checkout of %s, got success: %s", defaultBranch, out)
	}

	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := ws.SyncToBase(ctx, info, defaultBranch)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Outcome != ports.WorkspaceSyncUpdated {
		t.Fatalf("outcome = %q (reason %q), want %q", res.Outcome, res.Reason, ports.WorkspaceSyncUpdated)
	}

	// The orchestrator worktree shows the default branch's content...
	head := revParse(t, git, info.Path, "HEAD")
	if want := revParse(t, git, repo, "origin/"+defaultBranch); head != want {
		t.Fatalf("orchestrator HEAD = %s, want %s", head, want)
	}
	// ...while still being on its OWN branch, not the default branch.
	out, err := runCommand(ctx, git, "-C", info.Path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("abbrev-ref: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "ao/proj-orchestrator" {
		t.Fatalf("orchestrator branch = %q, want ao/proj-orchestrator", got)
	}
	// ...and the main checkout still holds the default branch, undisturbed.
	out, err = runCommand(ctx, git, "-C", repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("abbrev-ref repo: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != defaultBranch {
		t.Fatalf("main checkout branch = %q, want %q", got, defaultBranch)
	}
}

// TestSyncToBaseSkipsDirtyWorktree covers requirement 4: an orchestrator should
// not be editing files, but "should not" is not "cannot". Uncommitted work is
// never discarded, and the skip is reported rather than swallowed.
func TestSyncToBaseSkipsDirtyWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo, defaultBranch, staleSHA := setupForkedOrigin(t, git, tmp)
	runGit(t, git, repo, "branch", "ao/proj-orchestrator", staleSHA)

	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	scratch := filepath.Join(info.Path, "README.md")
	if err := os.WriteFile(scratch, []byte("precious uncommitted work\n"), 0o644); err != nil {
		t.Fatalf("dirty the worktree: %v", err)
	}

	res, err := ws.SyncToBase(ctx, info, defaultBranch)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Outcome != ports.WorkspaceSyncSkipped || res.Reason != ports.WorkspaceSyncReasonDirty {
		t.Fatalf("outcome/reason = %q/%q, want %q/%q", res.Outcome, res.Reason, ports.WorkspaceSyncSkipped, ports.WorkspaceSyncReasonDirty)
	}
	// The uncommitted edit survives, byte for byte.
	body, err := os.ReadFile(scratch)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "precious uncommitted work\n" {
		t.Fatalf("uncommitted work was destroyed: %q", body)
	}
	if got := revParse(t, git, info.Path, "HEAD"); got != staleSHA {
		t.Fatalf("HEAD moved to %s despite dirty worktree, want %s", got, staleSHA)
	}
	// The skip must be legible as staleness, not as success.
	if !res.Stale() {
		t.Fatalf("Stale() = false, want true — a skipped update on a behind branch must read as stale: %#v", res)
	}
}

// TestSyncToBaseSkipsDivergedBranch covers the other half of requirement 4:
// committed work on the orchestrator branch is not discarded either. A
// fast-forward exists exactly when the branch has no commits of its own, so
// refusing a non-fast-forward is what makes "never destroys work" provable
// rather than best-effort.
func TestSyncToBaseSkipsDivergedBranch(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo, defaultBranch, staleSHA := setupForkedOrigin(t, git, tmp)
	runGit(t, git, repo, "branch", "ao/proj-orchestrator", staleSHA)

	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// A commit that exists only on the orchestrator branch.
	writeCommit(t, git, info.Path, "local.txt", "local only\n", "orchestrator-only commit")
	diverged := revParse(t, git, info.Path, "HEAD")

	res, err := ws.SyncToBase(ctx, info, defaultBranch)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Outcome != ports.WorkspaceSyncSkipped || res.Reason != ports.WorkspaceSyncReasonDiverged {
		t.Fatalf("outcome/reason = %q/%q, want %q/%q", res.Outcome, res.Reason, ports.WorkspaceSyncSkipped, ports.WorkspaceSyncReasonDiverged)
	}
	if got := revParse(t, git, info.Path, "HEAD"); got != diverged {
		t.Fatalf("HEAD = %s, want the diverged commit %s kept intact", got, diverged)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "local.txt")); err != nil {
		t.Fatalf("committed work was destroyed: %v", err)
	}
	if !res.Stale() {
		t.Fatalf("Stale() = false, want true for a diverged (therefore behind) branch: %#v", res)
	}
}

// TestSyncToBaseAlreadyCurrentIsNotReportedAsUpdated keeps the outcome honest:
// a no-op must be distinguishable from a real advance, otherwise the logs that
// make staleness visible cannot be trusted.
func TestSyncToBaseAlreadyCurrentIsNotReportedAsUpdated(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo, defaultBranch, _ := setupForkedOrigin(t, git, tmp)

	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// First sync brings it current; the second has nothing to do.
	if _, err := ws.SyncToBase(ctx, info, defaultBranch); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	res, err := ws.SyncToBase(ctx, info, defaultBranch)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if res.Outcome != ports.WorkspaceSyncAlreadyCurrent {
		t.Fatalf("outcome = %q, want %q", res.Outcome, ports.WorkspaceSyncAlreadyCurrent)
	}
	if res.Stale() {
		t.Fatalf("Stale() = true for an up-to-date worktree: %#v", res)
	}
}

// TestSyncToBaseWithoutBaseBranchSkips: nothing to sync to is a skip with a
// reason, never a silent success.
func TestSyncToBaseWithoutBaseBranchSkips(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo, defaultBranch, staleSHA := setupForkedOrigin(t, git, tmp)
	runGit(t, git, repo, "branch", "ao/proj-orchestrator", staleSHA)

	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := ws.SyncToBase(ctx, info, "   ")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Outcome != ports.WorkspaceSyncSkipped || res.Reason != ports.WorkspaceSyncReasonNoBaseBranch {
		t.Fatalf("outcome/reason = %q/%q, want %q/%q", res.Outcome, res.Reason, ports.WorkspaceSyncSkipped, ports.WorkspaceSyncReasonNoBaseBranch)
	}
}

// TestSyncToBaseUnknownBaseBranchSkips: a base branch that resolves to no ref at
// all must be reported, not treated as "nothing to do".
func TestSyncToBaseUnknownBaseBranchSkips(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo, defaultBranch, staleSHA := setupForkedOrigin(t, git, tmp)
	runGit(t, git, repo, "branch", "ao/proj-orchestrator", staleSHA)

	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := ws.SyncToBase(ctx, info, "branch-that-does-not-exist")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Outcome != ports.WorkspaceSyncSkipped || res.Reason != ports.WorkspaceSyncReasonBaseUnreachable {
		t.Fatalf("outcome/reason = %q/%q, want %q/%q", res.Outcome, res.Reason, ports.WorkspaceSyncSkipped, ports.WorkspaceSyncReasonBaseUnreachable)
	}
}

// TestSyncToBaseLogsEverySkipThatLeavesTheTreeStale pins requirement 5 at the
// adapter: silently staying stale is the exact bug being fixed, so a skip that
// leaves the worktree behind its base MUST be audible. This test exists because
// the dirty path originally returned without logging while the diverged path
// logged — the same staleness, half of it silent.
func TestSyncToBaseLogsEverySkipThatLeavesTheTreeStale(t *testing.T) {
	git := requireGit(t)
	for _, tc := range []struct {
		name       string
		wantReason string
		arrange    func(t *testing.T, git, worktreePath string)
	}{
		{
			name:       "dirty",
			wantReason: ports.WorkspaceSyncReasonDirty,
			arrange: func(t *testing.T, _, path string) {
				if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("edited\n"), 0o644); err != nil {
					t.Fatalf("dirty: %v", err)
				}
			},
		},
		{
			name:       "diverged",
			wantReason: ports.WorkspaceSyncReasonDiverged,
			arrange: func(t *testing.T, git, path string) {
				writeCommit(t, git, path, "local.txt", "local\n", "orchestrator-only commit")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			repo, defaultBranch, staleSHA := setupForkedOrigin(t, git, tmp)
			runGit(t, git, repo, "branch", "ao/proj-orchestrator", staleSHA)
			ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
			ctx := context.Background()
			info, err := ws.Create(ctx, cfg)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			tc.arrange(t, git, info.Path)

			var logged bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			res, err := ws.SyncToBase(ctx, info, defaultBranch)
			if err != nil {
				t.Fatalf("sync: %v", err)
			}
			if res.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", res.Reason, tc.wantReason)
			}
			out := logged.String()
			if out == "" {
				t.Fatalf("a %s skip left the worktree stale and logged NOTHING — staying silently stale is the bug being fixed", tc.name)
			}
			if !strings.Contains(out, "STALE") || !strings.Contains(out, tc.wantReason) {
				t.Fatalf("skip log does not name the staleness or its reason: %s", out)
			}
		})
	}
}

// TestSyncToBasePicksUpCommitsPushedAfterCreate proves the sync actually
// FETCHES rather than only reading refs the repo already had. A sync that
// skipped the fetch would leave the worktree at the pre-push commit.
func TestSyncToBasePicksUpCommitsPushedAfterCreate(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo, defaultBranch, staleSHA := setupForkedOrigin(t, git, tmp)
	runGit(t, git, repo, "branch", "ao/proj-orchestrator", staleSHA)

	ws, cfg := newOrchestratorWorkspace(t, git, tmp, repo, defaultBranch)
	ctx := context.Background()
	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Someone else pushes to the default branch, from a separate clone, AFTER the
	// orchestrator worktree exists. Our repo has not fetched it.
	other := filepath.Join(tmp, "other")
	run(t, git, "clone", filepath.Join(tmp, "origin.git"), other)
	runGit(t, git, other, "config", "user.email", "ao@example.com")
	runGit(t, git, other, "config", "user.name", "Ao Agents")
	runGit(t, git, other, "checkout", defaultBranch)
	writeCommit(t, git, other, "three.txt", "three\n", "advance 3")
	runGit(t, git, other, "push", "origin", defaultBranch)
	pushed := revParse(t, git, other, "HEAD")

	res, err := ws.SyncToBase(ctx, info, defaultBranch)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Outcome != ports.WorkspaceSyncUpdated {
		t.Fatalf("outcome = %q (reason %q), want %q", res.Outcome, res.Reason, ports.WorkspaceSyncUpdated)
	}
	if got := revParse(t, git, info.Path, "HEAD"); got != pushed {
		t.Fatalf("worktree HEAD = %s, want the just-pushed commit %s — the sync did not fetch", got, pushed)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "three.txt")); err != nil {
		t.Fatalf("commit pushed after create is missing from the worktree: %v", err)
	}
}
