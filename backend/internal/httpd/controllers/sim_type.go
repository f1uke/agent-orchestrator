package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtype"
)

// SimTypeController types text through the device's XCTest runner: characters,
// not key presses, so the keyboard's language does not decide what arrives.
// See internal/simtype for why it exists and how it proves itself.
//
// It is a touch, so it takes the gesture hold like every other one, and only a
// session holding the device's lease gets one. It is `ao sim type`'s default
// route; the Device tab keeps its own per-keystroke route (POST .../gesture),
// which has to echo a key in milliseconds.
type SimTypeController struct {
	Runner SimRunner
	Leases simsvc.Manager
}

// Register mounts the route.
func (c *SimTypeController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/sim-devices/{udid}/type", c.typeText)
}

// SimTypeInput is the body of POST .../sim-devices/{udid}/type.
type SimTypeInput struct {
	Text   string `json:"text" description:"The characters to type, at most 100. A longer text is sent in chunks, one request each."`
	WaitMs int    `json:"waitMs,omitempty" description:"How long to wait for a runner that is still starting, in milliseconds, before taking the gesture hold. Capped at 15000."`
}

// SimTypeField is the element that had keyboard focus.
type SimTypeField struct {
	Type        string `json:"type" description:"XCUIElement type, e.g. TextField, SecureTextField."`
	ID          string `json:"id,omitempty"`
	Label       string `json:"label,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

// SimTypeLanding is where the text was proven to have arrived.
type SimTypeLanding struct {
	Field    string `json:"field,omitempty" description:"The element's accessibility label."`
	Path     string `json:"path" description:"Its path in ao sim ax."`
	Shown    string `json:"shown" description:"What it reads now: dots for a secure field."`
	Evidence string `json:"evidence" enum:"exact,case,masked,reformatted" description:"exact: the text itself. case: apart from capitalisation. masked: a secure field gained one dot per character. reformatted: its letters and digits inside the field's own formatting."`
}

// SimTypeResponse is a type that was proven on screen.
type SimTypeResponse struct {
	UDID     string         `json:"udid"`
	App      string         `json:"app" description:"Bundle id of the application holding the field."`
	Field    SimTypeField   `json:"field"`
	Landed   SimTypeLanding `json:"landed"`
	Detail   string         `json:"detail" description:"Where the text went, in the words the CLI prints."`
	Keyboard bool           `json:"keyboard" description:"The software keyboard was on screen before typing."`
	TypingMs int            `json:"typingMs" description:"How long XCTest took to type it."`
	Warning  string         `json:"warning,omitempty" description:"What XCTest said while typing, when it complained about text the screen shows did arrive."`
	// The keyboard is the device's, so changing it is reported.
	KeyboardSwitchedTo string `json:"keyboardSwitchedTo,omitempty" description:"The layout the software keyboard was switched to for a secure field, which takes only what the keyboard on screen can type."`
	KeyboardRestored   bool   `json:"keyboardRestored,omitempty" description:"The keyboard was switched back afterwards."`
}

// maxTypeWait bounds a caller's wait for a starting runner, so a type still
// answers well inside the request timeout.
const maxTypeWait = 15 * time.Second

func (c *SimTypeController) typeText(w http.ResponseWriter, r *http.Request) {
	if c.Leases == nil {
		// No lease service is no arbitration, and an unarbitrated touch is the
		// one thing a touch route may never do.
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/sim-devices/{udid}/type")
		return
	}
	var in SimTypeInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	switch n := utf8.RuneCountInString(in.Text); {
	case n == 0:
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID", "nothing to type", nil)
		return
	case len(simtype.Chunks(in.Text, simtype.ChunkRunes)) > 1:
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_INVALID",
			fmt.Sprintf("%d characters is more than one request types (%d); send it in chunks", n, simtype.ChunkRunes), nil)
		return
	}
	if c.Runner == nil {
		writeSimRunnerNotReady(w, r, SimRunnerView{State: "unavailable",
			Reason: "this daemon cannot run the XCTest runner (it needs macOS with Xcode)"})
		return
	}
	udid := chi.URLParam(r, "udid")
	sessionID := chi.URLParam(r, "sessionId")

	// Waited for BEFORE the hold: a hold taken while a runner builds keeps
	// every other command off the device for nothing.
	wait := min(time.Duration(max(in.WaitMs, 0))*time.Millisecond, maxTypeWait)
	if status, err := c.Runner.Await(r.Context(), udid, wait); err != nil {
		writeSimRunnerNotReady(w, r, SimRunnerView{State: string(status.State), Reason: status.Reason})
		return
	}

	holder := &leaseHolder{leases: c.Leases, sessionID: domain.SessionID(sessionID),
		intent: simsvc.GestureIntent{Kind: "type", Text: in.Text}}
	result, err := simtype.Run(r.Context(), holder, runnerReader{c.Runner}, c.Runner, udid, in.Text)
	if err != nil {
		writeSimTypeError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SimTypeResponse{
		UDID: udid,
		App:  result.App,
		Field: SimTypeField{Type: result.Field.Type, ID: result.Field.ID, Label: result.Field.Label,
			Placeholder: result.Field.Placeholder},
		Landed: SimTypeLanding{Field: result.Landing.Field, Path: result.Landing.Path,
			Shown: result.Landing.Shown, Evidence: string(result.Landing.How)},
		Detail:   result.Landing.String(),
		Keyboard: result.Keyboard,
		TypingMs: result.TypingMs,
		Warning:  result.Warning,

		KeyboardSwitchedTo: result.KeyboardSwitchedTo,
		KeyboardRestored:   result.KeyboardRestored,
	})
}

// writeSimRunnerNotReady is a type that never reached the runner. Nothing was
// typed, so the caller may take another route - and says it did.
func writeSimRunnerNotReady(w http.ResponseWriter, r *http.Request, runner SimRunnerView) {
	message := "the XCTest runner is " + runner.State
	if runner.Reason != "" {
		message += ": " + runner.Reason
	}
	envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "SIM_RUNNER_NOT_READY", message,
		map[string]any{"state": runner.State, "reason": runner.Reason})
}

// writeSimTypeError keeps apart the outcomes a caller acts on differently:
// nothing was typed (tap the field; or another route may be taken), something
// may have been typed (read it back, never send it again), or the device was
// not this caller's to touch.
func writeSimTypeError(w http.ResponseWriter, r *http.Request, err error) {
	var noFocus *simtype.NoFocusError
	switch {
	case errors.Is(err, simtype.ErrUnavailable):
		writeSimRunnerNotReady(w, r, SimRunnerView{State: string(simrunner.StateFailed), Reason: err.Error()})
	case errors.As(err, &noFocus):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_TYPE_NO_FOCUS", err.Error(),
			map[string]any{"keyboard": noFocus.Keyboard, "checked": noFocus.Checked})
	case errors.Is(err, simtype.ErrUnsuitable):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_TYPE_UNSUITABLE", err.Error(), nil)
	case errors.Is(err, simtype.ErrRefused):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_TYPE_REFUSED", err.Error(), nil)
	case errors.Is(err, simpaste.ErrNotDelivered):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_TYPE_NOT_DELIVERED", err.Error(), nil)
	case errors.Is(err, simpaste.ErrNotProven):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SIM_TYPE_NOT_PROVEN", err.Error(), nil)
	default:
		var refused *simsvc.HoldRefusedError
		var held *simsvc.HeldError
		if errors.As(err, &refused) || errors.As(err, &held) || errors.Is(err, simsvc.ErrInvalid) ||
			errors.Is(err, simsvc.ErrNotFound) {
			writeSimError(w, r, err)
			return
		}
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SIM_TYPE_FAILED", err.Error(), nil)
	}
}

// runnerReader reads the screen through the runner, for the proof. It never
// falls back to the accessibility bridge: a proof read by a different reader
// than the one before it would compare two different trees.
type runnerReader struct{ runner SimRunner }

func (r runnerReader) AX(ctx context.Context, udid string) (simbridge.Snapshot, error) {
	h, _, err := r.runner.Read(ctx, udid, 0)
	if err != nil {
		return simbridge.Snapshot{}, err
	}
	snap := simbridge.SnapshotFromXCTest(h)
	if !snap.Usable() {
		return simbridge.Snapshot{}, errors.New("the XCTest runner read an empty screen")
	}
	return snap, nil
}
