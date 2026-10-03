package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learndecide"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

// LearningDecideService is the controller-facing contract over decide.
type LearningDecideService interface {
	StartDecide(ctx context.Context, project, task string, budgetUSD float64) error
	DecideProgress() (learndecide.Progress, error)
	Proposals(ctx context.Context, project domain.ProjectID, all bool) ([]domain.LearnProposal, error)
	Proposal(ctx context.Context, id int64) (domain.LearnProposal, []domain.LearnDraft, error)
}

// LearningDecideController owns the decide and proposal routes.
type LearningDecideController struct {
	Svc LearningDecideService
}

// StartLearningDecideRequest is the body of POST /api/v1/learning/decide.
type StartLearningDecideRequest struct {
	Project   string  `json:"project,omitempty" description:"Project whose ready tasks to decide; empty decides every learning project."`
	Task      string  `json:"task,omitempty" description:"Decide this one task now (e.g. solo:<session>), ready or not."`
	BudgetUSD float64 `json:"budgetUsd" description:"Most this run may spend, at API prices. At most 50."`
}

// StartLearningDecideResponse is the body of POST /api/v1/learning/decide.
type StartLearningDecideResponse struct {
	Started bool `json:"started"`
}

// LearningDecideRunDTO is the decide loop's current or last pass.
type LearningDecideRunDTO struct {
	Running    bool       `json:"running"`
	Manual     bool       `json:"manual"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Tasks      int        `json:"tasks"`
	Failed     int        `json:"failed"`
	Proposals  int        `json:"proposals"`
	Dropped    int        `json:"dropped"`
	CostUSD    float64    `json:"costUsd"`
	BudgetUSD  float64    `json:"budgetUsd"`
	StopReason string     `json:"stopReason,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
}

// LearningProposalsQuery is the query of GET /api/v1/learning/proposals.
type LearningProposalsQuery struct {
	Project string `query:"project,omitempty" description:"Project id; empty lists every project's."`
	All     bool   `query:"all,omitempty" description:"Include rejected, dropped, applied, stale and superseded proposals, not only pending ones."`
}

// LearningRuleVerdictDTO is a proposal's relation to one standing rule.
type LearningRuleVerdictDTO struct {
	RuleID  string `json:"ruleId"`
	Verdict string `json:"verdict" enum:"consistent,refines,contradicts"`
	Note    string `json:"note,omitempty"`
}

// LearningVerifierDTO is the adversarial check of a proposal.
type LearningVerifierDTO struct {
	ContradictsRule bool   `json:"contradictsRule"`
	Grounded        bool   `json:"grounded"`
	SensitiveData   bool   `json:"sensitiveData"`
	Notes           string `json:"notes,omitempty"`
}

// LearningProposalDTO is a change learning proposes.
type LearningProposalDTO struct {
	ID           int64                    `json:"id"`
	ProjectID    string                   `json:"projectId"`
	TaskKey      string                   `json:"taskKey"`
	Action       string                   `json:"action" enum:"create_skill,update_skill,edit_rule_file,conflict"`
	TargetPath   string                   `json:"targetPath" description:"The file it would change, or rule:<id> for a conflict card."`
	Scope        string                   `json:"scope"`
	Title        string                   `json:"title"`
	Rationale    string                   `json:"rationale"`
	BaseSHA256   string                   `json:"baseSha256,omitempty" description:"The target's hash when the diff was computed; empty for a new file."`
	NewContent   string                   `json:"newContent"`
	Diff         string                   `json:"diff" description:"Unified diff AO computed from the target as it was."`
	Confidence   float64                  `json:"confidence"`
	Outcome      string                   `json:"outcome" enum:"merged,abandoned,unknown,ongoing,day"`
	RuleVerdicts []LearningRuleVerdictDTO `json:"ruleVerdicts"`
	Verifier     LearningVerifierDTO      `json:"verifier"`
	Status       string                   `json:"status" enum:"pending,rejected,applied,stale,superseded,dropped"`
	DropReason   string                   `json:"dropReason,omitempty"`
	EvidenceIDs  []int64                  `json:"evidenceIds"`
	CreatedAt    time.Time                `json:"createdAt"`
	UpdatedAt    time.Time                `json:"updatedAt"`
}

// ListLearningProposalsResponse is the body of GET /api/v1/learning/proposals.
type ListLearningProposalsResponse struct {
	Proposals []LearningProposalDTO `json:"proposals"`
	Run       LearningDecideRunDTO  `json:"run"`
}

// LearningProposalIDParam is the path of GET /api/v1/learning/proposals/{id}.
type LearningProposalIDParam struct {
	ID int64 `path:"id" description:"Proposal id."`
}

// LearningProposalResponse is the body of GET /api/v1/learning/proposals/{id}.
type LearningProposalResponse struct {
	Proposal LearningProposalDTO `json:"proposal"`
	Evidence []LearningDraftDTO  `json:"evidence"`
}

// Register mounts the decide routes.
func (c *LearningDecideController) Register(r chi.Router) {
	r.Post("/learning/decide", c.start)
	r.Get("/learning/proposals", c.list)
	r.Get("/learning/proposals/{id}", c.get)
}

func writeDecideError(w http.ResponseWriter, r *http.Request, method, path string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, learning.ErrDecideUnavailable):
		apispec.NotImplemented(w, r, method, path)
	case errors.Is(err, learning.ErrUnknownProject):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "Unknown project", nil)
	case errors.Is(err, learning.ErrUnknownProposal):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROPOSAL_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, learning.ErrNotLearning):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "NOT_LEARNING", "The project does not learn from sessions", nil)
	case errors.Is(err, learning.ErrInvalidBudget):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BUDGET", err.Error(), nil)
	case errors.Is(err, learndecide.ErrBusy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "DECIDE_BUSY", "A decide run is already in progress", nil)
	default:
		envelope.WriteError(w, r, err)
	}
	return true
}

func (c *LearningDecideController) start(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/decide"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", path)
		return
	}
	var in StartLearningDecideRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if writeDecideError(w, r, "POST", path, c.Svc.StartDecide(r.Context(), in.Project, in.Task, in.BudgetUSD)) {
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, StartLearningDecideResponse{Started: true})
}

func (c *LearningDecideController) list(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/proposals"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", path)
		return
	}
	q := r.URL.Query()
	all := q.Get("all") == "true" || q.Get("all") == "1"
	rows, err := c.Svc.Proposals(r.Context(), domain.ProjectID(q.Get("project")), all)
	if writeDecideError(w, r, "GET", path, err) {
		return
	}
	p, err := c.Svc.DecideProgress()
	if writeDecideError(w, r, "GET", path, err) {
		return
	}
	out := ListLearningProposalsResponse{Proposals: make([]LearningProposalDTO, 0, len(rows)), Run: LearningDecideRunDTO{
		Running: p.Running, Manual: p.Manual, StartedAt: timePtr(p.StartedAt), FinishedAt: timePtr(p.FinishedAt),
		Tasks: p.Tasks, Failed: p.Failed, Proposals: p.Proposals, Dropped: p.Dropped, CostUSD: p.CostUSD,
		BudgetUSD: p.BudgetUSD, StopReason: p.StopReason, LastError: p.LastError,
	}}
	for _, pr := range rows {
		out.Proposals = append(out.Proposals, proposalDTO(pr))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *LearningDecideController) get(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/proposals/{id}"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", path)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ID", "id must be a positive integer", nil)
		return
	}
	p, ev, err := c.Svc.Proposal(r.Context(), id)
	if writeDecideError(w, r, "GET", path, err) {
		return
	}
	out := LearningProposalResponse{Proposal: proposalDTO(p), Evidence: make([]LearningDraftDTO, 0, len(ev))}
	for _, d := range ev {
		out.Evidence = append(out.Evidence, draftDTO(d))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func proposalDTO(p domain.LearnProposal) LearningProposalDTO {
	out := LearningProposalDTO{
		ID: p.ID, ProjectID: string(p.ProjectID), TaskKey: p.TaskKey, Action: string(p.Action), TargetPath: p.TargetPath,
		Scope: p.Scope, Title: p.Title, Rationale: p.Rationale, BaseSHA256: p.BaseSHA256, NewContent: p.NewContent,
		Diff: p.Diff, Confidence: p.Confidence, Outcome: string(p.Outcome), Status: string(p.Status), DropReason: p.DropReason,
		Verifier: LearningVerifierDTO{ContradictsRule: p.Verifier.ContradictsRule, Grounded: p.Verifier.Grounded,
			SensitiveData: p.Verifier.SensitiveData, Notes: p.Verifier.Notes},
		RuleVerdicts: make([]LearningRuleVerdictDTO, 0, len(p.RuleVerdicts)), EvidenceIDs: p.EvidenceIDs,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if out.EvidenceIDs == nil {
		out.EvidenceIDs = []int64{}
	}
	for _, v := range p.RuleVerdicts {
		out.RuleVerdicts = append(out.RuleVerdicts, LearningRuleVerdictDTO{RuleID: v.RuleID, Verdict: v.Verdict, Note: v.Note})
	}
	return out
}
