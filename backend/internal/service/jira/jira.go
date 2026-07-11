// Package jira is the read-only service that resolves a session's bound Jira key
// and returns the issue's display context for the Summary tab. It is the seam
// between the HTTP controller and the jira-cli adapter.
package jira

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

// SessionGateway is the session dependency: reading a session plus setting its
// Jira binding (the link/unlink-after-creation path). Satisfied by
// *service/session.Service.
type SessionGateway interface {
	SessionReader
	SetIssueBinding(ctx context.Context, id domain.SessionID, issueID, displayName string) (domain.Session, error)
}

// IssueSearcher runs cross-project issue search and lists projects — the REST
// path that replaces the unusable `jira issue list`. Satisfied by
// *adapters/jira.Client.
type IssueSearcher interface {
	SearchIssues(ctx context.Context, jql string, limit int) ([]jiraadapter.IssueSummary, error)
	ListProjects(ctx context.Context, query string) ([]jiraadapter.ProjectRef, error)
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

// Service resolves session → Jira context, drives the status-move write, and
// powers cross-project search + the after-the-fact link/unlink.
type Service struct {
	sessions SessionGateway
	issues   IssueReader
	moves    TransitionMover
	searcher IssueSearcher
}

// New builds a Service. A nil issues/moves/searcher means that slice of Jira
// access is unconfigured; linked sessions then report a FetchError (display) or
// an unavailable error (transitions/move/search) rather than panicking.
func New(sessions SessionGateway, issues IssueReader, moves TransitionMover, searcher IssueSearcher) *Service {
	return &Service{sessions: sessions, issues: issues, moves: moves, searcher: searcher}
}

// Jira issue-key shapes for query classification. fullKeyRE is a complete key
// (PROJECT-123); projectKeyRE is a bare project key (e.g. ACME, DEMO) typed on its
// own — matched case-insensitively after upper-casing.
var (
	fullKeyRE    = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)
	projectKeyRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
)

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

// Search returns issues matching a free-text query, optionally scoped to a
// project key. The JQL is built here (buildJQL) so the query semantics stay
// testable; the adapter is a dumb executor.
func (s *Service) Search(ctx context.Context, project, text string) ([]jiraadapter.IssueSummary, error) {
	if s.searcher == nil {
		return nil, fmt.Errorf("%w: Jira search is not configured", jiraadapter.ErrUnavailable)
	}
	return s.searcher.SearchIssues(ctx, s.buildJQL(ctx, project, text), 25)
}

// Projects lists the user's Jira projects (optionally filtered) for the project
// picker.
func (s *Service) Projects(ctx context.Context, query string) ([]jiraadapter.ProjectRef, error) {
	if s.searcher == nil {
		return nil, fmt.Errorf("%w: Jira search is not configured", jiraadapter.ErrUnavailable)
	}
	return s.searcher.ListProjects(ctx, query)
}

// Resolve validates a single issue key exists/visible and returns its summary —
// used to confirm a key before binding it to a session. A malformed key is a
// bad-key error; an unknown one is not-found.
func (s *Service) Resolve(ctx context.Context, key string) (jiraadapter.IssueSummary, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if !fullKeyRE.MatchString(key) {
		return jiraadapter.IssueSummary{}, fmt.Errorf("%w: %q", jiraadapter.ErrBadKey, key)
	}
	if s.searcher == nil {
		return jiraadapter.IssueSummary{}, fmt.Errorf("%w: Jira search is not configured", jiraadapter.ErrUnavailable)
	}
	rows, err := s.searcher.SearchIssues(ctx, `key = "`+key+`"`, 1)
	if err != nil {
		return jiraadapter.IssueSummary{}, err
	}
	if len(rows) == 0 {
		return jiraadapter.IssueSummary{}, fmt.Errorf("%w: %s", jiraadapter.ErrNotFound, key)
	}
	return rows[0], nil
}

// SetBinding links an EXISTING session to a Jira issue after the fact: it
// resolves the key (validating it), then sets issue_id = "jira:<KEY>". The
// display name is preserved if the session already has one, else it takes the
// issue title (capped) so the sidebar shows something readable rather than the
// raw key. Returns the resolved issue summary for the response.
func (s *Service) SetBinding(ctx context.Context, id domain.SessionID, key string) (jiraadapter.IssueSummary, error) {
	iss, err := s.Resolve(ctx, key)
	if err != nil {
		return jiraadapter.IssueSummary{}, err
	}
	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		return jiraadapter.IssueSummary{}, err
	}
	display := strings.TrimSpace(sess.DisplayName)
	if display == "" {
		display = capDisplayName(iss.Title)
	}
	if display == "" {
		display = iss.Key
	}
	if _, err := s.sessions.SetIssueBinding(ctx, id, issueIDPrefix+iss.Key, display); err != nil {
		return jiraadapter.IssueSummary{}, err
	}
	return iss, nil
}

// Unlink removes a session's Jira binding: issue_id becomes the plain display
// label (so the card still shows a name) and no longer carries the "jira:"
// prefix. Reports ErrNotLinked when the session was not Jira-bound.
func (s *Service) Unlink(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	key, ok := jiraKey(string(sess.IssueID))
	if !ok {
		return domain.Session{}, ErrNotLinked
	}
	label := strings.TrimSpace(sess.DisplayName)
	if label == "" {
		label = key
	}
	return s.sessions.SetIssueBinding(ctx, id, label, label)
}

// buildJQL classifies the query into JQL. A full key resolves that one issue; a
// bare project key (confirmed to exist, so we never send a 400-inducing
// `project = NOPE`) scopes to that project — which is how "demo" surfaces DEMO-*
// that a text match never would; anything else is a summary/text contains-search.
// Always newest-first.
func (s *Service) buildJQL(ctx context.Context, project, text string) string {
	project = strings.TrimSpace(project)
	text = strings.TrimSpace(text)
	if fullKeyRE.MatchString(strings.ToUpper(text)) {
		return `key = "` + strings.ToUpper(text) + `"`
	}
	scope := project
	if scope == "" && projectKeyRE.MatchString(strings.ToUpper(text)) {
		if key := s.confirmProjectKey(ctx, text); key != "" {
			scope = key
			text = "" // the whole query WAS the project key; don't also text-match it
		}
	}
	var clauses []string
	if scope != "" {
		clauses = append(clauses, `project = "`+escapeJQL(scope)+`"`)
	}
	if text != "" {
		esc := escapeJQL(text)
		clauses = append(clauses, `(summary ~ "`+esc+`*" OR text ~ "`+esc+`*")`)
	}
	jql := strings.Join(clauses, " AND ")
	if jql != "" {
		jql += " "
	}
	return jql + "ORDER BY updated DESC"
}

// confirmProjectKey returns the canonical project key when text names a real
// project (case-insensitive exact key match), else "". Guards buildJQL from
// emitting `project = <not-a-project>` (a 400).
func (s *Service) confirmProjectKey(ctx context.Context, text string) string {
	if s.searcher == nil {
		return ""
	}
	projects, err := s.searcher.ListProjects(ctx, text)
	if err != nil {
		return ""
	}
	text = strings.TrimSpace(text)
	for _, p := range projects {
		if strings.EqualFold(p.Key, text) {
			return p.Key
		}
	}
	return ""
}

// escapeJQL escapes a value for a JQL double-quoted string literal.
func escapeJQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// capDisplayName trims a title to the session display-name length budget (20
// runes, matching the create path), on a rune boundary.
func capDisplayName(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= 20 {
		return s
	}
	return strings.TrimSpace(string(r[:20]))
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
