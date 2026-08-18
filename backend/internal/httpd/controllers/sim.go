package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
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
	// Intent describes the gesture this hold is about to bracket, mirroring
	// SimGestureInput's own coordinate/text/name fields one-for-one: it is what
	// a session recording gestures on this device needs in order to capture
	// the step, and is optional so a caller with nothing to record (or one
	// that predates this field) still gets the hold.
	Kind       string  `json:"kind,omitempty" description:"tap, swipe, type, button, drag-begin, drag-move or drag-end - the gesture this hold covers."`
	X          float64 `json:"x,omitempty" description:"Normalized 0..1 screen coordinates, for tap/swipe/drag."`
	Y          float64 `json:"y,omitempty"`
	ToX        float64 `json:"toX,omitempty" description:"Where a swipe or drag ends."`
	ToY        float64 `json:"toY,omitempty"`
	DurationMS int     `json:"durationMs,omitempty" description:"Swipe duration in milliseconds."`
	Text       string  `json:"text,omitempty" description:"The text a type gesture sends."`
	Name       string  `json:"name,omitempty" description:"button: home or app-switcher."`
	// Label and ID are the one pair with no SimGestureInput counterpart: a tap
	// that named its target (`ao sim tap --label`/`--id`) rather than pointing
	// at one. When either is set, a recording open on this device resolves the
	// step by that name against the screen it already reads, instead of
	// hit-testing X/Y - the name is what the caller actually asked for, and
	// hit-testing a coordinate to rediscover it would recover a worse answer
	// to a question already answered.
	Label string `json:"label,omitempty" description:"tap: the element label this hold's gesture is about to tap, when acquired for a by-name tap."`
	ID    string `json:"id,omitempty" description:"tap: the element's accessibility identifier, the other way a by-name tap may target it."`
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

// ReleaseSimHoldQuery is the query string of DELETE .../hold/{token}. It is a
// query parameter rather than a body because every DELETE route in this
// package takes its arguments that way; see releaseHold for why an absent or
// unparseable value falls back to true.
type ReleaseSimHoldQuery struct {
	Performed *bool `query:"performed,omitempty" description:"Whether the gesture this hold covered actually happened. Defaults to true when omitted. A session recording gestures on this device keeps the step only when it did: a gesture that was attempted and failed must not be written into the flow as if it had happened."`
}

// StartSimRecordingInput is the body of POST .../sim-recordings/{udid}.
type StartSimRecordingInput struct {
	Name string `json:"name,omitempty" description:"Optional label for the recording, e.g. the flow it will become."`
}

// SimRecordingResponse is the { recording } body returned by starting a
// recording.
type SimRecordingResponse struct {
	Recording domain.SimRecording `json:"recording"`
}

// SimRecordingWithStepsResponse is the { recording, steps } body returned by
// reading or stopping a recording.
type SimRecordingWithStepsResponse struct {
	Recording domain.SimRecording `json:"recording"`
	// StepCount is how many steps the recording has captured. It is stated
	// separately from Steps because the surface that watches a recording most
	// closely - the Device tab's live counter - needs only this, and asking
	// for the whole step array once a second to count it would drag every
	// gesture's selector back over the wire to be thrown away. It always
	// equals len(Steps) when the steps are included.
	StepCount int                       `json:"stepCount"`
	Steps     []domain.SimRecordingStep `json:"steps"`
	// Flow is the file this recording became, present only on the response
	// that stopped it.
	Flow *SimFlowView `json:"flow,omitempty"`
}

// ReadSimRecordingQuery is the query string of GET .../sim-recordings/{udid}.
type ReadSimRecordingQuery struct {
	Steps string `query:"steps,omitempty" description:"all (default) returns every captured step; none returns stepCount alone, for a caller that only needs to know how many there are."`
}

// StopSimRecordingQuery is the query string of DELETE .../sim-recordings/{udid}:
// what to do with the flow the daemon writes as the recording closes.
type StopSimRecordingQuery struct {
	Out   string `query:"out,omitempty" description:"Absolute path to write the flow to, instead of this session's own flows directory."`
	Entry string `query:"entry,omitempty" description:"Path to a shared entry-point flow, emitted as a runFlow step before the recorded steps."`
}

// simRecordingService is the recording surface the recording routes depend
// on. It is a separate interface from Manager - rather than three more
// methods added there - because starting, reading and stopping a recording is
// not bracketing one gesture the way AcquireHold/ReleaseHold is, and every
// other Manager fake in this package would otherwise have to grow methods it
// never exercises. *simsvc.Service already satisfies it (see
// internal/service/sim/recording.go); c.Svc is asserted against it per
// request, the same way a nil Svc is checked per request.
type simRecordingService interface {
	StartRecording(ctx context.Context, sessionID domain.SessionID, udid, name string) (domain.SimRecording, error)
	StopRecording(ctx context.Context, sessionID domain.SessionID, udid string) (domain.SimRecording, []domain.SimRecordingStep, error)
	GetRecording(ctx context.Context, udid string) (domain.SimRecording, []domain.SimRecordingStep, bool, error)
}

// SimController owns the simulator device-lease and gesture-recording routes.
// A nil Svc returns 501, mirroring the other optional-service controllers; so
// does a Svc that does not implement simRecordingService (only ever true in a
// test double, since the concrete service implements both).
//
// DataDir and Screen are what stopping a recording needs in order to hand back
// a file rather than an array of steps. Building the flow moved here from the
// CLI deliberately: the Device tab has no Go in it and could not run the
// emitter, and a second emitter in the renderer would be a second answer to
// "how many of these steps need review" - the one number this whole surface
// exists to report. One emitter, one writer, every caller reading the same
// result.
type SimController struct {
	Svc simsvc.Manager
	// DataDir is where a session's own flows directory lives. Empty means
	// stopping still stops - the steps are still returned - but no file is
	// written, because inventing a location for somebody's recording is worse
	// than handing back what was captured and saying nothing about a path.
	DataDir string
	// Screen names the device for the flow's provenance header. Nil, or a
	// machine that cannot list simulators, leaves the udid in its place rather
	// than failing a stop over a comment.
	Screen SimScreenProvider
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
	// A recording hangs off the session the same way: it captures that
	// session's gestures on the device, and (per the service) requires a live
	// lease to start.
	r.Post("/sessions/{sessionId}/sim-recordings/{udid}", c.startRecording)
	r.Get("/sessions/{sessionId}/sim-recordings/{udid}", c.getRecording)
	r.Delete("/sessions/{sessionId}/sim-recordings/{udid}", c.stopRecording)
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
	intent := simsvc.GestureIntent{
		Kind: in.Kind, X: in.X, Y: in.Y, ToX: in.ToX, ToY: in.ToY,
		DurationMS: in.DurationMS, Text: in.Text, Name: in.Name,
		Label: in.Label, ID: in.ID,
	}
	hold, err := c.Svc.AcquireHold(r.Context(), sessionID(r), chi.URLParam(r, "udid"), time.Duration(in.HoldSeconds)*time.Second, intent)
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
	// performed is a query parameter, not a body: every DELETE route in this
	// package takes its arguments from the path or query, and a body here would
	// be the first exception. It defaults to true when absent, so a client that
	// has not been updated to send it keeps recording the gestures it actually
	// performs. An unparseable value also falls back to that same default,
	// deliberately: a gesture that already happened must not be refused over a
	// malformed query string.
	performed := true
	if v := r.URL.Query().Get("performed"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			performed = parsed
		}
	}
	// No End: this route releases a hold taken for a gesture that was composed
	// in full before it started, so its end already travelled with the intent.
	// A drag - the one gesture whose end is not knowable up front - never
	// reaches here: it is opened, followed and closed inside the daemon by
	// internal/simgesture.Drags, which carries the real end point back with its
	// own release.
	outcome := simsvc.GestureOutcome{Performed: performed}
	if err := c.Svc.ReleaseHold(r.Context(), chi.URLParam(r, "udid"), chi.URLParam(r, "token"), outcome); err != nil {
		writeSimError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReleaseSimHoldResponse{Released: true})
}

// simRecorder asserts c.Svc against the recording surface, answering 501 the
// same way a nil Svc does when it is absent or does not implement it.
func (c *SimController) simRecorder(w http.ResponseWriter, r *http.Request, method, path string) (simRecordingService, bool) {
	rec, ok := c.Svc.(simRecordingService)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, method, path)
		return nil, false
	}
	return rec, true
}

func (c *SimController) startRecording(w http.ResponseWriter, r *http.Request) {
	rec, ok := c.simRecorder(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-recordings/{udid}")
	if !ok {
		return
	}
	var in StartSimRecordingInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	recording, err := rec.StartRecording(r.Context(), sessionID(r), chi.URLParam(r, "udid"), in.Name)
	if err != nil {
		writeSimRecordingError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimRecordingResponse{Recording: recording})
}

func (c *SimController) getRecording(w http.ResponseWriter, r *http.Request) {
	rec, ok := c.simRecorder(w, r, "GET", "/api/v1/sessions/{sessionId}/sim-recordings/{udid}")
	if !ok {
		return
	}
	recording, steps, found, err := rec.GetRecording(r.Context(), chi.URLParam(r, "udid"))
	if err != nil {
		writeSimRecordingError(w, r, err)
		return
	}
	if !found {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SIM_NOT_FOUND",
			"no recording has ever been started on this simulator", nil)
		return
	}
	if steps == nil {
		steps = []domain.SimRecordingStep{}
	}
	body := SimRecordingWithStepsResponse{Recording: recording, StepCount: len(steps), Steps: steps}
	// `steps=none` is for the caller watching a recording rather than reading
	// it: the Device tab's counter asks once a second while a human drags, and
	// every one of those answers would otherwise carry every selector captured
	// so far only to be counted and dropped.
	if r.URL.Query().Get("steps") == "none" {
		body.Steps = []domain.SimRecordingStep{}
	}
	envelope.WriteJSON(w, http.StatusOK, body)
}

func (c *SimController) stopRecording(w http.ResponseWriter, r *http.Request) {
	rec, ok := c.simRecorder(w, r, "DELETE", "/api/v1/sessions/{sessionId}/sim-recordings/{udid}")
	if !ok {
		return
	}
	udid := chi.URLParam(r, "udid")
	recording, steps, err := rec.StopRecording(r.Context(), sessionID(r), udid)
	if err != nil {
		writeSimRecordingError(w, r, err)
		return
	}
	if steps == nil {
		steps = []domain.SimRecordingStep{}
	}

	body := SimRecordingWithStepsResponse{Recording: recording, StepCount: len(steps), Steps: steps}
	flow, err := c.writeFlow(r, recording, steps)
	if err != nil {
		// The recording has already stopped on the daemon side. That side
		// effect happened and cannot be undone here, so the failure has to say
		// so rather than read as "nothing happened" and invite a retry that
		// would find no open recording.
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_FLOW_WRITE_FAILED",
			"the recording stopped, but its flow could not be written: "+err.Error(),
			map[string]any{"udid": recording.UDID, "stepCount": len(steps)})
		return
	}
	body.Flow = flow
	envelope.WriteJSON(w, http.StatusOK, body)
}

// writeFlow turns the recording that just stopped into a file, and reports
// where it landed.
//
// A nil flow with a nil error is the honest answer for a daemon with no data
// directory: the steps are in the response, and nothing pretends a file was
// written.
func (c *SimController) writeFlow(r *http.Request, recording domain.SimRecording, steps []domain.SimRecordingStep) (*SimFlowView, error) {
	if c.DataDir == "" {
		return nil, nil
	}
	recordedAt := recording.UpdatedAt
	if recording.StoppedAt != nil {
		recordedAt = *recording.StoppedAt
	}
	device, runtime := c.describeDevice(r, recording.UDID)
	body, err := simflow.Emit(simrecord.Steps(steps), simflow.EmitOptions{
		Device:     device,
		Runtime:    runtime,
		RecordedAt: recordedAt.UTC().Format(time.RFC3339),
		Entry:      strings.TrimSpace(r.URL.Query().Get("entry")),
	})
	if err != nil {
		return nil, err
	}

	// `out` is how `ao sim record stop --out` still writes wherever it was
	// told to. It is resolved by the CLI before it gets here, so this side
	// never has to guess which working directory a relative path meant.
	if out := strings.TrimSpace(r.URL.Query().Get("out")); out != "" {
		written, err := simrecord.WriteTo(out, body)
		if err != nil {
			return nil, err
		}
		view := simFlowView(written)
		return &view, nil
	}

	written, err := simrecord.Write(c.DataDir, string(sessionID(r)), recording.Name, recordedAt, body)
	if err != nil {
		return nil, err
	}
	view := simFlowView(written)
	return &view, nil
}

// describeDevice names the simulator for the flow's provenance comment. A
// machine that cannot answer leaves the udid in place: a header comment is
// never worth failing a stop over, and the udid still identifies the device.
func (c *SimController) describeDevice(r *http.Request, udid string) (name, runtime string) {
	if c.Screen == nil {
		return udid, ""
	}
	listing, err := c.Screen.Devices(r.Context())
	if err != nil {
		return udid, ""
	}
	for _, d := range listing.Devices {
		if strings.EqualFold(d.UDID, udid) {
			return d.Name, d.Runtime
		}
	}
	return udid, ""
}

// writeSimRecordingError maps recording refusals the same way writeSimError
// maps hold/lease refusals: one code with a reason a caller can act on in
// details, rather than a bare message it would have to pattern-match.
func writeSimRecordingError(w http.ResponseWriter, r *http.Request, err error) {
	var refused *simsvc.RecordingRefusedError
	switch {
	case errors.As(err, &refused):
		details := map[string]any{"udid": refused.UDID, "reason": string(refused.Reason)}
		if refused.Lease.SessionID != "" {
			details["holder"] = string(refused.Lease.SessionID)
			details["expiresAt"] = refused.Lease.ExpiresAt.UTC().Format(time.RFC3339)
		}
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_RECORDING_REFUSED", err.Error(), details)
	case errors.Is(err, simsvc.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SIM_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, simsvc.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_OPERATION_FAILED", "Simulator recording operation failed", nil)
	}
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
