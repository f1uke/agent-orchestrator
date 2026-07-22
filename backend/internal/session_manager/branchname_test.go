package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
