package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	jirasvc "github.com/aoagents/agent-orchestrator/backend/internal/service/jira"
)

// JiraService is the controller-facing Jira read contract, satisfied by
// *service/jira.Service.
type JiraService interface {
	Context(ctx context.Context, id domain.SessionID) (jirasvc.Result, error)
}

// JiraController serves the session-scoped, display-only Jira context route.
type JiraController struct {
	Svc JiraService
}

// Register mounts the Jira route.
func (c *JiraController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/jira", c.get)
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
