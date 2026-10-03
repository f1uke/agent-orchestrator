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
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

// LearningService is the controller-facing contract over learning capture.
type LearningService interface {
	RecordTranscriptRef(ctx context.Context, id domain.SessionID, ref domain.HookTranscriptRef) error
	Status(ctx context.Context) ([]learning.ProjectStatus, error)
	Excerpts(ctx context.Context, projectID domain.ProjectID, limit int) ([]domain.LearnExcerpt, error)
	Forget(ctx context.Context, projectID domain.ProjectID) (int, error)
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
}

// ForgetLearningResponse is the body of DELETE /api/v1/learning/projects/{id}.
type ForgetLearningResponse struct {
	DeletedTurns int `json:"deletedTurns" description:"Captured turns deleted."`
}

// LearningStatusResponse is the body of GET /api/v1/learning/status.
type LearningStatusResponse struct {
	Projects []LearningProjectStatusDTO `json:"projects"`
}

// Register mounts the learning routes.
func (c *LearningController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/transcript-ref", c.transcriptRef)
	r.Get("/learning/status", c.status)
	r.Get("/learning/excerpts", c.excerpts)
	r.Delete("/learning/projects/{id}", c.forget)
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
		out.Projects = append(out.Projects, dto)
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
