package smoke

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// composeSummary renders a deterministic, agent-legible results summary the
// worker (or orchestrator) reads to know what passed, failed, or was skipped.
// Decided cases are listed in seq order with an icon, name, PR/file ref, and a
// note/evidence line; a trailing line counts anything not yet checked.
//
// Retired cases are counted but not listed. They are no longer part of what the
// user was asked to play, so folding an old verdict of theirs into "3 of 4
// checked" would misreport the checklist - but dropping them silently would hide
// the fact that it shrank, which is the one thing retiring is FOR.
func composeSummary(sessionID domain.SessionID, checks []domain.SmokeCheck) string {
	var pass, fail, skip, pending, retired int
	active := make([]domain.SmokeCheck, 0, len(checks))
	for _, c := range checks {
		if c.Retired() {
			retired++
			continue
		}
		active = append(active, c)
		switch c.Verdict {
		case domain.SmokePass:
			pass++
		case domain.SmokeFail:
			fail++
		case domain.SmokeSkip:
			skip++
		default:
			pending++
		}
	}
	total := len(active)
	checked := total - pending

	var b strings.Builder
	fmt.Fprintf(&b, "Smoke test results for this session: %d of %d checked · %d pass, %d fail", checked, total, pass, fail)
	if skip > 0 {
		fmt.Fprintf(&b, ", %d skipped", skip)
	}
	b.WriteString(".\n")

	for _, c := range active {
		if c.Verdict == domain.SmokePending {
			continue
		}
		b.WriteString("\n")
		b.WriteString(verdictIcon(c.Verdict))
		fmt.Fprintf(&b, " CHECK %d — %s", c.Seq, c.Name)
		if ref := caseRef(c); ref != "" {
			fmt.Fprintf(&b, " (%s)", ref)
		}
		if detail := caseDetail(c); detail != "" {
			b.WriteString("\n  ")
			b.WriteString(detail)
		}
	}

	if pending > 0 {
		fmt.Fprintf(&b, "\n… (%d pending, not yet checked)", pending)
	}
	if retired > 0 {
		fmt.Fprintf(&b, "\n… (%d retired out of the checklist; their results are kept)", retired)
	}

	fmt.Fprintf(&b, "\n\nOpen the Tests tab or run `ao smoke list %s` for full notes + evidence.", sessionID)
	return b.String()
}

func verdictIcon(v domain.SmokeVerdict) string {
	switch v {
	case domain.SmokePass:
		return "✓"
	case domain.SmokeFail:
		return "✗"
	case domain.SmokeSkip:
		return "⊘"
	default:
		return "○"
	}
}

func caseRef(c domain.SmokeCheck) string {
	parts := make([]string, 0, 2)
	if c.PRNum > 0 {
		parts = append(parts, fmt.Sprintf("PR #%d", c.PRNum))
	}
	if c.FileRef != "" {
		parts = append(parts, c.FileRef)
	}
	return strings.Join(parts, ", ")
}

func caseDetail(c domain.SmokeCheck) string {
	parts := make([]string, 0, 2)
	if note := strings.TrimSpace(c.Note); note != "" {
		parts = append(parts, "note: "+note)
	}
	if n := len(c.Evidence); n > 0 {
		parts = append(parts, fmt.Sprintf("Evidence: %d attached", n))
	}
	return strings.Join(parts, ". ")
}
