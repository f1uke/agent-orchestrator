package session

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// PRThreadComment is one review comment on a PR thread.
type PRThreadComment struct {
	ID        string
	Author    string
	Body      string
	URL       string
	Resolved  bool
	IsBot     bool
	CreatedAt time.Time
}

// PRCommentThread is a review thread with its comments, anchored to a file/line.
type PRCommentThread struct {
	ThreadID string
	Path     string
	Line     int
	Resolved bool
	IsBot    bool
	Comments []PRThreadComment
}

// PRCommentGroup is one PR's review threads.
type PRCommentGroup struct {
	PRURL    string
	HTMLURL  string
	Provider string
	Number   int
	HeadSHA  string
	Threads  []PRCommentThread
}

// ListPRCommentThreads returns each of the session's PRs with its review threads
// and comments. Comments are attached to their thread; a comment referencing an
// unknown thread id gets a synthesized thread from its own file/line so nothing
// is dropped.
func (s *Service) ListPRCommentThreads(ctx context.Context, id domain.SessionID) ([]PRCommentGroup, error) {
	if _, ok, err := s.store.GetSession(ctx, id); err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	} else if !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]PRCommentGroup, 0, len(prs))
	for _, pr := range prs {
		threads, err := s.store.ListPRReviewThreads(ctx, pr.URL)
		if err != nil {
			return nil, err
		}
		comments, err := s.store.ListPRComments(ctx, pr.URL)
		if err != nil {
			return nil, err
		}
		out = append(out, PRCommentGroup{
			PRURL:    pr.URL,
			HTMLURL:  pr.HTMLURL,
			Provider: pr.Provider,
			Number:   pr.Number,
			HeadSHA:  pr.HeadSHA,
			Threads:  buildPRCommentThreads(threads, comments),
		})
	}
	return out, nil
}

// buildPRCommentThreads keys threads by id (preserving list order), attaches
// comments, and synthesizes a thread for any comment whose thread id is unknown.
func buildPRCommentThreads(threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment) []PRCommentThread {
	order := make([]string, 0, len(threads))
	byID := make(map[string]*PRCommentThread, len(threads))
	add := func(id, path string, line int, resolved, isBot bool) *PRCommentThread {
		t := &PRCommentThread{ThreadID: id, Path: path, Line: line, Resolved: resolved, IsBot: isBot}
		byID[id] = t
		order = append(order, id)
		return t
	}
	for _, th := range threads {
		add(th.ThreadID, th.Path, th.Line, th.Resolved, th.IsBot)
	}
	for _, c := range comments {
		t, ok := byID[c.ThreadID]
		if !ok {
			t = add(c.ThreadID, c.File, c.Line, c.Resolved, c.IsBot)
		}
		t.Comments = append(t.Comments, PRThreadComment{
			ID:        c.ID,
			Author:    c.Author,
			Body:      c.Body,
			URL:       c.URL,
			Resolved:  c.Resolved,
			IsBot:     c.IsBot,
			CreatedAt: c.CreatedAt,
		})
	}
	res := make([]PRCommentThread, 0, len(order))
	for _, id := range order {
		res = append(res, *byID[id])
	}
	return res
}
