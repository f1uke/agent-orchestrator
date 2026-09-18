package controllers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	iosrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
)

// IOSProjectResponse is the body of GET /api/v1/sessions/{sessionId}/ios-project:
// what this session can build, and the run it already has.
//
// An empty `project.name` is the ordinary answer on a worktree with no Xcode
// project, not an error - it is exactly the test the run bar uses to decide
// whether to render at all, so most sessions get it.
type IOSProjectResponse struct {
	Project iosrunsvc.Project `json:"project" description:"The Xcode project at the root of this session's worktree. An empty name means there is none, and the run bar does not render."`
	Run     *iosrunsvc.Run    `json:"run,omitempty" description:"The run this session started, if it has one. Its handleId is the terminal to attach."`
}

// IOSProjectQuery is the query string of GET .../ios-project.
type IOSProjectQuery struct {
	Refresh bool `query:"refresh,omitempty" description:"Re-read the project from disk instead of serving the cached listing. The run bar sends it when a picker is opened, so a scheme or configuration added seconds ago in the terminal is there."`
}

// StartIOSRunInput is the body of POST /api/v1/sessions/{sessionId}/ios-runs.
type StartIOSRunInput struct {
	Scheme string `json:"scheme" description:"The Xcode scheme to build, as listed on the project."`
	// Required, and deliberately not defaulted to Debug: a project with no Debug
	// configuration (nter-ios-app has Dev, UAT, Production and no Debug) cannot
	// build one, and the failure arrives minutes later as an empty PODS_ROOT.
	Configuration string `json:"configuration" description:"The build configuration - which environment to build for, as listed on the project. Required: there is no safe default across projects."`
	UDID          string `json:"udid,omitempty" description:"The simulator to install and launch on. Omitted uses the one assigned to this session."`
}

// StartIOSRunResponse is the body of a started run (201).
type StartIOSRunResponse struct {
	Run iosrunsvc.Run `json:"run"`
}

// IOSRunController owns the session-scoped iOS run routes. A nil Svc returns 501,
// which is the right answer on a machine that cannot build for iOS at all.
type IOSRunController struct {
	Svc iosrunsvc.Manager
}

// Register mounts the iOS run routes on the supplied router.
func (c *IOSRunController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/ios-project", c.project)
	r.Post("/sessions/{sessionId}/ios-runs", c.start)
}

func (c *IOSRunController) project(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/ios-project")
		return
	}
	id := sessionID(r)
	project, err := c.Svc.Project(r.Context(), id, queryBool(r.URL.Query().Get("refresh")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// `schemes` and `configurations` are declared arrays in the spec, so neither
	// may be `null` on the wire: a project with none (a CocoaPods workspace with
	// no `Pods/`, say) leaves the slice nil, and a renderer reading `.length` off
	// that answer takes the whole view down. Normalising in the handler is the
	// convention the spec generator documents and assumes.
	if project.Schemes == nil {
		project.Schemes = []string{}
	}
	if project.Configurations == nil {
		project.Configurations = []string{}
	}
	res := IOSProjectResponse{Project: project}
	// The run rides on the project read rather than on a route of its own: the
	// bar asks this question on every poll anyway, and a second request for
	// "is my build still going" would double the traffic to say nothing new.
	if run, ok, runErr := c.Svc.Current(r.Context(), id); runErr == nil && ok {
		res.Run = &run
	}
	envelope.WriteJSON(w, http.StatusOK, res)
}

func (c *IOSRunController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/ios-runs")
		return
	}
	var in StartIOSRunInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	run, err := c.Svc.Start(r.Context(), sessionID(r), in.Scheme, in.Configuration, in.UDID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, StartIOSRunResponse{Run: run})
}
