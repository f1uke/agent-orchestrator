package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	childrensvc "github.com/aoagents/agent-orchestrator/backend/internal/service/children"
)

// ChildParam is the {sessionId}/{agentId} path parameters of one child.
type ChildParam struct {
	SessionID string `path:"sessionId" description:"The worker session that owns the child."`
	AgentID   string `path:"agentId" description:"Claude Code's id for the subagent."`
}

// CreateChildInput is the body of POST .../children: what Claude Code's
// WorktreeCreate hook reported.
type CreateChildInput struct {
	Name string `json:"name" description:"The worktree name Claude Code chose; agent-<id> for a subagent."`
	Cwd  string `json:"cwd" description:"The session's working directory when the subagent was launched."`
}

// ChildResponse carries one child.
type ChildResponse struct {
	Child domain.SessionChild `json:"child"`
}

// ListChildrenResponse is the body of GET .../children.
type ListChildrenResponse struct {
	Children []domain.SessionChild `json:"children"`
}

// ChildBriefResponse is the body of POST .../children/{agentId}/start.
type ChildBriefResponse struct {
	Known bool   `json:"known" description:"False when the subagent is not a child worktree of this worker."`
	Brief string `json:"brief,omitempty" description:"Standing context for the subagent, delivered as SubagentStart additionalContext."`
}

// StopChildResponse is the body of POST .../children/{agentId}/stop.
type StopChildResponse struct {
	Known  bool                 `json:"known" description:"False when the subagent is not a child worktree of this worker."`
	Block  bool                 `json:"block" description:"Keep the subagent going; Reason is its next instruction."`
	Reason string               `json:"reason,omitempty"`
	Child  *domain.SessionChild `json:"child,omitempty"`
}

// DescribeChildInput is the body of POST .../children/{agentId}/describe.
type DescribeChildInput struct {
	AgentType   string `json:"agentType,omitempty" description:"The subagent type the worker launched."`
	Description string `json:"description,omitempty" description:"The Agent call's short description of the task."`
}

// ChildNotesResponse is the body of POST .../children/notes.
type ChildNotesResponse struct {
	Notes []string `json:"notes" description:"What the worker has not yet been told about its children, oldest first."`
}

// ChildrenService is what the controller needs from the child service.
type ChildrenService interface {
	List(ctx context.Context, id domain.SessionID) ([]domain.SessionChild, error)
	Create(ctx context.Context, id domain.SessionID, in childrensvc.CreateInput) (domain.SessionChild, error)
	Brief(ctx context.Context, id domain.SessionID, agentID string) (string, bool, error)
	Stop(ctx context.Context, id domain.SessionID, agentID string) (childrensvc.StopOutcome, error)
	Describe(ctx context.Context, id domain.SessionID, agentID, agentType, description string) error
	Notes(ctx context.Context, id domain.SessionID) ([]string, error)
}

// ChildrenController owns the /sessions/{id}/children routes: the hook
// callbacks through which AO creates, briefs, stops and reports on a worker's
// child worktrees. A nil Svc returns 501.
type ChildrenController struct {
	Svc ChildrenService
}

// Register mounts the child routes.
func (c *ChildrenController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/children", c.list)
	r.Post("/sessions/{sessionId}/children", c.create)
	r.Post("/sessions/{sessionId}/children/notes", c.notes)
	r.Post("/sessions/{sessionId}/children/{agentId}/start", c.start)
	r.Post("/sessions/{sessionId}/children/{agentId}/stop", c.stop)
	r.Post("/sessions/{sessionId}/children/{agentId}/describe", c.describe)
}

// workerID is the literal session in the path. Children belong to the session
// whose subagent made them, so no crew-task scoping applies.
func workerID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "sessionId"))
}

func (c *ChildrenController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/children")
		return
	}
	children, err := c.Svc.List(r.Context(), workerID(r))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if children == nil {
		children = []domain.SessionChild{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListChildrenResponse{Children: children})
}

func (c *ChildrenController) create(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/children")
		return
	}
	var in CreateChildInput
	if err := decodeCrewRunBody(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	child, err := c.Svc.Create(r.Context(), workerID(r), childrensvc.CreateInput{Name: in.Name, Cwd: in.Cwd})
	if err != nil {
		c.fail(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ChildResponse{Child: child})
}

func (c *ChildrenController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/children/{agentId}/start")
		return
	}
	brief, known, err := c.Svc.Brief(r.Context(), workerID(r), chi.URLParam(r, "agentId"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChildBriefResponse{Known: known, Brief: brief})
}

func (c *ChildrenController) stop(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/children/{agentId}/stop")
		return
	}
	out, err := c.Svc.Stop(r.Context(), workerID(r), chi.URLParam(r, "agentId"))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	res := StopChildResponse{Known: out.Known, Block: out.Block, Reason: out.Reason}
	if out.Known {
		child := out.Child
		res.Child = &child
	}
	envelope.WriteJSON(w, http.StatusOK, res)
}

func (c *ChildrenController) describe(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/children/{agentId}/describe")
		return
	}
	var in DescribeChildInput
	if err := decodeCrewRunBody(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	if err := c.Svc.Describe(r.Context(), workerID(r), chi.URLParam(r, "agentId"), in.AgentType, in.Description); err != nil {
		c.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *ChildrenController) notes(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/children/notes")
		return
	}
	notes, err := c.Svc.Notes(r.Context(), workerID(r))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if notes == nil {
		notes = []string{}
	}
	envelope.WriteJSON(w, http.StatusOK, ChildNotesResponse{Notes: notes})
}

func (c *ChildrenController) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, childrensvc.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, childrensvc.ErrChildNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "CHILD_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, childrensvc.ErrNotWorker):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "NOT_A_LIVE_WORKER", err.Error(), nil)
	case errors.Is(err, childrensvc.ErrBadName):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "NOT_A_SUBAGENT_WORKTREE", err.Error(), nil)
	case errors.Is(err, childrensvc.ErrNested):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "NESTED_CHILD_REFUSED", err.Error(), nil)
	case errors.Is(err, childrensvc.ErrForeignCwd):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "NOT_FROM_WORKER_WORKTREE", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "CHILD_OPERATION_FAILED", err.Error(), nil)
	}
}
