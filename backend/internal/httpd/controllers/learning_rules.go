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
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learnrules"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

// LearningRulesService is the controller-facing contract over the
// standing-rules corpus.
type LearningRulesService interface {
	Rules(ctx context.Context, project domain.ProjectID, query string, limit int) ([]learning.RuleHit, error)
	RuleSources(ctx context.Context, project domain.ProjectID) (learning.RulesStatus, error)
	RefreshRules(ctx context.Context, budgetUSD float64) error
	ProtectedRules(ctx context.Context, project domain.ProjectID) ([]domain.LearnProtectedRule, error)
	Protect(ctx context.Context, req learning.ProtectRequest) (domain.LearnProtectedRule, error)
	Unprotect(ctx context.Context, id int64) error
	CheckForbidden(ctx context.Context, project domain.ProjectID, text string) ([]rules.ForbiddenHit, error)
}

// LearningRulesController owns the standing-rules corpus routes.
type LearningRulesController struct {
	Svc LearningRulesService
}

// LearningRulesQuery is the query of GET /api/v1/learning/rules.
type LearningRulesQuery struct {
	Project string `query:"project,omitempty" description:"Project id. The project's rules and every global one; empty lists the global rules only."`
	Q       string `query:"q,omitempty" description:"Search the rules (BM25 over text, heading and tags). Empty lists them in source order."`
	Limit   int    `query:"limit,omitempty" description:"Most rules to return. Default 50, at most 2000."`
}

// LearningRuleDTO is one standing rule.
type LearningRuleDTO struct {
	ID          string   `json:"id" description:"Chunk hash prefix and position; stable while the source text of that chunk is."`
	Text        string   `json:"text"`
	Quote       string   `json:"quote" description:"The span of the source that states the rule."`
	Tags        []string `json:"tags"`
	Heading     string   `json:"heading,omitempty"`
	SourceKey   string   `json:"sourceKey"`
	SourceLabel string   `json:"sourceLabel"`
	SourceKind  string   `json:"sourceKind" enum:"claude_md,agents_md,skill,ao_prompt,knowledge_index,memory"`
	Scope       string   `json:"scope" enum:"global,project"`
	ProjectID   string   `json:"projectId,omitempty"`
	Score       float64  `json:"score,omitempty" description:"Search score; set only when the request had a query."`
}

// ListLearningRulesResponse is the body of GET /api/v1/learning/rules.
type ListLearningRulesResponse struct {
	Rules []LearningRuleDTO `json:"rules"`
}

// LearningRuleSourcesQuery is the query of GET /api/v1/learning/rules/sources.
type LearningRuleSourcesQuery struct {
	Project string `query:"project,omitempty" description:"Project id. The project's sources and every global one; empty lists every source."`
}

// LearningRuleSourceDTO is one source of the corpus.
type LearningRuleSourceDTO struct {
	Key         string    `json:"key"`
	Scope       string    `json:"scope" enum:"global,project"`
	ProjectID   string    `json:"projectId,omitempty"`
	Kind        string    `json:"kind" enum:"claude_md,agents_md,skill,ao_prompt,knowledge_index,memory"`
	Label       string    `json:"label"`
	Chunks      int       `json:"chunks"`
	Rules       int       `json:"rules"`
	RefreshedAt time.Time `json:"refreshedAt"`
	Error       string    `json:"error,omitempty" description:"Why some chunks are not atomized yet; the ones that are still count."`
}

// LearningRulesRefreshDTO is the refresh loop's current or last pass.
type LearningRulesRefreshDTO struct {
	Running    bool       `json:"running"`
	Manual     bool       `json:"manual"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Sources    int        `json:"sources"`
	Atomized   int        `json:"atomized"`
	Failed     int        `json:"failed"`
	CostUSD    float64    `json:"costUsd"`
	BudgetUSD  float64    `json:"budgetUsd"`
	StopReason string     `json:"stopReason,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
}

// ListLearningRuleSourcesResponse is the body of GET /api/v1/learning/rules/sources.
type ListLearningRuleSourcesResponse struct {
	Sources []LearningRuleSourceDTO `json:"sources"`
	Refresh LearningRulesRefreshDTO `json:"refresh"`
}

// RefreshLearningRulesRequest is the body of POST /api/v1/learning/rules/refresh.
type RefreshLearningRulesRequest struct {
	BudgetUSD float64 `json:"budgetUsd" description:"Most this refresh may spend on model runs, at API prices. At most 50."`
}

// RefreshLearningRulesResponse is the body of POST /api/v1/learning/rules/refresh.
type RefreshLearningRulesResponse struct {
	Started bool `json:"started"`
}

// LearningProtectedRulesQuery is the query of GET /api/v1/learning/protected-rules.
type LearningProtectedRulesQuery struct {
	Project string `query:"project,omitempty" description:"Project id. The project's pinned rules and every global one; empty lists all."`
}

// LearningProtectedRuleDTO is a rule the human pinned.
type LearningProtectedRuleDTO struct {
	ID        int64     `json:"id"`
	ProjectID string    `json:"projectId,omitempty" description:"Empty means every project."`
	Text      string    `json:"text"`
	Patterns  []string  `json:"patterns" description:"RE2 patterns, matched case-insensitively, that a proposed change must never contain."`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ListLearningProtectedRulesResponse is the body of GET /api/v1/learning/protected-rules.
type ListLearningProtectedRulesResponse struct {
	Rules []LearningProtectedRuleDTO `json:"rules"`
}

// ProtectLearningRuleRequest is the body of POST /api/v1/learning/protected-rules.
type ProtectLearningRuleRequest struct {
	Project  string   `json:"project,omitempty" description:"Project the rule applies in; empty means every project."`
	Text     string   `json:"text,omitempty" description:"The rule. Give this or from."`
	From     string   `json:"from,omitempty" description:"Id of a corpus rule to copy the text from."`
	Patterns []string `json:"patterns,omitempty"`
	Note     string   `json:"note,omitempty"`
}

// LearningProtectedRuleIDParam is the path of DELETE /api/v1/learning/protected-rules/{id}.
type LearningProtectedRuleIDParam struct {
	ID int64 `path:"id" description:"Protected rule id."`
}

// UnprotectLearningRuleResponse is the body of DELETE /api/v1/learning/protected-rules/{id}.
type UnprotectLearningRuleResponse struct {
	Deleted bool `json:"deleted"`
}

// CheckLearningForbiddenRequest is the body of POST /api/v1/learning/protected-rules/check.
type CheckLearningForbiddenRequest struct {
	Project string `json:"project,omitempty" description:"Project whose rules apply besides the global ones."`
	Text    string `json:"text"`
}

// LearningForbiddenHitDTO is a forbidden pattern found in the text.
type LearningForbiddenHitDTO struct {
	RuleID  int64  `json:"ruleId"`
	Rule    string `json:"rule"`
	Pattern string `json:"pattern"`
	Match   string `json:"match"`
}

// CheckLearningForbiddenResponse is the body of POST /api/v1/learning/protected-rules/check.
type CheckLearningForbiddenResponse struct {
	Hits []LearningForbiddenHitDTO `json:"hits"`
}

// MaxRulesLimit bounds GET /learning/rules.
const MaxRulesLimit = 2000

// Register mounts the rules routes.
func (c *LearningRulesController) Register(r chi.Router) {
	r.Get("/learning/rules", c.list)
	r.Get("/learning/rules/sources", c.sources)
	r.Post("/learning/rules/refresh", c.refresh)
	r.Get("/learning/protected-rules", c.protected)
	r.Post("/learning/protected-rules", c.protect)
	r.Post("/learning/protected-rules/check", c.check)
	r.Delete("/learning/protected-rules/{id}", c.unprotect)
}

// writeRulesError maps the service's errors; it reports whether it wrote one.
func writeRulesError(w http.ResponseWriter, r *http.Request, method, path string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, learning.ErrRulesUnavailable):
		apispec.NotImplemented(w, r, method, path)
	case errors.Is(err, learning.ErrUnknownProject):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "Unknown project", nil)
	case errors.Is(err, learning.ErrUnknownRule):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "RULE_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, learning.ErrInvalidProtectedRule):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PROTECTED_RULE", err.Error(), nil)
	case errors.Is(err, learning.ErrInvalidBudget):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BUDGET", err.Error(), nil)
	case errors.Is(err, learnrules.ErrBusy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "RULES_REFRESH_BUSY", "A rules refresh is already in progress", nil)
	default:
		envelope.WriteError(w, r, err)
	}
	return true
}

func (c *LearningRulesController) list(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/rules"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", path)
		return
	}
	q := r.URL.Query()
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > MaxRulesLimit {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_LIMIT", "limit must be between 0 and 2000", nil)
			return
		}
		if n > 0 {
			limit = n
		}
	}
	hits, err := c.Svc.Rules(r.Context(), domain.ProjectID(q.Get("project")), q.Get("q"), limit)
	if writeRulesError(w, r, "GET", path, err) {
		return
	}
	out := ListLearningRulesResponse{Rules: make([]LearningRuleDTO, 0, len(hits))}
	for _, h := range hits {
		tags := h.Tags
		if tags == nil {
			tags = []string{}
		}
		out.Rules = append(out.Rules, LearningRuleDTO{
			ID: h.ID, Text: h.Text, Quote: h.Quote, Tags: tags, Heading: h.Heading, SourceKey: h.SourceKey,
			SourceLabel: h.SourceLabel, SourceKind: string(h.SourceKind), Scope: string(h.Scope),
			ProjectID: string(h.ProjectID), Score: h.Score,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *LearningRulesController) sources(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/rules/sources"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", path)
		return
	}
	st, err := c.Svc.RuleSources(r.Context(), domain.ProjectID(r.URL.Query().Get("project")))
	if writeRulesError(w, r, "GET", path, err) {
		return
	}
	p := st.Progress
	out := ListLearningRuleSourcesResponse{
		Sources: make([]LearningRuleSourceDTO, 0, len(st.Sources)),
		Refresh: LearningRulesRefreshDTO{
			Running: p.Running, Manual: p.Manual, StartedAt: timePtr(p.StartedAt), FinishedAt: timePtr(p.FinishedAt),
			Sources: p.Sources, Atomized: p.Atomized, Failed: p.Failed, CostUSD: p.CostUSD, BudgetUSD: p.BudgetUSD,
			StopReason: p.StopReason, LastError: p.LastError,
		},
	}
	for _, s := range st.Sources {
		out.Sources = append(out.Sources, LearningRuleSourceDTO{
			Key: s.Key, Scope: string(s.Scope), ProjectID: string(s.ProjectID), Kind: string(s.Kind), Label: s.Label,
			Chunks: len(s.Chunks), Rules: s.Rules, RefreshedAt: s.RefreshedAt, Error: s.Error,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *LearningRulesController) refresh(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/rules/refresh"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", path)
		return
	}
	var in RefreshLearningRulesRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if writeRulesError(w, r, "POST", path, c.Svc.RefreshRules(r.Context(), in.BudgetUSD)) {
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, RefreshLearningRulesResponse{Started: true})
}

func (c *LearningRulesController) protected(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/protected-rules"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", path)
		return
	}
	rows, err := c.Svc.ProtectedRules(r.Context(), domain.ProjectID(r.URL.Query().Get("project")))
	if writeRulesError(w, r, "GET", path, err) {
		return
	}
	out := ListLearningProtectedRulesResponse{Rules: make([]LearningProtectedRuleDTO, 0, len(rows))}
	for _, p := range rows {
		out.Rules = append(out.Rules, protectedDTO(p))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *LearningRulesController) protect(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/protected-rules"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", path)
		return
	}
	var in ProtectLearningRuleRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	p, err := c.Svc.Protect(r.Context(), learning.ProtectRequest{
		ProjectID: domain.ProjectID(in.Project), Text: in.Text, From: in.From, Patterns: in.Patterns, Note: in.Note,
	})
	if writeRulesError(w, r, "POST", path, err) {
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, protectedDTO(p))
}

func (c *LearningRulesController) unprotect(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/protected-rules/{id}"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", path)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ID", "id must be a positive integer", nil)
		return
	}
	if writeRulesError(w, r, "DELETE", path, c.Svc.Unprotect(r.Context(), id)) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, UnprotectLearningRuleResponse{Deleted: true})
}

func (c *LearningRulesController) check(w http.ResponseWriter, r *http.Request) {
	const path = "/api/v1/learning/protected-rules/check"
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", path)
		return
	}
	var in CheckLearningForbiddenRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	hits, err := c.Svc.CheckForbidden(r.Context(), domain.ProjectID(in.Project), in.Text)
	if writeRulesError(w, r, "POST", path, err) {
		return
	}
	out := CheckLearningForbiddenResponse{Hits: make([]LearningForbiddenHitDTO, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, LearningForbiddenHitDTO{RuleID: h.RuleID, Rule: h.Rule, Pattern: h.Pattern, Match: h.Match})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func protectedDTO(p domain.LearnProtectedRule) LearningProtectedRuleDTO {
	patterns := p.Patterns
	if patterns == nil {
		patterns = []string{}
	}
	return LearningProtectedRuleDTO{ID: p.ID, ProjectID: string(p.ProjectID), Text: p.Text, Patterns: patterns, Note: p.Note,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
