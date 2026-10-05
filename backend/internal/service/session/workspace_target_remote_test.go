package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// runGit runs git in dir for fixture setup and fails the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
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

// serviceWithProject builds a service whose session s1 targets prTarget and
// whose project is registered with repoURL.
func (f staleTargetFixture) serviceWithProject(t *testing.T, prTarget, repoURL string) *Service {
	t.Helper()
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", f.worktree)
	rec := fake.sessions["s1"]
	rec.PRTarget = prTarget
	fake.sessions["s1"] = rec
	fake.projects["proj"] = domain.ProjectRecord{ID: "proj", RepoOriginURL: repoURL}
	return newServiceWithStore(t, &multiPRFakeStore{fakeStore: fake})
}

// renameOriginAndAddForeignRemote reshapes the fixture into the advisor-ios-app
// layout that exposed the bug: the project's own remote is NOT called origin,
// and a second remote points at a different project whose main is unrelated.
func (f staleTargetFixture) renameOriginAndAddForeignRemote(t *testing.T) {
	t.Helper()
	runGit(t, f.shared, "remote", "rename", "origin", "Advisor")

	other := filepath.Join(filepath.Dir(f.forge), "other.git")
	runGit(t, filepath.Dir(f.forge), "init", "-q", "--bare", "-b", "main", other)
	seed := filepath.Join(filepath.Dir(f.forge), "other-seed")
	runGit(t, filepath.Dir(f.forge), "clone", "-q", other, seed)
	if err := os.WriteFile(filepath.Join(seed, "other.txt"), []byte("other project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "-A")
	runGit(t, seed, "commit", "-qm", "another project")
	runGit(t, seed, "push", "-q", "origin", "main")

	runGit(t, f.shared, "remote", "add", "Nter", other)
	runGit(t, f.shared, "fetch", "-q", "Nter")
}

// countFetches records every `git fetch` the service runs, by its arguments.
func countFetches(t *testing.T) func() [][]string {
	t.Helper()
	var mu sync.Mutex
	var fetches [][]string
	origGit := gitOutput
	t.Cleanup(func() { gitOutput = origGit })
	gitOutput = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "fetch" {
			mu.Lock()
			fetches = append(fetches, append([]string(nil), args...))
			mu.Unlock()
		}
		return origGit(ctx, dir, args...)
	}
	return func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return append([][]string(nil), fetches...)
	}
}

// TestWorkspaceChanges_FetchesFromARemoteNotNamedOrigin is the reported bug,
// reproduced: a project whose remote is "Advisor" (and which also carries a
// second project's remote) was never fetched, and the tab compared against the
// human's local checkout of the target - billing every commit merged since
// their last pull to the session.
func TestWorkspaceChanges_FetchesFromARemoteNotNamedOrigin(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)
	f.renameOriginAndAddForeignRemote(t)

	res, err := f.serviceWithProject(t, "main", f.forge).WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatalf("want an available diff, got %+v", res)
	}
	if hasPath(res, "landed-by-others.txt") || hasPath(res, "mine-first.txt") {
		t.Errorf("diff measured against a stale target: %v", changedPaths(res))
	}
	if !hasPath(res, "mine-second.txt") {
		t.Errorf("the session's real pending change is missing: %v", changedPaths(res))
	}
	if res.TargetRef != "Advisor/main" {
		t.Errorf("targetRef = %q, want Advisor/main", res.TargetRef)
	}
	if res.TargetFetch != TargetFetchCurrent || res.TargetFetchedAt.IsZero() {
		t.Errorf("freshness = %q at %v (%q), want current with a fetch time",
			res.TargetFetch, res.TargetFetchedAt, res.TargetFetchError)
	}
	if res.MergeBase != f.targetTip {
		t.Errorf("merge base = %s, want the forge's target tip %s", res.MergeBase, f.targetTip)
	}
}

// TestLocateTarget_ChoosesTheProjectsRemote pins the resolution order, with no
// "origin" anywhere and two remotes pointing at different projects.
func TestLocateTarget_ChoosesTheProjectsRemote(t *testing.T) {
	cases := []struct {
		name        string
		target      string
		repoURL     string
		keepTracked bool
		want        targetLocation
	}{
		{name: "several remotes and nothing to choose by", target: "main",
			want: targetLocation{Branch: "main"}},
		{name: "matching project URL", target: "main", repoURL: "FORGE",
			want: targetLocation{Remote: "Advisor", Branch: "main"}},
		{name: "tracked remote of the local target", target: "main", keepTracked: true,
			want: targetLocation{Remote: "Advisor", Branch: "main"}},
		{name: "remote-qualified target", target: "Nter/main", repoURL: "FORGE",
			want: targetLocation{Remote: "Nter", Branch: "main"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStaleTargetFixture(t)
			f.renameOriginAndAddForeignRemote(t)
			if !tc.keepTracked {
				runGit(t, f.shared, "config", "--unset", "branch.main.remote")
			}
			repoURL := strings.ReplaceAll(tc.repoURL, "FORGE", f.forge)
			svc := f.serviceWithProject(t, tc.target, repoURL)
			rec := domain.SessionRecord{ID: "s1", ProjectID: "proj"}
			got := svc.locateTarget(context.Background(), rec, f.worktree, tc.target)
			if got != tc.want {
				t.Errorf("locateTarget(%q) = %+v, want %+v", tc.target, got, tc.want)
			}
		})
	}
}

// TestWorkspaceChanges_RemoteQualifiedTargetIsFetched covers a PR target stored
// as "origin/<branch>". It was fetched as refs/heads/origin/<branch>, which no
// forge has, so the tab was stuck on "could not refresh" forever.
func TestWorkspaceChanges_RemoteQualifiedTargetIsFetched(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)
	fetches := countFetches(t)

	res, err := f.serviceWithProject(t, "origin/main", "").WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetFetch != TargetFetchCurrent {
		t.Fatalf("freshness = %q (%q), want current", res.TargetFetch, res.TargetFetchError)
	}
	if res.TargetRef != "origin/main" || res.MergeBase != f.targetTip {
		t.Errorf("targetRef = %q, merge base = %s; want origin/main at %s", res.TargetRef, res.MergeBase, f.targetTip)
	}
	got := fetches()
	if len(got) != 1 || got[0][len(got[0])-1] != "+refs/heads/main:refs/remotes/origin/main" {
		t.Errorf("fetches = %v, want one narrow fetch of main", got)
	}
}

// TestWorkspaceChanges_RefreshSkipsTheThrottle is the refresh button: reads
// inside the throttle window share one fetch, but an explicit refresh fetches
// again at once.
func TestWorkspaceChanges_RefreshSkipsTheThrottle(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)
	fetches := countFetches(t)
	svc := f.service(t)

	for range 3 {
		if _, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(fetches()); n != 1 {
		t.Fatalf("fetched %d times across 3 plain reads, want 1", n)
	}
	if _, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	if n := len(fetches()); n != 2 {
		t.Fatalf("fetched %d times after a refresh, want 2", n)
	}
}

// TestWorkspaceChanges_TargetOnlyOnTheRemote covers a target branch that exists
// on the forge but has never been fetched and has no local branch.
func TestWorkspaceChanges_TargetOnlyOnTheRemote(t *testing.T) {
	f := newStaleTargetFixture(t)
	colleague := filepath.Join(filepath.Dir(f.forge), "colleague")
	runGit(t, colleague, "push", "-q", "origin", f.landedByOthers+":refs/heads/release")

	fetchDisabled(t)
	res, err := f.serviceWithProject(t, "release", "").WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Available || res.Reason != ChangesNoTargetBranch {
		t.Fatalf("before any fetch: %+v, want no_target_branch", res)
	}

	fetchInline(t)
	res, err = f.serviceWithProject(t, "release", "").WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.TargetRef != "origin/release" {
		t.Fatalf("after the fetch: available=%v targetRef=%q reason=%q", res.Available, res.TargetRef, res.Reason)
	}
}

// TestWorkspaceChanges_FetchTimeSurvivesAFailure keeps "fetched 2m ago" honest
// when the next refresh fails: the ref is exactly as fresh as the last success.
func TestWorkspaceChanges_FetchTimeSurvivesAFailure(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)
	svc := f.service(t)

	first, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if first.TargetFetchedAt.IsZero() {
		t.Fatalf("a successful fetch must carry its time: %+v", first)
	}
	runGit(t, f.shared, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	second, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.TargetFetch != TargetFetchFailed {
		t.Fatalf("freshness = %q, want failed", second.TargetFetch)
	}
	if !second.TargetFetchedAt.Equal(first.TargetFetchedAt) {
		t.Errorf("fetchedAt = %v, want the last success %v", second.TargetFetchedAt, first.TargetFetchedAt)
	}
}

// TestWorkspaceChanges_RetryAfterFailureReportsInFlight: once a fetch has
// failed, the status stays "failed" while a retry runs (a known failure must not
// hide behind a spinner). The reader still has to know an answer is coming, or
// the recovery is not seen until the next slow poll - which is what happened
// when the refresh button was pressed after the network came back.
func TestWorkspaceChanges_RetryAfterFailureReportsInFlight(t *testing.T) {
	fetchInline(t)
	f := newStaleTargetFixture(t)
	svc := f.service(t)
	runGit(t, f.shared, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if res, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{}); err != nil ||
		res.TargetFetch != TargetFetchFailed || res.TargetFetchInFlight {
		t.Fatalf("setup: want a settled failure, got %+v (%v)", res, err)
	}
	runGit(t, f.shared, "remote", "set-url", "origin", f.forge)

	release := make(chan struct{})
	orig := goFetch
	t.Cleanup(func() { goFetch = orig })
	done := make(chan struct{})
	goFetch = func(fn func()) {
		go func() {
			<-release
			fn()
			close(done)
		}()
	}
	res, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetFetch != TargetFetchFailed || !res.TargetFetchInFlight {
		t.Errorf("during the retry: fetch = %q inFlight = %v, want failed + in flight", res.TargetFetch, res.TargetFetchInFlight)
	}
	close(release)
	<-done
	res, err = svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetFetch != TargetFetchCurrent || res.TargetFetchInFlight {
		t.Errorf("after the retry: fetch = %q inFlight = %v, want current", res.TargetFetch, res.TargetFetchInFlight)
	}
}

// TestWorkspaceChanges_LocalOnlyTargetNamesTheLocalRef: with no remote at all
// the tab compares against the local branch and must say so.
func TestWorkspaceChanges_LocalOnlyTargetNamesTheLocalRef(t *testing.T) {
	fetchInline(t)
	dir := changesTestRepo(t)
	svc := changesService(t, dir, []domain.PullRequest{{URL: "pr1", TargetBranch: "main"}})

	res, err := svc.WorkspaceChanges(context.Background(), "s1", WorkspaceChangesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetRef != "main" || res.TargetFetch != "" || !res.TargetFetchedAt.IsZero() {
		t.Errorf("targetRef = %q fetch = %q at %v; want the local main and no freshness",
			res.TargetRef, res.TargetFetch, res.TargetFetchedAt)
	}
}

func TestSameRepoURL(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://gitlab.example.com/group/sub/proj", "git@gitlab.example.com:group/sub/proj.git", true},
		{"https://GitLab.example.com/group/proj.git/", "https://gitlab.example.com/group/proj", true},
		{"ssh://git@github.com/acme/app.git", "https://github.com/acme/app", true},
		{"https://gitlab.example.com/group/proj", "https://gitlab.example.com/group/other", false},
		{"https://gitlab.example.com/group/proj", "https://github.com/group/proj", false},
		{"/tmp/forge.git", "/tmp/forge.git", true},
	}
	for _, tc := range cases {
		if got := sameRepoURL(tc.a, tc.b); got != tc.want {
			t.Errorf("sameRepoURL(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
