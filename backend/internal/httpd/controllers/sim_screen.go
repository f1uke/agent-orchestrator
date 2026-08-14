package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
)

// The routes behind the desktop app's Simulator tab: which simulators this
// machine has, and - for a human watching one - the ability to touch it.
//
// The touching half is the reason these live in the daemon rather than in the
// renderer. A click that reached a device by any route other than the lease and
// the gesture hold would reintroduce exactly the interleaving the last two
// slices exist to prevent, so the click goes through internal/simgesture: the
// same sequence `ao sim tap` runs, with the same refusals.

// SimScreenProvider is the machine-local half of the simulator surface: what
// devices exist, their live screens, and the driver that touches them. It is
// nil on a machine that cannot do any of it (not a mac, no Node), and every
// route answers 501 rather than pretending there is nothing to see.
type SimScreenProvider interface {
	Devices(ctx context.Context) (simctl.Listing, error)
	Subscribe(ctx context.Context, udid string) (<-chan simstream.Event, error)
	Driver(ctx context.Context) (simbridge.Driver, error)
}

// SimDeviceLeaseView is what AO knows about who is driving one device. The
// state is only ever "held" or "unknown"; there is no "free", because AO cannot
// see a human driving the same simulator from Xcode.
type SimDeviceLeaseView struct {
	State      domain.SimLeaseState `json:"state" description:"held when an AO session holds a live lease; unknown otherwise. Never free - AO cannot see a human driving the device from Xcode."`
	Holder     string               `json:"holder,omitempty" description:"Session that holds the lease, when the state is held."`
	AcquiredAt *time.Time           `json:"acquiredAt,omitempty"`
	ExpiresAt  *time.Time           `json:"expiresAt,omitempty"`
	Reason     string               `json:"reason,omitempty" description:"Why the state is unknown."`
}

// SimDeviceView is one simulator plus its lease state.
type SimDeviceView struct {
	UDID              string             `json:"udid"`
	Name              string             `json:"name"`
	Runtime           string             `json:"runtime" description:"Human-readable runtime, e.g. iOS 26.3."`
	RuntimeIdentifier string             `json:"runtimeIdentifier"`
	State             string             `json:"state" description:"simctl's own state, e.g. Booted or Shutdown."`
	Available         bool               `json:"available"`
	Default           bool               `json:"default" description:"True for the one device an unqualified request resolves to. Never set when several are booted."`
	Lease             SimDeviceLeaseView `json:"lease"`
}

// ListSimDevicesResponse is the body of GET /sim/devices.
type ListSimDevicesResponse struct {
	Devices []SimDeviceView `json:"devices"`
	// DefaultUDID is the device an unqualified request resolves to, and is null
	// whenever there is no unambiguous answer - with several booted, a picker
	// must ask rather than choose.
	DefaultUDID   *string `json:"defaultUdid"`
	DefaultReason string  `json:"defaultReason" description:"Why that device is the default, or why there is none."`
}

// SimGestureInput is the body of POST .../sim-devices/{udid}/gesture. One route
// carries every gesture on purpose: the arbitration around them is identical,
// and five routes would be five places to forget the hold.
type SimGestureInput struct {
	Kind string `json:"kind" description:"tap, swipe, type, button, drag-begin, drag-move or drag-end."`
	// tap and swipe: normalized 0..1 screen coordinates, the same numbers
	// `ao sim ax` reports per element.
	X float64 `json:"x,omitempty"`
	Y float64 `json:"y,omitempty"`
	// swipe: where the drag ends, and how long it takes.
	ToX        float64 `json:"toX,omitempty"`
	ToY        float64 `json:"toY,omitempty"`
	DurationMS int     `json:"durationMs,omitempty" description:"Swipe duration in milliseconds. Omit for 300."`
	// type: the text to send. What the keys produce is decided by the guest's
	// own active input source, so read the result back rather than assuming.
	Text string `json:"text,omitempty"`
	// button: home or app-switcher.
	Name string `json:"name,omitempty"`
	// drag-begin, drag-move and drag-end are one touch spread over several
	// requests, for a drag that follows a finger instead of being replayed once
	// it has been let go. They use X and Y, take one hold across the whole drag,
	// and the touch is lifted by a watchdog if the moves stop arriving.
}

// SimGestureResponse says what happened on the device.
type SimGestureResponse struct {
	UDID   string `json:"udid"`
	Kind   string `json:"kind"`
	Detail string `json:"detail" description:"What was done, in the same words the CLI prints."`
	// Rescued: the bridge had to release a touch the gesture left down. The
	// gesture succeeded, but the device was recovered rather than driven
	// cleanly, and that is never hidden.
	Rescued bool `json:"rescued,omitempty"`
}

// SimDeviceParam is the {udid} path parameter of the device-scoped routes.
type SimDeviceParam struct {
	UDID string `path:"udid" description:"Simulator udid (matched case-insensitively)."`
}

// SimSessionDeviceParam is the {sessionId}/{udid} pair of the gesture route.
type SimSessionDeviceParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1. The gesture is arbitrated as this session."`
	UDID      string `path:"udid" description:"Simulator udid (matched case-insensitively)."`
}

// SimScreenController owns the device listing and the gesture route.
type SimScreenController struct {
	Screen SimScreenProvider
	Leases simsvc.Manager
	// Drags is the touches currently held down. It is per-daemon rather than
	// per-request because a drag is one touch spanning several requests.
	Drags *simgesture.Drags
}

// Register mounts the routes. The live frame stream is not here: it is a
// WebSocket and lives outside the per-request timeout middleware, mounted
// alongside the terminal mux.
func (c *SimScreenController) Register(r chi.Router) {
	r.Get("/sim/devices", c.devices)
	// The gesture hangs off the session it is arbitrated as. A click in the
	// desktop app is that session's click, which is what makes it arbitrable at
	// all - a human's click with no session behind it could not be.
	r.Post("/sessions/{sessionId}/sim-devices/{udid}/gesture", c.gesture)
}

func (c *SimScreenController) devices(w http.ResponseWriter, r *http.Request) {
	if c.Screen == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sim/devices")
		return
	}
	listing, err := c.Screen.Devices(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_DEVICES_FAILED", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSimDevicesResponse{
		Devices:       c.withLeases(r.Context(), listing.Devices),
		DefaultUDID:   listing.DefaultUDID,
		DefaultReason: listing.DefaultReason,
	})
}

// withLeases joins what the machine has with what AO has claimed. A lease read
// that fails is not fatal: the devices are still real, and the honest answer is
// unknown with the reason that AO could not be asked.
func (c *SimScreenController) withLeases(ctx context.Context, devices []simctl.Device) []SimDeviceView {
	held := map[string]domain.SimLease{}
	reason := domain.SimLeaseUnknownReason
	if c.Leases == nil {
		reason = domain.SimLeaseNoDaemonReason
	} else if leases, err := c.Leases.List(ctx); err != nil {
		reason = domain.SimLeaseNoDaemonReason
	} else {
		for _, lease := range leases {
			held[domain.NormalizeSimUDID(lease.UDID)] = lease
		}
	}

	out := make([]SimDeviceView, 0, len(devices))
	for _, d := range devices {
		view := SimDeviceView{
			UDID: d.UDID, Name: d.Name, Runtime: d.Runtime, RuntimeIdentifier: d.RuntimeIdentifier,
			State: d.State, Available: d.Available, Default: d.Default,
			Lease: SimDeviceLeaseView{State: domain.SimLeaseUnknown, Reason: reason},
		}
		if lease, ok := held[domain.NormalizeSimUDID(d.UDID)]; ok {
			acquired, expires := lease.AcquiredAt.UTC(), lease.ExpiresAt.UTC()
			view.Lease = SimDeviceLeaseView{
				State:      domain.SimLeaseHeld,
				Holder:     string(lease.SessionID),
				AcquiredAt: &acquired,
				ExpiresAt:  &expires,
			}
		}
		out = append(out, view)
	}
	return out
}

func (c *SimScreenController) gesture(w http.ResponseWriter, r *http.Request) {
	if c.Screen == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture")
		return
	}
	if c.Leases == nil {
		// No lease service is no arbitration, and an unarbitrated touch is the
		// one thing this route may never do.
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture")
		return
	}
	var in SimGestureInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}

	// A move or an end belongs to a touch that is already down, opened against
	// a device this daemon resolved when the drag began. Resolving again per
	// event put `xcrun simctl list` - most of a second - in the middle of a
	// drag, which is felt as the picture stalling every couple of seconds.
	// An unknown device is still refused: there is no drag open for it.
	udid := chi.URLParam(r, "udid")
	device := simctl.Device{UDID: udid}
	if !isDragStep(in.Kind) {
		resolved, err := c.resolveDevice(r.Context(), udid)
		if err != nil {
			writeSimResolveError(w, r, err)
			return
		}
		device = resolved
	}
	driver, err := c.Screen.Driver(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "SIM_DRIVER_UNAVAILABLE", err.Error(), nil)
		return
	}
	sessionID := chi.URLParam(r, "sessionId")
	holder := &leaseHolder{leases: c.Leases, sessionID: domain.SessionID(sessionID)}

	// A drag is the one gesture that is not known before it starts, so it is
	// not composed - it is opened, followed and closed. Everything about the
	// arbitration is the same; only the shape of the hold's lifetime differs.
	if isDragKind(in.Kind) {
		c.drag(w, r, in, driver, holder, device.UDID, sessionID)
		return
	}

	gesture, err := composeSimGesture(in)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID", err.Error(), nil)
		return
	}

	result, err := simgesture.Run(r.Context(), holder, driver, device.UDID, gesture)
	if err != nil {
		writeSimGestureError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimGestureResponse{
		UDID: device.UDID, Kind: gesture.Action, Detail: gesture.Detail, Rescued: result.Lifted,
	})
}

func isDragKind(kind string) bool {
	return kind == "drag-begin" || kind == "drag-move" || kind == "drag-end"
}

// isDragStep is a drag event that continues one already open, as opposed to the
// begin that opens it.
func isDragStep(kind string) bool { return kind == "drag-move" || kind == "drag-end" }

// drag routes one step of a held touch. The registry owns the hold, the
// watchdog and the lift; this only says which step it is and turns a refusal
// into words.
func (c *SimScreenController) drag(
	w http.ResponseWriter, r *http.Request, in SimGestureInput,
	driver simbridge.Driver, holder simgesture.Holder, udid, sessionID string,
) {
	if c.Drags == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture")
		return
	}
	point := simbridge.Point{X: in.X, Y: in.Y}
	if err := simbridge.ValidatePoint(in.Kind, point); err != nil {
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID", err.Error(), nil)
		return
	}

	var err error
	switch in.Kind {
	case "drag-begin":
		err = c.Drags.Begin(r.Context(), holder, driver, udid, sessionID, point)
	case "drag-move":
		err = c.Drags.Move(r.Context(), driver, udid, sessionID, point)
	default:
		err = c.Drags.End(r.Context(), udid, sessionID, point)
	}
	if err != nil {
		writeSimDragError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimGestureResponse{UDID: udid, Kind: in.Kind, Detail: "drag"})
}

// writeSimDragError keeps a drag's refusals in the same shape a single
// gesture's are, so a client has one way to read "the device said no".
func writeSimDragError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, simgesture.ErrDragHeldByOther):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_DEVICE_BUSY", err.Error(), nil)
	case errors.Is(err, simgesture.ErrNoDrag):
		// Not an error the human caused: a drag the watchdog already lifted, or
		// a move that outran its own begin.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_DRAG_ENDED", err.Error(), nil)
	default:
		writeSimGestureError(w, r, err)
	}
}

// resolveDevice refuses a device this machine does not have or has not booted,
// using the same rule the CLI uses, before anything is composed.
func (c *SimScreenController) resolveDevice(ctx context.Context, udid string) (simctl.Device, error) {
	listing, err := c.Screen.Devices(ctx)
	if err != nil {
		return simctl.Device{}, err
	}
	return simctl.Resolve(listing.Devices, udid)
}

// leaseHolder takes the gesture hold straight from the lease service. The CLI
// takes the same hold over HTTP; the sequence between them is shared, so the
// only difference between a human's click and an agent's command is how the
// hold is asked for.
type leaseHolder struct {
	leases    simsvc.Manager
	sessionID domain.SessionID
}

func (h *leaseHolder) Acquire(ctx context.Context, udid string, ttl time.Duration) (string, error) {
	hold, err := h.leases.AcquireHold(ctx, h.sessionID, udid, ttl)
	if err != nil {
		return "", err
	}
	return hold.Token, nil
}

func (h *leaseHolder) Release(ctx context.Context, udid, token string) {
	if token == "" {
		return
	}
	// A hold that could not be handed back lapses on its own within a minute,
	// and must never turn a gesture that happened into a reported failure.
	_ = h.leases.ReleaseHold(ctx, udid, token)
}

// composeSimGesture turns a request into events. Every gesture is composed by
// internal/simbridge, the same code the CLI composes with, so a click and a
// command produce byte-identical event streams.
func composeSimGesture(in SimGestureInput) (simgesture.Gesture, error) {
	switch in.Kind {
	case "tap":
		at := simbridge.Point{X: in.X, Y: in.Y}
		events, err := simbridge.Tap(at)
		if err != nil {
			return simgesture.Gesture{}, err
		}
		return simgesture.Gesture{
			Action: "tap", Detail: fmt.Sprintf("(%.3f, %.3f)", at.X, at.Y), Events: events, Last: at,
		}, nil
	case "swipe":
		from := simbridge.Point{X: in.X, Y: in.Y}
		to := simbridge.Point{X: in.ToX, Y: in.ToY}
		duration := time.Duration(in.DurationMS) * time.Millisecond
		if duration <= 0 {
			duration = 300 * time.Millisecond
		}
		events, err := simbridge.Swipe(from, to, duration)
		if err != nil {
			return simgesture.Gesture{}, err
		}
		return simgesture.Gesture{
			Action: "swipe",
			Detail: fmt.Sprintf("(%.3f, %.3f) to (%.3f, %.3f) over %s", from.X, from.Y, to.X, to.Y, duration),
			Events: events, Last: to,
		}, nil
	case "type":
		events, err := simbridge.Type(in.Text)
		if err != nil {
			return simgesture.Gesture{}, err
		}
		return simgesture.Gesture{
			Action: "type", Detail: fmt.Sprintf("%d characters", len([]rune(in.Text))), Events: events,
		}, nil
	case "button":
		events, err := simbridge.Button(in.Name)
		if err != nil {
			return simgesture.Gesture{}, err
		}
		return simgesture.Gesture{Action: "button", Detail: in.Name, Events: events}, nil
	default:
		return simgesture.Gesture{}, fmt.Errorf("unknown gesture kind %q: use tap, swipe, type or button", in.Kind)
	}
}

// writeSimResolveError maps the shared device-resolution outcomes. A device
// that is not there is a 404; one that exists but is shut down is a 422,
// because the request was about a real device and AO will not boot it.
func writeSimResolveError(w http.ResponseWriter, r *http.Request, err error) {
	var notBooted *simctl.NotBootedError
	switch {
	case errors.Is(err, simctl.ErrUnknownUDID):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SIM_NOT_FOUND", err.Error(), nil)
	case errors.As(err, &notBooted):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_NOT_BOOTED",
			notBooted.Error()+". AO never boots, shuts down or erases a simulator for you.", nil)
	case errors.Is(err, simctl.ErrNoDevices), errors.Is(err, simctl.ErrNoBooted):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SIM_NOT_FOUND", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_DEVICES_FAILED", err.Error(), nil)
	}
}

// writeSimGestureError keeps the one distinction that matters: a gesture whose
// recovery release also failed may have left a finger down on the device, and
// the person who clicked has to be told.
func writeSimGestureError(w http.ResponseWriter, r *http.Request, err error) {
	var failed *simgesture.FailedError
	if !errors.As(err, &failed) {
		writeSimError(w, r, err)
		return
	}
	message := fmt.Sprintf("The %s failed: %v.", failed.Action, failed.Cause)
	switch {
	case failed.LiftErr != nil:
		message += fmt.Sprintf(" The follow-up release ALSO failed (%v), so the device may have a finger held down:"+
			" a stuck touch wedges input until the simulator is rebooted.", failed.LiftErr)
	case failed.Lifted:
		message += " The touch was released afterwards, so the device is not left with a finger down."
	}
	envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_GESTURE_FAILED", message, nil)
}
