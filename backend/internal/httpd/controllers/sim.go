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
	// TakeOver claims a device another session already holds. A gesture in
	// flight is still left alone, and the request is refused while one is - the
	// lease says who may drive, and a human may decide that is now them, but a
	// touch that is happening is not interruptible.
	TakeOver bool `json:"takeOver,omitempty" description:"Claim the device even if another session holds it. Refused while a gesture is in flight."`
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

// AcquireSimHoldInput is the body of POST .../sim-leases/{udid}/hold.
type AcquireSimHoldInput struct {
	HoldSeconds int `json:"holdSeconds,omitempty" description:"How long the gesture may hold the device, in seconds (1-60). Omit for the 30 second default. This is not a working window: it only bounds how long a command killed mid-gesture keeps the device."`
}

// SimHoldResponse is the { hold } body returned by acquiring a gesture hold.
type SimHoldResponse struct {
	Hold domain.SimHold `json:"hold"`
}

// ReleaseSimHoldResponse is the body returned by releasing a gesture hold.
type ReleaseSimHoldResponse struct {
	Released bool `json:"released" description:"True when the caller's gesture hold was dropped."`
}

// SimHoldParam is the {sessionId}/{udid}/{token} path parameters for releasing
// a gesture hold.
type SimHoldParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
	UDID      string `path:"udid" description:"Simulator udid (matched case-insensitively)."`
	Token     string `path:"token" description:"The token returned when the hold was taken."`
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
	// The gesture hold hangs off the lease it depends on: it is a second, much
	// shorter claim on the same device, and it cannot exist without the lease.
	r.Post("/sessions/{sessionId}/sim-leases/{udid}/hold", c.acquireHold)
	r.Delete("/sessions/{sessionId}/sim-leases/{udid}/hold/{token}", c.releaseHold)
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
	//
	// Taking over is a separate call rather than a flag on the same one, because
	// they refuse for different reasons: an ordinary claim is refused because
	// somebody holds the device, a takeover only because a touch is in flight.
	claim := c.Svc.Acquire
	if in.TakeOver {
		claim = c.Svc.TakeOver
	}
	lease, err := claim(r.Context(), sessionID(r), in.UDID, time.Duration(in.TTLSeconds)*time.Second)
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

func (c *SimController) acquireHold(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-leases/{udid}/hold")
		return
	}
	var in AcquireSimHoldInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	hold, err := c.Svc.AcquireHold(r.Context(), sessionID(r), chi.URLParam(r, "udid"), time.Duration(in.HoldSeconds)*time.Second)
	if err != nil {
		writeSimError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimHoldResponse{Hold: hold})
}

func (c *SimController) releaseHold(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/sim-leases/{udid}/hold/{token}")
		return
	}
	if err := c.Svc.ReleaseHold(r.Context(), chi.URLParam(r, "udid"), chi.URLParam(r, "token")); err != nil {
		writeSimError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseSimHoldResponse{Released: true})
}

// writeSimError maps the service sentinels. Contention gets its own 409 with
// the holder in details: a caller must be able to tell "someone else has this
// device" apart from a transient failure, and must never be able to read the
// refusal as permission to proceed.
func writeSimError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		held    *simsvc.HeldError
		refused *simsvc.HoldRefusedError
	)
	switch {
	case errors.As(err, &refused):
		// One code with a reason, not three codes: the caller always has to act
		// on "you may not touch this device right now", and the reason says
		// which of claim-it / wait-for-them / wait-for-the-gesture applies.
		details := map[string]any{"udid": refused.UDID, "reason": string(refused.Reason)}
		if refused.Lease.SessionID != "" {
			details["holder"] = string(refused.Lease.SessionID)
			details["expiresAt"] = refused.Lease.ExpiresAt.UTC().Format(time.RFC3339)
		}
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_DEVICE_BUSY", err.Error(), details)
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
