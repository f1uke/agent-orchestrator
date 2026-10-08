package sessionmanager

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestApplyConventionPrefix(t *testing.T) {
	cases := []struct {
		name      string
		sanitized string
		cfg       domain.GitConventionConfig
		want      string
	}{
		{
			"none leaves name untouched",
			"feature/PROJ-2271-checkout-result",
			domain.GitConventionConfig{},
			"feature/PROJ-2271-checkout-result",
		},
		{
			"gitflow leaves the inferred type untouched",
			"bugfix/PROJ-2271-fix-crash",
			domain.GitConventionConfig{Workflow: domain.GitWorkflowGitflow, BranchPrefix: "feature/"},
			"bugfix/PROJ-2271-fix-crash",
		},
		{
			"custom replaces the type but keeps jira key and desc",
			"feature/PROJ-2271-checkout-result",
			domain.GitConventionConfig{Workflow: domain.GitWorkflowCustom, BranchPrefix: "feat/"},
			"feat/PROJ-2271-checkout-result",
		},
		{
			"custom normalizes a prefix without a trailing slash",
			"bugfix/ABC-1-x",
			domain.GitConventionConfig{Workflow: domain.GitWorkflowCustom, BranchPrefix: "story"},
			"story/ABC-1-x",
		},
		{
			"custom supports a nested prefix",
			"chore/cleanup",
			domain.GitConventionConfig{Workflow: domain.GitWorkflowCustom, BranchPrefix: "team/feat/"},
			"team/feat/cleanup",
		},
		{
			"custom with no slash in name prepends the prefix whole",
			"cleanup",
			domain.GitConventionConfig{Workflow: domain.GitWorkflowCustom, BranchPrefix: "feat/"},
			"feat/cleanup",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := applyConventionPrefix(c.sanitized, c.cfg); got != c.want {
				t.Fatalf("applyConventionPrefix(%q, %+v) = %q, want %q", c.sanitized, c.cfg, got, c.want)
			}
		})
	}
}

func TestExtractJiraKey(t *testing.T) {
	cases := []struct {
		name  string
		texts []string
		want  string
	}{
		{"in title", []string{"PROJ-2271 result UI", "brief"}, "PROJ-2271"},
		{"in brief url", []string{"E-Item", "see https://x.atlassian.net/browse/ABC-42 now"}, "ABC-42"},
		{"none", []string{"no key here", "plain brief"}, ""},
		{"lowercase not matched", []string{"proj-2271", ""}, ""},
		{"multi-letter project", []string{"PROJ12-9 thing", ""}, "PROJ12-9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractJiraKey(c.texts...); got != c.want {
				t.Fatalf("extractJiraKey(%v) = %q, want %q", c.texts, got, c.want)
			}
		})
	}
}

func TestSanitizeBranchName(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		want   string
		wantOK bool
	}{
		{"clean keeps jira key uppercase", "feature/PROJ-2271-checkout-result", "feature/PROJ-2271-checkout-result", true},
		{"backticked with prose", "`feature/PROJ-2271-x`\nSure, here you go!", "feature/PROJ-2271-x", true},
		{"label prefix", "branch: bugfix/ABC-1-fix-crash", "bugfix/ABC-1-fix-crash", true},
		{"spaces and junk", "feature/PROJ 2271  gift card!!", "feature/PROJ-2271-gift-card", true},
		{"key with digit in project", "feature/proj2-15-add-thing", "feature/PROJ2-15-add-thing", true},
		{"dedup suffix stays lowercase", "feature/proj-2271-result-2", "feature/PROJ-2271-result-2", true},
		{"no key leaves desc lowercase", "chore/cleanup-old-files", "chore/cleanup-old-files", true},
		{"no gitflow prefix", "proj-2271-result", "", false},
		{"bad type", "release/PROJ-1-x", "", false},
		{"dotdot", "feature/PROJ..1", "", false},
		{"empty", "", "", false},
		{"trailing slash trimmed then ok", "chore/cleanup/", "chore/cleanup", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := sanitizeBranchName(c.raw)
			if got != c.want || ok != c.wantOK {
				t.Fatalf("sanitizeBranchName(%q) = (%q,%v), want (%q,%v)", c.raw, got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestEnsureUniqueBranch(t *testing.T) {
	existing := map[string]bool{
		"feature/proj-2271-x":   true,
		"feature/proj-2271-x-2": true,
	}
	if got := ensureUniqueBranch(existing, "feature/proj-2271-y"); got != "feature/proj-2271-y" {
		t.Fatalf("free candidate changed: %q", got)
	}
	if got := ensureUniqueBranch(existing, "feature/proj-2271-x"); got != "feature/proj-2271-x-3" {
		t.Fatalf("collision suffix wrong: %q", got)
	}
	// An uppercase Jira key must still collide with the lowercased existing set
	// (case-insensitive filesystems), and the returned name keeps its casing.
	if got := ensureUniqueBranch(existing, "feature/PROJ-2271-x"); got != "feature/PROJ-2271-x-3" {
		t.Fatalf("mixed-case collision suffix wrong: %q", got)
	}
}

func TestBuildNamingPromptMentionsKeyAndRules(t *testing.T) {
	p := buildNamingPrompt("E-Item Order Result", "make the UI", "PROJ-2271")
	for _, want := range []string{"PROJ-2271", "feature", "bugfix", "hotfix", "chore", "E-Item Order Result"} {
		if !contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestExistingBranchNamesReadsTheProjectRemote: a generated name must be
// de-duplicated against the project's own remote, which in advisor-ios-app is
// `Advisor` (no origin) - and not against another project's remote beside it.
func TestExistingBranchNamesReadsTheProjectRemote(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q", "-b", "develop")
	git("commit", "-q", "--allow-empty", "-m", "init")
	git("remote", "add", "Advisor", "https://gitlab.example.com/mobility/advisor-ios-app")
	git("remote", "add", "Nter", "https://gitlab.example.com/mobility/nter-ios-app")
	git("update-ref", "refs/remotes/Advisor/feature/MOBILITY-1-taken", "HEAD")
	git("symbolic-ref", "refs/remotes/Advisor/HEAD", "refs/remotes/Advisor/develop")
	git("update-ref", "refs/remotes/Nter/feature/other-project", "HEAD")

	got := (&Manager{}).existingBranchNames(context.Background(), domain.ProjectRecord{
		Path: dir, RepoOriginURL: "git@gitlab.example.com:mobility/advisor-ios-app.git",
	})
	if !got["feature/mobility-1-taken"] || !got["develop"] {
		t.Errorf("names = %v, want the Advisor branch and local develop", got)
	}
	if got["advisor"] || got["feature/other-project"] || got["nter/feature/other-project"] {
		t.Errorf("names = %v, want neither the shortened Advisor/HEAD nor the other project's branches", got)
	}
}

// TestSpawnAutoNamedBranchUsesTheSessionsIssueKey: the namer is an LLM and the
// key it is told to use is only a hint, so a brief that mentions another ticket
// can win. A session spawned with an issue must land on that issue's key; one
// spawned without an issue keeps whatever the namer produced.
func TestSpawnAutoNamedBranchUsesTheSessionsIssueKey(t *testing.T) {
	cases := []struct {
		name    string
		issue   domain.IssueID
		namerOK string
		want    string
	}{
		{"issue key replaces a prompt key", "jira:ABC-2", "feature/ABC-1-fix-login", "feature/ABC-2-fix-login"},
		{"issue key is added when the namer left none", "jira:ABC-2", "bugfix/fix-login", "bugfix/ABC-2-fix-login"},
		{"a bare --issue key counts too", "ABC-2", "feature/ABC-1-fix-login", "feature/ABC-2-fix-login"},
		{"a gitlab issue uses its number", "gitlab:group/repo#12", "feature/ABC-1-fix-login", "feature/12-fix-login"},
		{"a github issue uses its number", "github:owner/repo#7", "feature/fix-login", "feature/7-fix-login"},
		{"no issue keeps the namer's name", "", "feature/ABC-1-fix-login", "feature/ABC-1-fix-login"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _, _, ws := newManager()
			m.genBranchName = func(context.Context, ports.Agent, ports.SpawnConfig, domain.ProjectRecord) (string, bool) {
				return c.namerOK, true
			}
			_, err := m.Spawn(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, AutoNameBranch: true, IssueID: c.issue,
				Prompt: "Fix the login screen. See ABC-1 for the earlier attempt.",
			})
			if err != nil {
				t.Fatal(err)
			}
			if ws.lastCfg.Branch != c.want {
				t.Fatalf("auto-named branch = %q, want %q", ws.lastCfg.Branch, c.want)
			}
		})
	}
}

// TestNamingKeyHint: the hint handed to the namer is the session's issue key
// when it has an issue, and a prompt key only when it has none.
func TestNamingKeyHint(t *testing.T) {
	cases := []struct {
		issue  domain.IssueID
		prompt string
		want   string
	}{
		{"jira:ABC-2", "see ABC-1", "ABC-2"},
		{"gitlab:group/repo#12", "see ABC-1", "12"},
		{"", "see ABC-1", "ABC-1"},
		{"", "no key", ""},
	}
	for _, c := range cases {
		if got := namingKeyHint(c.issue, c.prompt); got != c.want {
			t.Fatalf("namingKeyHint(%q, %q) = %q, want %q", c.issue, c.prompt, got, c.want)
		}
	}
}
