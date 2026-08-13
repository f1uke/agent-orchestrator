package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
)

// AcquireSimLeaseInput is the body of POST .../sim-leases.
type AcquireSimLeaseInput struct {
	UDID       string `json:"udid" description:"Simulator udid to claim (case-insensitive)."`
	TTLSeconds int    `json:"ttlSeconds,omitempty" description:"How long to hold it, in seconds. Omit for the 10 minute default; the caller may hold for as little as a second (one gesture) and at most an hour."`
}

// SimLeaseResponse is the { lease } body returned by acquire.
type SimLeaseResponse struct {
	Lease domain.SimLease `json:"lease"`
}

// ListSimLeasesResponse is the body of GET /sim/leases: every lease still live
// on this machine. Devices with no lease are absent - AO knows its own claims
// and nothing else, so it cannot report a device as free.
type ListSimLeasesResponse struct {
	Leases []domain.SimLease `json:"leases"`
}

// ReleaseSimLeaseResponse is the body returned by release.
type ReleaseSimLeaseResponse struct {
	Released bool `json:"released" description:"True when the caller's lease was dropped."`
}

// SimController owns the simulator device-lease routes. A nil Svc returns 501,
// mirroring the other optional-service controllers.
type SimController struct {
	Svc simsvc.Manager
}

// Register mounts the sim-lease routes on the supplied router. Listing is
// machine-wide (leases are about one machine's devices, not one project);
// acquiring and releasing hang off the session that owns the lease.
func (c *SimController) Register(r chi.Router) {
	r.Get("/sim/leases", c.list)
	r.Post("/sessions/{sessionId}/sim-leases", c.acquire)
	r.Delete("/sessions/{sessionId}/sim-leases/{udid}", c.release)
}

func (c *SimController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sim/leases")
		return
	}
	leases, err := c.Svc.List(r.Context())
	if err != nil {
		writeSimError(w, r, err)
		return
	}
	if leases == nil {
		leases = []domain.SimLease{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListSimLeasesResponse{Leases: leases})
}

func (c *SimController) acquire(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-leases")
		return
	}
	var in AcquireSimLeaseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	// A zero TTL is passed through untouched so the default lives in exactly one
	// place (the service), not once per caller.
	lease, err := c.Svc.Acquire(r.Context(), sessionID(r), in.UDID, time.Duration(in.TTLSeconds)*time.Second)
	if err != nil {
		writeSimError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimLeaseResponse{Lease: lease})
}

func (c *SimController) release(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/sim-leases/{udid}")
		return
	}
	if err := c.Svc.Release(r.Context(), sessionID(r), chi.URLParam(r, "udid")); err != nil {
		writeSimError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseSimLeaseResponse{Released: true})
}

// writeSimError maps the service sentinels. Contention gets its own 409 with
// the holder in details: a caller must be able to tell "someone else has this
// device" apart from a transient failure, and must never be able to read the
// refusal as permission to proceed.
func writeSimError(w http.ResponseWriter, r *http.Request, err error) {
	var held *simsvc.HeldError
	switch {
	case errors.As(err, &held):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_DEVICE_LEASED", err.Error(), map[string]any{
			"udid":      held.Lease.UDID,
			"holder":    string(held.Lease.SessionID),
			"expiresAt": held.Lease.ExpiresAt.UTC().Format(time.RFC3339),
		})
	case errors.Is(err, simsvc.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID", err.Error(), nil)
	case errors.Is(err, simsvc.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SIM_NOT_FOUND", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_OPERATION_FAILED", "Simulator lease operation failed", nil)
	}
}
