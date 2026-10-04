package decide

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/skills"
)

// CheckEdit runs the gates again on content the person edited before
// approving p: the file must still parse, add nothing their protected rules
// forbid and no sensitive value. It returns the content as it will be written
// (em dashes replaced, one trailing newline).
func CheckEdit(p domain.LearnProposal, content string, protected []domain.LearnProtectedRule, red *redact.Redactor) (string, error) {
	return checkEdit(p, diffBase(p.Diff), content, protected, red)
}

// CheckRewrite runs the same gates on the person's edit of what an applied
// proposal wrote; what is added is measured against the file as it is now.
func CheckRewrite(p domain.LearnProposal, current, content string, protected []domain.LearnProtectedRule, red *redact.Redactor) (string, error) {
	base := map[string]bool{}
	for _, l := range strings.Split(current, "\n") {
		base[l] = true
	}
	return checkEdit(p, base, content, protected, red)
}

func checkEdit(p domain.LearnProposal, base map[string]bool, content string, protected []domain.LearnProtectedRule, red *redact.Redactor) (string, error) {
	content = noEmDash(strings.TrimRight(content, "\n")) + "\n"
	if strings.TrimSpace(content) == "" {
		return "", errors.New("the edit is empty")
	}
	switch p.Action {
	case domain.LearnProposeCreateMemory, domain.LearnProposeUpdateMemory:
		if _, body, err := skills.Parse(quoteDescription(content)); err != nil {
			return "", fmt.Errorf("memory file: %w", err)
		} else if len(body) > skills.MaxBodyBytes {
			return "", fmt.Errorf("the body is %d bytes, at most %d", len(body), skills.MaxBodyBytes)
		}
		content = quoteDescription(content)
	case domain.LearnProposeUpdateSkill:
		if _, err := skills.Check(content); err != nil {
			return "", fmt.Errorf("skill file: %w", err)
		}
	case domain.LearnProposeEditRuleFile:
	default:
		return "", fmt.Errorf("a %s proposal has no file to edit", p.Action)
	}
	addedText := addedSince(base, content)
	if hits := rules.Forbidden(addedText, p.ProjectID, protected); len(hits) > 0 {
		return "", fmt.Errorf("adds %q, which your protected rule %q forbids", hits[0].Match, hits[0].Rule)
	}
	if red != nil {
		if _, counts := red.Redact(addedText); len(counts) > 0 {
			kinds := make([]string, 0, len(counts))
			for k := range counts {
				kinds = append(kinds, k)
			}
			sort.Strings(kinds)
			return "", fmt.Errorf("adds a value that looks sensitive (%s)", strings.Join(kinds, ", "))
		}
	}
	return content, nil
}

// diffBase is the lines of the file a diff was made against.
func diffBase(diff string) map[string]bool {
	base := map[string]bool{}
	first := strings.SplitN(diff, "\n--- ", 2)[0]
	for _, l := range strings.Split(first, "\n") {
		if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") || strings.HasPrefix(l, "@@") {
			continue
		}
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "-") {
			base[l[1:]] = true
		}
	}
	return base
}

// addedSince is the text of content's lines that base did not have: what the
// person is adding, not what was there.
func addedSince(base map[string]bool, content string) string {
	var b strings.Builder
	for _, l := range strings.Split(content, "\n") {
		if !base[l] {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return b.String()
}
