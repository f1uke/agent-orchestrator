package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// SimFleet is the clones AO makes for sessions. *simsvc.Fleet satisfies it.
type SimFleet interface {
	Clones(ctx context.Context) ([]domain.SimClone, error)
	Bases(ctx context.Context) ([]simsvc.BaseStatus, error)
	Claim(ctx context.Context, sessionID domain.SessionID, label, model string) (domain.SimClone, error)
	Remove(ctx context.Context, sessionID domain.SessionID, label string) (domain.SimClone, error)
}

// SimCloneView is one device AO cloned for a session.
type SimCloneView struct {
	UDID      string    `json:"udid"`
	SessionID string    `json:"sessionId"`
	Label     string    `json:"label" description:"The device's name within its session; primary is the one exported as AO_SIM_UDID."`
	Primary   bool      `json:"primary"`
	Base      string    `json:"base" description:"Name of the base simulator it was cloned from."`
	Name      string    `json:"name" description:"The clone's own simctl name."`
	CreatedAt time.Time `json:"createdAt"`
}

// SimBaseView is one base simulator and whether this machine can clone it.
type SimBaseView struct {
	Name       string `json:"name"`
	Key        string `json:"key" description:"The label a clone of this base gets when none is named."`
	DeviceType string `json:"deviceType"`
	UDID       string `json:"udid,omitempty" description:"Empty when the base is missing."`
	Problem    string `json:"problem,omitempty" description:"Why it cannot be cloned right now, with what to do about it."`
}

// ListSimClonesResponse is the body of GET /sim/clones.
type ListSimClonesResponse struct {
	Clones []SimCloneView `json:"clones"`
	Bases  []SimBaseView  `json:"bases"`
}

// ClaimSimCloneInput is the body of POST /sessions/{sessionId}/sim-clones.
type ClaimSimCloneInput struct {
	Label string `json:"label,omitempty" description:"Which of the session's devices; empty is the primary, or the model's own label when model is set."`
	Model string `json:"model,omitempty" description:"Base model to clone for a new device, matched by unique prefix (e.g. iPhone SE). Empty is the default."`
}

// SimCloneResponse wraps one clone.
type SimCloneResponse struct {
	Clone SimCloneView `json:"clone"`
}

// SimCloneParam is the {sessionId}/{label} path of one session device.
type SimCloneParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
	Label     string `path:"label" description:"The device's label within the session."`
}

// SimClonesController owns the routes that make and remove a session's
// devices. A nil Fleet answers 501.
type SimClonesController struct {
	Fleet SimFleet
}

// Register mounts the clone routes.
func (c *SimClonesController) Register(r chi.Router) {
	r.Get("/sim/clones", c.list)
	r.Post("/sessions/{sessionId}/sim-clones", c.claim)
	r.Delete("/sessions/{sessionId}/sim-clones/{label}", c.remove)
}

func (c *SimClonesController) list(w http.ResponseWriter, r *http.Request) {
	if c.Fleet == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sim/clones")
		return
	}
	clones, err := c.Fleet.Clones(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_CLONES_FAILED", err.Error(), nil)
		return
	}
	// The bases are read off the machine, which may have no simulators at
	// all; the clones are still worth answering with.
	bases, _ := c.Fleet.Bases(r.Context())
	out := ListSimClonesResponse{Clones: make([]SimCloneView, 0, len(clones)), Bases: make([]SimBaseView, 0, len(bases))}
	for _, clone := range clones {
		out.Clones = append(out.Clones, simCloneView(clone))
	}
	for _, b := range bases {
		out.Bases = append(out.Bases, SimBaseView{Name: b.Base.Name, Key: b.Base.Key, DeviceType: b.Base.DeviceType, UDID: b.UDID, Problem: b.Problem})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *SimClonesController) claim(w http.ResponseWriter, r *http.Request) {
	if c.Fleet == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-clones")
		return
	}
	var in ClaimSimCloneInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	clone, err := c.Fleet.Claim(r.Context(), sessionID(r), in.Label, in.Model)
	if err != nil {
		writeSimCloneError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimCloneResponse{Clone: simCloneView(clone)})
}

func (c *SimClonesController) remove(w http.ResponseWriter, r *http.Request) {
	if c.Fleet == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/sim-clones/{label}")
		return
	}
	clone, err := c.Fleet.Remove(r.Context(), sessionID(r), chi.URLParam(r, "label"))
	if err != nil {
		writeSimCloneError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimCloneResponse{Clone: simCloneView(clone)})
}

// writeSimCloneError says what is wrong with the machine in the words a person
// needs to fix it: a missing or booted base is not an internal error.
func writeSimCloneError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		missing *simsvc.BaseMissingError
		booted  *simsvc.BaseBootedError
	)
	switch {
	case errors.As(err, &missing):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_BASE_MISSING", err.Error(),
			map[string]any{"base": missing.Base.Name})
	case errors.As(err, &booted):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_BASE_BOOTED", err.Error(),
			map[string]any{"base": booted.Base.Name, "udid": booted.UDID})
	case errors.Is(err, simctl.ErrUnavailable):
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "SIM_UNAVAILABLE", "this machine cannot make simulators: "+err.Error(), nil)
	case errors.Is(err, simsvc.ErrInvalid), errors.Is(err, simsvc.ErrNotFound), isHeld(err):
		writeSimError(w, r, err)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_CLONE_FAILED", err.Error(), nil)
	}
}

func isHeld(err error) bool {
	var held *simsvc.HeldError
	return errors.As(err, &held)
}

func simCloneView(c domain.SimClone) SimCloneView {
	return SimCloneView{
		UDID: c.UDID, SessionID: string(c.SessionID), Label: c.Label, Primary: c.Primary(),
		Base: c.Base, Name: c.Name, CreatedAt: c.CreatedAt.UTC(),
	}
}
