package session

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
)

// DispatchCommentToWorker renders the review-comment-dispatch template for one
// PR review thread's comments and delivers it to the worker session, appending
// the operator's optional extra prompt. Comment bodies and the extra prompt are
// attacker-influenceable and reach the worker PTY, so both are sanitized.
func (s *Service) DispatchCommentToWorker(ctx context.Context, id domain.SessionID, prURL, threadID, extraPrompt string) error {
	if _, ok, err := s.store.GetSession(ctx, id); err != nil {
		return err
	} else if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return err
	}
	found := false
	for _, p := range prs {
		if p.URL == prURL {
			found = true
			break
		}
	}
	if !found {
		return apierr.NotFound("PR_NOT_FOUND", "Unknown PR for session")
	}
	comments, err := s.store.ListPRComments(ctx, prURL)
	if err != nil {
		return err
	}
	items := make([]messagetemplates.ReviewCommentItem, 0, len(comments))
	for _, c := range comments {
		if c.ThreadID == threadID {
			// File and Body are provider-controlled and reach the worker PTY;
			// sanitize both (Line is an int and safe).
			items = append(items, messagetemplates.ReviewCommentItem{
				Index: len(items) + 1,
				File:  domain.SanitizeControlChars(c.File),
				Line:  c.Line,
				Body:  domain.SanitizeControlChars(c.Body),
			})
		}
	}
	if len(items) == 0 {
		return apierr.Invalid("NO_COMMENTS", "Thread has no comments to dispatch", nil)
	}
	if s.renderer == nil {
		return apierr.Invalid("DISPATCH_UNAVAILABLE", "Comment dispatch is not configured", nil)
	}
	msg, err := s.renderer.Render(messagetemplates.NameReviewCommentDispatch, messagetemplates.ReviewCommentData{
		PRURL:    domain.SanitizeControlChars(prURL),
		Count:    len(items),
		Comments: items,
	})
	if err != nil {
		return err
	}
	if extra := strings.TrimSpace(extraPrompt); extra != "" {
		msg += "\n\n" + domain.SanitizeControlChars(extra)
	}
	return s.Send(ctx, id, msg)
}
