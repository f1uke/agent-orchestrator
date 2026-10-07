package controllers

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/simhealth"
)

// SimDoctorQuery is the query string of GET .../sim-doctor.
type SimDoctorQuery struct {
	UDID   string `query:"udid,omitempty" description:"Simulator to check. Omit for the session's assigned one."`
	App    string `query:"app,omitempty" description:"Bundle id whose installed build to check. Omit to skip the build check."`
	Expect string `query:"expect,omitempty" description:"Absolute path of a .app on this Mac that the installed app must be. Requires app."`
}

// SimDoctorCheckView is one line of the report.
type SimDoctorCheckView struct {
	Name    string `json:"name" description:"device, lease, app, debugger or proxy CA."`
	Status  string `json:"status" description:"OK, WARN or FAIL. Only FAIL makes the report not ok."`
	Message string `json:"message"`
}

// SimDoctorResponse is the body of GET .../sim-doctor: `ao sim doctor --json`.
type SimDoctorResponse struct {
	Checks []SimDoctorCheckView `json:"checks" description:"Every check that applied, in order. Only the device line is present when there is no device to check."`
	OK     bool                 `json:"ok" description:"True when no check is FAIL."`
}

// sessionReader is the one session read the doctor needs: that the session
// exists, so a mistyped id is a 404 rather than a report about nobody.
type sessionReader interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

// SimDoctorController owns the read-only simulator health check. A nil
// Readers answers 501, the state of a daemon with no lease service.
type SimDoctorController struct {
	Sessions sessionReader
	Readers  *simhealth.Readers
}

// Register mounts the route. It hangs off the session because every answer is
// relative to one: its assigned device, its lease, its project's root CAs.
func (c *SimDoctorController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/sim-doctor", c.doctor)
}

func (c *SimDoctorController) doctor(w http.ResponseWriter, r *http.Request) {
	if c.Readers == nil || c.Sessions == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/sim-doctor")
		return
	}
	q := r.URL.Query()
	req := simhealth.Request{
		SessionID: sessionID(r),
		UDID:      strings.TrimSpace(q.Get("udid")),
		App:       strings.TrimSpace(q.Get("app")),
		Expect:    strings.TrimSpace(q.Get("expect")),
	}
	if req.Expect != "" && req.App == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "SIM_DOCTOR_EXPECT_WITHOUT_APP",
			"expect names a build of an app, so it needs app (the bundle id) too", nil)
		return
	}
	if req.Expect != "" && !filepath.IsAbs(req.Expect) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "SIM_DOCTOR_EXPECT_NOT_ABSOLUTE",
			"expect must be an absolute path: the daemon does not share the caller's working directory", nil)
		return
	}
	if _, err := c.Sessions.Get(r.Context(), req.SessionID); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	report := simhealth.Diagnose(r.Context(), *c.Readers, req)
	view := SimDoctorResponse{Checks: make([]SimDoctorCheckView, 0, len(report.Checks)), OK: report.OK}
	for _, check := range report.Checks {
		view.Checks = append(view.Checks, SimDoctorCheckView{Name: check.Name, Status: string(check.Status), Message: check.Message})
	}
	envelope.WriteJSON(w, http.StatusOK, view)
}
