package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// staleTargetFixture is a REAL three-repo topology in which the target branch is
// genuinely ahead of what the session's repository has fetched.
//
// The separate `colleague` clone is load-bearing, not decoration. Pushing from
// the session's own worktree updates THAT repository's remote-tracking ref as a
// side effect, so a fixture that lands work that way has no divergence at all —
// "stale" and "fresh" resolve to the same commit and every assertion passes
// whether or not the code fetches. The landing must happen in a repository the
// session's repository shares no refs with.
type staleTargetFixture struct {
	forge    string // bare repo standing in for the forge
	shared   string // clone standing in for the project repo
	worktree string // the session's worktree, on feature/x
	// landedByOthers is a commit on the target branch that the shared clone HAS
	// fetched into origin/main but whose LOCAL main still predates.
	landedByOthers string
	// targetTip is the forge's current main. The shared clone has NOT fetched it.
	// It is also the session's own first commit, since it has already landed.
	targetTip string
}

func newStaleTargetFixture(t *testing.T) staleTargetFixture {
	t.Helper()
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	forge := filepath.Join(root, "forge.git")
	git(root, "init", "-q", "--bare", "-b", "main", forge)

	// C0: the branch point everything else descends from.
	seed := filepath.Join(root, "seed")
	git(root, "clone", "-q", forge, seed)
	write(seed, "base.txt", "base\n")
	git(seed, "add", "-A")
	git(seed, "commit", "-qm", "C0")
	git(seed, "push", "-q", "origin", "main")

	// The project repo. Its LOCAL main is pinned at C0 from here on — exactly
	// like a human checkout that has not been pulled in a while.
	shared := filepath.Join(root, "shared")
	git(root, "clone", "-q", forge, shared)

	// C1: someone else's work lands on the target. The shared clone fetches it
	// into origin/main, but its local main stays at C0.
	write(seed, "landed-by-others.txt", "theirs\n")
	git(seed, "add", "-A")
	git(seed, "commit", "-qm", "C1 someone else's merged work")
	git(seed, "push", "-q", "origin", "main")
	git(shared, "fetch", "-q", "origin")
	landedByOthers := git(shared, "rev-parse", "origin/main")

	// The session worktree is cut from origin/main (C1) — which is what AO
	// itself does — so the session branch is already AHEAD of local main.
	worktree := filepath.Join(root, "sess")
	git(shared, "worktree", "add", "-q", "-b", "feature/x", worktree, "origin/main")

	write(worktree, "mine-first.txt", "mine 1\n")
	git(worktree, "add", "-A")
	git(worktree, "commit", "-qm", "session commit 1")
	git(worktree, "push", "-q", "origin", "feature/x")
	mineFirst := git(worktree, "rev-parse", "HEAD")

	// The session's first commit LANDS on the target, pushed from a repository
	// the shared clone shares no refs with. shared/origin/main stays at C1.
	colleague := filepath.Join(root, "colleague")
	git(root, "clone", "-q", forge, colleague)
	git(colleague, "push", "-q", "origin", mineFirst+":refs/heads/main")

	write(worktree, "mine-second.txt", "mine 2\n")
	git(worktree, "add", "-A")
	git(worktree, "commit", "-qm", "session commit 2")

	f := staleTargetFixture{
		forge: forge, shared: shared, worktree: worktree,
		landedByOthers: landedByOthers, targetTip: mineFirst,
	}
	// Guard the fixture itself: without a genuine divergence these tests would
	// pass against the broken code. This repo has shipped fixtures that only
	// looked like they exercised the case.
	if got := git(shared, "rev-parse", "origin/main"); got != landedByOthers {
		t.Fatalf("fixture: origin/main = %s, want it PINNED at %s", got, landedByOthers)
	}
	if git(shared, "rev-parse", "main") == landedByOthers {
		t.Fatal("fixture: local main must be BEHIND origin/main")
	}
	if f.targetTip == landedByOthers {
		t.Fatal("fixture: the forge's target tip must be AHEAD of the fetched ref")
	}
	return f
}

func (f staleTargetFixture) service(t *testing.T) *Service {
	t.Helper()
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", f.worktree)
	rec := fake.sessions["s1"]
	rec.PRTarget = "main"
	fake.sessions["s1"] = rec
	return newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})
}

// fetchInline makes the background refresh run inline, so assertions observe the
// post-fetch state instead of racing it.
func fetchInline(t *testing.T) {
	t.Helper()
	orig := goFetch
	t.Cleanup(func() { goFetch = orig })
	goFetch = func(fn func()) { fn() }
}

// fetchDisabled drops the background refresh entirely, isolating the parts of
// the fix that need no network at all.
func fetchDisabled(t *testing.T) {
	t.Helper()
	orig := goFetch
	t.Cleanup(func() { goFetch = orig })
	goFetch = func(func()) {}
}

func changedPaths(res WorkspaceChangesResult) []string {
	paths := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func hasPath(res WorkspaceChangesResult, path string) bool {
	for _, f := range res.Files {
		if f.Path == path {
			return true
		}
	}
	return false
}

// TestWorkspaceChanges_PrefersRemoteTrackingOverStaleLocalRef is the defect that
// needs no network: the local branch ref is a human checkout that only advances
// on `git pull`, while AO cuts session worktrees from origin/<branch>. Measuring
// against the local ref therefore bills another person's already-merged commit
// to this session.
//
// The fetch is disabled so this can only pass by choosing the right ref.
func TestWorkspaceChanges_PrefersRemoteTrackingOverStaleLocalRef(t *testing.T) {
	fetchDisabled(t)
	f := newStaleTargetFixture(t)

	res, err := f.service(t).WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want an available diff, got %+v", res)
	}
	if hasPath(res, "landed-by-others.txt") {
		t.Errorf("diff bills another person's merged commit to this session: %v", changedPaths(res))
	}
	if res.MergeBase != f.landedByOthers {
		t.Errorf("merge base = %s, want the fetched origin/main %s", res.MergeBase, f.landedByOthers)
	}
}

// TestWorkspaceChanges_FetchesTargetBranch covers the merge-base dimension: work
// that is already on the session branch has since LANDED on the target. Only a
// fetch can move the merge base forward past it; without one the panel keeps
// counting landed work as this session's pending changes.
func TestWorkspaceChanges_FetchesTargetBranch(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)

	res, err := f.service(t).WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want an available diff, got %+v", res)
	}
	if hasPath(res, "mine-first.txt") {
		t.Errorf("work already merged into the target is still counted: %v", changedPaths(res))
	}
	if !hasPath(res, "mine-second.txt") {
		t.Errorf("the session's real pending change is missing: %v", changedPaths(res))
	}
	if res.MergeBase != f.targetTip {
		t.Errorf("merge base = %s, want the fetched target tip %s", res.MergeBase, f.targetTip)
	}
	if res.TargetFetch != TargetFetchCurrent {
		t.Errorf("freshness = %q (%q), want %q", res.TargetFetch, res.TargetFetchError, TargetFetchCurrent)
	}
}

// TestWorkspaceChanges_FetchIsReadOnly guards the hard constraint: freshness may
// never be bought with the user's working tree. HEAD, the local branch refs and
// the tracked/untracked file state must all survive the refresh untouched.
func TestWorkspaceChanges_FetchIsReadOnly(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)
	snapshot := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	headBefore := snapshot(f.worktree, "rev-parse", "HEAD")
	statusBefore := snapshot(f.worktree, "status", "--porcelain")
	localBefore := snapshot(f.shared, "for-each-ref", "refs/heads/")

	if _, err := f.service(t).WorkspaceChanges(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}

	if got := snapshot(f.worktree, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("HEAD moved: %s -> %s", headBefore, got)
	}
	if got := snapshot(f.worktree, "status", "--porcelain"); got != statusBefore {
		t.Errorf("working tree changed:\n%s\n-->\n%s", statusBefore, got)
	}
	if got := snapshot(f.shared, "for-each-ref", "refs/heads/"); got != localBefore {
		t.Errorf("local branches changed:\n%s\n-->\n%s", localBefore, got)
	}
}

// TestWorkspaceChanges_FetchFailureIsVisible is the anti-confidently-wrong
// guard. Offline, an auth failure or a dead remote must leave the diff computed
// from the refs we already have AND say so, so the user can tell "this is
// current" from "this could not be refreshed".
func TestWorkspaceChanges_FetchFailureIsVisible(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)
	// Point origin at nothing — the shape an offline or unauthenticated fetch
	// takes from this code's point of view.
	cmd := exec.Command("git", "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	cmd.Dir = f.shared
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("set-url: %v\n%s", err, out)
	}

	res, err := f.service(t).WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatalf("a failed fetch must not error the endpoint: %v", err)
	}
	if !res.Available {
		t.Fatalf("a failed fetch must not blank the diff: %+v", res)
	}
	if res.TargetFetch != TargetFetchFailed {
		t.Fatalf("freshness = %q, want %q", res.TargetFetch, TargetFetchFailed)
	}
	if strings.TrimSpace(res.TargetFetchError) == "" {
		t.Error("a failed fetch must carry a reason for the UI to show")
	}
	// The diff must still be the honest best-effort answer from known refs.
	if !hasPath(res, "mine-second.txt") {
		t.Errorf("diff corrupted by the failed fetch: %v", changedPaths(res))
	}
	if res.MergeBase != f.landedByOthers {
		t.Errorf("merge base = %s, want the last known origin/main %s", res.MergeBase, f.landedByOthers)
	}
}

// TestWorkspaceChanges_FailedFetchSettlesAndStopsRetrying is the test the
// inline-fetch ones could not be: it runs the refresh on a real goroutine, the
// way production does.
//
// Running it inline hid a genuine bug. The status was read straight after
// kicking the fetch off, so with a real goroutine it was still "in flight" and
// every subsequent load reported "refreshing" forever — the warning never
// appeared, the spinner never stopped, and because the throttle keyed off the
// last SUCCESS, a dead remote was re-fetched on every single panel load. Caught
// end-to-end against a real daemon, not by the unit tests.
func TestWorkspaceChanges_FailedFetchSettlesAndStopsRetrying(t *testing.T) {
	f := newStaleTargetFixture(t)
	cmd := exec.Command("git", "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	cmd.Dir = f.shared
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("set-url: %v\n%s", err, out)
	}

	var mu sync.Mutex
	fetches := 0
	origGit := gitOutput
	t.Cleanup(func() { gitOutput = origGit })
	gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "fetch" {
			mu.Lock()
			fetches++
			mu.Unlock()
		}
		return origGit(ctx, dir, args...)
	}

	// A REAL goroutine, with a handle to wait on — no inline shortcut. The
	// once-guard keeps a regression that fires EXTRA fetches reporting as a
	// clean assertion failure instead of a double-close panic.
	done := make(chan struct{})
	var once sync.Once
	origGo := goFetch
	t.Cleanup(func() { goFetch = origGo })
	goFetch = func(fn func()) {
		go func() {
			fn()
			once.Do(func() { close(done) })
		}()
	}

	svc := f.service(t)
	if _, err := svc.WorkspaceChanges(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the background fetch never finished")
	}

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetFetch != TargetFetchFailed {
		t.Errorf("freshness = %q, want %q — a settled failure must stop reading as in-flight",
			res.TargetFetch, TargetFetchFailed)
	}
	if strings.TrimSpace(res.TargetFetchError) == "" {
		t.Error("a failed fetch must carry a reason for the UI to show")
	}
	mu.Lock()
	defer mu.Unlock()
	if fetches != 1 {
		t.Errorf("fetched %d times against a dead remote, want 1 — failures must be throttled too", fetches)
	}
}

// TestWorkspaceChanges_TargetBranchMissingOnRemote covers a target that exists
// locally but has been deleted (or renamed) on the forge. The fetch cannot
// succeed, and that must read as "could not refresh", not as a clean diff.
func TestWorkspaceChanges_TargetBranchMissingOnRemote(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", f.worktree)
	rec := fake.sessions["s1"]
	rec.PRTarget = "main"
	fake.sessions["s1"] = rec
	svc := newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})

	cmd := exec.Command("git", "branch", "-D", "main")
	cmd.Dir = f.forge
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("delete remote branch: %v\n%s", err, out)
	}

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatalf("a branch missing on the remote must not error: %v", err)
	}
	if res.TargetFetch != TargetFetchFailed {
		t.Fatalf("freshness = %q, want %q for a branch gone from the remote", res.TargetFetch, TargetFetchFailed)
	}
	// The already-fetched ref still answers, so the panel degrades to a stale
	// diff that is LABELLED stale rather than to an empty view.
	if !res.Available {
		t.Fatalf("want the last known diff, got %+v", res)
	}
}

// TestWorkspaceChanges_ReportsRefreshingWithoutBlocking is the UI-feel
// contract: the endpoint answers from the refs it already has and reports that
// a refresh is in flight, so the first render never waits on the network.
func TestWorkspaceChanges_ReportsRefreshingWithoutBlocking(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	orig := goFetch
	t.Cleanup(func() {
		close(release)
		goFetch = orig
	})
	goFetch = func(fn func()) {
		go func() {
			close(started)
			<-release // a slow forge: still fetching when the response is built
			fn()
		}()
	}
	f := newStaleTargetFixture(t)

	res, err := f.service(t).WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	// A bounded wait, never a bare receive: if the refresh is never kicked off
	// this must FAIL, not hang the suite until the panic timeout.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no background refresh was started")
	}
	if !res.Available {
		t.Fatalf("must answer from known refs while fetching: %+v", res)
	}
	if res.TargetFetch != TargetFetchRefreshing {
		t.Fatalf("freshness = %q, want %q", res.TargetFetch, TargetFetchRefreshing)
	}
	// Answered from the refs already on disk, which is the whole point.
	if res.MergeBase != f.landedByOthers {
		t.Errorf("merge base = %s, want the already-fetched %s", res.MergeBase, f.landedByOthers)
	}
}

// TestWorkspaceChanges_NoRemoteReportsNoFreshness keeps the signal honest: a
// repository with no origin has nothing to be behind, so it must not wear a
// "could not refresh" warning.
func TestWorkspaceChanges_NoRemoteReportsNoFreshness(t *testing.T) {
	fetchInline(t)
	dir := changesTestRepo(t) // a plain local repo, no remotes
	svc := changesService(t, dir, []domain.PullRequest{{URL: "pr1", TargetBranch: "main"}})

	res, err := svc.WorkspaceChanges(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want an available diff, got %+v", res)
	}
	if res.TargetFetch != "" {
		t.Errorf("freshness = %q, want none for a repo with no remote", res.TargetFetch)
	}
}

// TestTargetFetch_DeduplicatesPerRepo guards the cost constraint. Many sessions
// share one underlying repository, and the panel refetches on every mount and
// window focus; without throttling and single-flight that is a fetch storm on
// one repo for one branch.
func TestTargetFetch_DeduplicatesPerRepo(t *testing.T) {
	f := newStaleTargetFixture(t)

	var mu sync.Mutex
	fetches := 0
	origGit := gitOutput
	t.Cleanup(func() { gitOutput = origGit })
	gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "fetch" {
			mu.Lock()
			fetches++
			mu.Unlock()
		}
		return origGit(ctx, dir, args...)
	}
	fetchInline(t)

	svc := f.service(t)
	for range 5 {
		if _, err := svc.WorkspaceChanges(context.Background(), "s1"); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if fetches != 1 {
		t.Fatalf("fetched %d times across 5 panel loads, want 1 (throttled per repo+branch)", fetches)
	}
}

// TestDiffContext_NeedsNoFetch pins why the Reviews path is immune and must
// stay untouched: it diffs the PR's BaseSHA..HeadSHA, two commits the forge
// already pinned, never a branch ref that can drift. Adding a fetch there would
// buy nothing and put the network in front of every review comment.
func TestDiffContext_NeedsNoFetch(t *testing.T) {
	dir, baseSHA, headSHA := diffContextTestRepo(t)
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", dir)
	svc := newServiceWithStore(t, &multiPRFakeStore{
		fakeStore: fake,
		prs:       []domain.PullRequest{{URL: "pr1", BaseSHA: baseSHA, HeadSHA: headSHA}},
	})

	origGit := gitOutput
	t.Cleanup(func() { gitOutput = origGit })
	gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "fetch" {
			t.Error("the Reviews path must not touch the network: it diffs pinned SHAs")
		}
		return origGit(ctx, dir, args...)
	}

	res, err := svc.DiffContext(context.Background(), "s1", DiffContextQuery{
		PRURL: "pr1", Path: "a.go", Line: 2, Mode: "hunk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("res = %+v", res)
	}
}

// TestWorkspaceFileDiff_UsesFreshTarget keeps the file viewer consistent with
// the list. It shares the same target resolution, so a stale ref would open a
// file on hunks belonging to somebody else's merged commit.
func TestWorkspaceFileDiff_UsesFreshTarget(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)

	// mine-first.txt has already landed on the target, so against a fresh target
	// it is not part of this session's diff at all.
	res, err := f.service(t).WorkspaceFileDiff(context.Background(), "s1", FileDiffQuery{Path: "mine-first.txt", Base: DiffBaseTarget})
	if err != nil {
		t.Fatal(err)
	}
	if res.Available {
		t.Fatalf("a file already merged into the target must not diff as this session's work: %+v", res)
	}
}
