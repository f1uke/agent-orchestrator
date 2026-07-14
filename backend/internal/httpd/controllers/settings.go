package controllers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/autonudge"
	"github.com/aoagents/agent-orchestrator/backend/internal/evidenceretention"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptoverrides"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
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

// AutoNudgeService is the auto-nudge settings store surface the controller
// needs. *autonudge.Store satisfies this directly.
type AutoNudgeService interface {
	Get() autonudge.Settings
	Set(autonudge.Settings) error
}

// EvidenceRetentionService is the evidence-retention settings store surface the
// controller needs. *evidenceretention.Store satisfies this directly.
type EvidenceRetentionService interface {
	Get() evidenceretention.Settings
	Set(evidenceretention.Settings) error
}

// EvidenceSweeper runs the age-based evidence retention sweep on demand (the
// manual trigger), reading the current TTL and purging expired blobs + rows. It
// returns how many items were removed and how many bytes that freed. The daemon
// provides a concrete implementation that shares the exact sweep the periodic
// background job runs.
type EvidenceSweeper interface {
	SweepEvidenceNow(ctx context.Context) (purged int, freedBytes int64, err error)
}

// SystemPromptsService is the prompt-override store surface the controller needs.
// *promptoverrides.Store satisfies this directly.
type SystemPromptsService interface {
	Get() promptoverrides.Overrides
	SetBase(prompts.Kind, string) error
	ClearBase(prompts.Kind) error
}

// MessageTemplatesService is the template-override store surface the controller
// needs. *promptoverrides.Store satisfies this directly.
type MessageTemplatesService interface {
	Get() promptoverrides.Overrides
	SetTemplate(name, text string) error
	ClearTemplate(name string) error
}

// SettingsController serves the global auto-reclaim settings. Nil keeps the
// routes registered but returns OpenAPI-backed 501s, matching every other
// controller in this package.
type SettingsController struct {
	Svc               SettingsService
	SpawnConfirm      SpawnConfirmService
	AutoNudge         AutoNudgeService
	EvidenceRetention EvidenceRetentionService
	EvidenceSweeper   EvidenceSweeper
	SystemPrompts     SystemPromptsService
	MessageTemplates  MessageTemplatesService
}

// Register mounts the settings routes on the supplied router.
func (c *SettingsController) Register(r chi.Router) {
	r.Get("/settings/reclaim", c.get)
	r.Put("/settings/reclaim", c.set)
	r.Get("/settings/spawn-confirm", c.getSpawnConfirm)
	r.Put("/settings/spawn-confirm", c.setSpawnConfirm)
	r.Get("/settings/auto-nudge", c.getAutoNudge)
	r.Put("/settings/auto-nudge", c.setAutoNudge)
	r.Get("/settings/evidence-retention", c.getEvidenceRetention)
	r.Put("/settings/evidence-retention", c.setEvidenceRetention)
	r.Post("/settings/evidence-retention/sweep", c.sweepEvidenceRetention)
	r.Get("/settings/prompts", c.getPrompts)
	r.Put("/settings/prompts/{kind}", c.setPrompt)
	r.Delete("/settings/prompts/{kind}", c.clearPrompt)
	r.Get("/settings/message-templates", c.getMessageTemplates)
	r.Put("/settings/message-templates/{name}", c.setMessageTemplate)
	r.Delete("/settings/message-templates/{name}", c.clearMessageTemplate)
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

func (c *SettingsController) getAutoNudge(w http.ResponseWriter, r *http.Request) {
	if c.AutoNudge == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/auto-nudge")
		return
	}
	s := c.AutoNudge.Get()
	envelope.WriteJSON(w, http.StatusOK, AutoNudgeSettingsResponse{Enabled: s.Enabled})
}

func (c *SettingsController) setAutoNudge(w http.ResponseWriter, r *http.Request) {
	if c.AutoNudge == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/auto-nudge")
		return
	}
	var in SetAutoNudgeSettingsRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	next := autonudge.Settings{Enabled: in.Enabled}
	if err := c.AutoNudge.Set(next); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, AutoNudgeSettingsResponse{Enabled: next.Enabled})
}

func (c *SettingsController) getEvidenceRetention(w http.ResponseWriter, r *http.Request) {
	if c.EvidenceRetention == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/evidence-retention")
		return
	}
	s := c.EvidenceRetention.Get()
	envelope.WriteJSON(w, http.StatusOK, EvidenceRetentionSettingsResponse{Enabled: s.Enabled, MaxAgeDays: s.MaxAgeDays})
}

func (c *SettingsController) setEvidenceRetention(w http.ResponseWriter, r *http.Request) {
	if c.EvidenceRetention == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/evidence-retention")
		return
	}
	var in SetEvidenceRetentionSettingsRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	next := evidenceretention.Settings{Enabled: in.Enabled, MaxAgeDays: in.MaxAgeDays}
	if err := c.EvidenceRetention.Set(next); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, EvidenceRetentionSettingsResponse{Enabled: next.Enabled, MaxAgeDays: next.MaxAgeDays})
}

func (c *SettingsController) sweepEvidenceRetention(w http.ResponseWriter, r *http.Request) {
	if c.EvidenceSweeper == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/settings/evidence-retention/sweep")
		return
	}
	purged, freed, err := c.EvidenceSweeper.SweepEvidenceNow(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "EVIDENCE_SWEEP_FAILED", "Evidence retention sweep failed", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, EvidenceRetentionSweepResponse{Purged: purged, FreedBytes: freed})
}

func (c *SettingsController) getPrompts(w http.ResponseWriter, r *http.Request) {
	if c.SystemPrompts == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/prompts")
		return
	}
	ov := c.SystemPrompts.Get()
	items := make([]SystemPromptItem, 0, len(prompts.KnownKinds()))
	for _, k := range prompts.KnownKinds() {
		item := SystemPromptItem{Kind: string(k), Default: prompts.DefaultBase(k)}
		if v, ok := ov.Base[k]; ok {
			v := v
			item.Override = &v
		}
		items = append(items, item)
	}
	envelope.WriteJSON(w, http.StatusOK, SystemPromptsResponse{Prompts: items})
}

func (c *SettingsController) setPrompt(w http.ResponseWriter, r *http.Request) {
	if c.SystemPrompts == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/prompts/{kind}")
		return
	}
	kind := prompts.Kind(chi.URLParam(r, "kind"))
	if !kind.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", fmt.Sprintf("unknown prompt kind %q", kind), nil)
		return
	}
	var in SetSystemPromptRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.SystemPrompts.SetBase(kind, in.Base); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	c.getPrompts(w, r)
}

func (c *SettingsController) clearPrompt(w http.ResponseWriter, r *http.Request) {
	if c.SystemPrompts == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/settings/prompts/{kind}")
		return
	}
	kind := prompts.Kind(chi.URLParam(r, "kind"))
	if !kind.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", fmt.Sprintf("unknown prompt kind %q", kind), nil)
		return
	}
	if err := c.SystemPrompts.ClearBase(kind); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	c.getPrompts(w, r)
}

func (c *SettingsController) getMessageTemplates(w http.ResponseWriter, r *http.Request) {
	if c.MessageTemplates == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/message-templates")
		return
	}
	ov := c.MessageTemplates.Get()
	items := make([]MessageTemplateItem, 0, len(messagetemplates.KnownNames()))
	for _, n := range messagetemplates.KnownNames() {
		// Placeholders returns nil for templates with no documented tokens
		// (e.g. merge-conflict). A nil Go slice marshals to JSON null, which
		// violates the OpenAPI schema's required non-nullable array and
		// crashes frontend code that calls .length/.join on it. Coerce to an
		// empty slice so the wire always honors the schema.
		ph := messagetemplates.Placeholders(n)
		if ph == nil {
			ph = []string{}
		}
		item := MessageTemplateItem{
			Name:         string(n),
			Default:      messagetemplates.Default(n),
			Placeholders: ph,
		}
		if v, ok := ov.Templates[string(n)]; ok {
			v := v
			item.Override = &v
		}
		items = append(items, item)
	}
	envelope.WriteJSON(w, http.StatusOK, MessageTemplatesResponse{Templates: items})
}

func (c *SettingsController) setMessageTemplate(w http.ResponseWriter, r *http.Request) {
	if c.MessageTemplates == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/message-templates/{name}")
		return
	}
	name := messagetemplates.Name(chi.URLParam(r, "name"))
	if !name.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", fmt.Sprintf("unknown template name %q", name), nil)
		return
	}
	var in SetMessageTemplateRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.MessageTemplates.SetTemplate(string(name), in.Template); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	c.getMessageTemplates(w, r)
}

func (c *SettingsController) clearMessageTemplate(w http.ResponseWriter, r *http.Request) {
	if c.MessageTemplates == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/settings/message-templates/{name}")
		return
	}
	name := messagetemplates.Name(chi.URLParam(r, "name"))
	if !name.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", fmt.Sprintf("unknown template name %q", name), nil)
		return
	}
	if err := c.MessageTemplates.ClearTemplate(string(name)); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	c.getMessageTemplates(w, r)
}
