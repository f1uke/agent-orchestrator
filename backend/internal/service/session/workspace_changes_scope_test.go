package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// runGitIn runs git in dir, failing the test on error.
func runGitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// scopeTestRepo builds main + feature/x, where feature/x carries COMMITTED work
// and the worktree is left clean, so a test can move HEAD wherever it likes.
func scopeTestRepo(t *testing.T) (dir, baseSHA string) {
	t.Helper()
	dir = t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitIn(t, dir, "init", "-q")
	runGitIn(t, dir, "config", "user.email", "t@t")
	runGitIn(t, dir, "config", "user.name", "t")
	write("keep.go", "l1\nl2\nl3\n")
	runGitIn(t, dir, "add", "-A")
	runGitIn(t, dir, "commit", "-qm", "base")
	runGitIn(t, dir, "branch", "-M", "main")
	baseSHA = runGitIn(t, dir, "rev-parse", "HEAD")

	runGitIn(t, dir, "checkout", "-qb", "feature/x")
	write("keep.go", "l1\nCHANGED\nl3\n")
	write("added.go", "brand new\n")
	runGitIn(t, dir, "add", "-A")
	runGitIn(t, dir, "commit", "-qm", "work")
	return dir, baseSHA
}

// scopeService seeds a session whose Metadata.Branch is branch (empty for a row
// that never recorded one) and whose PR targets main.
func scopeService(t *testing.T, dir, branch string) *Service {
	t.Helper()
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	rec := fake.sessions["s1"]
	rec.Metadata.Branch = branch
	fake.sessions["s1"] = rec
	return newServiceWithStore(t, &multiPRFakeStore{
		fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1", TargetBranch: "main"}},
	})
}

// A worker that checks out its base commit — to install a baseline build and
// prove an upgrade path — used to make the whole panel claim the branch matched
// its target. The branch is what the session OWNS, so that is what is diffed.
func TestWorkspaceChanges_DetachedHeadStillDiffsTheBranch(t *testing.T) {
	dir, baseSHA := scopeTestRepo(t)
	runGitIn(t, dir, "checkout", "-q", baseSHA)
	svc := scopeService(t, dir, "feature/x")

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want available, got %+v", res)
	}
	if len(res.Files) != 2 || !hasPath(res, "added.go") || !hasPath(res, "keep.go") {
		t.Fatalf("files = %+v, want the branch's two committed files", res.Files)
	}
	if res.Branch != "feature/x" || res.DiffSubject != ChangesSubjectBranch {
		t.Errorf("subject = %q on %q, want the session branch", res.DiffSubject, res.Branch)
	}
	// The reader must be able to tell a detached worktree from an empty diff.
	if res.HeadState != HeadDetached {
		t.Errorf("head state = %q, want %q", res.HeadState, HeadDetached)
	}
	if res.HeadLabel == "" || !strings.HasPrefix(baseSHA, res.HeadLabel) {
		t.Errorf("head label = %q, want a short sha of %s", res.HeadLabel, baseSHA)
	}
	if res.IncludesWorktree {
		t.Error("a worktree parked on another commit must not be folded into the branch's diff")
	}
	// Every listed file is committed work by construction.
	for _, f := range res.Files {
		if !f.Committed {
			t.Errorf("%s reported uncommitted in a committed-only list", f.Path)
		}
	}
}

// The uncommitted work in a detached worktree is measured against a different
// baseline, so it is not folded in — but it is COUNTED, so the panel can say it
// exists rather than trading one blind spot for another.
func TestWorkspaceChanges_DetachedHeadCountsTheWorkItLeavesOut(t *testing.T) {
	dir, baseSHA := scopeTestRepo(t)
	runGitIn(t, dir, "checkout", "-q", baseSHA)
	if err := os.WriteFile(filepath.Join(dir, "keep.go"), []byte("l1\nl2\nl3\nlocal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := scopeService(t, dir, "feature/x")

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.PendingPaths != 2 {
		t.Errorf("pending paths = %d, want 2 (the edited file and the untracked one)", res.PendingPaths)
	}
	// The untracked file belongs to the detached checkout, not to the branch.
	if hasPath(res, "scratch.log") {
		t.Error("scratch.log is worktree-only work and must not appear in the branch's diff")
	}
}

// The ordinary case: HEAD on the session's branch keeps the union of committed,
// uncommitted and untracked work in one list.
func TestWorkspaceChanges_OnBranchIncludesWorktree(t *testing.T) {
	dir, _ := scopeTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("u\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := scopeService(t, dir, "feature/x")

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.HeadState != HeadOnBranch || !res.IncludesWorktree {
		t.Errorf("head state = %q includesWorktree = %v, want on_branch and true", res.HeadState, res.IncludesWorktree)
	}
	if !hasPath(res, "untracked.go") {
		t.Errorf("files = %+v, want the untracked file folded in", res.Files)
	}
	if res.PendingPaths != 0 {
		t.Errorf("pending paths = %d, want 0 — the pending work is IN the list", res.PendingPaths)
	}
}

// A worktree checked out on some OTHER branch is the same shape of lie as a
// detached one, and gets the same treatment: the session's branch is diffed and
// the worktree's position is named.
func TestWorkspaceChanges_OtherBranchCheckedOut(t *testing.T) {
	dir, baseSHA := scopeTestRepo(t)
	runGitIn(t, dir, "checkout", "-qb", "spike/try", baseSHA)
	svc := scopeService(t, dir, "feature/x")

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.HeadState != HeadOnOtherBranch || res.HeadLabel != "spike/try" {
		t.Errorf("head = %q/%q, want other_branch/spike/try", res.HeadState, res.HeadLabel)
	}
	if !hasPath(res, "added.go") || res.IncludesWorktree {
		t.Errorf("want the branch's committed diff without the worktree, got %+v", res)
	}
}

// A branch that is named but has no ref here cannot be diffed. Falling back to
// HEAD is fine; doing it SILENTLY is not, so the payload says both what it could
// not find and what it answered instead.
func TestWorkspaceChanges_MissingBranchRefFallsBackToHeadAndSaysSo(t *testing.T) {
	dir, _ := scopeTestRepo(t)
	svc := scopeService(t, dir, "feature/renamed-away")

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.BranchMissing || res.DiffSubject != ChangesSubjectHead {
		t.Errorf("want branchMissing with a head subject, got %+v", res)
	}
	if !res.Available || !hasPath(res, "added.go") {
		t.Errorf("want the HEAD diff as the fallback answer, got %+v", res)
	}
}

// A session that recorded no branch still owns whatever the worktree stands on,
// so nothing about the ordinary case changes for those rows.
func TestWorkspaceChanges_NoRecordedBranchAdoptsTheCheckout(t *testing.T) {
	dir, _ := scopeTestRepo(t)
	svc := scopeService(t, dir, "")

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "feature/x" || res.HeadState != HeadOnBranch || !res.IncludesWorktree {
		t.Errorf("want the checked-out branch adopted, got %+v", res)
	}
}

// Nothing recorded AND nothing checked out: HEAD is all there is, and the
// payload says so rather than implying a branch it never measured.
func TestWorkspaceChanges_NoRecordedBranchDetachedReportsHead(t *testing.T) {
	dir, baseSHA := scopeTestRepo(t)
	headSHA := runGitIn(t, dir, "rev-parse", "feature/x")
	runGitIn(t, dir, "checkout", "-q", headSHA)
	svc := scopeService(t, dir, "")

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "" || res.HeadState != HeadDetached || res.DiffSubject != ChangesSubjectHead {
		t.Errorf("want a declared head-only answer, got %+v", res)
	}
	if !res.IncludesWorktree {
		t.Error("with HEAD as the subject the worktree IS the subject's worktree and belongs in the list")
	}
	if res.MergeBase != baseSHA {
		t.Errorf("merge base = %q, want the base commit %q", res.MergeBase, baseSHA)
	}
}

// A row the list offers must open on the comparison the list counted. Before the
// scope was shared, a file listed from the branch opened on "no diff to show"
// because the per-file diff still measured the detached worktree.
func TestWorkspaceFileDiff_DetachedHeadDiffsTheBranchFile(t *testing.T) {
	dir, baseSHA := scopeTestRepo(t)
	runGitIn(t, dir, "checkout", "-q", baseSHA)
	svc := scopeService(t, dir, "feature/x")

	res, err := svc.WorkspaceFileDiff(context.Background(), "s1", FileDiffQuery{Path: "added.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Lines) == 0 {
		t.Fatalf("want the branch's version of added.go, got %+v", res)
	}
	found := false
	for _, l := range res.Lines {
		if l.Kind == "add" && strings.Contains(l.Text, "brand new") {
			found = true
		}
	}
	if !found {
		t.Errorf("lines = %+v, want the added line the branch committed", res.Lines)
	}
}
