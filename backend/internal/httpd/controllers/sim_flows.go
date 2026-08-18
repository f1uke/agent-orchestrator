package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

// The routes behind the Device tab's list of what this session has recorded.
//
// A recorded flow only earns its keep if it can be found again and handed to
// somebody: the workflow these exist for is a human playing through an app by
// hand to demonstrate how to reach a screen, then giving that flow to a worker
// to replay and carry on from. Finding it meant opening files one at a time,
// because the flows sat in a flat directory beside every screenshot the
// session had taken, under a name made of a timestamp and a udid.
//
// These are file operations rather than device operations, which is why they
// are their own controller: nothing here touches a simulator, takes a lease or
// needs one, and a session whose device is long gone can still list, name and
// delete what it recorded.

// SimFlowView is one recorded flow as a list needs it.
//
// It deliberately carries no step CONTENT - not the selectors, and above all
// not the text a step typed. A list exists to find a file, and the flow itself
// is the place to read what is in it.
type SimFlowView struct {
	Name             string    `json:"name" description:"What a human called it, empty when it has not been named. Read back out of the file name, which is the only place it is stored."`
	FileName         string    `json:"fileName" description:"The base name, which is what a human writes in prose."`
	Path             string    `json:"path" description:"Absolute path, which is what a worker can act on."`
	RecordedAt       time.Time `json:"recordedAt"`
	TimeFromFileName bool      `json:"timeFromFileName" description:"False when recordedAt fell back to the file's modification time because its name carries no timestamp."`
	Steps            int       `json:"steps"`
	Review           int       `json:"review" description:"How many steps are marked \"# REVIEW:\" - the number a human must check before trusting the flow."`
	CountsKnown      bool      `json:"countsKnown" description:"False for a flow recorded before flows stated their own counts; steps and review are then unmeasured rather than zero."`
	Bytes            int64     `json:"bytes"`
}

// ListSimFlowsResponse is the body of GET .../sim-flows.
type ListSimFlowsResponse struct {
	Flows []SimFlowView `json:"flows"`
}

// SimFlowResponse is the { flow } body returned by renaming one.
type SimFlowResponse struct {
	Flow SimFlowView `json:"flow"`
}

// RenameSimFlowInput is the body of PATCH .../sim-flows/{fileName}.
type RenameSimFlowInput struct {
	Name string `json:"name" description:"What to call it. Slugified; an empty name puts it back to its timestamp alone."`
}

// SimFlowParam is the {sessionId}/{fileName} pair the per-flow routes take.
type SimFlowParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
	FileName  string `path:"fileName" description:"The flow's base file name. Anything that is not a bare .yaml file name is refused."`
}

// SimFlowsController owns a session's recorded flows on disk. DataDir empty
// means the daemon was built without one, and every route answers 501 rather
// than guessing at a location.
type SimFlowsController struct {
	DataDir string
}

// Register mounts the routes. They hang off the session that recorded them,
// which is also the directory they live in.
func (c *SimFlowsController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/sim-flows", c.list)
	r.Patch("/sessions/{sessionId}/sim-flows/{fileName}", c.rename)
	r.Delete("/sessions/{sessionId}/sim-flows/{fileName}", c.delete)
}

func (c *SimFlowsController) ready(w http.ResponseWriter, r *http.Request, method, path string) bool {
	if c.DataDir == "" {
		apispec.NotImplemented(w, r, method, path)
		return false
	}
	return true
}

func (c *SimFlowsController) list(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, "GET", "/api/v1/sessions/{sessionId}/sim-flows") {
		return
	}
	flows, err := simrecord.List(c.DataDir, string(sessionID(r)))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_FLOW_LIST_FAILED",
			"Could not read this session's recordings", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSimFlowsResponse{Flows: simFlowViews(flows)})
}

func (c *SimFlowsController) rename(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, "PATCH", "/api/v1/sessions/{sessionId}/sim-flows/{fileName}") {
		return
	}
	var in RenameSimFlowInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	flow, err := simrecord.Rename(c.DataDir, string(sessionID(r)), chi.URLParam(r, "fileName"), in.Name)
	if err != nil {
		writeSimFlowError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimFlowResponse{Flow: simFlowView(flow)})
}

func (c *SimFlowsController) delete(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, "DELETE", "/api/v1/sessions/{sessionId}/sim-flows/{fileName}") {
		return
	}
	if err := simrecord.Delete(c.DataDir, string(sessionID(r)), chi.URLParam(r, "fileName")); err != nil {
		writeSimFlowError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeSimFlowError keeps "there is no such recording" apart from "that is not
// a name I will act on". A caller that mistyped a file name and a caller
// sending a path are different callers, and only one of them is making a
// mistake.
func writeSimFlowError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, simrecord.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SIM_FLOW_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, simrecord.ErrInvalidName):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_FLOW_INVALID_NAME", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_FLOW_OPERATION_FAILED",
			"Could not complete that operation on the recording", nil)
	}
}

func simFlowView(f simrecord.Flow) SimFlowView {
	return SimFlowView{
		Name: f.Name, FileName: f.FileName, Path: f.Path,
		RecordedAt: f.RecordedAt.UTC(), TimeFromFileName: f.TimeFromFileName,
		Steps: f.Steps, Review: f.Review, CountsKnown: f.Known, Bytes: f.Bytes,
	}
}

func simFlowViews(flows []simrecord.Flow) []SimFlowView {
	views := make([]SimFlowView, 0, len(flows))
	for _, f := range flows {
		views = append(views, simFlowView(f))
	}
	return views
}
