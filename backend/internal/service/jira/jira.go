// Package jira is the read-only service that resolves a session's bound Jira key
// and returns the issue's display context for the Summary tab. It is the seam
// between the HTTP controller and the jira-cli adapter.
package jira

import (
	"context"
	"errors"
	"fmt"
	"strings"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrNotLinked reports that a status action targeted a session with no Jira
// binding. The controller maps it to a 4xx (nothing to move).
var ErrNotLinked = errors.New("jira: session is not linked to a Jira issue")

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

// TransitionMover lists an issue's available status transitions and applies one
// — the single sanctioned Jira write. Satisfied by *adapters/jira.Client.
type TransitionMover interface {
	Transitions(ctx context.Context, key string) ([]jiraadapter.Transition, error)
	Move(ctx context.Context, key, transitionID string) error
}

// MoveResult is the outcome of an applied status move: the issue's new status
// (re-read best-effort) so the UI can update the pill without a round trip.
type MoveResult struct {
	Key            string
	Status         string
	StatusCategory string
	StatusColor    string
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

// Service resolves session → Jira context, and drives the status-move write.
type Service struct {
	sessions SessionReader
	issues   IssueReader
	moves    TransitionMover
}

// New builds a Service. A nil issues/moves reader means Jira access is
// unconfigured; linked sessions then report a FetchError (display) or an
// unavailable error (transitions/move) rather than panicking.
func New(sessions SessionReader, issues IssueReader, moves TransitionMover) *Service {
	return &Service{sessions: sessions, issues: issues, moves: moves}
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

// Transitions lists the session's Jira issue's available status transitions,
// read live. Errors surface to the caller (unlike Context's display fetch) so the
// Move-status dialog can show why the list is unavailable.
func (s *Service) Transitions(ctx context.Context, id domain.SessionID) ([]jiraadapter.Transition, error) {
	key, err := s.requireKey(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.moves == nil {
		return nil, fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	return s.moves.Transitions(ctx, key)
}

// Move applies a status transition to the session's Jira issue — the one
// sanctioned write. On success it re-reads the issue (best-effort) so the result
// carries the new status.
func (s *Service) Move(ctx context.Context, id domain.SessionID, transitionID string) (MoveResult, error) {
	key, err := s.requireKey(ctx, id)
	if err != nil {
		return MoveResult{}, err
	}
	if s.moves == nil {
		return MoveResult{}, fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	if err := s.moves.Move(ctx, key, transitionID); err != nil {
		return MoveResult{}, err
	}
	res := MoveResult{Key: key}
	// Best-effort re-read so the UI reflects the new status immediately; a read
	// failure here does not undo the (already successful) move.
	if s.issues != nil {
		if iss, err := s.issues.Get(ctx, key); err == nil {
			res.Status = iss.Status
			res.StatusCategory = iss.StatusCategory
			res.StatusColor = iss.StatusColor
		}
	}
	return res, nil
}

// requireKey resolves the session's bound Jira key, returning ErrNotLinked when
// the session has no Jira binding (the status actions have nothing to target).
func (s *Service) requireKey(ctx context.Context, id domain.SessionID) (string, error) {
	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		return "", err
	}
	key, ok := jiraKey(string(sess.IssueID))
	if !ok {
		return "", ErrNotLinked
	}
	return key, nil
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
