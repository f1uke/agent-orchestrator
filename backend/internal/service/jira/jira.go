// Package jira is the read-only service that resolves a session's bound Jira key
// and returns the issue's display context for the Summary tab. It is the seam
// between the HTTP controller and the jira-cli adapter.
package jira

import (
	"context"
	"errors"
	"strings"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// issueIDPrefix is the canonical prefix a Jira-bound session carries in
// sessions.issue_id (domain.CanonicalIssueID form "<provider>:<native>").
const issueIDPrefix = string(domain.TrackerProviderJira) + ":"

// SessionReader reads one session so the service can resolve its bound issue id.
// Satisfied by *service/session.Service.
type SessionReader interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

// IssueReader fetches one Jira issue's display projection. Satisfied by
// *adapters/jira.Client.
type IssueReader interface {
	Get(ctx context.Context, key string) (jiraadapter.Issue, error)
}

// Result is the service's answer for one session:
//   - Linked is false when the session has no Jira binding (the common case);
//     the UI renders nothing.
//   - Issue is set when the bound key resolved.
//   - FetchError is a user-facing message when the session IS Jira-linked but
//     the live fetch failed (missing issue, auth, or jira-cli unavailable). It
//     is returned as a normal 200 so a Jira hiccup never breaks the Summary tab.
type Result struct {
	Linked     bool
	Issue      *jiraadapter.Issue
	FetchError string
}

// Service resolves session → Jira context.
type Service struct {
	sessions SessionReader
	issues   IssueReader
}

// New builds a Service. A nil issues reader means Jira access is unconfigured;
// linked sessions then report a FetchError rather than panicking.
func New(sessions SessionReader, issues IssueReader) *Service {
	return &Service{sessions: sessions, issues: issues}
}

// Context returns the Jira display context for a session. It returns an error
// only when the session itself cannot be read (propagated for the HTTP layer to
// map, e.g. 404); Jira-side failures are folded into Result.FetchError.
func (s *Service) Context(ctx context.Context, id domain.SessionID) (Result, error) {
	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		return Result{}, err
	}
	key, ok := jiraKey(string(sess.IssueID))
	if !ok {
		return Result{Linked: false}, nil
	}
	if s.issues == nil {
		return Result{Linked: true, FetchError: "Jira access is not configured."}, nil
	}
	iss, err := s.issues.Get(ctx, key)
	if err != nil {
		return Result{Linked: true, FetchError: fetchMessage(err)}, nil
	}
	return Result{Linked: true, Issue: &iss}, nil
}

// jiraKey extracts the Jira issue key from a canonical issue id, or reports
// false when the session is not Jira-bound (plain manual title, github:/gitlab:
// intake id, or empty).
func jiraKey(issueID string) (string, bool) {
	if !strings.HasPrefix(issueID, issueIDPrefix) {
		return "", false
	}
	key := strings.TrimSpace(strings.TrimPrefix(issueID, issueIDPrefix))
	if key == "" {
		return "", false
	}
	return key, true
}

// fetchMessage turns an adapter sentinel into a short, user-facing explanation.
func fetchMessage(err error) string {
	switch {
	case errors.Is(err, jiraadapter.ErrNotFound):
		return "Jira issue not found or not visible to your account."
	case errors.Is(err, jiraadapter.ErrAuthFailed):
		return "Jira authentication failed — check your jira-cli login."
	case errors.Is(err, jiraadapter.ErrBadKey):
		return "The linked Jira key is invalid."
	case errors.Is(err, jiraadapter.ErrUnavailable):
		return "Couldn't reach Jira (jira-cli unavailable)."
	default:
		return "Couldn't load the Jira issue."
	}
}
