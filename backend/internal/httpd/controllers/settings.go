package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/reclaimsettings"
	"github.com/aoagents/agent-orchestrator/backend/internal/spawnconfirm"
)

// SettingsService is the reclaim-settings store surface the controller needs.
// *reclaimsettings.Store satisfies this directly.
type SettingsService interface {
	Get() reclaimsettings.Settings
	Set(reclaimsettings.Settings) error
}

// SpawnConfirmService is the spawn-confirm settings store surface the controller
// needs. *spawnconfirm.Store satisfies this directly.
type SpawnConfirmService interface {
	Get() spawnconfirm.Settings
	Set(spawnconfirm.Settings) error
}

// SettingsController serves the global auto-reclaim settings. Nil keeps the
// routes registered but returns OpenAPI-backed 501s, matching every other
// controller in this package.
type SettingsController struct {
	Svc          SettingsService
	SpawnConfirm SpawnConfirmService
}

// Register mounts the settings routes on the supplied router.
func (c *SettingsController) Register(r chi.Router) {
	r.Get("/settings/reclaim", c.get)
	r.Put("/settings/reclaim", c.set)
	r.Get("/settings/spawn-confirm", c.getSpawnConfirm)
	r.Put("/settings/spawn-confirm", c.setSpawnConfirm)
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

func (c *SettingsController) getSpawnConfirm(w http.ResponseWriter, r *http.Request) {
	if c.SpawnConfirm == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/spawn-confirm")
		return
	}
	s := c.SpawnConfirm.Get()
	envelope.WriteJSON(w, http.StatusOK, SpawnConfirmSettingsResponse{Enabled: s.Enabled})
}

func (c *SettingsController) setSpawnConfirm(w http.ResponseWriter, r *http.Request) {
	if c.SpawnConfirm == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/spawn-confirm")
		return
	}
	var in SetSpawnConfirmSettingsRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	next := spawnconfirm.Settings{Enabled: in.Enabled}
	if err := c.SpawnConfirm.Set(next); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SpawnConfirmSettingsResponse{Enabled: next.Enabled})
}
