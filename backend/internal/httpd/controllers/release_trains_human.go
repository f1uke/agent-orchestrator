package controllers

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

// ReleaseTrainRuleIDParam is the path of PUT /api/v1/release-trains/rules/{ruleId}.
type ReleaseTrainRuleIDParam struct {
	RuleID string `path:"ruleId" description:"Rule id from the Board's catalogue, e.g. C2."`
}

// ReleaseTrainHitIDParam is the path of /api/v1/release-trains/hides/{hitId}.
type ReleaseTrainHitIDParam struct {
	HitID string `path:"hitId" description:"Hit id, percent-encoded: its slashes are %2F, e.g. B1~v%2F21-2026%2Fdi."`
}

// ReleaseTrainPlanIDParam is the path of PUT /api/v1/release-trains/llb-plans/{planId}.
type ReleaseTrainPlanIDParam struct {
	PlanID string `path:"planId" description:"Plan id: 1-64 of a-z, 0-9, '.', '_', '-'."`
}

// ReleaseTrainStaleQuery is the query of DELETE /api/v1/release-trains/hides.
type ReleaseTrainStaleQuery struct {
	Stale bool `query:"stale" description:"Must be true: removes every hide the Board lists as no longer matching."`
}

// ReleaseTrainRuleSettingRequest is the body of PUT /api/v1/release-trains/rules/{ruleId}.
type ReleaseTrainRuleSettingRequest struct {
	Enabled *bool `json:"enabled" description:"true or false overrides the rule's default; null goes back to it."`
}

// ReleaseTrainRuleSettingResponse is the rule's catalogue row after the write.
type ReleaseTrainRuleSettingResponse struct {
	Rule rt.RuleInfo `json:"rule"`
}

// ReleaseTrainHideRequest is the body of PUT /api/v1/release-trains/hides/{hitId}.
type ReleaseTrainHideRequest struct {
	Reason string `json:"reason,omitempty" description:"Why it is hidden; shown in the Hidden list."`
}

// ReleaseTrainHideResponse is the hide as stored.
type ReleaseTrainHideResponse struct {
	HitID    rt.HitID    `json:"hitId"`
	Severity rt.Severity `json:"severity" description:"The hit's severity when hidden; a higher one brings it back to the badge."`
	Reason   string      `json:"reason,omitempty"`
	HiddenAt time.Time   `json:"hiddenAt"`
}

// ReleaseTrainUnhideStaleResponse is the body of DELETE /api/v1/release-trains/hides?stale=true.
type ReleaseTrainUnhideStaleResponse struct {
	Removed []rt.HitID `json:"removed"`
}

// ReleaseTrainLlbPlansResponse is the body of GET /api/v1/release-trains/llb-plans.
type ReleaseTrainLlbPlansResponse struct {
	Plans []rt.LlbPlan `json:"plans"`
}

// ReleaseTrainLlbPlanInput is the body of PUT /api/v1/release-trains/llb-plans/{planId}.
type ReleaseTrainLlbPlanInput struct {
	LearningDecisionBy
	Name        string            `json:"name"`
	TargetKind  rt.TargetKind     `json:"targetKind"`
	TargetTrain string            `json:"targetTrain,omitempty" description:"Train key, e.g. 22-2026; required when targetKind is train."`
	Note        string            `json:"note,omitempty"`
	Branches    []rt.PlanBranch   `json:"branches,omitempty"`
	Accepted    []rt.PlanAccepted `json:"accepted,omitempty"`
	IfVersion   int               `json:"ifVersion,omitempty" description:"The version this edit started from; 0 or absent creates a plan that does not exist yet."`
}

// ReleaseTrainLlbPlanResponse is the plan as stored, at its new version.
type ReleaseTrainLlbPlanResponse struct {
	Plan rt.LlbPlan `json:"plan"`
}

func (b LearningDecisionBy) updatedBy() string {
	via := b.Via
	if via == "" {
		via = "api"
	}
	if b.Session != "" {
		return via + ":" + b.Session
	}
	return via
}

func (c *ReleaseTrainsController) setRule(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/release-trains/rules/{ruleId}")
		return
	}
	var req ReleaseTrainRuleSettingRequest
	if err := decodeJSONStrict(r, &req); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	info, err := c.Svc.SetRule(r.Context(), rt.RuleID(chi.URLParam(r, "ruleId")), req.Enabled)
	switch {
	case errors.Is(err, rtsvc.ErrUnknownRule):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RELEASE_RULE_NOT_FOUND", err.Error(), nil)
	case err != nil:
		envelope.WriteError(w, r, err)
	default:
		envelope.WriteJSON(w, http.StatusOK, ReleaseTrainRuleSettingResponse{Rule: info})
	}
}

func hitIDParam(r *http.Request) (rt.HitID, bool) {
	id, err := url.PathUnescape(chi.URLParam(r, "hitId"))
	return rt.HitID(id), err == nil && strings.Contains(id, "~")
}

func (c *ReleaseTrainsController) hide(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/release-trains/hides/{hitId}")
		return
	}
	id, ok := hitIDParam(r)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_HIT_ID", "a hit id is <rule>~<subject>", nil)
		return
	}
	var req ReleaseTrainHideRequest
	if err := decodeJSONStrict(r, &req); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	hide, err := c.Svc.Hide(r.Context(), id, req.Reason)
	switch {
	case errors.Is(err, rtsvc.ErrUnknownHit), errors.Is(err, rtsvc.ErrNoBoard):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RELEASE_HIT_NOT_FOUND", err.Error(), map[string]any{"hitId": id})
	case err != nil:
		envelope.WriteError(w, r, err)
	default:
		envelope.WriteJSON(w, http.StatusOK, ReleaseTrainHideResponse{HitID: id, Severity: hide.Severity, Reason: hide.Reason, HiddenAt: hide.HiddenAt})
	}
}

func (c *ReleaseTrainsController) unhide(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/release-trains/hides/{hitId}")
		return
	}
	id, ok := hitIDParam(r)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_HIT_ID", "a hit id is <rule>~<subject>", nil)
		return
	}
	err := c.Svc.Unhide(r.Context(), id)
	switch {
	case errors.Is(err, rtsvc.ErrUnknownHit):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RELEASE_HIDE_NOT_FOUND", err.Error(), map[string]any{"hitId": id})
	case err != nil:
		envelope.WriteError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (c *ReleaseTrainsController) unhideStale(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/release-trains/hides")
		return
	}
	if r.URL.Query().Get("stale") != "true" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "STALE_REQUIRED",
			"only stale hides can be removed in bulk: add ?stale=true", nil)
		return
	}
	removed, err := c.Svc.UnhideStale(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseTrainUnhideStaleResponse{Removed: removed})
}

func (c *ReleaseTrainsController) plans(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/release-trains/llb-plans")
		return
	}
	plans, err := c.Svc.Plans(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseTrainLlbPlansResponse{Plans: plans})
}

func (c *ReleaseTrainsController) putPlan(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/release-trains/llb-plans/{planId}")
		return
	}
	var in ReleaseTrainLlbPlanInput
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	p := rt.LlbPlan{
		ID: rt.PlanID(chi.URLParam(r, "planId")), Name: in.Name, TargetKind: in.TargetKind, Note: in.Note,
		Branches: in.Branches, Accepted: in.Accepted, UpdatedBy: in.updatedBy(),
	}
	if p.Branches == nil {
		p.Branches = []rt.PlanBranch{}
	}
	if p.Accepted == nil {
		p.Accepted = []rt.PlanAccepted{}
	}
	if in.TargetTrain != "" {
		train, ok := rt.ParseTrainKey(in.TargetTrain)
		if !ok {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PLAN",
				"targetTrain is a train key such as 22-2026", map[string]any{"targetTrain": in.TargetTrain})
			return
		}
		p.TargetTrain = &train
	}
	put, err := c.Svc.PutPlan(r.Context(), p, in.IfVersion)
	var verr *rt.PlanVersionError
	var taken *rt.BranchTakenError
	switch {
	case errors.Is(err, rt.ErrInvalidPlan):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PLAN", err.Error(), nil)
	case errors.As(err, &verr):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PLAN_VERSION_CONFLICT", verr.Error(),
			map[string]any{"planId": verr.Plan, "currentVersion": verr.Current})
	case errors.As(err, &taken):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PLAN_BRANCH_TAKEN", taken.Error(),
			map[string]any{"app": taken.Branch.App, "branch": taken.Branch.Branch, "planId": taken.Owner})
	case err != nil:
		envelope.WriteError(w, r, err)
	default:
		envelope.WriteJSON(w, http.StatusOK, ReleaseTrainLlbPlanResponse{Plan: put})
	}
}
