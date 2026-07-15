package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	jirasvc "github.com/aoagents/agent-orchestrator/backend/internal/service/jira"
)

// JiraService is the controller-facing Jira contract, satisfied by
// *service/jira.Service. Context is the display read; Transitions + Move are the
// status-move write path; Search/Projects power the pre-session pickers;
// SetBinding/Unlink link an existing session to an issue after the fact.
type JiraService interface {
	Context(ctx context.Context, id domain.SessionID) (jirasvc.Result, error)
	Transitions(ctx context.Context, id domain.SessionID, key string) ([]jiraadapter.Transition, error)
	Move(ctx context.Context, id domain.SessionID, key, transitionID string) (jirasvc.MoveResult, error)
	Search(ctx context.Context, p jirasvc.SearchParams) ([]jiraadapter.IssueSummary, error)
	Projects(ctx context.Context, query string) ([]jiraadapter.ProjectRef, error)
	CurrentUser(ctx context.Context) (jiraadapter.CurrentUser, error)
	SetBinding(ctx context.Context, id domain.SessionID, key string) (jiraadapter.IssueSummary, error)
	Unlink(ctx context.Context, id domain.SessionID) (domain.Session, error)
	// By-key reads for the pre-session Browse Jira detail view (no session binding).
	GetIssue(ctx context.Context, key string) (jiraadapter.Issue, error)
	IssueTransitions(ctx context.Context, key string) ([]jiraadapter.Transition, error)
	MoveIssue(ctx context.Context, key, transitionID string) (jirasvc.MoveResult, error)
	// DownloadAttachment streams one attachment's bytes for the Summary tab's
	// inline media previews. Caller closes the reader.
	DownloadAttachment(ctx context.Context, id domain.SessionID, attachmentID string) (io.ReadCloser, string, error)
}

// JiraController serves the session-scoped, display-only Jira context route.
type JiraController struct {
	Svc JiraService
}

// Register mounts the Jira routes: the pre-session pickers (search + projects,
// no session context yet), the session display read + status-move write path,
// and the after-the-fact link (PUT) / unlink (DELETE) on the same session path.
func (c *JiraController) Register(r chi.Router) {
	r.Get("/jira/search", c.search)
	r.Get("/jira/projects", c.projects)
	r.Get("/jira/myself", c.myself)
	r.Get("/jira/issue", c.issue)
	r.Get("/jira/issue/transitions", c.issueTransitions)
	r.Post("/jira/issue/move", c.issueMove)
	r.Get("/sessions/{sessionId}/jira", c.get)
	r.Put("/sessions/{sessionId}/jira", c.link)
	r.Delete("/sessions/{sessionId}/jira", c.unlink)
	r.Get("/sessions/{sessionId}/jira/transitions", c.transitions)
	r.Post("/sessions/{sessionId}/jira/move", c.move)
	r.Get("/sessions/{sessionId}/jira/attachments/{attachmentId}", c.attachment)
}

// attachment streams one Jira attachment's bytes for the Summary tab's inline
// media previews (image thumbnail / video player) and the shared lightbox. A
// READ proxy over the adapter's DownloadAttachment; honours the display-only rule
// (never writes to Jira). Bytes flow daemon → renderer, which renders them from a
// blob: URL (a direct loopback-http subresource is CSP-blocked on app://).
func (c *JiraController) attachment(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/jira/attachments/{attachmentId}")
		return
	}
	rc, ctype, err := c.Svc.DownloadAttachment(r.Context(), sessionID(r), strings.TrimSpace(chi.URLParam(r, "attachmentId")))
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	defer func() { _ = rc.Close() }()
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// search resolves the pre-session issue picker query (New task + link-existing)
// and the Browse Jira list. A free-text query or an exact key; optionally scoped
// to a project and narrowed by assignee (accountId or "unassigned"), issue types,
// hide-done and active-sprint — all pushed into the server-side JQL. A raw `jql`
// param, when set, drives the search verbatim (advanced mode).
func (c *JiraController) search(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/jira/search")
		return
	}
	q := r.URL.Query()
	issues, err := c.Svc.Search(r.Context(), jirasvc.SearchParams{
		Project:      strings.TrimSpace(q.Get("project")),
		Text:         strings.TrimSpace(q.Get("q")),
		Assignee:     strings.TrimSpace(q.Get("assignee")),
		Types:        splitTypes(q.Get("type")),
		HideDone:     queryBool(q.Get("hideDone")),
		ActiveSprint: queryBool(q.Get("activeSprint")),
		JQL:          strings.TrimSpace(q.Get("jql")),
	})
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	out := JiraSearchResponse{Issues: make([]JiraIssueSummary, 0, len(issues))}
	for _, it := range issues {
		out.Issues = append(out.Issues, jiraIssueSummaryDTO(it))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

// projects lists the user's Jira projects for the project picker.
func (c *JiraController) projects(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/jira/projects")
		return
	}
	ps, err := c.Svc.Projects(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	out := JiraProjectsResponse{Projects: make([]JiraProject, 0, len(ps))}
	for _, p := range ps {
		out.Projects = append(out.Projects, JiraProject{Key: p.Key, Name: p.Name})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

// myself resolves the authenticated Jira account so Browse Jira can highlight the
// viewer's own rows. The account id is stable; the frontend caches it.
func (c *JiraController) myself(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/jira/myself")
		return
	}
	me, err := c.Svc.CurrentUser(r.Context())
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, JiraMyselfResponse{AccountID: me.AccountID, DisplayName: me.DisplayName})
}

// issue reads one issue's full display projection by key for the pre-session Browse
// Jira detail view. Unlike the session display read, a Jira-side failure surfaces as
// a real HTTP error so the detail panel can show it.
func (c *JiraController) issue(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/jira/issue")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_KEY_REQUIRED", "An issue key is required.", nil))
		return
	}
	iss, err := c.Svc.GetIssue(r.Context(), key)
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, JiraIssueResponse{Issue: jiraIssueDTO(iss)})
}

// issueTransitions lists the live status transitions for any issue by key — the
// detail view's Move-status entry, pre-session.
func (c *JiraController) issueTransitions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/jira/issue/transitions")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_KEY_REQUIRED", "An issue key is required.", nil))
		return
	}
	ts, err := c.Svc.IssueTransitions(r.Context(), key)
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	out := JiraTransitionsResponse{Transitions: make([]JiraTransition, 0, len(ts))}
	for _, t := range ts {
		out.Transitions = append(out.Transitions, JiraTransition{
			ID: t.ID, Name: t.Name, To: t.To, ToCategory: t.ToCategory, ToColor: t.ToColor,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

// issueMove applies a status transition to any issue by key — the ONE sanctioned
// write, from the pre-session detail view. User-initiated (UI confirms first); the
// body carries only the key and a transition id.
func (c *JiraController) issueMove(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/jira/issue/move")
		return
	}
	var req JiraIssueMoveRequest
	if err := decodeJSON(r, &req); err != nil {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_MOVE_BODY_INVALID", "Malformed request body.", nil))
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_KEY_REQUIRED", "An issue key is required.", nil))
		return
	}
	if strings.TrimSpace(req.TransitionID) == "" {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_TRANSITION_REQUIRED", "A transition id is required.", nil))
		return
	}
	res, err := c.Svc.MoveIssue(r.Context(), strings.TrimSpace(req.Key), req.TransitionID)
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, JiraMoveResponse{
		Key:            res.Key,
		Status:         res.Status,
		StatusCategory: res.StatusCategory,
		StatusColor:    res.StatusColor,
	})
}

// link binds an existing session to a Jira issue (issue_id = "jira:<KEY>"). The
// key is resolved/validated first, so an unknown key never binds.
func (c *JiraController) link(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/jira")
		return
	}
	id := sessionID(r)
	var req JiraLinkRequest
	if err := decodeJSON(r, &req); err != nil {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_LINK_BODY_INVALID", "Malformed request body.", nil))
		return
	}
	if strings.TrimSpace(req.IssueKey) == "" {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_KEY_REQUIRED", "An issue key is required.", nil))
		return
	}
	iss, err := c.Svc.SetBinding(r.Context(), id, req.IssueKey)
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	summary := jiraIssueSummaryDTO(iss)
	envelope.WriteJSON(w, http.StatusOK, JiraLinkResponse{SessionID: id, Linked: true, Issue: &summary})
}

// unlink removes a session's Jira binding.
func (c *JiraController) unlink(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/jira")
		return
	}
	id := sessionID(r)
	if _, err := c.Svc.Unlink(r.Context(), id); err != nil {
		writeJiraError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, JiraLinkResponse{SessionID: id, Linked: false})
}

func (c *JiraController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/jira")
		return
	}
	id := sessionID(r)
	res, err := c.Svc.Context(r.Context(), id)
	if err != nil {
		// Only a failure to read the session itself lands here (e.g. unknown
		// session → 404). Jira-side failures are folded into res.FetchError.
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, jiraContextResponse(id, res))
}

// transitions lists the available status transitions (read live) for the linked
// issue, or — with ?key=<KEY> naming a subtask of it — for that subtask.
func (c *JiraController) transitions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/jira/transitions")
		return
	}
	id := sessionID(r)
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	ts, err := c.Svc.Transitions(r.Context(), id, key)
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	out := JiraTransitionsResponse{SessionID: id, Transitions: make([]JiraTransition, 0, len(ts))}
	for _, t := range ts {
		out.Transitions = append(out.Transitions, JiraTransition{
			ID: t.ID, Name: t.Name, To: t.To, ToCategory: t.ToCategory, ToColor: t.ToColor,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

// move applies a status transition — the ONE sanctioned Jira write. It is always
// user-initiated (and the UI confirms first); the body carries only a transition
// id (and optionally a subtask key in the session's issue tree), so nothing but
// the status can change.
func (c *JiraController) move(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/jira/move")
		return
	}
	id := sessionID(r)
	var req JiraMoveRequest
	if err := decodeJSON(r, &req); err != nil {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_MOVE_BODY_INVALID", "Malformed request body.", nil))
		return
	}
	if strings.TrimSpace(req.TransitionID) == "" {
		envelope.WriteError(w, r, apierr.Invalid("JIRA_TRANSITION_REQUIRED", "A transition id is required.", nil))
		return
	}
	res, err := c.Svc.Move(r.Context(), id, strings.TrimSpace(req.IssueKey), req.TransitionID)
	if err != nil {
		writeJiraError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, JiraMoveResponse{
		SessionID:      id,
		Key:            res.Key,
		Status:         res.Status,
		StatusCategory: res.StatusCategory,
		StatusColor:    res.StatusColor,
	})
}

// writeJiraError maps a session-read error (already an apierr, e.g. 404) or a
// Jira adapter/service sentinel onto the API envelope. Unlike the display read,
// the status actions surface failures as real HTTP errors so the dialog can show
// them.
func writeJiraError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		envelope.WriteError(w, r, apiErr)
		return
	}
	switch {
	case errors.Is(err, jirasvc.ErrNotLinked):
		envelope.WriteError(w, r, apierr.Invalid("SESSION_NOT_JIRA_LINKED", "This session is not linked to a Jira issue.", nil))
	case errors.Is(err, jirasvc.ErrKeyNotInIssueTree):
		envelope.WriteError(w, r, apierr.Invalid("JIRA_KEY_NOT_IN_TREE", "That issue isn't part of this session's Jira issue (only the issue or its subtasks can be moved).", nil))
	case errors.Is(err, jiraadapter.ErrNotFound):
		envelope.WriteError(w, r, apierr.NotFound("JIRA_ISSUE_NOT_FOUND", "Jira issue not found or not visible to your account."))
	case errors.Is(err, jiraadapter.ErrBadKey):
		envelope.WriteError(w, r, apierr.Invalid("JIRA_BAD_KEY", "The linked Jira key is invalid.", nil))
	case errors.Is(err, jiraadapter.ErrBadQuery):
		envelope.WriteError(w, r, apierr.Invalid("JIRA_BAD_QUERY", jiraErrMessage(err, "The Jira search query is invalid."), nil))
	case errors.Is(err, jiraadapter.ErrBadTransition):
		envelope.WriteError(w, r, apierr.Invalid("JIRA_TRANSITION_REJECTED", jiraErrMessage(err, "Jira rejected the transition (a workflow validator or permission)."), nil))
	case errors.Is(err, jiraadapter.ErrAuthFailed):
		envelope.WriteError(w, r, apierr.Internal("JIRA_AUTH_FAILED", jiraErrMessage(err, "Jira authentication failed — set JIRA_API_TOKEN (or AO_JIRA_TOKEN).")))
	case errors.Is(err, jiraadapter.ErrUnavailable):
		envelope.WriteError(w, r, apierr.Internal("JIRA_UNAVAILABLE", jiraErrMessage(err, "Couldn't reach Jira.")))
	default:
		envelope.WriteError(w, r, err)
	}
}

// jiraErrMessage surfaces the sentinel-wrapped detail (e.g. Jira's validator
// text) when present, falling back to a generic message.
func jiraErrMessage(err error, fallback string) string {
	if msg := strings.TrimSpace(err.Error()); msg != "" {
		return msg
	}
	return fallback
}

// jiraContextResponse maps the service result onto the wire DTO.
func jiraContextResponse(id domain.SessionID, res jirasvc.Result) JiraContextResponse {
	out := JiraContextResponse{SessionID: id, Linked: res.Linked, FetchError: res.FetchError}
	if res.Issue != nil {
		out.Issue = jiraIssueDTO(*res.Issue)
	}
	return out
}

// queryBool reads a boolean query param — "true"/"1" (case-insensitive) are true,
// everything else (including absent) is false.
func queryBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		return true
	default:
		return false
	}
}

// splitTypes turns the comma-separated `type` query param into a trimmed,
// non-empty list of issue-type names for the JQL `issuetype in (...)` clause.
func splitTypes(raw string) []string {
	var types []string
	for _, t := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(t); s != "" {
			types = append(types, s)
		}
	}
	return types
}

// jiraIssueSummaryDTO maps a search/resolve row to its wire shape.
func jiraIssueSummaryDTO(it jiraadapter.IssueSummary) JiraIssueSummary {
	return JiraIssueSummary{
		Key:               it.Key,
		Type:              it.Type,
		Title:             it.Title,
		Status:            it.Status,
		StatusCategory:    it.StatusCategory,
		StatusColor:       it.StatusColor,
		Assignee:          it.Assignee,
		AssigneeAccountId: it.AssigneeAccountId,
		Parent:            jiraParentDTO(it.Parent),
		Sprint:            jiraSprintDTO(it.Sprint),
		URL:               it.URL,
	}
}

// jiraParentDTO maps an adapter parent ref to its wire shape (nil-safe).
func jiraParentDTO(p *jiraadapter.ParentRef) *JiraParentRef {
	if p == nil {
		return nil
	}
	return &JiraParentRef{Key: p.Key, Title: p.Title}
}

// jiraSprintDTO maps an adapter sprint to its wire shape (nil-safe).
func jiraSprintDTO(sp *jiraadapter.Sprint) *JiraSprint {
	if sp == nil {
		return nil
	}
	return &JiraSprint{
		Name:      sp.Name,
		State:     sp.State,
		StartDate: sp.StartDate,
		EndDate:   sp.EndDate,
	}
}

func jiraIssueDTO(iss jiraadapter.Issue) *JiraIssue {
	dto := &JiraIssue{
		Key:            iss.Key,
		URL:            iss.URL,
		Type:           iss.Type,
		Title:          iss.Title,
		Status:         iss.Status,
		StatusCategory: iss.StatusCategory,
		StatusColor:    iss.StatusColor,
		Priority:       iss.Priority,
		Assignee:       iss.Assignee,
		Reporter:       iss.Reporter,
		Description:    iss.Description,
	}
	dto.Parent = jiraParentDTO(iss.Parent)
	dto.Sprint = jiraSprintDTO(iss.Sprint)
	for _, s := range iss.Subtasks {
		dto.Subtasks = append(dto.Subtasks, JiraSubtask{
			Key:            s.Key,
			Title:          s.Title,
			Type:           s.Type,
			Status:         s.Status,
			StatusCategory: s.StatusCategory,
			StatusColor:    s.StatusColor,
		})
	}
	for _, a := range iss.Attachments {
		dto.Attachments = append(dto.Attachments, JiraAttachment{ID: a.ID, Filename: a.Filename, MimeType: a.MimeType})
	}
	return dto
}
