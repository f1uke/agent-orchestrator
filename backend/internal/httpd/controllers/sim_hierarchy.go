package controllers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
)

// SimRunner is the daemon's XCTest runner (internal/simrunner): it reads the
// screen, and types for `ao sim type` (SimTypeController).
type SimRunner interface {
	Read(ctx context.Context, udid string, opts simrunner.ReadOptions) (simbridge.XCTestHierarchy, simrunner.Status, error)
	Await(ctx context.Context, udid string, wait time.Duration) (simrunner.Status, error)
	Focus(ctx context.Context, udid string) (simrunner.TypeAnswer, error)
	Type(ctx context.Context, udid, text string, opts simrunner.TypeOptions) (simrunner.TypeAnswer, error)
}

// SimHierarchyController serves the screen as XCTest reads it.
//
// It is a read, like `ao sim ax` itself: no lease, no hold. The runner only
// exists while some session holds the device, so on a device nobody holds the
// answer is the runner's state - and the caller reads through the in-app
// accessibility bridge instead, saying so.
type SimHierarchyController struct {
	Runner SimRunner
}

// Register mounts the route.
func (c *SimHierarchyController) Register(r chi.Router) {
	r.Get("/sim/devices/{udid}/hierarchy", c.hierarchy)
}

// SimHierarchyQuery is the query of GET /sim/devices/{udid}/hierarchy.
type SimHierarchyQuery struct {
	WaitMs  int  `query:"waitMs,omitempty" description:"How long to wait for a runner that is still starting, in milliseconds. 0 answers at once. Capped at 30000."`
	HitTest bool `query:"hitTest,omitempty" description:"Also hit-test every element's tap point, so an element drawn under something else (a row under the tab bar, a button under the keyboard) comes back with covered. Costs about a millisecond per element."`
}

// SimRunnerView is a device's XCTest runner as the caller needs to report it.
type SimRunnerView struct {
	State  string `json:"state" enum:"off,starting,ready,failed,unavailable" description:"off: no session holds the device. starting: launching. ready: answering. failed: the last attempt failed and is retried. unavailable: this daemon cannot run one."`
	Reason string `json:"reason,omitempty" description:"Why the runner is not answering, in words a person can act on."`
}

// SimHierarchyResponse is the runner's state and, when it answered, the screen.
type SimHierarchyResponse struct {
	Runner    SimRunnerView              `json:"runner"`
	Hierarchy *simbridge.XCTestHierarchy `json:"hierarchy,omitempty"`
}

// maxHierarchyWait bounds a caller's wait for a starting runner.
const maxHierarchyWait = 30 * time.Second

func (c *SimHierarchyController) hierarchy(w http.ResponseWriter, r *http.Request) {
	if c.Runner == nil {
		envelope.WriteJSON(w, http.StatusOK, SimHierarchyResponse{Runner: SimRunnerView{
			State:  "unavailable",
			Reason: "this daemon cannot run the XCTest reader (it needs macOS with Xcode)",
		}})
		return
	}
	wait := time.Duration(0)
	if raw := r.URL.Query().Get("waitMs"); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil || ms < 0 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_WAIT",
				"waitMs must be a non-negative number of milliseconds", nil)
			return
		}
		wait = min(time.Duration(ms)*time.Millisecond, maxHierarchyWait)
	}
	hitTest := false
	if raw := r.URL.Query().Get("hitTest"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_HIT_TEST",
				"hitTest must be true or false", nil)
			return
		}
		hitTest = parsed
	}
	h, status, err := c.Runner.Read(r.Context(), chi.URLParam(r, "udid"), simrunner.ReadOptions{Wait: wait, HitTest: hitTest})
	resp := SimHierarchyResponse{Runner: SimRunnerView{State: string(status.State), Reason: status.Reason}}
	if err == nil {
		resp.Hierarchy = &h
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}
