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
	"github.com/aoagents/agent-orchestrator/backend/internal/learnsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learncollect"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

// LearningService is the controller-facing contract over learning capture.
type LearningService interface {
	RecordTranscriptRef(ctx context.Context, id domain.SessionID, ref domain.HookTranscriptRef) error
	Status(ctx context.Context) ([]learning.ProjectStatus, error)
	Excerpts(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LearnExcerpt, error)
	Forget(ctx context.Context, projectID domain.ProjectID) (int, error)
	CollectStatus(ctx context.Context) (learning.CollectStatus, error)
	StartCollect(ctx context.Context, project string, budgetUSD float64) error
	Drafts(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LearnDraft, error)
	Settings() (learnsettings.Settings, error)
	SetSettings(learnsettings.Settings) error
}

// LearningController owns learning capture's routes: the transcript bookkeeping
// agent hooks report, and the read-only view of what capture stored.
type LearningController struct {
	Svc LearningService
}

// TranscriptRefRequest is the body of POST /api/v1/sessions/{sessionId}/transcript-ref.
//
// It is reported by `ao hooks`, separately from the activity signal, because
// the activity route promises never to carry a native id or a path. The prompt
// is a fingerprint computed inside the hook process: its text never reaches the
// daemon.
type TranscriptRefRequest struct {
	ClaudeSessionID string `json:"claudeSessionId,omitempty" description:"The harness's own conversation id."`
	TranscriptPath  string `json:"transcriptPath" description:"Absolute path of the conversation file the session is writing. Must be a .jsonl directly inside one of Claude Code's project directories."`
	PromptSHA256    string `json:"promptSha256,omitempty" description:"On a prompt submit: hex sha256 of the prompt with whitespace collapsed. Recorded only for projects that learn from sessions."`
	PromptBytes     int    `json:"promptBytes,omitempty" description:"On a prompt submit: the collapsed prompt's length in bytes."`
}

// TranscriptRefResponse is the body of POST /api/v1/sessions/{sessionId}/transcript-ref.
type TranscriptRefResponse struct {
	OK bool `json:"ok"`
}

// LearningExcerptsQuery is the query of GET /api/v1/learning/excerpts.
type LearningExcerptsQuery struct {
	Project string `query:"project" description:"Project id."`
	Limit   int    `query:"limit,omitempty" description:"Most turns to return, newest first. Default 50, at most 500."`
}

// LearningWindowDTO is the bounded context on one side of a captured turn.
type LearningWindowDTO struct {
	AgentText string   `json:"agentText,omitempty" description:"The agent's nearest words, redacted and clipped in bytes."`
	Actions   []string `json:"actions" description:"What the agent did, as tool plus one whitelisted target, and markers for messages other than the human's."`
}

// LearningExcerptDTO is one captured human turn.
type LearningExcerptDTO struct {
	ID          int64             `json:"id"`
	ProjectID   string            `json:"projectId"`
	SessionID   string            `json:"sessionId"`
	TurnAt      time.Time         `json:"turnAt"`
	SourceClass string            `json:"sourceClass" enum:"typed,queued,suggestion_accepted,app_send,smoke_report"`
	GitBranch   string            `json:"gitBranch,omitempty"`
	Before      LearningWindowDTO `json:"before"`
	HumanText   string            `json:"humanText" description:"What the human typed, redacted."`
	After       LearningWindowDTO `json:"after"`
	Redactions  map[string]int    `json:"redactions" description:"How many values of each kind redaction replaced in this turn."`
}

// ListLearningExcerptsResponse is the body of GET /api/v1/learning/excerpts.
type ListLearningExcerptsResponse struct {
	Excerpts []LearningExcerptDTO `json:"excerpts"`
}

// LearningFailingFileDTO is a transcript whose last capture pass failed.
type LearningFailingFileDTO struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// LearningProjectStatusDTO is capture's account of one project.
type LearningProjectStatusDTO struct {
	ProjectID         string                   `json:"projectId"`
	Enabled           bool                     `json:"enabled" description:"Whether the project learns from sessions now. A project that was switched off is listed while it still holds captured turns."`
	Transcripts       int                      `json:"transcripts" description:"Transcript files capture is tracking."`
	Excerpts          int                      `json:"excerpts" description:"Human turns stored."`
	BySourceClass     map[string]int           `json:"bySourceClass"`
	HumanTurns        int                      `json:"humanTurns" description:"Human turns read across all tracked transcripts."`
	MachineTurns      int                      `json:"machineTurns" description:"Turns read that were not the human's: AO notices, other sessions' messages, briefs."`
	Prompts           int                      `json:"prompts" description:"Prompts the agent hooks saw since tracking began."`
	UnmatchedPrompts  int                      `json:"unmatchedPrompts" description:"Prompts the hooks saw more than 30 minutes ago that capture never found in a transcript. Non-zero means the transcript format may have changed."`
	LastCaptureAt     *time.Time               `json:"lastCaptureAt,omitempty"`
	FailingFiles      []LearningFailingFileDTO `json:"failingFiles"`
	AtRiskTranscripts int                      `json:"atRiskTranscripts" description:"Transcripts with unread turns whose last write is 25 days old or more; Claude Code deletes them at 30."`
	Uncollected       int                      `json:"uncollected" description:"Captured turns no model has read yet."`
	Drafts            map[string]int           `json:"drafts" description:"Candidate lessons by status: open, reversed, consumed, dropped."`
	LastCollectAt     *time.Time               `json:"lastCollectAt,omitempty"`
	LastCollectError  string                   `json:"lastCollectError,omitempty" description:"The error of the last model run, when it failed."`
	LastCollectStderr string                   `json:"lastCollectStderr,omitempty" description:"The end of what the CLI wrote to stderr on that failure."`
}

// ForgetLearningResponse is the body of DELETE /api/v1/learning/projects/{id}.
type ForgetLearningResponse struct {
	DeletedTurns int `json:"deletedTurns" description:"Captured turns deleted."`
}

// LearningStatusResponse is the body of GET /api/v1/learning/status.
type LearningStatusResponse struct {
	Projects []LearningProjectStatusDTO `json:"projects"`
	Collect  LearningCollectStatusDTO   `json:"collect"`
}

// LearningCollectRunDTO is the current or last collect run.
type LearningCollectRunDTO struct {
	Running    bool       `json:"running"`
	Manual     bool       `json:"manual"`
	Project    string     `json:"project,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Jobs       int        `json:"jobs"`
	Failed     int        `json:"failed"`
	Drafts     int        `json:"drafts"`
	CostUSD    float64    `json:"costUsd"`
	BudgetUSD  float64    `json:"budgetUsd"`
	StopReason string     `json:"stopReason,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
}

// LearningCollectStatusDTO is the collect stage across projects.
type LearningCollectStatusDTO struct {
	Enabled        bool                  `json:"enabled"`
	TodaySpendUSD  float64               `json:"todaySpendUsd" description:"What today's model runs cost, at API prices as the CLI reports them."`
	DailyBudgetUSD float64               `json:"dailyBudgetUsd"`
	Model          string                `json:"model"`
	Effort         string                `json:"effort"`
	Run            LearningCollectRunDTO `json:"run"`
}

// StartLearningCollectRequest is the body of POST /api/v1/learning/collect.
type StartLearningCollectRequest struct {
	Project   string  `json:"project,omitempty" description:"Project to collect; empty collects every project that learns from sessions."`
	BudgetUSD float64 `json:"budgetUsd" description:"Most this run may spend, at API prices. At most 50."`
}

// StartLearningCollectResponse is the body of POST /api/v1/learning/collect.
type StartLearningCollectResponse struct {
	Started bool `json:"started"`
}

// LearningDraftsQuery is the query of GET /api/v1/learning/drafts.
type LearningDraftsQuery struct {
	Project string `query:"project" description:"Project id."`
	Limit   int    `query:"limit,omitempty" description:"Most drafts to return, newest first. Default 50, at most 500."`
}

// LearningDraftDTO is one candidate lesson.
type LearningDraftDTO struct {
	ID                int64      `json:"id"`
	ProjectID         string     `json:"projectId"`
	SessionID         string     `json:"sessionId"`
	TaskKey           string     `json:"taskKey"`
	Kind              string     `json:"kind" enum:"correction,rule,procedure,fact,preference"`
	Statement         string     `json:"statement"`
	AppliesWhen       string     `json:"appliesWhen,omitempty"`
	ScopeHint         string     `json:"scopeHint,omitempty"`
	Confidence        float64    `json:"confidence"`
	About             string     `json:"about,omitempty" enum:"agent_practice,product_decision,one_off,question" description:"What the lesson concerns, as the collect model tagged it. Only agent_practice is about how agents work."`
	Quote             string     `json:"quote" description:"The human's own words the draft rests on, a checked substring of the anchor turn."`
	AnchorExcerptID   int64      `json:"anchorExcerptId"`
	AnchorSourceClass string     `json:"anchorSourceClass,omitempty"`
	AnchorTurnAt      *time.Time `json:"anchorTurnAt,omitempty"`
	AgentBefore       string     `json:"agentBefore,omitempty"`
	Weak              bool       `json:"weak" description:"Rests only on a suggestion the human accepted."`
	SupersedesID      int64      `json:"supersedesId,omitempty"`
	Status            string     `json:"status" enum:"open,reversed,consumed,dropped"`
	CreatedAt         time.Time  `json:"createdAt"`
}

// ListLearningDraftsResponse is the body of GET /api/v1/learning/drafts.
type ListLearningDraftsResponse struct {
	Drafts []LearningDraftDTO `json:"drafts"`
}

// LearningSettingsDTO is learning's model knobs.
type LearningSettingsDTO struct {
	CollectModel   string  `json:"collectModel"`
	CollectEffort  string  `json:"collectEffort" enum:"low,medium,high,xhigh,max"`
	RulesModel     string  `json:"rulesModel,omitempty" description:"Model that splits the standing rules into statements. Empty on a write keeps the current one."`
	DecideModel    string  `json:"decideModel,omitempty" description:"Model that draws proposals from a finished task and verifies them. Empty on a write keeps the current one."`
	DecideEffort   string  `json:"decideEffort,omitempty" enum:"low,medium,high,xhigh,max" description:"Empty on a write keeps the current one."`
	DailyBudgetUSD float64 `json:"dailyBudgetUsd" description:"The background model runs (collect and rules) stop for the rest of the local day at this spend; 0 pauses them."`
}

// Register mounts the learning routes.
func (c *LearningController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/transcript-ref", c.transcriptRef)
	r.Get("/learning/status", c.status)
	r.Get("/learning/excerpts", c.excerpts)
	r.Delete("/learning/projects/{id}", c.forget)
	r.Post("/learning/collect", c.startCollect)
	r.Get("/learning/drafts", c.drafts)
	r.Get("/learning/settings", c.getSettings)
	r.Put("/learning/settings", c.putSettings)
}

func (c *LearningController) startCollect(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/learning/collect")
		return
	}
	var in StartLearningCollectRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	err := c.Svc.StartCollect(r.Context(), in.Project, in.BudgetUSD)
	switch {
	case err == nil:
		envelope.WriteJSON(w, http.StatusAccepted, StartLearningCollectResponse{Started: true})
	case errors.Is(err, learning.ErrUnknownProject):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "Unknown project", nil)
	case errors.Is(err, learning.ErrNotLearning):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "NOT_LEARNING", "The project does not learn from sessions", nil)
	case errors.Is(err, learncollect.ErrBusy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "COLLECT_BUSY", "A collect run is already in progress", nil)
	case errors.Is(err, learning.ErrCollectUnavailable):
		apispec.NotImplemented(w, r, "POST", "/api/v1/learning/collect")
	default:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_COLLECT", err.Error(), nil)
	}
}

func (c *LearningController) drafts(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/learning/drafts")
		return
	}
	project := r.URL.Query().Get("project")
	if project == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_REQUIRED", "project is required", nil)
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_LIMIT", "limit must be a non-negative integer", nil)
			return
		}
		limit = n
	}
	rows, err := c.Svc.Drafts(r.Context(), domain.ProjectID(project), limit)
	if errors.Is(err, learning.ErrUnknownProject) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "Unknown project", nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := ListLearningDraftsResponse{Drafts: make([]LearningDraftDTO, 0, len(rows))}
	for _, d := range rows {
		out.Drafts = append(out.Drafts, draftDTO(d))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func draftDTO(d domain.LearnDraft) LearningDraftDTO {
	dto := LearningDraftDTO{
		ID: d.ID, ProjectID: string(d.ProjectID), SessionID: string(d.SessionID), TaskKey: d.TaskKey,
		Kind: string(d.Kind), Statement: d.Statement, AppliesWhen: d.AppliesWhen, ScopeHint: d.ScopeHint,
		Confidence: d.Confidence, About: string(d.About), Quote: d.Quote, AnchorExcerptID: d.AnchorExcerptID,
		AnchorSourceClass: string(d.AnchorSourceClass), AgentBefore: d.AgentBefore, Weak: d.Weak,
		SupersedesID: d.SupersedesID, Status: string(d.Status), CreatedAt: d.CreatedAt,
	}
	if !d.AnchorTurnAt.IsZero() {
		t := d.AnchorTurnAt
		dto.AnchorTurnAt = &t
	}
	return dto
}

func (c *LearningController) getSettings(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/learning/settings")
		return
	}
	s, err := c.Svc.Settings()
	if errors.Is(err, learning.ErrCollectUnavailable) {
		apispec.NotImplemented(w, r, "GET", "/api/v1/learning/settings")
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, LearningSettingsDTO{CollectModel: s.CollectModel, CollectEffort: s.CollectEffort, RulesModel: s.RulesModel,
		DecideModel: s.DecideModel, DecideEffort: s.DecideEffort, DailyBudgetUSD: s.DailyBudgetUSD})
}

func (c *LearningController) putSettings(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/learning/settings")
		return
	}
	var in LearningSettingsDTO
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	next := learnsettings.Settings{CollectModel: in.CollectModel, CollectEffort: in.CollectEffort, RulesModel: in.RulesModel,
		DecideModel: in.DecideModel, DecideEffort: in.DecideEffort, DailyBudgetUSD: in.DailyBudgetUSD}
	// A client from before a field existed keeps its current value.
	if cur, err := c.Svc.Settings(); err == nil {
		for _, f := range []struct{ next, cur *string }{
			{&next.RulesModel, &cur.RulesModel}, {&next.DecideModel, &cur.DecideModel}, {&next.DecideEffort, &cur.DecideEffort},
		} {
			if *f.next == "" {
				*f.next = *f.cur
			}
		}
	}
	in.RulesModel, in.DecideModel, in.DecideEffort = next.RulesModel, next.DecideModel, next.DecideEffort
	err := c.Svc.SetSettings(next)
	switch {
	case errors.Is(err, learning.ErrCollectUnavailable):
		apispec.NotImplemented(w, r, "PUT", "/api/v1/learning/settings")
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
	default:
		envelope.WriteJSON(w, http.StatusOK, in)
	}
}

func (c *LearningController) forget(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/learning/projects/{id}")
		return
	}
	n, err := c.Svc.Forget(r.Context(), domain.ProjectID(chi.URLParam(r, "id")))
	switch {
	case err == nil:
		envelope.WriteJSON(w, http.StatusOK, ForgetLearningResponse{DeletedTurns: n})
	case errors.Is(err, learning.ErrUnknownProject):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "Unknown project", nil)
	case errors.Is(err, learning.ErrStillLearning):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "STILL_LEARNING", "The project still learns from sessions; turn learnFromSessions off first", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}

func (c *LearningController) transcriptRef(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/transcript-ref")
		return
	}
	var in TranscriptRefRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	err := c.Svc.RecordTranscriptRef(r.Context(), sessionID(r), domain.HookTranscriptRef{
		ClaudeSessionID: in.ClaudeSessionID,
		TranscriptPath:  in.TranscriptPath,
		PromptSHA256:    in.PromptSHA256,
		PromptBytes:     in.PromptBytes,
	})
	switch {
	case err == nil:
		envelope.WriteJSON(w, http.StatusOK, TranscriptRefResponse{OK: true})
	case errors.Is(err, learning.ErrInvalidTranscriptPath):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_TRANSCRIPT_PATH", "transcriptPath is not a Claude Code transcript", nil)
	case errors.Is(err, ports.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}

func (c *LearningController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/learning/status")
		return
	}
	projects, err := c.Svc.Status(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := LearningStatusResponse{Projects: make([]LearningProjectStatusDTO, 0, len(projects))}
	for _, p := range projects {
		dto := LearningProjectStatusDTO{
			ProjectID:         string(p.ProjectID),
			Enabled:           p.Enabled,
			Transcripts:       p.Transcripts,
			Excerpts:          p.Excerpts,
			BySourceClass:     map[string]int{},
			HumanTurns:        p.HumanTurns,
			MachineTurns:      p.MachineTurns,
			Prompts:           p.Prompts,
			UnmatchedPrompts:  p.UnmatchedPrompts,
			FailingFiles:      make([]LearningFailingFileDTO, 0, len(p.FailingFiles)),
			AtRiskTranscripts: p.AtRiskTranscripts,
		}
		for k, v := range p.BySourceClass {
			dto.BySourceClass[string(k)] = v
		}
		if !p.LastCaptureAt.IsZero() {
			t := p.LastCaptureAt
			dto.LastCaptureAt = &t
		}
		for _, f := range p.FailingFiles {
			dto.FailingFiles = append(dto.FailingFiles, LearningFailingFileDTO{Path: f.Path, Error: f.Error})
		}
		dto.Uncollected = p.Uncollected
		dto.Drafts = map[string]int{}
		for k, v := range p.Drafts {
			dto.Drafts[string(k)] = v
		}
		if !p.LastCollectAt.IsZero() {
			t := p.LastCollectAt
			dto.LastCollectAt = &t
		}
		if p.LastCollectFail != nil {
			dto.LastCollectError, dto.LastCollectStderr = p.LastCollectFail.Error, p.LastCollectFail.StderrTail
		}
		out.Projects = append(out.Projects, dto)
	}
	cs, err := c.Svc.CollectStatus(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out.Collect = LearningCollectStatusDTO{
		Enabled: cs.Enabled, TodaySpendUSD: cs.TodaySpendUSD, DailyBudgetUSD: cs.DailyBudgetUSD,
		Model: cs.Model, Effort: cs.Effort,
		Run: LearningCollectRunDTO{
			Running: cs.Run.Running, Manual: cs.Run.Manual, Project: cs.Run.Project, Jobs: cs.Run.Jobs,
			Failed: cs.Run.Failed, Drafts: cs.Run.Drafts, CostUSD: cs.Run.CostUSD, BudgetUSD: cs.Run.BudgetUSD,
			StopReason: cs.Run.StopReason, LastError: cs.Run.LastError,
		},
	}
	if !cs.Run.StartedAt.IsZero() {
		t := cs.Run.StartedAt
		out.Collect.Run.StartedAt = &t
	}
	if !cs.Run.FinishedAt.IsZero() {
		t := cs.Run.FinishedAt
		out.Collect.Run.FinishedAt = &t
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *LearningController) excerpts(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/learning/excerpts")
		return
	}
	project := r.URL.Query().Get("project")
	if project == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_REQUIRED", "project is required", nil)
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_LIMIT", "limit must be a non-negative integer", nil)
			return
		}
		limit = n
	}
	rows, err := c.Svc.Excerpts(r.Context(), domain.ProjectID(project), limit)
	if errors.Is(err, learning.ErrUnknownProject) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PROJECT_NOT_FOUND", "Unknown project", nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := ListLearningExcerptsResponse{Excerpts: make([]LearningExcerptDTO, 0, len(rows))}
	for _, e := range rows {
		red := e.Redactions
		if red == nil {
			red = map[string]int{}
		}
		out.Excerpts = append(out.Excerpts, LearningExcerptDTO{
			ID:          e.ID,
			ProjectID:   string(e.ProjectID),
			SessionID:   string(e.SessionID),
			TurnAt:      e.TurnAt,
			SourceClass: string(e.SourceClass),
			GitBranch:   e.GitBranch,
			Before:      windowDTO(e.Before),
			HumanText:   e.HumanText,
			After:       windowDTO(e.After),
			Redactions:  red,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func windowDTO(w domain.LearnWindow) LearningWindowDTO {
	actions := w.Actions
	if actions == nil {
		actions = []string{}
	}
	return LearningWindowDTO{AgentText: w.AgentText, Actions: actions}
}
