package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
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
	// Keyboard is what the device will turn key presses into. Only `type` needs
	// it, and asking the guest costs about a second - so the implementation
	// maintains it per device rather than asking in front of every keystroke.
	// What makes that safe (the pane sends the character the human meant, so a
	// switch to Thai is routed by the text before the mode is consulted) is
	// argued where the caching lives, in internal/simstream.
	Keyboard(ctx context.Context, udid string) (simkeyboard.Mode, error)
	// Pasteboard is the guest clipboard, which is how text reaches a field the
	// keyboard cannot be trusted to fill.
	Pasteboard() simpaste.Pasteboard
	// StartPower boots or shuts a device down and returns at once, because a
	// boot takes tens of seconds and the request cannot be held open for it.
	// See internal/simpower for why this is reachable from the desktop app and
	// from no `ao` subcommand.
	StartPower(ctx context.Context, udid string, op simpower.Op, done func()) error
	// PowerStatus is what is in flight, keyed by normalized udid, so the
	// listing the pane already polls carries the progress too.
	PowerStatus() map[string]simpower.Status
	// ClearPower drops a remembered failure the machine has since made moot.
	ClearPower(udid string)
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

// SimDeviceFrameView is what a device's body looks like around its screen, in
// multiples of the screen's width, so the pane can draw it at whatever size the
// screen is being shown. Read from the artwork Xcode ships; absent for a device
// this machine has none for, and then the pane draws no body rather than
// inventing one.
type SimDeviceFrameView struct {
	Thickness float64 `json:"thickness" description:"Body around the screen, as a fraction of screen width."`
	Radius    float64 `json:"radius" description:"The display's own corner radius, as a fraction of screen width."`
}

// SimDevicePowerView is a power operation the desktop app started on this
// device and that has not finished cleanly. There is no "booted successfully"
// here on purpose: once a boot works, the device's own State says so, and a
// second field repeating it is a second field to be wrong.
type SimDevicePowerView struct {
	Op        simpower.Op    `json:"op" description:"boot or shutdown."`
	State     simpower.State `json:"state" description:"running while the operation is in flight; failed when it did not work."`
	StartedAt time.Time      `json:"startedAt"`
	Reason    string         `json:"reason,omitempty" description:"Why it failed, in the machine's own words where there are any."`
}

// SimDeviceView is one simulator plus its lease state.
type SimDeviceView struct {
	UDID              string              `json:"udid"`
	Name              string              `json:"name"`
	Runtime           string              `json:"runtime" description:"Human-readable runtime, e.g. iOS 26.3."`
	RuntimeIdentifier string              `json:"runtimeIdentifier"`
	State             string              `json:"state" description:"simctl's own state, e.g. Booted or Shutdown."`
	Available         bool                `json:"available"`
	Default           bool                `json:"default" description:"True for the one device an unqualified request resolves to. Never set when several are booted."`
	Lease             SimDeviceLeaseView  `json:"lease"`
	Frame             *SimDeviceFrameView `json:"frame,omitempty"`
	// Power is set only while a boot or shutdown this daemon started is still
	// running, or has failed and not yet been superseded. Absent is the normal
	// case, and means the State field above is the whole story.
	Power *SimDevicePowerView `json:"power,omitempty"`
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
	Kind string `json:"kind" description:"tap, swipe, type, key, button, drag-begin, drag-move, drag-end, pinch-begin, pinch-move or pinch-end."`
	// tap and swipe: normalized 0..1 screen coordinates, the same numbers
	// `ao sim ax` reports per element.
	X float64 `json:"x,omitempty"`
	Y float64 `json:"y,omitempty"`
	// swipe: where the drag ends, and how long it takes.
	ToX        float64 `json:"toX,omitempty"`
	ToY        float64 `json:"toY,omitempty"`
	DurationMS int     `json:"durationMs,omitempty" description:"Swipe duration in milliseconds. Omit for 300."`
	// pinch-begin, pinch-move and pinch-end: the SECOND finger. X/Y is the
	// first one, and both are ordinary normalized screen coordinates.
	//
	// They are a separate pair of fields rather than a repeat of X/Y because
	// the KIND is what says how many fingers are down - a held touch may not
	// change that mid-way (see simgesture.ErrGripChanged), so the request has
	// to declare it rather than have it inferred from which fields happen to
	// be present. A `drag-move` that quietly became a pinch because two extra
	// numbers arrived is the same class of bug as a pinch that quietly became
	// a drag because they did not.
	X2 float64 `json:"x2,omitempty"`
	Y2 float64 `json:"y2,omitempty"`
	// type: the text to send. The keys are US-keyboard key presses and the
	// GUEST turns them into characters using its own input mode, so this route
	// asks the device which mode that is and refuses text it cannot promise.
	Text string `json:"text,omitempty"`
	// type: send the key presses even when the simulator would turn them into
	// other characters - which is how Thai text is entered on a Thai guest.
	// What is then promised is key presses, not characters.
	RawKeys bool `json:"rawKeys,omitempty"`
	// type: always deliver through the simulator's pasteboard rather than as
	// key presses. Without it the route is chosen per request: key presses when
	// the guest will deliver them faithfully, the pasteboard when it would not.
	Paste bool `json:"paste,omitempty"`
	// type: the physical keys a person actually pressed to produce Text on this
	// Mac, in the same order, one per character.
	//
	// ⚠ Only a caller that WATCHED someone press them may send these - the
	// Device tab, forwarding a real keyboard. They are sent to the device as
	// themselves, so what arrives is whatever the simulator's input mode makes
	// of them, exactly as it would in Simulator.app: correct for a person,
	// because the same layout resolved the character they saw themselves type,
	// and wrong for an agent, which is why a string an agent chose must be sent
	// as Text alone and planned from the guest's input mode.
	Keys []SimKeyPress `json:"keys,omitempty" description:"The physical keys a person actually pressed to produce Text on this Mac, one per character, in the same order. Only a caller that watched someone press them may send these: they are forwarded to the device as themselves, so the simulator's input mode decides what they produce, exactly as it would in Simulator.app. A string chosen by an agent has no keys behind it and must be sent as Text alone."`
	// button: home or app-switcher. key: enter, backspace, tab or one of the
	// arrow keys - the keys that produce no character, and so cannot be
	// remapped by the guest's keyboard input mode the way a letter can.
	Name string `json:"name,omitempty"`
	// drag-begin, drag-move and drag-end are one touch spread over several
	// requests, for a drag that follows a finger instead of being replayed once
	// it has been let go. They use X and Y, take one hold across the whole drag,
	// and the touch is lifted by a watchdog if the moves stop arriving.
	//
	// pinch-begin, pinch-move and pinch-end are the same held touch with TWO
	// contacts - the Device tab's Option-drag, following a human's hand. They
	// are the same three steps, the same one hold, the same watchdog and the
	// same registry: only the number of fingers differs, which is why they
	// carry X2/Y2 and are otherwise routed identically. See simbridge.Grip.
}

// SimKeyPress is one physical key press, named the way a browser names it.
// `code` is a POSITION on the keyboard - where the key sits on a US layout,
// whatever the layout in force prints on it - which is the same thing the
// device's HID usages are.
type SimKeyPress struct {
	Code  string `json:"code" description:"KeyboardEvent.code, e.g. KeyF, Digit1, Semicolon."`
	Shift bool   `json:"shift,omitempty" description:"Shift was held. Part of the key press, not of the character."`
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
	// Asked before typing rather than during it. It takes no lease because it
	// touches nothing: it reads which input mode the guest will interpret key
	// presses through, which is the one thing a keystroke cannot be planned
	// without and the one thing that costs about a second to find out.
	r.Get("/sim/devices/{udid}/keyboard", c.keyboard)
	// The gesture hangs off the session it is arbitrated as. A click in the
	// desktop app is that session's click, which is what makes it arbitrable at
	// all - a human's click with no session behind it could not be.
	r.Post("/sessions/{sessionId}/sim-devices/{udid}/gesture", c.gesture)
	// Powering a device on and off. Session-scoped for the same reason the
	// gesture is: shutting a device down has to be arbitrated against whoever
	// is driving it, and arbitration in AO is per session.
	//
	// ⚠ There is deliberately no `ao sim` command behind this. See
	// internal/simpower: booting is a human capability exercised through the
	// desktop app, because an agent that could boot devices would accumulate
	// 4 GB virtual machines with nobody watching.
	r.Post("/sessions/{sessionId}/sim-devices/{udid}/power", c.power)
}

// SimKeyboardView is what the pane needs in order to PACE typing: whether this
// guest will read US ASCII key presses as the characters they were sent as.
//
// ⚠ It is deliberately not a routing decision, and the pane is not allowed to
// treat it as one. The daemon plans every `type` request from scratch, with the
// mode as it is at that moment; this only tells the pane whether sending
// characters one at a time is cheap here. Being wrong costs speed, never
// correctness - which is why there is still exactly one implementation of "is
// this keyboard safe", and it is not in the renderer.
type SimKeyboardView struct {
	UDID string `json:"udid"`
	// Mode is the guest's input mode in words, for a human reading a pane that
	// is behaving unexpectedly. Never the text being typed.
	Mode string `json:"mode"`
	// SendsUSASCII is the pacing answer: may characters go one at a time.
	SendsUSASCII bool `json:"sendsUSASCII"`
	// Reason says why not, so an unreadable guest can be told from a remapping
	// one without reading the daemon's logs.
	Reason string `json:"reason,omitempty"`
}

// keyboard answers which input mode a guest reads key presses through, and
// warms the daemon's own copy of that answer as a side effect.
//
// A guest that will not say is answered with a no rather than an error: the
// question is "may I send these one at a time", and "I could not find out" is
// safely no. An error would leave the pane with nothing to pace by at exactly
// the moment somebody is about to type into it.
func (c *SimScreenController) keyboard(w http.ResponseWriter, r *http.Request) {
	if c.Screen == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sim/devices/{udid}/keyboard")
		return
	}
	udid := chi.URLParam(r, "udid")
	mode, err := c.Screen.Keyboard(r.Context(), udid)
	view := SimKeyboardView{UDID: udid, Mode: mode.Describe()}
	switch {
	case err != nil:
		view.Mode = "unknown"
		view.Reason = "the simulator would not say which keyboard input mode it is using"
	case !mode.SendsUSASCII():
		view.Reason = "the simulator's keyboard input mode is " + mode.Describe() +
			", which would remap the key presses"
	default:
		view.SendsUSASCII = true
	}
	envelope.WriteJSON(w, http.StatusOK, view)
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

	power := c.Screen.PowerStatus()

	out := make([]SimDeviceView, 0, len(devices))
	for _, d := range devices {
		view := SimDeviceView{
			UDID: d.UDID, Name: d.Name, Runtime: d.Runtime, RuntimeIdentifier: d.RuntimeIdentifier,
			State: d.State, Available: d.Available, Default: d.Default,
			Lease: SimDeviceLeaseView{State: domain.SimLeaseUnknown, Reason: reason},
		}
		if d.Frame != nil {
			view.Frame = &SimDeviceFrameView{Thickness: d.Frame.Thickness, Radius: d.Frame.Radius}
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
		view.Power = c.powerView(d, power[domain.NormalizeSimUDID(d.UDID)], len(power) > 0)
		out = append(out, view)
	}
	return out
}

// powerView reports a device's in-flight or failed power operation, and drops
// a failure the machine has since made moot.
//
// The dropping is what stops a stale sentence outliving what it described. A
// boot that timed out on a device which is Booted now - because it finished
// thirty seconds after we stopped waiting, or because somebody booted it from
// Xcode - would otherwise keep saying it failed for as long as the daemon ran,
// with the device visibly up beside it.
func (c *SimScreenController) powerView(d simctl.Device, status simpower.Status, tracked bool) *SimDevicePowerView {
	if !tracked || status.State == "" {
		return nil
	}
	if status.State == simpower.Failed && reachedGoal(d, status.Op) {
		c.Screen.ClearPower(d.UDID)
		return nil
	}
	return &SimDevicePowerView{
		Op: status.Op, State: status.State, StartedAt: status.StartedAt.UTC(), Reason: status.Reason,
	}
}

// reachedGoal says whether the device is now in the state the operation was
// trying to reach, whoever or whatever got it there.
func reachedGoal(d simctl.Device, op simpower.Op) bool {
	switch op {
	case simpower.Boot:
		return d.Booted()
	case simpower.Shutdown:
		return !d.Booted()
	default:
		return false
	}
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
	holder := &leaseHolder{leases: c.Leases, sessionID: domain.SessionID(sessionID), intent: gestureIntentFrom(in)}

	// A drag is the one gesture that is not known before it starts, so it is
	// not composed - it is opened, followed and closed. Everything about the
	// arbitration is the same; only the shape of the hold's lifetime differs.
	if isDragKind(in.Kind) {
		c.drag(w, r, in, driver, holder, device.UDID, sessionID)
		return
	}

	// Typing is the one gesture whose meaning the device decides: it reads the
	// key presses through whichever input mode it has selected, so a guest set
	// to Thai turns "fa12345" into "ดฟๅ/_ภถ". The mode is established before
	// anything is composed, and a device that cannot say is refused rather than
	// typed at hopefully.
	//
	// ⚠ Keys a person pressed are the exception, and it is the whole of this
	// fix: forwarding a key does not need the mode, because the mode is what
	// makes forwarding right rather than what stands in its way. Asking anyway
	// would put a guest round trip in front of every keystroke for an answer
	// nothing reads.
	var keyboard simbridge.ProbedKeyboard
	if in.Kind == "type" && !in.RawKeys && !in.Paste && !simbridge.ForwardableKeys(keyPresses(in.Keys)) {
		keyboard.Mode, keyboard.Err = c.Screen.Keyboard(r.Context(), device.UDID)
	}

	gesture, err := composeSimGesture(in, keyboard)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID", err.Error(), nil)
		return
	}
	if gesture.Action == "paste" {
		c.paste(w, r, holder, driver, device.UDID, in.Text, gesture.Detail)
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

// paste delivers text through the guest pasteboard and proves it landed. It is
// the same sequence `ao sim type` runs, for the same reason every other gesture
// is shared: a click and a command must reach the device the same way.
func (c *SimScreenController) paste(
	w http.ResponseWriter, r *http.Request,
	holder simgesture.Holder, driver simbridge.Driver, udid, text, why string,
) {
	pb := c.Screen.Pasteboard()
	if pb == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture")
		return
	}
	result, err := simpaste.Run(r.Context(), holder, driver, pb, udid, text)
	if err != nil {
		if errors.Is(err, simpaste.ErrNotDelivered) {
			envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID",
				err.Error()+" ("+why+", so the text went through the pasteboard)", nil)
			return
		}
		writeSimGestureError(w, r, err)
		return
	}
	response := SimGestureResponse{
		UDID: udid, Kind: "type",
		Detail: fmt.Sprintf("%d characters pasted (%s)", len([]rune(text)), why),
	}
	if !result.Restored {
		// The payload is still on the guest pasteboard where any app on the
		// device can read it, and it is a password often enough to say so.
		response.Detail += "; WARNING: the simulator pasteboard could not be put back"
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

// keyPresses carries the request's key presses across the package boundary.
// The two types are deliberately separate: one is the wire shape, the other is
// what the composer takes, and collapsing them would put JSON tags on the
// composer.
func keyPresses(in []SimKeyPress) []simbridge.KeyPress {
	if len(in) == 0 {
		return nil
	}
	keys := make([]simbridge.KeyPress, len(in))
	for i, k := range in {
		keys[i] = simbridge.KeyPress{Code: k.Code, Shift: k.Shift}
	}
	return keys
}

// heldTouch reads a kind as one step of a touch that stays down across several
// requests: which step it is, and how many fingers it puts on the screen. A
// kind that is not a held touch answers ("", 0).
//
// The finger count comes from the KIND rather than from which coordinates
// arrived, because simgesture refuses a held touch that changes how many
// fingers are down - so the count is a thing the caller declares once and is
// then held to, not a thing inferred per request.
func heldTouch(kind string) (phase string, fingers int) {
	switch kind {
	case "drag-begin":
		return "begin", 1
	case "drag-move":
		return "move", 1
	case "drag-end":
		return "end", 1
	case "pinch-begin":
		return "begin", 2
	case "pinch-move":
		return "move", 2
	case "pinch-end":
		return "end", 2
	}
	return "", 0
}

func isDragKind(kind string) bool {
	_, fingers := heldTouch(kind)
	return fingers > 0
}

// isDragStep is a held-touch event that continues one already open, as opposed
// to the begin that opens it.
func isDragStep(kind string) bool {
	phase, fingers := heldTouch(kind)
	return fingers > 0 && phase != "begin"
}

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
	phase, fingers := heldTouch(in.Kind)
	// The registry holds a grip rather than a point, so one finger and two are
	// the same path - one hold, one watchdog, one lift that releases whatever
	// is down as a set. See simbridge.Grip for why that is not a convenience.
	grip := simbridge.OneFinger(simbridge.Point{X: in.X, Y: in.Y})
	if fingers == 2 {
		grip = simbridge.TwoFingers(simbridge.Point{X: in.X, Y: in.Y}, simbridge.Point{X: in.X2, Y: in.Y2})
	}
	// Validate, not ValidatePoint: a pair has to answer for both contacts and
	// for the gap between them, and a pinch whose fingers land as one touch
	// reads exactly like a pinch that worked while nothing zoomed.
	if err := grip.Validate(in.Kind); err != nil {
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID", err.Error(), nil)
		return
	}

	var err error
	switch phase {
	case "begin":
		err = c.Drags.Begin(r.Context(), holder, driver, udid, sessionID, grip)
	case "move":
		err = c.Drags.Move(r.Context(), driver, udid, sessionID, grip)
	default:
		err = c.Drags.End(r.Context(), udid, sessionID, grip)
	}
	if err != nil {
		writeSimDragError(w, r, err)
		return
	}
	detail := "drag"
	if fingers == 2 {
		detail = "pinch"
	}
	envelope.WriteJSON(w, http.StatusOK, SimGestureResponse{UDID: udid, Kind: in.Kind, Detail: detail})
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
	case errors.Is(err, simgesture.ErrGripChanged):
		// A step that says one finger for a touch that has two down, or the
		// other way round. The touch it interrupted has been released, so this
		// is the same shape of answer as any other ended drag: the client's
		// next begin will work. It is named separately from SIM_DRAG_ENDED
		// because the cause is different and only one of the two is a bug in
		// the caller.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_GRIP_CHANGED", err.Error(), nil)
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
	// intent is what this gesture request said it was about to do, resolved
	// once (from the request body) when the holder is built. Acquire carries it
	// straight to AcquireHold: it is the same information a recorded step
	// needs, and the gesture that will run is exactly the one this hold was
	// asked for.
	intent simsvc.GestureIntent
}

func (h *leaseHolder) Acquire(ctx context.Context, udid string, ttl time.Duration) (string, error) {
	hold, err := h.leases.AcquireHold(ctx, h.sessionID, udid, ttl, h.intent)
	if err != nil {
		return "", err
	}
	return hold.Token, nil
}

func (h *leaseHolder) Release(ctx context.Context, udid, token string, outcome simgesture.Outcome) {
	if token == "" {
		return
	}
	// The outcome is simgesture's own account of what the gesture did (see
	// internal/simgesture.Outcome) - carried straight through, not overridden.
	// Performed is its verdict on whether the gesture actually reached the
	// device; End is where a drag's finger came up, which nothing knew when
	// this hold was taken on the finger going down. A hold that could not be
	// handed back lapses on its own within a minute either way, so this never
	// turns a gesture that happened into a reported failure; it only ever
	// affects what gets recorded.
	_ = h.leases.ReleaseHold(ctx, udid, token, simsvc.GestureOutcome{
		Performed: outcome.Performed,
		End:       outcome.End,
	})
}

// gestureIntentFrom turns a gesture request into what the recorder needs to
// know about it. It mirrors SimGestureInput's own fields one-for-one, because
// that is exactly the information a recorded step needs and inventing a
// second vocabulary for it would be two things to keep in step.
//
// It never sets GestureIntent's Label/ID: the Device tab's click is always a
// point on screen, never a name, so there is nothing in SimGestureInput to
// carry - that pair has no counterpart here by construction, not by omission.
func gestureIntentFrom(in SimGestureInput) simsvc.GestureIntent {
	at := simbridge.Point{X: in.X, Y: in.Y}
	if _, fingers := heldTouch(in.Kind); fingers == 2 {
		// A pinch is recorded at the point BETWEEN its fingers, which is what
		// `ao sim pinch` records too - the gesture is about that point and
		// neither finger alone describes it. Grip.At is where that midpoint is
		// defined, so the two routes cannot drift.
		at = simbridge.TwoFingers(at, simbridge.Point{X: in.X2, Y: in.Y2}).At()
	}
	return simsvc.GestureIntent{
		Kind:       in.Kind,
		X:          at.X,
		Y:          at.Y,
		ToX:        in.ToX,
		ToY:        in.ToY,
		DurationMS: in.DurationMS,
		Text:       in.Text,
		Name:       in.Name,
	}
}

// composeSimGesture turns a request into events. Every gesture is composed by
// internal/simbridge, the same code the CLI composes with, so a click and a
// command produce byte-identical event streams.
//
// keyboard is what the device said when asked, and only `type` reads it: the
// guest turns the key presses we send into characters using that mode, so text
// cannot be routed honestly without it. A device that would not say is carried
// as an error rather than as a mode, because "unknown" and "US" must never
// collapse into the same value.
func composeSimGesture(in SimGestureInput, keyboard simbridge.ProbedKeyboard) (simgesture.Gesture, error) {
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
		route, err := simbridge.PlanText(in.Text, keyboard,
			simbridge.TextOptions{RawKeys: in.RawKeys, Paste: in.Paste, Keys: keyPresses(in.Keys)})
		if err != nil {
			return simgesture.Gesture{}, err
		}
		if route.Paste {
			// Signalled to the caller rather than composed: a paste is not a
			// list of events, it is a sequence that has to prove itself.
			return simgesture.Gesture{Action: "paste", Detail: route.Why}, nil
		}
		// Key presses, not characters, when the caller waived the promise: the
		// whole bug was a command claiming characters it had not delivered.
		detail := fmt.Sprintf("%d characters", len([]rune(in.Text)))
		if in.RawKeys {
			detail = fmt.Sprintf("%d key presses", len([]rune(in.Text)))
		}
		if route.Forwarded {
			// Named for what was done rather than for what it produced: these
			// keys were sent as pressed, and the simulator decided the rest.
			detail = fmt.Sprintf("%d key presses forwarded", len(in.Keys))
		}
		return simgesture.Gesture{Action: "type", Detail: detail, Events: route.Events}, nil
	case "button":
		events, err := simbridge.Button(in.Name)
		if err != nil {
			return simgesture.Gesture{}, err
		}
		return simgesture.Gesture{Action: "button", Detail: in.Name, Events: events}, nil
	case "key":
		events, err := simbridge.Key(in.Name)
		if err != nil {
			return simgesture.Gesture{}, err
		}
		return simgesture.Gesture{Action: "key", Detail: in.Name, Events: events}, nil
	default:
		return simgesture.Gesture{}, fmt.Errorf("unknown gesture kind %q: use tap, swipe, type, key or button", in.Kind)
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
			notBooted.Error()+". Boot it from the Device tab's simulator picker, or with `ao sim boot`.", nil)
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

// SimPowerInput is the body of POST .../sim-devices/{udid}/power.
type SimPowerInput struct {
	// State is the power state to put the device in, named as a state rather
	// than as a verb so the request is idempotent in intent: asking for
	// "booted" twice is asking for the same world twice.
	State string `json:"state" description:"booted or shutdown."`
	// ConfirmHolder must name the session that currently leases the device
	// when that session is somebody else. It exists so the confirmation is a
	// property of the protocol rather than of the UI: a picker holding a list
	// that went stale seconds ago cannot shut down a device on the strength of
	// a lease it read before somebody else took it.
	ConfirmHolder string `json:"confirmHolder,omitempty" description:"The session that currently leases the device. Required, and must match, when another session holds it."`
}

// SimPowerResponse acknowledges that the work has started. It is not a report
// that the device reached the state: a boot takes tens of seconds, so progress
// arrives on the device listing as SimDeviceView.power.
type SimPowerResponse struct {
	UDID  string `json:"udid"`
	State string `json:"state" description:"The state the device is being taken to."`
	// Detail says what was started, in words a person can be shown.
	Detail string `json:"detail"`
}

// power boots a simulator or shuts one down.
//
// It answers 202 and does the work behind the request. A cold boot beats the
// daemon's 60-second per-request ceiling (config.DefaultRequestTimeout), so a
// synchronous route would time out on exactly the boots that most need
// reporting - and progress that lived in the request would die with the
// popover being closed or the renderer being reloaded, both of which somebody
// does while waiting a minute for a device.
func (c *SimScreenController) power(w http.ResponseWriter, r *http.Request) {
	const route = "/api/v1/sessions/{sessionId}/sim-devices/{udid}/power"
	if c.Screen == nil || c.Leases == nil {
		// No lease service is no arbitration, and cutting power to a device
		// somebody may be mid-gesture on is not something to do unarbitrated.
		apispec.NotImplemented(w, r, "POST", route)
		return
	}
	var in SimPowerInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	op, err := powerOp(in.State)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "SIM_INVALID_POWER_STATE", err.Error(), nil)
		return
	}

	sessionID := domain.SessionID(chi.URLParam(r, "sessionId"))
	device, err := c.findDevice(r.Context(), chi.URLParam(r, "udid"))
	if err != nil {
		writeSimResolveError(w, r, err)
		return
	}

	// Already there is a conflict rather than a quiet success: a picker that
	// asked has a list that disagrees with the machine, and saying so is how
	// it finds out.
	if reachedGoal(device, op) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_POWER_ALREADY", fmt.Sprintf(
			"simulator %s is already %s", device.Label(), strings.ToLower(device.State)), nil)
		return
	}

	done := func() {}
	if op == simpower.Shutdown {
		release, err := c.arbitrateShutdown(r.Context(), sessionID, device, in.ConfirmHolder)
		if err != nil {
			writeShutdownRefusal(w, r, err)
			return
		}
		done = release
	}

	if err := c.Screen.StartPower(r.Context(), device.UDID, op, done); err != nil {
		done()
		writePowerStartError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, SimPowerResponse{
		UDID: device.UDID, State: in.State,
		Detail: fmt.Sprintf("%s %s", op, device.Label()),
	})
}

// arbitrateShutdown decides whether this device may be powered off, and takes
// the device while it is being powered off. It returns the function that gives
// it back.
//
// 🗝 The rule, and why it is this rule. internal/service/sim.TakeOver already
// answers the question "may a human take this device away from a session", and
// its answer is: yes to a LEASE, never to a GESTURE THAT IS HAPPENING. Cutting
// power is the same question with more at stake, so it gets the same answer by
// running through the same primitive rather than by reimplementing the
// judgement:
//
//   - a gesture in flight refuses the take-over, so it refuses the shutdown.
//     That is not negotiable and no confirmation overrides it: the simulator's
//     HID layer has no per-caller state, so a touch cut off mid-gesture leaves
//     the driver believing a finger is still down, and a stuck touch wedges
//     the device's input until somebody reboots it.
//   - a lease held by another session yields, but only to a request that NAMES
//     that session. AO already ships a "Take over from @X" button, so a human
//     may already take a device away from a worker; refusing them the power
//     switch would be stricter than that, and a wedged 4 GB device held by a
//     session that is not coming back is precisely the memory problem this
//     whole control exists for.
//   - our own lease, or none at all, needs no naming - the confirmation for
//     those lives in the UI, where the human is the only party being asked.
//
// The lease is held for the length of the shutdown and given back when it
// settles, so the device is arbitrated for exactly as long as it is being
// powered off.
func (c *SimScreenController) arbitrateShutdown(
	ctx context.Context, sessionID domain.SessionID, device simctl.Device, confirmHolder string,
) (func(), error) {
	if holder, ok := c.currentHolder(ctx, device.UDID); ok && holder != sessionID {
		if !strings.EqualFold(strings.TrimSpace(confirmHolder), string(holder)) {
			return nil, &shutdownUnconfirmedError{UDID: device.UDID, Holder: holder}
		}
	}
	if _, err := c.Leases.TakeOver(ctx, sessionID, device.UDID, simpower.ShutdownTimeout); err != nil {
		return nil, err
	}
	// Detached from the request, which is answered long before the shutdown
	// finishes.
	release := context.WithoutCancel(ctx)
	return func() { _ = c.Leases.Release(release, sessionID, device.UDID) }, nil
}

// currentHolder is the session leasing a device right now, if AO knows of one.
// A lease service that cannot be read is treated as holding nothing: the
// take-over below is what actually arbitrates, and this only decides whether a
// name has to be confirmed first.
func (c *SimScreenController) currentHolder(ctx context.Context, udid string) (domain.SessionID, bool) {
	leases, err := c.Leases.List(ctx)
	if err != nil {
		return "", false
	}
	key := domain.NormalizeSimUDID(udid)
	for _, lease := range leases {
		if domain.NormalizeSimUDID(lease.UDID) == key {
			return lease.SessionID, true
		}
	}
	return "", false
}

// shutdownUnconfirmedError is a shutdown aimed at a device somebody else holds
// without saying whose it is.
type shutdownUnconfirmedError struct {
	UDID   string
	Holder domain.SessionID
}

func (e *shutdownUnconfirmedError) Error() string {
	return fmt.Sprintf("simulator %s is leased by @%s: shutting it down has to name them", e.UDID, e.Holder)
}

// findDevice locates a device whatever state it is in.
//
// Deliberately not simctl.Resolve, which refuses a device that is not booted -
// the right answer for every route that wants to touch a screen, and the wrong
// one for the single route whose whole job is to change that.
func (c *SimScreenController) findDevice(ctx context.Context, udid string) (simctl.Device, error) {
	listing, err := c.Screen.Devices(ctx)
	if err != nil {
		return simctl.Device{}, err
	}
	if strings.TrimSpace(udid) == "" {
		return simctl.Device{}, fmt.Errorf("%w: no device named", simctl.ErrUnknownUDID)
	}
	key := domain.NormalizeSimUDID(udid)
	for _, device := range listing.Devices {
		if domain.NormalizeSimUDID(device.UDID) == key {
			return device, nil
		}
	}
	return simctl.Device{}, fmt.Errorf("%w: %s is not a simulator on this machine", simctl.ErrUnknownUDID, udid)
}

// powerOp maps the requested state onto the operation that reaches it.
func powerOp(state string) (simpower.Op, error) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "booted":
		return simpower.Boot, nil
	case "shutdown":
		return simpower.Shutdown, nil
	default:
		return "", fmt.Errorf("unknown power state %q: use booted or shutdown", state)
	}
}

// writeShutdownRefusal keeps the two refusals apart, because they need
// different things from the person reading them: one is "wait a moment", the
// other is "say whose device this is".
func writeShutdownRefusal(w http.ResponseWriter, r *http.Request, err error) {
	var (
		unconfirmed *shutdownUnconfirmedError
		held        *simsvc.HeldError
	)
	switch {
	case errors.As(err, &unconfirmed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_POWER_HOLDER_UNCONFIRMED",
			unconfirmed.Error(), map[string]any{"udid": unconfirmed.UDID, "holder": string(unconfirmed.Holder)})
	case errors.As(err, &held) && held.MidGesture:
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_POWER_GESTURE_IN_FLIGHT",
			fmt.Sprintf("a gesture from @%s is in flight on simulator %s: shutting it down now would leave a "+
				"touch held, which wedges the device's input until it is rebooted. Retry in a moment",
				held.Lease.SessionID, held.Lease.UDID),
			map[string]any{"udid": held.Lease.UDID, "holder": string(held.Lease.SessionID)})
	default:
		writeSimError(w, r, err)
	}
}

// writePowerStartError maps the refusals that come from the power surface
// itself rather than from the arbitration around it.
func writePowerStartError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, simpower.ErrBusy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SIM_POWER_BUSY", err.Error(), nil)
	case errors.Is(err, simpower.ErrUnavailable):
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "SIM_UNAVAILABLE", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_POWER_FAILED", err.Error(), nil)
	}
}
