package notify

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Notification copy follows a few rules that keep the tray consistent. They are
// stated here because a message that is fine on its own can still disagree with
// the three beside it:
//
//  1. The title opens with the state, then names the work. The state prefix
//     makes the tray scannable; the board label after it is what the human
//     actually recognises. The number is NOT in the title - nobody recognises
//     "3039", everybody recognises "cta http proto".
//  2. Every identifier carries its sigil: "#149" GitHub, "!3039" GitLab,
//     "@agent-orchestrator-105" session. See the AO id reference convention.
//  3. The body never restates the title. It carries the identifier plus the one
//     fact that changes what the human would do next.
//  4. Facts are stated positively. No "has no known X" hedging.
//  5. The board label is shown exactly as the human typed it. Do not capitalise
//     or otherwise transform it: mutating a user-authored string and showing it
//     back reads as a spelling error ("gl approval gate" -> "Gl approval gate").
//     Rule 1 is what makes that free, since the label is never sentence-initial.
//
// The same string is used for BOTH the in-app tray row and the macOS toast, so
// titles stay short. Measured in the real system font (13pt semibold) against a
// ~250pt banner title: "Ready to merge: " leaves room for a 20-character label,
// which is exactly the longest board label in use, so the other prefixes are
// kept shorter to preserve headroom.
const (
	prefixNeedsInput = "Needs you: "
	prefixReady      = "Ready to merge: "
	prefixMerged     = "Merged: "
	prefixClosed     = "Closed: "
)

func enrich(intent Intent) (domain.NotificationRecord, error) {
	rec := domain.NotificationRecord{
		SessionID: intent.SessionID,
		ProjectID: intent.ProjectID,
		PRURL:     strings.TrimSpace(intent.PRURL),
		Type:      intent.Type,
		Status:    domain.NotificationUnread,
		CreatedAt: intent.CreatedAt,
	}
	if !intent.Type.Valid() {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationType
	}
	if intent.Type != domain.NotificationNeedsInput && rec.PRURL == "" {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationRecord
	}
	rec.Title = titleForIntent(intent)
	rec.Body = bodyForIntent(intent)
	if err := rec.Validate(); err != nil {
		return domain.NotificationRecord{}, err
	}
	return rec, nil
}

func titleForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		return prefixNeedsInput + workLabel(intent)
	case domain.NotificationReadyToMerge:
		return prefixReady + workLabel(intent)
	case domain.NotificationPRMerged:
		return prefixMerged + workLabel(intent)
	case domain.NotificationPRClosedUnmerged:
		return prefixClosed + workLabel(intent)
	default:
		return "Notification"
	}
}

func bodyForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		// The session id is worth carrying because it is the handle the human
		// uses to address the session - but only when the title is not already
		// showing it as the fallback label.
		if ref := sessionRef(intent); ref != "" && ref != workLabel(intent) {
			return fmt.Sprintf("Paused until you reply. (%s)", ref)
		}
		return "Paused until you reply."
	case domain.NotificationReadyToMerge:
		// Where the PR is, and why it is ready. "CI passed" is the positive form
		// of the old "no known blocking CI"; the review half stays negative
		// because that IS the fact, but a checkable one rather than a hedge.
		// The target branch belongs here and only here: it is what the human is
		// about to merge INTO, so it changes the decision. On an already merged
		// or closed PR the same fact is spent history.
		return joinSentences(withTarget(prLocation(intent), intent), "CI passed, no unresolved review comments.")
	case domain.NotificationPRMerged, domain.NotificationPRClosedUnmerged:
		// The title already said what happened, so the body carries identity:
		// where it lives plus the PR's own title.
		return joinDetail(prLocation(intent), strings.TrimSpace(intent.PRTitle))
	default:
		return ""
	}
}

// workLabel names the work the way the human recognises it. The board label they
// typed comes first and is used verbatim; everything after it is a fallback for
// a session that has no label.
func workLabel(intent Intent) string {
	if v := strings.TrimSpace(intent.SessionDisplayName); v != "" {
		return v
	}
	// An orchestrator session never has a board label - it is not a board card.
	// The renderer already special-cases this to the literal word (see
	// isOrchestratorSession in CenterPane), so the tray must agree rather than
	// falling through to a bare session id.
	if intent.SessionKind == domain.KindOrchestrator {
		return "Orchestrator"
	}
	if v := strings.TrimSpace(intent.PRTitle); v != "" {
		return v
	}
	if ref := sessionRef(intent); ref != "" {
		return ref
	}
	if ref := prRef(intent); ref != "" {
		return ref
	}
	return "a session"
}

// prLocation renders "!3039 in demo-ios-app" from whichever of those facts are
// present.
func prLocation(intent Intent) string {
	out := prRef(intent)
	if repo := repoShort(intent.Repo); repo != "" {
		if out == "" {
			return repo
		}
		out += " in " + repo
	}
	return out
}

// withTarget appends ", targeting develop" to a non-empty location.
func withTarget(location string, intent Intent) string {
	target := strings.TrimSpace(intent.PRTargetBranch)
	if location == "" || target == "" {
		return location
	}
	return location + ", targeting " + target
}

// prRef renders the PR/MR number with the sigil its provider uses: "!3039" for a
// GitLab merge request, "#149" for a GitHub pull request.
func prRef(intent Intent) string {
	if intent.PRNumber <= 0 {
		return ""
	}
	if isGitLab(intent) {
		return fmt.Sprintf("!%d", intent.PRNumber)
	}
	return fmt.Sprintf("#%d", intent.PRNumber)
}

// sessionRef renders the session id as the handle the human addresses it by.
func sessionRef(intent Intent) string {
	if intent.SessionID == "" {
		return ""
	}
	return "@" + string(intent.SessionID)
}

// isGitLab prefers the observed provider and falls back to the URL shape, which
// is decisive on its own: a GitLab merge request URL carries "/-/merge_requests/".
func isGitLab(intent Intent) bool {
	if p := strings.TrimSpace(intent.Provider); p != "" {
		return strings.EqualFold(p, "gitlab")
	}
	return strings.Contains(intent.PRURL, "/-/merge_requests/")
}

// repoShort keeps the last path segment of a repo path, so the deeply nested
// "example-org/apps/demo-ios-app" reads as "demo-ios-app".
func repoShort(repo string) string {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	if repo == "" {
		return ""
	}
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// joinSentences appends a trailing sentence to an optional lead fragment,
// terminating the fragment so the two do not run together.
func joinSentences(lead, sentence string) string {
	if lead == "" {
		return sentence
	}
	return lead + ". " + sentence
}

// joinDetail appends free-form detail (a PR title, which may contain its own
// punctuation) to a lead fragment with a separator that survives it.
func joinDetail(lead, detail string) string {
	switch {
	case lead == "":
		return detail
	case detail == "":
		return lead
	default:
		return lead + " - " + detail
	}
}
