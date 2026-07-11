package controllers

import (
	"context"
	"errors"
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
// status-move write path (the one sanctioned Jira write).
type JiraService interface {
	Context(ctx context.Context, id domain.SessionID) (jirasvc.Result, error)
	Transitions(ctx context.Context, id domain.SessionID) ([]jiraadapter.Transition, error)
	Move(ctx context.Context, id domain.SessionID, transitionID string) (jirasvc.MoveResult, error)
}

// JiraController serves the session-scoped, display-only Jira context route.
type JiraController struct {
	Svc JiraService
}

// Register mounts the Jira routes: the display read, plus the status-move write
// path (list transitions + apply).
func (c *JiraController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/jira", c.get)
	r.Get("/sessions/{sessionId}/jira/transitions", c.transitions)
	r.Post("/sessions/{sessionId}/jira/move", c.move)
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

// transitions lists the linked issue's available status transitions (read live).
func (c *JiraController) transitions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/jira/transitions")
		return
	}
	id := sessionID(r)
	ts, err := c.Svc.Transitions(r.Context(), id)
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
// id, so nothing but the status can change.
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
	res, err := c.Svc.Move(r.Context(), id, req.TransitionID)
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
	case errors.Is(err, jiraadapter.ErrNotFound):
		envelope.WriteError(w, r, apierr.NotFound("JIRA_ISSUE_NOT_FOUND", "Jira issue not found or not visible to your account."))
	case errors.Is(err, jiraadapter.ErrBadKey):
		envelope.WriteError(w, r, apierr.Invalid("JIRA_BAD_KEY", "The linked Jira key is invalid.", nil))
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
	if iss.Sprint != nil {
		dto.Sprint = &JiraSprint{
			Name:      iss.Sprint.Name,
			State:     iss.Sprint.State,
			StartDate: iss.Sprint.StartDate,
			EndDate:   iss.Sprint.EndDate,
		}
	}
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
	return dto
}
