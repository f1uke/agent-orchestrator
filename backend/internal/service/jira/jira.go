// Package jira is the read-only service that resolves a session's bound Jira key
// and returns the issue's display context for the Summary tab. It is the seam
// between the HTTP controller and the Jira Cloud REST v3 adapter.
package jira

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrNotLinked reports that a status action targeted a session with no Jira
// binding. The controller maps it to a 4xx (nothing to move).
var ErrNotLinked = errors.New("jira: session is not linked to a Jira issue")

// ErrKeyNotInIssueTree reports that a status action named an issue key that is
// neither the session's bound issue nor one of its subtasks. It scopes the move
// write to the session's own issue tree so a session can never move an unrelated
// issue. The controller maps it to a 4xx.
var ErrKeyNotInIssueTree = errors.New("jira: issue is not in this session's issue tree")

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

// IssueSearcher runs cross-project issue search, lists projects, and resolves the
// authenticated account — the live-REST cluster powering Browse Jira (replacing the
// unusable `jira issue list`). Satisfied by *adapters/jira.Client.
type IssueSearcher interface {
	SearchIssues(ctx context.Context, jql string, limit int) ([]jiraadapter.IssueSummary, error)
	ListProjects(ctx context.Context, query string) ([]jiraadapter.ProjectRef, error)
	Myself(ctx context.Context) (jiraadapter.CurrentUser, error)
}

// IssueReader fetches one Jira issue's display projection. Satisfied by
// *adapters/jira.Client.
type IssueReader interface {
	Get(ctx context.Context, key string) (jiraadapter.Issue, error)
	// DownloadAttachment streams one attachment's bytes (+ Content-Type) for the
	// Summary tab's inline media previews. Caller closes the reader.
	DownloadAttachment(ctx context.Context, attachmentID string) (io.ReadCloser, string, error)
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
//     the live fetch failed (missing issue, auth, or Jira unavailable). It
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
// own — matched case-insensitively after upper-casing; bareNumberRE is just the
// number half of a key, which only resolves when a project is already selected.
var (
	fullKeyRE    = regexp.MustCompile(`^[A-Z][A-Z0-9]+-\d+$`)
	projectKeyRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
	bareNumberRE = regexp.MustCompile(`^\d+$`)
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

// DownloadAttachment streams one attachment's bytes for the Summary tab's inline
// media previews. It is session-scoped so it can require a live Jira binding and
// configured access (the attachment id itself is instance-global). Read-only
// (display) — it never writes to Jira. The caller closes the returned reader.
func (s *Service) DownloadAttachment(ctx context.Context, id domain.SessionID, attachmentID string) (io.ReadCloser, string, error) {
	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if _, ok := jiraKey(string(sess.IssueID)); !ok {
		return nil, "", fmt.Errorf("%w: session is not linked to a Jira issue", jiraadapter.ErrBadRequest)
	}
	if s.issues == nil {
		return nil, "", fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	return s.issues.DownloadAttachment(ctx, attachmentID)
}

// Transitions lists the available status transitions for the session's issue, or
// — when key names a subtask of that issue — for the subtask, read live. Errors
// surface to the caller (unlike Context's display fetch) so the Move-status dialog
// can show why the list is unavailable. An empty key means the bound issue.
func (s *Service) Transitions(ctx context.Context, id domain.SessionID, key string) ([]jiraadapter.Transition, error) {
	target, err := s.resolveActionKey(ctx, id, key)
	if err != nil {
		return nil, err
	}
	if s.moves == nil {
		return nil, fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	return s.moves.Transitions(ctx, target)
}

// Move applies a status transition to the session's issue — or to a subtask of it
// when key is set — the one sanctioned write. On success it re-reads the moved
// issue (best-effort) so the result carries the new status. An empty key means the
// bound issue.
func (s *Service) Move(ctx context.Context, id domain.SessionID, key, transitionID string) (MoveResult, error) {
	target, err := s.resolveActionKey(ctx, id, key)
	if err != nil {
		return MoveResult{}, err
	}
	if s.moves == nil {
		return MoveResult{}, fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	if err := s.moves.Move(ctx, target, transitionID); err != nil {
		return MoveResult{}, err
	}
	res := MoveResult{Key: target}
	// Best-effort re-read so the UI reflects the new status immediately; a read
	// failure here does not undo the (already successful) move.
	if s.issues != nil {
		if iss, err := s.issues.Get(ctx, target); err == nil {
			res.Status = iss.Status
			res.StatusCategory = iss.StatusCategory
			res.StatusColor = iss.StatusColor
		}
	}
	return res, nil
}

// resolveActionKey returns the issue key a status action targets. An empty key (or
// the bound key itself) means the session's bound issue — the original behavior. A
// non-empty key must be a subtask of the bound issue: the status-move write stays
// scoped to the session's own issue tree, so a session can never move an unrelated
// issue. Confirming membership costs one display read of the parent.
func (s *Service) resolveActionKey(ctx context.Context, id domain.SessionID, key string) (string, error) {
	bound, err := s.requireKey(ctx, id)
	if err != nil {
		return "", err
	}
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" || key == bound {
		return bound, nil
	}
	if !fullKeyRE.MatchString(key) {
		return "", fmt.Errorf("%w: %q", jiraadapter.ErrBadKey, key)
	}
	if s.issues == nil {
		return "", fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	parent, err := s.issues.Get(ctx, bound)
	if err != nil {
		return "", err
	}
	for _, sub := range parent.Subtasks {
		if strings.EqualFold(sub.Key, key) {
			return sub.Key, nil
		}
	}
	return "", fmt.Errorf("%w: %s is not a subtask of %s", ErrKeyNotInIssueTree, key, bound)
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

// SearchParams is the structured Browse Jira query. The structured fields are
// pushed into the JQL server-side (buildJQL) so Jira returns the right rows rather
// than the client paring a capped page. JQL, when set, is raw advanced JQL that
// REPLACES the whole structured query (the structured fields are then ignored) —
// Jira's advanced-search behavior.
type SearchParams struct {
	Project      string
	Text         string
	Assignee     string   // accountId, or the "unassigned" sentinel
	Types        []string // issue-type names for `issuetype in (...)`
	HideDone     bool     // exclude done issues: `statusCategory != Done`
	ActiveSprint bool     // only open sprints: `sprint in openSprints()`
	JQL          string   // raw advanced JQL; when non-empty, replaces everything above
}

// Search returns issues matching the query. Structured filters are ANDed into the
// JQL server-side — which is why "assignee = Fluke" surfaces all of Fluke's issues,
// not just those in the most-recent page. Advanced JQL (p.JQL), when set, drives the
// search verbatim. The adapter is a dumb executor.
func (s *Service) Search(ctx context.Context, p SearchParams) ([]jiraadapter.IssueSummary, error) {
	if s.searcher == nil {
		return nil, fmt.Errorf("%w: Jira search is not configured", jiraadapter.ErrUnavailable)
	}
	// Page up to the adapter's browse cap: Browse Jira groups results by sprint and
	// filters in the JQL, so a wide, paginated set gives each section its issues
	// instead of only what fits in one page.
	return s.searcher.SearchIssues(ctx, s.buildJQL(ctx, p), jiraadapter.SearchMaxResults)
}

// GetIssue fetches one issue's full display projection by key — the read behind the
// Browse Jira detail view (pre-session, so no session binding). Validates the key
// shape, then reads live. Errors surface to the caller so the detail panel can show
// why the fetch failed (unlike the session Context path, which folds them into a
// FetchError). Reuses the same adapter read as the session display.
func (s *Service) GetIssue(ctx context.Context, key string) (jiraadapter.Issue, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if !fullKeyRE.MatchString(key) {
		return jiraadapter.Issue{}, fmt.Errorf("%w: %q", jiraadapter.ErrBadKey, key)
	}
	if s.issues == nil {
		return jiraadapter.Issue{}, fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	return s.issues.Get(ctx, key)
}

// IssueTransitions lists the live status transitions for any issue by key — the
// Browse Jira detail view's Move-status entry, pre-session. Unlike the session
// Transitions path it is not scoped to a session's issue tree (there is no session);
// the user is acting directly on the issue they opened.
func (s *Service) IssueTransitions(ctx context.Context, key string) ([]jiraadapter.Transition, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if !fullKeyRE.MatchString(key) {
		return nil, fmt.Errorf("%w: %q", jiraadapter.ErrBadKey, key)
	}
	if s.moves == nil {
		return nil, fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	return s.moves.Transitions(ctx, key)
}

// MoveIssue applies a status transition to any issue by key — the one sanctioned
// write, from the Browse Jira detail view (pre-session). On success it re-reads the
// issue (best-effort) so the result carries the new status.
func (s *Service) MoveIssue(ctx context.Context, key, transitionID string) (MoveResult, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	if !fullKeyRE.MatchString(key) {
		return MoveResult{}, fmt.Errorf("%w: %q", jiraadapter.ErrBadKey, key)
	}
	if s.moves == nil {
		return MoveResult{}, fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	if err := s.moves.Move(ctx, key, transitionID); err != nil {
		return MoveResult{}, err
	}
	res := MoveResult{Key: key}
	if s.issues != nil {
		if iss, err := s.issues.Get(ctx, key); err == nil {
			res.Status = iss.Status
			res.StatusCategory = iss.StatusCategory
			res.StatusColor = iss.StatusColor
		}
	}
	return res, nil
}

// CurrentUser returns the Jira account that owns the configured API token, so
// Browse Jira can highlight the rows assigned to the viewer. The id is stable; the
// caller (and the frontend hook) cache it.
func (s *Service) CurrentUser(ctx context.Context) (jiraadapter.CurrentUser, error) {
	if s.searcher == nil {
		return jiraadapter.CurrentUser{}, fmt.Errorf("%w: Jira access is not configured", jiraadapter.ErrUnavailable)
	}
	return s.searcher.Myself(ctx)
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

// Unlink removes a session's Jira binding: issue_id is cleared to the empty
// string (the unbound representation - the column is NOT NULL with an empty
// default), while the display name is preserved so the card still shows a
// readable label. A session that
// never had a display name falls back to the key it was unlinked from, so the
// card does not go blank. Reports ErrNotLinked when the session was not
// Jira-bound.
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
	return s.sessions.SetIssueBinding(ctx, id, "", label)
}

// assigneeUnassigned is the sentinel the client sends for the "Unassigned" filter
// (a real accountId is never this word), mapped to the JQL `assignee is EMPTY`.
const assigneeUnassigned = "unassigned"

// buildJQL classifies the query into JQL. Raw advanced JQL (p.JQL), when set, is
// returned verbatim and drives the search fully (the structured fields are ignored)
// — Jira's advanced search; a malformed query surfaces as a 400 the caller renders.
// Otherwise: a full key resolves that one issue (ignoring the filters — an exact
// lookup is unambiguous); a bare number with a project selected resolves that
// project's issue, since a key is not searchable as prose; a bare project key
// (confirmed to exist, so we never send a 400-inducing `project = NOPE`) scopes to
// that project — which is how "demo" surfaces DEMO-* that a text match never would;
// anything else is a summary/text contains-search built by textClause, which owns
// the text-parser escaping. Assignee (accountId), issue types, hide-done and
// active-sprint, when set, are ANDed in as server-side filters. Always newest-first.
func (s *Service) buildJQL(ctx context.Context, p SearchParams) string {
	if raw := strings.TrimSpace(p.JQL); raw != "" {
		return raw
	}
	project := strings.TrimSpace(p.Project)
	text := strings.TrimSpace(p.Text)
	if fullKeyRE.MatchString(strings.ToUpper(text)) {
		return `key = "` + strings.ToUpper(text) + `"`
	}
	// A bare number is never findable as prose — an issue's key is not part of its
	// summary/description text, so `~ "2271*"` matches nothing. With a project
	// selected the number is unambiguous, so resolve it as that project's issue.
	if project != "" && bareNumberRE.MatchString(text) {
		return `key = "` + strings.ToUpper(escapeJQL(project)) + `-` + text + `"`
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
	if clause := textClause(text); clause != "" {
		clauses = append(clauses, clause)
	}
	if a := strings.TrimSpace(p.Assignee); a != "" {
		if strings.EqualFold(a, assigneeUnassigned) {
			clauses = append(clauses, "assignee is EMPTY")
		} else {
			clauses = append(clauses, `assignee = "`+escapeJQL(a)+`"`)
		}
	}
	if clause := issueTypeClause(p.Types); clause != "" {
		clauses = append(clauses, clause)
	}
	if p.HideDone {
		clauses = append(clauses, "statusCategory != Done")
	}
	if p.ActiveSprint {
		clauses = append(clauses, "sprint in openSprints()")
	}
	jql := strings.Join(clauses, " AND ")
	if jql != "" {
		jql += " "
	}
	return jql + "ORDER BY updated DESC"
}

// issueTypeClause builds an `issuetype in (...)` clause from the selected type
// names, or "" when none are selected (All types). Blanks are dropped and each
// name is quoted/escaped; multiple names (e.g. Sub-task / Subtask spelling
// variants) widen the match.
func issueTypeClause(types []string) string {
	quoted := make([]string, 0, len(types))
	for _, t := range types {
		if t = strings.TrimSpace(t); t != "" {
			quoted = append(quoted, `"`+escapeJQL(t)+`"`)
		}
	}
	if len(quoted) == 0 {
		return ""
	}
	return "issuetype in (" + strings.Join(quoted, ", ") + ")"
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

// escapeJQL escapes a value for a JQL double-quoted string literal. This is the
// OUTER of two escaping layers: it keeps a value from breaking out of the quotes.
// It is NOT sufficient on its own for the operand of `~` — see textClause.
func escapeJQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// textTerms splits a user query into plain search terms for the `~` operand.
//
// The operand of `~` is not a string — Jira hands it to a Lucene-style text
// parser where `+ - && || ! ( ) { } [ ] ^ " ~ * ? : \ /` are all operators. So
// `e-coupon` parses as "e AND NOT coupon" and matches nothing, which is why a
// large share of real searches silently returned zero rows.
//
// Splitting on every non-letter/digit rune neutralises the whole operator set at
// once, and it matches how Jira indexes text: "E-Coupon" is tokenized to `e` +
// `coupon`, so terms are what the index can actually match. Backslash-escaping
// instead (`e\-coupon`) is the tempting fix and is WRONG once a wildcard is
// involved — a wildcard term bypasses the analyzer, so `e\-coupon*` looks for a
// single token that the index never contains. Verified against the real Jira.
//
// Terms are lowercased so an uppercase AND/OR/NOT is a plain word rather than a
// boolean operator; text matching is case-insensitive, so nothing is lost.
// unicode.IsLetter keeps non-ASCII scripts (Thai summaries are common here) whole.
func textTerms(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// textClause builds the summary/text contains-clause, or "" when the query has no
// searchable characters at all (so we never emit a bare `~ "*"`). Terms are ANDed
// by Jira; only the LAST term takes the `*`, which is the word the user is still
// typing — that preserves the existing type-ahead prefix behaviour.
//
// The escapeJQL call is the outer, string-literal layer. Given textTerms it is
// currently a no-op — terms hold only letters and digits, so there is no `"` or
// `\` left to escape, and no test can distinguish it. It stays as the layer that
// keeps the two concerns honest: if textTerms is ever loosened to pass more
// characters through, this is what still stops them breaking out of the quotes.
func textClause(text string) string {
	terms := textTerms(text)
	if len(terms) == 0 {
		return ""
	}
	esc := escapeJQL(strings.Join(terms, " ")) + "*"
	return `(summary ~ "` + esc + `" OR text ~ "` + esc + `")`
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
		return "Jira authentication failed — check your Jira API token (JIRA_API_TOKEN)."
	case errors.Is(err, jiraadapter.ErrBadKey):
		return "The linked Jira key is invalid."
	case errors.Is(err, jiraadapter.ErrUnavailable):
		return "Couldn't reach Jira."
	default:
		return "Couldn't load the Jira issue."
	}
}
