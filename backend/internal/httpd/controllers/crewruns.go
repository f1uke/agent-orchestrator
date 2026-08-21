package controllers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	crewrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/crewrun"
)

// StartCrewRunInput is the body of POST .../crew/runs.
type StartCrewRunInput struct {
	Kind  string `json:"kind" description:"What is about to run: build, test or device." enum:"build,test,device"`
	Label string `json:"label,omitempty" description:"Free-text label for the run (e.g. the command), shown in the Tests tab."`
}

// StartCrewRunResponse is the body of POST .../crew/runs.
type StartCrewRunResponse struct {
	Run domain.CrewRun `json:"run"`
	// Certified is false when no tree-write detector could be established. The
	// run still goes ahead, and its result will be marked uncertified rather
	// than passed off as verified.
	Certified       bool   `json:"certified" description:"Whether a tree-write detector is watching this run. False means the result will be uncertified."`
	SupersededRunID string `json:"supersededRunId,omitempty" description:"A run this start had to abandon because it was left open; it was closed as uncertified."`
}

// EndCrewRunInput is the body of POST .../crew/runs/end.
type EndCrewRunInput struct {
	RunID  string `json:"runId,omitempty" description:"Run to close. Omitted closes the session's open run."`
	Result string `json:"result,omitempty" description:"What the build or test said. Omitted records a run that did not judge itself." enum:"pass,fail"`
}

// EndCrewRunResponse is the body of POST .../crew/runs/end: the closed run plus
// what the member should do about it.
type EndCrewRunResponse struct {
	Run         domain.CrewRun `json:"run"`
	Retry       bool           `json:"retry" description:"The run was discarded and an automatic re-run is still within the cap."`
	Attempt     int            `json:"attempt" description:"This run's position in the current discard streak."`
	MaxAttempts int            `json:"maxAttempts" description:"How many discards are retried automatically before a human decides."`
	Escalated   bool           `json:"escalated" description:"The cap is spent: stop re-running; the task parks at NEEDS YOU."`
}

// ListCrewRunsResponse is the body of GET .../crew/runs.
type ListCrewRunsResponse struct {
	Runs []domain.CrewRun `json:"runs"`
}

// CrewRunsController owns the session-scoped /crew/runs routes: the bracket a
// crew member puts around a build or a test run. A nil Svc returns 501,
// mirroring the other optional controllers.
type CrewRunsController struct {
	Svc crewrunsvc.Manager
}

// Register mounts the crew-run routes on the supplied router.
func (c *CrewRunsController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/crew/runs", c.list)
	r.Post("/sessions/{sessionId}/crew/runs", c.start)
	r.Post("/sessions/{sessionId}/crew/runs/end", c.end)
}

func (c *CrewRunsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/crew/runs")
		return
	}
	runs, err := c.Svc.List(r.Context(), sessionID(r))
	if err != nil {
		c.fail(w, r, err)
		return
	}
	if runs == nil {
		runs = []domain.CrewRun{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListCrewRunsResponse{Runs: runs})
}

func (c *CrewRunsController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/crew/runs")
		return
	}
	var in StartCrewRunInput
	if err := decodeCrewRunBody(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	res, err := c.Svc.Start(r.Context(), sessionID(r), crewrunsvc.StartInput{
		Kind:  domain.CrewRunKind(strings.TrimSpace(in.Kind)),
		Label: in.Label,
	})
	if err != nil {
		c.fail(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, StartCrewRunResponse{
		Run:             res.Run,
		Certified:       res.Certified,
		SupersededRunID: res.SupersededRunID,
	})
}

func (c *CrewRunsController) end(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/crew/runs/end")
		return
	}
	var in EndCrewRunInput
	if err := decodeCrewRunBody(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	res, err := c.Svc.End(r.Context(), sessionID(r), crewrunsvc.EndInput{
		RunID:  in.RunID,
		Result: domain.CrewRunResult(strings.TrimSpace(in.Result)),
	})
	if err != nil {
		c.fail(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, EndCrewRunResponse{
		Run:         res.Run,
		Retry:       res.Retry,
		Attempt:     res.Attempt,
		MaxAttempts: res.MaxAttempts,
		Escalated:   res.Escalated,
	})
}

func (c *CrewRunsController) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, crewrunsvc.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "CREW_RUN_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, crewrunsvc.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "CREW_RUN_INVALID", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "CREW_RUN_FAILED", "Crew run operation failed", nil)
	}
}

// decodeCrewRunBody accepts an absent body: `ao crew run --end` with no flags is
// a legitimate call and should not need to post `{}`.
func decodeCrewRunBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}
