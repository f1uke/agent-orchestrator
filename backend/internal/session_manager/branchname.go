package sessionmanager

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var (
	jiraKeyRe         = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)
	allowedTypes      = map[string]bool{"feature": true, "bugfix": true, "hotfix": true, "chore": true}
	nonBranchChars    = regexp.MustCompile(`[^a-z0-9/-]+`)
	repeatedSlashDash = regexp.MustCompile(`-{2,}`)
	// leadingJiraKeyRe matches a lowercased Jira key at the START of the branch's
	// segment after the type slash (e.g. the "star-2271" in "star-2271-ecoupon"),
	// so only the card key is re-uppercased and later "-2" de-dup suffixes or
	// hyphenated words in the description are left untouched.
	leadingJiraKeyRe = regexp.MustCompile(`^[a-z][a-z0-9]*-\d+`)
)

// extractJiraKey returns the first Jira-style key (e.g. STAR-2271) found across
// the given texts, or "" when none is present.
func extractJiraKey(texts ...string) string {
	for _, t := range texts {
		if m := jiraKeyRe.FindString(t); m != "" {
			return m
		}
	}
	return ""
}

// effectiveIssueID resolves the Jira link a session is seeded with. An explicit
// IssueID (from `ao spawn --issue`, or the manual link path) always wins and is
// preserved verbatim. Otherwise the key is derived from the spawn's BRANCH only,
// so a session whose branch reads "bugfix/STAR-2394-x" is linked to STAR-2394 and
// the Summary panel shows it without a manual "+ Link a Jira issue" step. It
// writes ONLY the internal session-to-issue association; it performs no Jira write.
//
// The free-text prompt is deliberately NOT scraped. A key-shaped token can appear
// in a brief incidentally - an example path, a quoted branch name, even a sentence
// warning the reader not to touch that path - and binding on it silently attached
// workers to another team's issue, which also aims the Move-status write path (the
// one sanctioned Jira write) at that issue. A branch name, by contrast, is typed
// deliberately, so a key there is a real statement of intent.
//
// This runs at seed time (seedRecord/todoSeedRecord), before branch auto-naming,
// so cfg.Branch here is exclusively the caller-supplied branch. That ordering is
// load-bearing: the auto-namer takes its key hint from the prompt, so binding from
// a generated branch would launder prose back into issue_id. A spawn that wants a
// link without naming a branch must pass --issue.
func effectiveIssueID(cfg ports.SpawnConfig) domain.IssueID {
	if cfg.IssueID != "" {
		return cfg.IssueID
	}
	// Auto-derive only for task sessions. An orchestrator is a standing dispatcher,
	// never "working" one issue, so it is excluded here just as it is from Jira-keyed
	// branch naming above.
	if cfg.Kind == domain.KindOrchestrator {
		return ""
	}
	if key := extractJiraKey(cfg.Branch); key != "" {
		return domain.IssueID(string(domain.TrackerProviderJira) + ":" + key)
	}
	return ""
}

// buildNamingPrompt asks an agent to emit ONLY a gitflow branch name.
func buildNamingPrompt(title, brief, jiraKeyHint string) string {
	keyLine := "No Jira key detected — omit the key segment."
	if jiraKeyHint != "" {
		keyLine = fmt.Sprintf("Detected Jira key: %s — put it uppercase right after the type slash.", jiraKeyHint)
	}
	return fmt.Sprintf(`Generate ONE git branch name for the task below. Output ONLY the branch name on a single line — no backticks, no quotes, no explanation.

Format: <type>/<JIRA-KEY>-<short-desc>
- <type> is exactly one of: feature, bugfix, hotfix, chore (infer from the task's intent).
- %s
- <short-desc>: 2 to 4 words, kebab-case, lowercase, abbreviated.
- Total length <= 60 characters. Use only lowercase a-z, 0-9, hyphen and one slash.
- Example: feature/STAR-2271-ecoupon-result

Task title: %s

Task brief:
%s`, keyLine, title, brief)
}

// sanitizeBranchName cleans a raw agent response into a safe gitflow branch name.
// ok == false means the output could not be trusted and the caller must fall back.
func sanitizeBranchName(raw string) (string, bool) {
	line := ""
	for _, l := range strings.Split(raw, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			line = s
			break
		}
	}
	line = strings.Trim(line, "`\"' \t")
	// strip a leading "branch:"-style label
	if i := strings.IndexByte(line, ':'); i >= 0 && i < 12 && !strings.Contains(line[:i], "/") {
		line = strings.TrimSpace(line[i+1:])
	}
	line = strings.ToLower(line)
	if strings.Contains(line, "..") {
		return "", false
	}
	line = nonBranchChars.ReplaceAllString(line, "-")
	line = repeatedSlashDash.ReplaceAllString(line, "-")
	for strings.Contains(line, "//") {
		line = strings.ReplaceAll(line, "//", "/")
	}
	line = strings.Trim(line, "-/")
	if line == "" || len(line) > 80 || strings.HasSuffix(line, ".lock") {
		return "", false
	}
	slash := strings.IndexByte(line, '/')
	if slash <= 0 {
		return "", false
	}
	if !allowedTypes[line[:slash]] {
		return "", false
	}
	if strings.TrimSpace(line[slash+1:]) == "" {
		return "", false
	}
	// Restore Jira-card casing: uppercase the key sitting right after the type
	// slash so the branch (and the worktree that mirrors it) reads
	// "feature/STAR-2271-x" like the Jira card, not "feature/star-2271-x".
	rest := line[slash+1:]
	if m := leadingJiraKeyRe.FindString(rest); m != "" {
		line = line[:slash+1] + strings.ToUpper(m) + rest[len(m):]
	}
	return line, true
}

// applyConventionPrefix rewrites a sanitized auto-named branch to satisfy the
// project's git convention. For a custom workflow it replaces the namer's type
// segment (feature/, bugfix/, …) with the configured fixed prefix while preserving
// the Jira key and description; for gitflow and none it returns the name unchanged,
// since the namer already emits a gitflow type. The input is a name that already
// passed sanitizeBranchName, so the output is composed of safe ref characters.
func applyConventionPrefix(sanitized string, cfg domain.GitConventionConfig) string {
	if cfg.Workflow != domain.GitWorkflowCustom {
		return sanitized
	}
	prefix := cfg.NormalizedBranchPrefix()
	if prefix == "" {
		return sanitized
	}
	rest := sanitized
	if slash := strings.IndexByte(sanitized, '/'); slash >= 0 {
		rest = sanitized[slash+1:]
	}
	return prefix + rest
}

// ensureUniqueBranch returns candidate, or candidate-2, candidate-3, ... until it
// is not present in existing. Keys in existing are bare branch names (no refs/…).
func ensureUniqueBranch(existing map[string]bool, candidate string) string {
	// Compare case-insensitively: existing names are lowercased and the candidate
	// now carries an uppercase Jira key, but macOS/Windows worktree directories
	// (and git's ref case-folding) collide regardless of case.
	if !existing[strings.ToLower(candidate)] {
		return candidate
	}
	for n := 2; n < 1000; n++ {
		next := fmt.Sprintf("%s-%d", candidate, n)
		if !existing[strings.ToLower(next)] {
			return next
		}
	}
	return candidate // pathological (1000 consecutive collisions); caller retries with the
	// session-unique default branch when workspace.Create rejects this name
}

func branchNameTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("AO_BRANCH_NAME_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 20 * time.Second
}

// generateBranchName asks the session's agent for a gitflow branch name. It is
// best-effort: ok == false on any failure and the caller falls back.
func (m *Manager) generateBranchName(ctx context.Context, agent ports.Agent, cfg ports.SpawnConfig, project domain.ProjectRecord) (string, bool) {
	namer, isNamer := agent.(ports.OneShotNamer)
	if !isNamer {
		return "", false
	}
	key := extractJiraKey(string(cfg.IssueID), cfg.Prompt)
	prompt := buildNamingPrompt(string(cfg.IssueID), cfg.Prompt, key)

	cctx, cancel := context.WithTimeout(ctx, branchNameTimeout())
	defer cancel()
	argv, ok, err := namer.OneShotArgv(cctx, prompt)
	if !ok || err != nil || len(argv) == 0 {
		return "", false
	}
	tmpDir, err := os.MkdirTemp("", "ao-branchname-")
	if err != nil {
		return "", false
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cmd := aoprocess.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = tmpDir
	out, err := cmd.Output()
	if cctx.Err() != nil || err != nil {
		return "", false
	}
	name, ok := sanitizeBranchName(string(out))
	if !ok {
		return "", false
	}
	return name, true
}

// existingBranchNames lists local and origin branch short-names in the project
// repo so a generated name can be de-duplicated before worktree creation.
func (m *Manager) existingBranchNames(ctx context.Context, project domain.ProjectRecord) map[string]bool {
	set := map[string]bool{}
	if strings.TrimSpace(project.Path) == "" {
		return set
	}
	cmd := aoprocess.CommandContext(ctx, "git", "-C", project.Path,
		"for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes/origin")
	out, err := cmd.Output()
	if err != nil {
		return set
	}
	for _, l := range strings.Split(string(out), "\n") {
		s := strings.TrimSpace(l)
		if s == "" || s == "origin" { // git shortens refs/remotes/origin/HEAD to bare "origin"
			continue
		}
		set[strings.ToLower(strings.TrimPrefix(s, "origin/"))] = true
	}
	return set
}
