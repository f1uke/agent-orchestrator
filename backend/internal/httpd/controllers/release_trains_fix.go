package controllers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

// ReleaseTrainFixIDParam is the path of POST /api/v1/release-trains/fixes/{fixId}/undo.
type ReleaseTrainFixIDParam struct {
	FixID string `path:"fixId" description:"Fix id from the Fixes list, e.g. fx-0a1b2c3d4e5f6a7b."`
}

// ReleaseTrainFixPlanRequest is the body of POST /api/v1/release-trains/fixes/plan.
type ReleaseTrainFixPlanRequest struct {
	HitID  rt.HitID `json:"hitId"`
	Choice string   `json:"choice,omitempty" description:"For a choose plan (A5): the side to take, jira or gitlab."`
}

// ReleaseTrainFixPlanResponse is the plan; computing it writes nothing.
type ReleaseTrainFixPlanResponse struct {
	Plan rt.FixPlan `json:"plan"`
}

// ReleaseTrainFixConfirmRequest is the body of POST /api/v1/release-trains/fixes.
type ReleaseTrainFixConfirmRequest struct {
	LearningDecisionBy
	HitID           rt.HitID `json:"hitId"`
	Choice          string   `json:"choice,omitempty"`
	PlanFingerprint string   `json:"planFingerprint" description:"The fingerprint of the plan the person reviewed; 409 when the plan changed since."`
}

// ReleaseTrainFixUndoRequest is the body of POST /api/v1/release-trains/fixes/{fixId}/undo.
type ReleaseTrainFixUndoRequest struct {
	LearningDecisionBy
}

// ReleaseTrainFixResponse is one fix with its audit events.
type ReleaseTrainFixResponse struct {
	Fix rt.Fix `json:"fix"`
}

// ReleaseTrainFixesResponse is the body of GET /api/v1/release-trains/fixes.
type ReleaseTrainFixesResponse struct {
	Fixes []rt.Fix `json:"fixes"`
}

func (c *ReleaseTrainsController) planFix(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/release-trains/fixes/plan")
		return
	}
	var req ReleaseTrainFixPlanRequest
	if err := decodeJSONStrict(r, &req); err != nil || req.HitID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "a hitId is required", nil)
		return
	}
	plan, err := c.Svc.PlanFix(r.Context(), req.HitID, req.Choice)
	if writeFixError(w, r, err, req.HitID) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseTrainFixPlanResponse{Plan: plan})
}

func (c *ReleaseTrainsController) confirmFix(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/release-trains/fixes")
		return
	}
	var req ReleaseTrainFixConfirmRequest
	if err := decodeJSONStrict(r, &req); err != nil || req.HitID == "" || req.PlanFingerprint == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "hitId and planFingerprint are required", nil)
		return
	}
	f, err := c.Svc.ConfirmFix(r.Context(), req.HitID, req.Choice, req.PlanFingerprint, req.updatedBy())
	if writeFixError(w, r, err, req.HitID) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseTrainFixResponse{Fix: f})
}

func (c *ReleaseTrainsController) fixes(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/release-trains/fixes")
		return
	}
	all, err := c.Svc.Fixes(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseTrainFixesResponse{Fixes: all})
}

func (c *ReleaseTrainsController) undoFix(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/release-trains/fixes/{fixId}/undo")
		return
	}
	var req ReleaseTrainFixUndoRequest
	if err := decodeJSONStrict(r, &req); err != nil && r.ContentLength > 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	f, err := c.Svc.UndoFix(r.Context(), chi.URLParam(r, "fixId"), req.updatedBy())
	if writeFixError(w, r, err, "") {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseTrainFixResponse{Fix: f})
}

func writeFixError(w http.ResponseWriter, r *http.Request, err error, hit rt.HitID) bool {
	var changed *rtsvc.PlanChangedError
	var verr *rt.PlanVersionError
	switch {
	case err == nil:
		return false
	case errors.As(err, &changed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "FIX_PLAN_CHANGED", changed.Error(),
			map[string]any{"plan": changed.Plan})
	case errors.As(err, &verr):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PLAN_VERSION_CONFLICT", verr.Error(),
			map[string]any{"planId": verr.Plan, "currentVersion": verr.Current})
	case errors.Is(err, rt.ErrNoSuchHit), errors.Is(err, rtsvc.ErrNoBoard):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RELEASE_HIT_NOT_FOUND", err.Error(), map[string]any{"hitId": hit})
	case errors.Is(err, rt.ErrNoSuchFix):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RELEASE_FIX_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, rt.ErrNotFixable), errors.Is(err, rt.ErrUnknownChoice), errors.Is(err, rtsvc.ErrNothingToRecord):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "FIX_NOT_RECORDABLE", err.Error(), nil)
	case errors.Is(err, rtsvc.ErrNoUndo), errors.Is(err, rt.ErrFixChanged):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "FIX_STATE_CONFLICT", err.Error(), nil)
	default:
		envelope.WriteError(w, r, err)
	}
	return true
}
