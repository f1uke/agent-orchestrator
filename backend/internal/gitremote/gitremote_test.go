package gitremote

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoWithRemotes builds a repository on main whose remotes are named and
// pointed as given (name -> URL). Remotes need not be reachable: resolution
// only reads configuration.
func repoWithRemotes(t *testing.T, remotes map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")
	for name, url := range remotes {
		runGit(t, dir, "remote", "add", name, url)
	}
	return dir
}

const (
	advisorURL = "https://gitlab.example.com/mobility/advisor-ios-app"
	nterURL    = "https://gitlab.example.com/mobility/nter-ios-app"
)

func TestPick(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		remotes map[string]string
		repoURL string
		tracked string // remote main tracks, "" for none
		want    string
	}{
		{name: "no remote", want: ""},
		{name: "the project's URL wins over origin",
			remotes: map[string]string{"origin": nterURL, "Advisor": advisorURL},
			repoURL: "git@gitlab.example.com:mobility/advisor-ios-app.git", want: "Advisor"},
		{name: "origin when the URL matches nothing",
			remotes: map[string]string{"origin": nterURL, "Advisor": advisorURL},
			repoURL: "https://elsewhere.example.com/x/y", want: "origin"},
		{name: "the tracked remote without origin",
			remotes: map[string]string{"Advisor": advisorURL, "Nter": nterURL},
			tracked: "Advisor", want: "Advisor"},
		{name: "the sole remote",
			remotes: map[string]string{"Advisor": advisorURL}, want: "Advisor"},
		{name: "several remotes and nothing to choose by",
			remotes: map[string]string{"Advisor": advisorURL, "Nter": nterURL}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := repoWithRemotes(t, tc.remotes)
			if tc.tracked != "" {
				runGit(t, dir, "config", "branch.main.remote", tc.tracked)
			}
			if got := Pick(ctx, Exec, dir, tc.repoURL, "main"); got != tc.want {
				t.Errorf("Pick = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProjectURL(t *testing.T) {
	ctx := context.Background()
	dir := repoWithRemotes(t, map[string]string{"Advisor": advisorURL, "Nter": nterURL})
	if got := ProjectURL(ctx, Exec, dir); got != "" {
		t.Errorf("ambiguous remotes: ProjectURL = %q, want empty", got)
	}
	runGit(t, dir, "config", "branch.main.remote", "Advisor")
	if got := ProjectURL(ctx, Exec, dir); got != advisorURL {
		t.Errorf("checked-out branch tracks Advisor: ProjectURL = %q, want %q", got, advisorURL)
	}
	runGit(t, dir, "remote", "add", "origin", nterURL)
	if got := ProjectURL(ctx, Exec, dir); got != nterURL {
		t.Errorf("origin present: ProjectURL = %q, want %q (origin outranks tracking when no URL is on record)", got, nterURL)
	}
}

func TestLocate(t *testing.T) {
	ctx := context.Background()
	dir := repoWithRemotes(t, map[string]string{"Advisor": advisorURL, "Nter": nterURL})
	cases := []struct {
		target, repoURL string
		want            Location
	}{
		{"develop", advisorURL, Location{Remote: "Advisor", Branch: "develop"}},
		{"develop", "", Location{Branch: "develop"}},
		{"Nter/feature/x", advisorURL, Location{Remote: "Nter", Branch: "feature/x"}},
		{"  ", advisorURL, Location{}},
	}
	for _, tc := range cases {
		if got := Locate(ctx, Exec, dir, tc.target, tc.repoURL); got != tc.want {
			t.Errorf("Locate(%q, %q) = %+v, want %+v", tc.target, tc.repoURL, got, tc.want)
		}
	}
	// A local branch literally named like a qualified target is that branch.
	runGit(t, dir, "branch", "Nter/feature/x")
	want := Location{Remote: "Advisor", Branch: "Nter/feature/x"}
	if got := Locate(ctx, Exec, dir, "Nter/feature/x", advisorURL); got != want {
		t.Errorf("local branch named Nter/feature/x: Locate = %+v, want %+v", got, want)
	}
}

func TestLocation(t *testing.T) {
	loc := Location{Remote: "Advisor", Branch: "develop"}
	if got, want := loc.Candidates(), []string{"refs/remotes/Advisor/develop", "refs/heads/develop", "develop"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Candidates = %v, want %v", got, want)
	}
	wantFetch := []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "Advisor", "+refs/heads/develop:refs/remotes/Advisor/develop"}
	if got := loc.FetchArgs(); !reflect.DeepEqual(got, wantFetch) {
		t.Errorf("FetchArgs = %v, want %v", got, wantFetch)
	}
	local := Location{Branch: "main"}
	if local.FetchArgs() != nil || local.TrackingRef() != "" {
		t.Errorf("a remoteless location must fetch nothing: %v %q", local.FetchArgs(), local.TrackingRef())
	}
	if got, want := local.Candidates(), []string{"refs/heads/main", "main"}; !reflect.DeepEqual(got, want) {
		t.Errorf("remoteless Candidates = %v, want %v", got, want)
	}
}

func TestDefaultBranch(t *testing.T) {
	ctx := context.Background()
	forge := t.TempDir()
	runGit(t, forge, "init", "-q", "--bare", "-b", "develop")
	seed := repoWithRemotes(t, map[string]string{"origin": forge})
	runGit(t, seed, "push", "-q", "origin", "main:develop")
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, filepath.Dir(clone), "clone", "-q", "-o", "Advisor", forge, clone)
	if got := DefaultBranch(ctx, Exec, clone, ""); got != "develop" {
		t.Errorf("DefaultBranch = %q, want develop (read from refs/remotes/Advisor/HEAD)", got)
	}
	if got := DefaultBranch(ctx, Exec, seed, ""); got != "" {
		t.Errorf("no remote HEAD known: DefaultBranch = %q, want empty", got)
	}
}

func TestSameRepo(t *testing.T) {
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
		{"", "", false},
	}
	for _, tc := range cases {
		if got := SameRepo(tc.a, tc.b); got != tc.want {
			t.Errorf("SameRepo(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
