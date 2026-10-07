package controllers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scriptstore"
)

// ScriptsStoreWorktreeView is a workspace's own worktree of the mobile scripts
// store.
type ScriptsStoreWorktreeView struct {
	Owner       string                   `json:"owner" description:"The workspace owner: the solo worker, or the crew's dev."`
	Store       string                   `json:"store" description:"The store's main checkout."`
	Path        string                   `json:"path" description:"The worktree; AO_SCRIPTS_STORE in the session."`
	Branch      string                   `json:"branch"`
	BaseBranch  string                   `json:"baseBranch" description:"The store branch a publish merges into."`
	State       domain.ScriptsStoreState `json:"state" enum:"active,held,removed"`
	HeldReason  domain.ScriptsStoreHold  `json:"heldReason,omitempty" enum:"uncommitted,publish_conflict,store_dirty_overlap,store_off_base,publish_failed"`
	HeldFiles   []string                 `json:"heldFiles,omitempty"`
	Uncommitted []string                 `json:"uncommitted" description:"Files in the worktree nobody committed; a publish leaves them."`
	Unpublished int                      `json:"unpublished" description:"Commits on the branch the base branch does not have."`
	PublishedAt *time.Time               `json:"publishedAt,omitempty"`
}

// ScriptsStatusResponse is the body of GET /sessions/{id}/scripts.
type ScriptsStatusResponse struct {
	Worktree   ScriptsStoreWorktreeView `json:"worktree"`
	StoreDirty []string                 `json:"storeDirty" description:"The store main checkout's own uncommitted files, which a publish cannot touch."`
}

// ScriptsPublishResponse is the body of POST /sessions/{id}/scripts/publish. A
// refused publish is an answer, not a failure: outcome says so and hold,
// detail and files say why.
type ScriptsPublishResponse struct {
	Worktree ScriptsStoreWorktreeView    `json:"worktree"`
	Outcome  ports.ScriptsPublishOutcome `json:"outcome" enum:"nothing,fast_forward,merged,refused"`
	SHA      string                      `json:"sha,omitempty" description:"The base branch's new tip."`
	Commits  int                         `json:"commits" description:"Commits the branch had that the base lacked."`
	Hold     domain.ScriptsStoreHold     `json:"hold,omitempty" enum:"publish_conflict,store_dirty_overlap,store_off_base,publish_failed"`
	Detail   string                      `json:"detail,omitempty"`
	Files    []string                    `json:"files,omitempty" description:"The conflicting files, or the main checkout's files in the way."`
}

// ScriptsService is what the controller needs from the scripts store service.
// Both calls take any session of the workspace: a crew member resolves to its
// owner.
type ScriptsService interface {
	Status(ctx context.Context, session domain.SessionID) (scriptstore.Status, error)
	Publish(ctx context.Context, session domain.SessionID) (scriptstore.Published, error)
}

// ScriptsController owns /sessions/{id}/scripts. A nil Svc returns 501.
type ScriptsController struct {
	Svc ScriptsService
}

// Register mounts the scripts routes.
func (c *ScriptsController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/scripts", c.status)
	r.Post("/sessions/{sessionId}/scripts/publish", c.publish)
}

func (c *ScriptsController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/scripts")
		return
	}
	st, err := c.Svc.Status(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ScriptsStatusResponse{Worktree: scriptsWorktreeView(st.Worktree), StoreDirty: nonNilList(st.StoreDirty)})
}

func (c *ScriptsController) publish(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/scripts/publish")
		return
	}
	out, err := c.Svc.Publish(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ScriptsPublishResponse{
		Worktree: scriptsWorktreeView(out.Worktree),
		Outcome:  out.Result.Outcome, SHA: out.Result.SHA, Commits: out.Result.Commits,
		Hold: out.Result.Hold, Detail: out.Result.Detail, Files: out.Result.Files,
	})
}

func (c *ScriptsController) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, scriptstore.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, scriptstore.ErrNoWorktree):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SCRIPTS_STORE_WORKTREE_NOT_FOUND", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SCRIPTS_STORE_OPERATION_FAILED", err.Error(), nil)
	}
}

func scriptsWorktreeView(w domain.ScriptsStoreWorktree) ScriptsStoreWorktreeView {
	v := ScriptsStoreWorktreeView{
		Owner: string(w.SessionID), Store: w.Store, Path: w.Path, Branch: w.Branch, BaseBranch: w.BaseBranch,
		State: w.State, HeldReason: w.HeldReason, HeldFiles: w.HeldFiles,
		Uncommitted: nonNilList(w.Uncommitted), Unpublished: w.Unpublished,
	}
	if !w.PublishedAt.IsZero() {
		at := w.PublishedAt
		v.PublishedAt = &at
	}
	return v
}

func nonNilList(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
