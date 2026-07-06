package sessionmanager

import "testing"

func TestExtractJiraKey(t *testing.T) {
	cases := []struct {
		name  string
		texts []string
		want  string
	}{
		{"in title", []string{"STAR-2271 result UI", "brief"}, "STAR-2271"},
		{"in brief url", []string{"E-Coupon", "see https://x.atlassian.net/browse/ABC-42 now"}, "ABC-42"},
		{"none", []string{"no key here", "plain brief"}, ""},
		{"lowercase not matched", []string{"star-2271", ""}, ""},
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
		{"clean keeps jira key uppercase", "feature/STAR-2271-ecoupon-result", "feature/STAR-2271-ecoupon-result", true},
		{"backticked with prose", "`feature/STAR-2271-x`\nSure, here you go!", "feature/STAR-2271-x", true},
		{"label prefix", "branch: bugfix/ABC-1-fix-crash", "bugfix/ABC-1-fix-crash", true},
		{"spaces and junk", "feature/STAR 2271  e coupon!!", "feature/STAR-2271-e-coupon", true},
		{"key with digit in project", "feature/star2-15-add-thing", "feature/STAR2-15-add-thing", true},
		{"dedup suffix stays lowercase", "feature/star-2271-result-2", "feature/STAR-2271-result-2", true},
		{"no key leaves desc lowercase", "chore/cleanup-old-files", "chore/cleanup-old-files", true},
		{"no gitflow prefix", "star-2271-result", "", false},
		{"bad type", "release/STAR-1-x", "", false},
		{"dotdot", "feature/STAR..1", "", false},
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
		"feature/star-2271-x":   true,
		"feature/star-2271-x-2": true,
	}
	if got := ensureUniqueBranch(existing, "feature/star-2271-y"); got != "feature/star-2271-y" {
		t.Fatalf("free candidate changed: %q", got)
	}
	if got := ensureUniqueBranch(existing, "feature/star-2271-x"); got != "feature/star-2271-x-3" {
		t.Fatalf("collision suffix wrong: %q", got)
	}
	// An uppercase Jira key must still collide with the lowercased existing set
	// (case-insensitive filesystems), and the returned name keeps its casing.
	if got := ensureUniqueBranch(existing, "feature/STAR-2271-x"); got != "feature/STAR-2271-x-3" {
		t.Fatalf("mixed-case collision suffix wrong: %q", got)
	}
}

func TestBuildNamingPromptMentionsKeyAndRules(t *testing.T) {
	p := buildNamingPrompt("E-Coupon Order Result", "make the UI", "STAR-2271")
	for _, want := range []string{"STAR-2271", "feature", "bugfix", "hotfix", "chore", "E-Coupon Order Result"} {
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
