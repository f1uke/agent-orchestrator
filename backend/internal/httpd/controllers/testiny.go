package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	rcloneadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/rclone"
	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	testinysvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testiny"
)

// TestinyService is the controller-facing Testiny contract, satisfied by
// *service/testiny.Service.
type TestinyService interface {
	Link(ctx context.Context, task domain.SessionID, ref, project, by string) (domain.TestinyRunView, error)
	Unlink(ctx context.Context, task domain.SessionID, id domain.TestinyRunID) error
	Runs(ctx context.Context, task domain.SessionID, refresh bool) ([]domain.TestinyRunView, error)
	RecordResults(ctx context.Context, task domain.SessionID, id domain.TestinyRunID, results []domain.TestinyResult, by, sha string) (domain.TestinyRunView, error)
	Case(ctx context.Context, task domain.SessionID, id int64) (domain.TestinyCaseDetail, error)
	UploadEvidence(ctx context.Context, task domain.SessionID, id domain.TestinyRunID, by string) (domain.TestinyEvidenceReport, error)
}

// TestinyRunsResponse is the body of GET /api/v1/sessions/{sessionId}/testiny/runs.
type TestinyRunsResponse struct {
	Runs []domain.TestinyRunView `json:"runs" description:"The task's runs in the order they were linked. A run Testiny could not read now carries fetchError."`
}

// TestinyRunsQuery is the query string of GET /api/v1/sessions/{sessionId}/testiny/runs.
type TestinyRunsQuery struct {
	Refresh string `query:"refresh,omitempty" description:"1 reads every run from Testiny now instead of serving a read from the last 15 seconds."`
}

// LinkTestinyRunInput is the body of POST /api/v1/sessions/{sessionId}/testiny/runs.
type LinkTestinyRunInput struct {
	Ref     string `json:"ref" description:"The run: its id (632), TR-632, or its URL (https://app.testiny.io/MOB/testruns/tr/632). Run ids are global in Testiny, so the run names its own project."`
	Project string `json:"project,omitempty" description:"The Testiny project the run should be in: its key, name or id. Optional; when given, a run in another project is refused, as is a URL whose key names another project."`
	From    string `json:"from,omitempty" description:"Session id of the agent linking the run ($AO_SESSION_ID). Empty when a person links it in the app."`
}

// TestinyResultInput is one case's result in RecordTestinyResultsInput.
type TestinyResultInput struct {
	CaseID  int64                    `json:"caseId" description:"The Testiny case id (7166 for TC-7166)."`
	Status  string                   `json:"status,omitempty" description:"PASSED, FAILED, BLOCKED, SKIPPED or NOTRUN. Empty when only steps are given: the case keeps its status."`
	Comment string                   `json:"comment,omitempty" description:"What happened, at most 300 characters. FAILED, BLOCKED and SKIPPED need one; PASSED and NOTRUN take none."`
	Steps   []TestinyStepResultInput `json:"steps,omitempty" description:"Results for steps of a STEPS case. Every other step keeps the result it has."`
}

// TestinyStepResultInput is one step's result in TestinyResultInput.
type TestinyStepResultInput struct {
	N      int    `json:"n" description:"The step's number, counting from 1."`
	Status string `json:"status" description:"PASSED, FAILED, BLOCKED, SKIPPED or NOTRUN. A step takes no comment."`
}

// RecordTestinyResultsInput is the body of POST /api/v1/sessions/{sessionId}/testiny/runs/{runId}/results.
type RecordTestinyResultsInput struct {
	Results []TestinyResultInput `json:"results" description:"The results to record. The whole batch is checked before any is written."`
	From    string               `json:"from,omitempty" description:"Session id of the agent recording them ($AO_SESSION_ID). Empty when a person sets them in the app."`
	SHA     string               `json:"sha,omitempty" description:"The commit the agent tested, for the tab's provenance line."`
}

// UploadTestinyEvidenceInput is the body of POST /api/v1/sessions/{sessionId}/testiny/runs/{runId}/evidence.
type UploadTestinyEvidenceInput struct {
	From string `json:"from,omitempty" description:"Session id of the agent uploading ($AO_SESSION_ID). Empty when a person uploads from the app."`
}

// TestinyRunParam is the {sessionId}/{runId} path of one linked run.
type TestinyRunParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
	RunID     string `path:"runId" description:"Testiny run id, e.g. 632 or TR-632."`
}

// TestinyCaseParam is the {sessionId}/{caseId} path of one case in a task's runs.
type TestinyCaseParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
	CaseID    string `path:"caseId" description:"Testiny case id, e.g. 7166 or TC-7166."`
}

// TestinyController owns a task's /testiny routes. They are task-scoped: a run
// linked from a crew's qa belongs to the task. A nil Svc answers 501.
type TestinyController struct {
	Svc TestinyService
}

// Register mounts the Testiny routes.
func (c *TestinyController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/testiny/runs", c.list)
	r.Post("/sessions/{sessionId}/testiny/runs", c.link)
	r.Delete("/sessions/{sessionId}/testiny/runs/{runId}", c.unlink)
	r.Post("/sessions/{sessionId}/testiny/runs/{runId}/results", c.recordResults)
	r.Get("/sessions/{sessionId}/testiny/cases/{caseId}", c.readCase)
}

// RegisterUntimed mounts the routes that outlive the REST timeout: an
// evidence upload sends screen recordings to Drive, which takes minutes.
func (c *TestinyController) RegisterUntimed(r chi.Router) {
	r.Post("/sessions/{sessionId}/testiny/runs/{runId}/evidence", c.uploadEvidence)
}

func (c *TestinyController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/testiny/runs")
		return
	}
	refresh := r.URL.Query().Get("refresh")
	res, err := c.Svc.Runs(r.Context(), sessionID(r), refresh == "1" || refresh == "true")
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	runs := res
	if runs == nil {
		runs = []domain.TestinyRunView{}
	}
	envelope.WriteJSON(w, http.StatusOK, TestinyRunsResponse{Runs: runs})
}

func (c *TestinyController) link(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/testiny/runs")
		return
	}
	var in LinkTestinyRunInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	view, err := c.Svc.Link(r.Context(), sessionID(r), in.Ref, strings.TrimSpace(in.Project), strings.TrimSpace(in.From))
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, view)
}

func (c *TestinyController) unlink(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/testiny/runs/{runId}")
		return
	}
	id, _, err := domain.ParseTestinyRunRef(chi.URLParam(r, "runId"))
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	if err := c.Svc.Unlink(r.Context(), sessionID(r), id); err != nil {
		writeTestinyError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *TestinyController) recordResults(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/testiny/runs/{runId}/results")
		return
	}
	id, _, err := domain.ParseTestinyRunRef(chi.URLParam(r, "runId"))
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	var in RecordTestinyResultsInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	results := make([]domain.TestinyResult, len(in.Results))
	for i, res := range in.Results {
		results[i] = domain.TestinyResult{CaseID: res.CaseID, Status: domain.TestinyCaseStatus(res.Status), Comment: res.Comment}
		for _, step := range res.Steps {
			results[i].Steps = append(results[i].Steps, domain.TestinyStepResult{N: step.N, Status: domain.TestinyCaseStatus(step.Status)})
		}
	}
	view, err := c.Svc.RecordResults(r.Context(), sessionID(r), id, results, strings.TrimSpace(in.From), strings.TrimSpace(in.SHA))
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, view)
}

func (c *TestinyController) uploadEvidence(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/testiny/runs/{runId}/evidence")
		return
	}
	id, _, err := domain.ParseTestinyRunRef(chi.URLParam(r, "runId"))
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	var in UploadTestinyEvidenceInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	report, err := c.Svc.UploadEvidence(r.Context(), sessionID(r), id, strings.TrimSpace(in.From))
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, report)
}

func (c *TestinyController) readCase(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/testiny/cases/{caseId}")
		return
	}
	id, err := domain.ParseTestinyCaseRef(chi.URLParam(r, "caseId"))
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	detail, err := c.Svc.Case(r.Context(), sessionID(r), id)
	if err != nil {
		writeTestinyError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, detail)
}

func writeTestinyError(w http.ResponseWriter, r *http.Request, err error) {
	write := func(status int, kind, code string) {
		envelope.WriteAPIError(w, r, status, kind, code, err.Error(), nil)
	}
	switch {
	case errors.Is(err, domain.ErrBadRunRef):
		write(http.StatusBadRequest, "bad_request", "TESTINY_BAD_RUN_REF")
	case errors.Is(err, domain.ErrBadCaseRef):
		write(http.StatusBadRequest, "bad_request", "TESTINY_BAD_CASE_REF")
	case errors.Is(err, domain.ErrBadTestinyResult):
		write(http.StatusBadRequest, "bad_request", "TESTINY_RESULT_INVALID")
	case errors.Is(err, domain.ErrBadTestinyEvidence):
		write(http.StatusUnprocessableEntity, "unprocessable", "TESTINY_EVIDENCE_INVALID")
	case errors.Is(err, testinysvc.ErrEvidenceOff):
		write(http.StatusConflict, "conflict", "TESTINY_EVIDENCE_OFF")
	case errors.Is(err, testinysvc.ErrRunClosed):
		write(http.StatusConflict, "conflict", "TESTINY_RUN_CLOSED")
	case errors.Is(err, testinysvc.ErrDriveDuplicate):
		write(http.StatusConflict, "conflict", "DRIVE_DUPLICATE")
	case errors.Is(err, rcloneadapter.ErrBinaryMissing):
		write(http.StatusServiceUnavailable, "unavailable", "DRIVE_RCLONE_MISSING")
	case errors.Is(err, rcloneadapter.ErrRemoteMissing):
		write(http.StatusUnprocessableEntity, "unprocessable", "DRIVE_REMOTE_MISSING")
	case errors.Is(err, rcloneadapter.ErrAuth):
		write(http.StatusBadGateway, "bad_gateway", "DRIVE_AUTH")
	case errors.Is(err, rcloneadapter.ErrUnavailable):
		write(http.StatusBadGateway, "bad_gateway", "DRIVE_UNAVAILABLE")
	case errors.Is(err, testinysvc.ErrRunNotLinked):
		write(http.StatusNotFound, "not_found", "TESTINY_RUN_NOT_LINKED")
	case errors.Is(err, testinysvc.ErrCaseNotInTask):
		write(http.StatusNotFound, "not_found", "TESTINY_CASE_NOT_IN_TASK")
	case errors.Is(err, testinysvc.ErrWriteNotYours):
		write(http.StatusForbidden, "forbidden", "TESTINY_WRITE_NOT_YOURS")
	case errors.Is(err, testinysvc.ErrSetByPerson):
		write(http.StatusConflict, "conflict", "TESTINY_RESULT_SET_BY_PERSON")
	case errors.Is(err, testinysvc.ErrSessionNotFound):
		write(http.StatusNotFound, "not_found", "SESSION_NOT_FOUND")
	case errors.Is(err, testinysvc.ErrRunNotFound):
		write(http.StatusNotFound, "not_found", "TESTINY_RUN_NOT_FOUND")
	case errors.Is(err, testinysvc.ErrWrongProject):
		write(http.StatusUnprocessableEntity, "unprocessable", "TESTINY_RUN_WRONG_PROJECT")
	case errors.Is(err, testinysvc.ErrProjectNotFound):
		write(http.StatusUnprocessableEntity, "unprocessable", "TESTINY_PROJECT_NOT_FOUND")
	case errors.Is(err, testinysvc.ErrOff):
		write(http.StatusConflict, "conflict", "TESTINY_OFF")
	case errors.Is(err, testinyadapter.ErrAuth):
		write(http.StatusBadGateway, "bad_gateway", "TESTINY_AUTH")
	case errors.Is(err, testinyadapter.ErrBinaryMissing):
		write(http.StatusBadGateway, "bad_gateway", "TESTINY_CLI_MISSING")
	case errors.Is(err, testinyadapter.ErrCLITooOld):
		write(http.StatusBadGateway, "bad_gateway", "TESTINY_CLI_TOO_OLD")
	case errors.Is(err, testinyadapter.ErrUnavailable), errors.Is(err, testinyadapter.ErrRejected), errors.Is(err, testinyadapter.ErrNotFound):
		write(http.StatusBadGateway, "bad_gateway", "TESTINY_UNAVAILABLE")
	default:
		envelope.WriteError(w, r, err)
	}
}
