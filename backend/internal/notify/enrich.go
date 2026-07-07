package notify

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
		return fmt.Sprintf("%s needs input", sessionLabel(intent))
	case domain.NotificationReadyToMerge:
		return fmt.Sprintf("%s is ready to merge", prLabel(intent))
	case domain.NotificationPRMerged:
		return fmt.Sprintf("%s was merged", prLabel(intent))
	case domain.NotificationPRClosedUnmerged:
		return fmt.Sprintf("%s was closed without merging", prLabel(intent))
	default:
		return "Notification"
	}
}

func bodyForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		return "The agent is waiting for your response."
	case domain.NotificationReadyToMerge:
		if s := sessionLabel(intent); s != "session" {
			return fmt.Sprintf("%s has no known blocking CI or review feedback.", s)
		}
		return fmt.Sprintf("The %s has no known blocking CI or review feedback.", prNoun(intent))
	case domain.NotificationPRMerged:
		if title := strings.TrimSpace(intent.PRTitle); title != "" {
			return fmt.Sprintf("%s was merged.", title)
		}
		return fmt.Sprintf("The %s was merged.", prNoun(intent))
	case domain.NotificationPRClosedUnmerged:
		if title := strings.TrimSpace(intent.PRTitle); title != "" {
			return fmt.Sprintf("%s was closed without merging.", title)
		}
		return fmt.Sprintf("The %s was closed without merging.", prNoun(intent))
	default:
		return ""
	}
}

// prNoun picks the provider-appropriate noun for a change request from the URL
// shape: a GitLab merge request (its URL carries "/-/merge_requests/") versus a
// GitHub pull request.
func prNoun(intent Intent) string {
	if strings.Contains(intent.PRURL, "/-/merge_requests/") {
		return "merge request"
	}
	return "pull request"
}

func sessionLabel(intent Intent) string {
	if v := strings.TrimSpace(intent.SessionDisplayName); v != "" {
		return v
	}
	if intent.SessionID != "" {
		return string(intent.SessionID)
	}
	return "session"
}

func prLabel(intent Intent) string {
	if intent.PRNumber > 0 {
		return fmt.Sprintf("PR #%d", intent.PRNumber)
	}
	if title := strings.TrimSpace(intent.PRTitle); title != "" {
		return "PR " + title
	}
	return "PR"
}
