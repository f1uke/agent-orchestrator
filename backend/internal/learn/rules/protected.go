package rules

import (
	"fmt"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// maxPatterns and maxPatternLen bound what one protected rule may carry.
const (
	maxPatterns   = 16
	maxPatternLen = 256
)

// CompilePatterns validates a protected rule's forbidden patterns. They are
// RE2 and always matched case-insensitively.
func CompilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	if len(patterns) > maxPatterns {
		return nil, fmt.Errorf("at most %d patterns per rule", maxPatterns)
	}
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			return nil, fmt.Errorf("empty pattern")
		}
		if len(p) > maxPatternLen {
			return nil, fmt.Errorf("pattern longer than %d bytes", maxPatternLen)
		}
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// ForbiddenHit is a forbidden pattern found in a text.
type ForbiddenHit struct {
	RuleID  int64
	Rule    string
	Pattern string
	Match   string
}

// Forbidden reports every forbidden pattern of the protected rules that apply
// to projectID (global ones and the project's own) that matches text. It is the
// deterministic gate every proposed change must pass: no model judgment can talk a
// match away.
func Forbidden(text string, projectID domain.ProjectID, protected []domain.LearnProtectedRule) []ForbiddenHit {
	var hits []ForbiddenHit
	for _, r := range protected {
		if r.ProjectID != "" && r.ProjectID != projectID {
			continue
		}
		res, err := CompilePatterns(r.Patterns)
		if err != nil {
			// Patterns are validated on save; one that no longer compiles must
			// still block rather than silently pass.
			hits = append(hits, ForbiddenHit{RuleID: r.ID, Rule: r.Text, Pattern: "(invalid) " + err.Error()})
			continue
		}
		for i, re := range res {
			if m := re.FindString(text); m != "" {
				hits = append(hits, ForbiddenHit{RuleID: r.ID, Rule: r.Text, Pattern: r.Patterns[i], Match: m})
			}
		}
	}
	return hits
}
