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
	repeatedSlashDash = regexp.MustCompile(`[-]{2,}`)
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
	return line, true
}

// ensureUniqueBranch returns candidate, or candidate-2, candidate-3, ... until it
// is not present in existing. Keys in existing are bare branch names (no refs/…).
func ensureUniqueBranch(existing map[string]bool, candidate string) string {
	if !existing[candidate] {
		return candidate
	}
	for n := 2; n < 1000; n++ {
		next := fmt.Sprintf("%s-%d", candidate, n)
		if !existing[next] {
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
	defer os.RemoveAll(tmpDir)

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
