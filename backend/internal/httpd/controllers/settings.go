package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
)

// SettingsService is the reclaim-settings store surface the controller needs.
// *reclaimsettings.Store satisfies this directly.
type SettingsService interface {
	Get() reclaimsettings.Settings
	Set(reclaimsettings.Settings) error
}

// SettingsController serves the global auto-reclaim settings. Nil keeps the
// routes registered but returns OpenAPI-backed 501s, matching every other
// controller in this package.
type SettingsController struct {
	Svc SettingsService
}

// Register mounts the settings routes on the supplied router.
func (c *SettingsController) Register(r chi.Router) {
	r.Get("/settings/reclaim", c.get)
	r.Put("/settings/reclaim", c.set)
}

func (c *SettingsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/reclaim")
		return
	}
	s := c.Svc.Get()
	envelope.WriteJSON(w, http.StatusOK, ReclaimSettingsResponse{Enabled: s.Enabled, GraceMinutes: s.GraceMinutes})
}

func (c *SettingsController) set(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/reclaim")
		return
	}
	var in SetReclaimSettingsRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	next := reclaimsettings.Settings{Enabled: in.Enabled, GraceMinutes: in.GraceMinutes}
	if err := c.Svc.Set(next); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReclaimSettingsResponse{Enabled: next.Enabled, GraceMinutes: next.GraceMinutes})
}
