package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
)

// The commands that touch the screen.
//
// Every one of them holds two things for the whole operation and gives both
// back afterwards:
//
//   - the device LEASE, which keeps other AO sessions off the simulator;
//   - the gesture HOLD, which keeps any second command - including another of
//     this session's own - from starting a gesture while this one is running.
//
// Both are needed. The device has one finger and no notion of who is touching
// it: two overlapping gestures do not queue, they merge into a single touch
// that teleports, and the first release lifts the other's finger. A gesture
// that loses its release wedges input until the device is rebooted, which on a
// shared machine breaks whoever else is using that simulator.
//
// This is also why a touch command REFUSES when the daemon is unreachable,
// where the read-only commands degrade: without the daemon there is no hold,
// and without a hold there is nothing to stop two gestures merging.

// simGestureResult is the `--json` payload of every touch command.
type simGestureResult struct {
	UDID              string `json:"udid"`
	Name              string `json:"name"`
	Runtime           string `json:"runtime"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	Action            string `json:"action"`
	Detail            string `json:"detail"`
	// Rescued: the bridge had to release a touch the gesture left down. The
	// command succeeded, but the device was recovered rather than driven
	// cleanly, and that is never hidden.
	Rescued bool `json:"rescued,omitempty"`
	// Keyboard is the simulator input mode `type` verified before sending
	// anything, e.g. "en_US@sw=QWERTY;hw=Automatic". Empty for the gestures that
	// do not depend on one, and for `--raw-keys`, which promises nothing about
	// what the key presses become.
	Keyboard string `json:"keyboard,omitempty"`
	// Route is why the pasteboard was used instead of key presses. Empty when
	// the text was typed.
	Route string `json:"route,omitempty"`
	// Target is what a tap by name resolved to. Absent for a tap by
	// coordinate, which has no name to report - the shipped shape is unchanged
	// for the form that shipped with it.
	Target *simTapMatch `json:"target,omitempty"`
	// PasteboardLeftBehind: the guest pasteboard could not be put back, so the
	// text is still on it where any app on the device can read it. Never hidden:
	// the text is a password often enough that it has to be said out loud.
	PasteboardLeftBehind bool   `json:"pasteboardLeftBehind,omitempty"`
	Note                 string `json:"note"`
}

// acquireSimHoldRequest mirrors controllers.AcquireSimHoldInput.
type acquireSimHoldRequest struct {
	HoldSeconds int `json:"holdSeconds,omitempty"`
}

// simHoldResponse mirrors controllers.SimHoldResponse.
type simHoldResponse struct {
	Hold struct {
		UDID      string    `json:"udid"`
		SessionID string    `json:"sessionId"`
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expiresAt"`
	} `json:"hold"`
}

func newSimTapCommand(ctx *commandContext) *cobra.Command {
	opts := simTouchOptions{}
	target := simTapTarget{}
	cmd := &cobra.Command{
		Use:   "tap <x> <y> | --label <name> | --id <identifier>",
		Short: "Tap a point, or the element with a given name, on a claimed simulator",
		Long: "Tap the screen at a normalized 0..1 coordinate, or name the element to tap.\n\n" +
			"The coordinates are exactly the `tap` values `ao sim ax` prints for each " +
			"element, so the way to tap something is to read the tree and copy its point - " +
			"never to estimate one from a screenshot.\n\n" +
			"--label and --id skip the copying: they take the name `ao sim ax` shows for an " +
			"element (its label, or its value when it has none) and its accessibility " +
			"identifier. The screen is read first, so this costs one accessibility read - and " +
			"replaces the `ao sim ax` you would have run anyway. Matching ignores case, prefers " +
			"an exact name, and falls back to a name that CONTAINS the text, saying which it " +
			"did. Two different elements answering to one name is refused rather than guessed, " +
			"and so is an element that is off screen or disabled - each with what to do about it.\n\n" +
			"The device must be claimed by this session (`ao sim claim`) first.",
		Example: `  ao sim ax
  ao sim tap 0.5 0.934
  ao sim tap --label "Continue"
  ao sim tap --id sign-in-button`,
		Args: func(_ *cobra.Command, args []string) error { return target.validate(args) },
		RunE: func(cmd *cobra.Command, args []string) error {
			if selector, named := target.selector(); named {
				return ctx.runSimTapByName(cmd, opts, selector)
			}
			at, err := parseSimPoint(args[0], args[1])
			if err != nil {
				return err
			}
			events, err := simbridge.Tap(at)
			if err != nil {
				return usageError{err}
			}
			return ctx.runSimGesture(cmd, opts, simGesture{
				action: "tap",
				detail: fmt.Sprintf("(%.3f, %.3f)", at.X, at.Y),
				events: events,
				last:   at,
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&target.label, "label", "", "Tap the element with this name instead of a point")
	f.StringVar(&target.id, "id", "", "Tap the element with this accessibility identifier instead of a point")
	opts.bind(cmd)
	return cmd
}

// simTapTarget is the two ways of naming what to tap. They are flags rather
// than a positional argument so a name can never be mistaken for a coordinate,
// or the other way round.
type simTapTarget struct {
	label string
	id    string
}

func (t simTapTarget) selector() (simbridge.Selector, bool) {
	switch {
	case strings.TrimSpace(t.label) != "":
		return simbridge.Selector{Kind: simbridge.SelectByLabel, Text: t.label}, true
	case strings.TrimSpace(t.id) != "":
		return simbridge.Selector{Kind: simbridge.SelectByID, Text: t.id}, true
	default:
		return simbridge.Selector{}, false
	}
}

// validate refuses every way of asking for two targets at once, or none. All of
// it is CLI misuse (exit 2) and is caught before the device is approached.
func (t simTapTarget) validate(args []string) error {
	named := t.label != "" || t.id != ""
	switch {
	case t.label != "" && t.id != "":
		return usageError{errors.New("--label and --id name the same tap two different ways; pass one")}
	case named && len(args) > 0:
		return usageError{fmt.Errorf("--label/--id name what to tap, so there is no coordinate to give as well - got %d", len(args))}
	case named && strings.TrimSpace(t.label+t.id) == "":
		return usageError{errors.New("--label/--id need something to match; an empty name matches every element on the screen")}
	case !named && len(args) != 2:
		return usageError{fmt.Errorf("`ao sim tap` takes an x and a y, or --label/--id - got %d argument(s)", len(args))}
	}
	return nil
}

func newSimSwipeCommand(ctx *commandContext) *cobra.Command {
	opts := simTouchOptions{}
	var duration time.Duration
	cmd := &cobra.Command{
		Use:   "swipe <x1> <y1> <x2> <y2>",
		Short: "Swipe between two points on a claimed simulator",
		Long: "Drag from one normalized 0..1 point to another - how you scroll a list or " +
			"dismiss a sheet so the rest of a screen can be read.\n\n" +
			"The device must be claimed by this session (`ao sim claim`) first.",
		Example: `  ao sim swipe 0.5 0.8 0.5 0.2
  ao sim swipe 0.5 0.8 0.5 0.2 --duration 800ms`,
		Args: cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			from, err := parseSimPoint(args[0], args[1])
			if err != nil {
				return err
			}
			to, err := parseSimPoint(args[2], args[3])
			if err != nil {
				return err
			}
			events, err := simbridge.Swipe(from, to, duration)
			if err != nil {
				return usageError{err}
			}
			return ctx.runSimGesture(cmd, opts, simGesture{
				action: "swipe",
				detail: fmt.Sprintf("(%.3f, %.3f) to (%.3f, %.3f) over %s", from.X, from.Y, to.X, to.Y, duration),
				events: events,
				last:   to,
			})
		},
	}
	cmd.Flags().DurationVar(&duration, "duration", 300*time.Millisecond, "How long the drag takes")
	opts.bind(cmd)
	return cmd
}

func newSimDragCommand(ctx *commandContext) *cobra.Command {
	opts := simTouchOptions{}
	var duration time.Duration
	cmd := &cobra.Command{
		Use:   "drag <x1> <y1> <x2> <y2> [<x3> <y3> ...]",
		Short: "Drag through a route of points on a claimed simulator, without lifting",
		Long: "Hold one finger down and move it through every point given, in order.\n\n" +
			"This is the gesture a human makes by holding the pointer down in the desktop " +
			"app's Device tab and moving it: the finger stays down for the whole route, so " +
			"an app sees one drag. Sending the same route as separate `swipe` commands lifts " +
			"between them, which reads as several flicks instead - the difference between " +
			"dragging something onto a target and throwing it three times.\n\n" +
			"Two points is a swipe. The coordinates are the same normalized 0..1 values " +
			"`ao sim ax` prints for each element.\n\n" +
			"The device must be claimed by this session (`ao sim claim`) first.",
		Example: `  ao sim drag 0.5 0.8 0.5 0.2
  ao sim drag 0.5 0.8 0.5 0.5 0.2 0.5 --duration 1s`,
		// The arity rule is "pairs, at least two of them", which cobra has no
		// matcher for - and getting it wrong is a mistyped coordinate, so it is
		// refused as a usage error (exit 2) rather than a failure of the run.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) < 4 || len(args)%2 != 0 {
				return usageError{fmt.Errorf("a drag takes pairs of coordinates - at least a start and an end - "+
					"got %d value(s)", len(args))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			points := make([]simbridge.Point, 0, len(args)/2)
			for i := 0; i < len(args); i += 2 {
				at, err := parseSimPoint(args[i], args[i+1])
				if err != nil {
					return err
				}
				points = append(points, at)
			}
			events, err := simbridge.Path(points, duration)
			if err != nil {
				return usageError{err}
			}
			last := points[len(points)-1]
			return ctx.runSimGesture(cmd, opts, simGesture{
				action: "drag",
				detail: fmt.Sprintf("%d points ending at (%.3f, %.3f) over %s", len(points), last.X, last.Y, duration),
				events: events,
				last:   last,
			})
		},
	}
	cmd.Flags().DurationVar(&duration, "duration", 600*time.Millisecond, "How long the whole route takes")
	opts.bind(cmd)
	return cmd
}

func newSimTypeCommand(ctx *commandContext) *cobra.Command {
	opts := simTouchOptions{}
	var rawKeys, forcePaste bool
	cmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type text into a claimed simulator's focused field",
		Long: "Put text into whatever has keyboard focus.\n\n" +
			"Tap the field first: this types, it does not choose where the text goes.\n\n" +
			"HOW IT GETS THERE depends on the simulator, and the command says which route it " +
			"took. Key presses are used wherever they can be trusted, because an app that " +
			"watches each keystroke - a live validator, a character counter, a masked field - " +
			"then sees what a person would. But the keys sent are US-keyboard key presses and " +
			"the SIMULATOR decides what each one produces: Simulator.app ships with I/O > " +
			"Keyboard > \"Use the Same Keyboard Language as macOS\" ticked, so a Mac on a Thai " +
			"input source makes `type \"fa12345\"` arrive as \"\u0e14\u0e1f\u0e45/_\u0e20\u0e16\". " +
			"Where that would happen the text goes through the simulator's PASTEBOARD instead, " +
			"and is checked on screen afterwards - so the characters asked for are the " +
			"characters that arrive, including in a secure field, where nothing can be read " +
			"back to catch a mistake.\n\n" +
			"Non-ASCII text also goes by pasteboard, because no US keyboard key can send it.\n\n" +
			"`--paste` always uses the pasteboard. `--raw-keys` always sends key presses and " +
			"promises only key presses, which is how Thai text is deliberately entered on a " +
			"Thai guest.\n\n" +
			"The device must be claimed by this session (`ao sim claim`) first.",
		Example: `  ao sim tap 0.5 0.125
  ao sim type "hello@example.com"
  ao sim type "fa12345" --raw-keys`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			// Checked here as well as in the shared sequence, and before the
			// device is approached at all: a caller with no session is not
			// allowed to drive anything, and asking the guest what keyboard it
			// has would spawn a process on a device this command may not use.
			// runSimGestureOn remains the enforcement point for every gesture.
			if _, err := simSessionID("`ao sim type`"); err != nil {
				return err
			}
			// The device is resolved here rather than inside the shared
			// sequence because what may be typed depends on which device it is.
			device, err := ctx.resolveBootedSimDevice(cmd.Context(), opts.udid)
			if err != nil {
				return err
			}
			route, err := ctx.planSimType(cmd.Context(), device, text, rawKeys, forcePaste)
			if err != nil {
				return err
			}
			if route.Paste {
				return ctx.runSimPaste(cmd, opts, device, text, route)
			}
			runes := strconv.Itoa(len([]rune(text)))
			detail := runes + " characters"
			if rawKeys {
				detail = runes + " key presses (--raw-keys: the simulator decides what they produce)"
			}
			return ctx.runSimGestureOn(cmd, opts, device, simGesture{
				action:   "type",
				detail:   detail,
				events:   route.Events,
				keyboard: route.Keyboard.Identifier,
			})
		},
	}
	cmd.Flags().BoolVar(&rawKeys, "raw-keys", false,
		"Send the key presses even when the simulator would turn them into other characters")
	cmd.Flags().BoolVar(&forcePaste, "paste", false,
		"Always deliver the text through the simulator's pasteboard rather than as key presses")
	opts.bind(cmd)
	return cmd
}

// planSimType asks the device what it will do with key presses, then hands the
// decision to internal/simbridge, which is where it lives for both surfaces.
//
// The probe is the only part that differs between a CLI and the daemon: one
// shells out, the other has a Screen. The DECISION must not differ, because two
// answers to "is this keyboard safe" means one of them silently types the wrong
// characters again - the exact bug being fixed.
func (c *commandContext) planSimType(ctx context.Context, device simDevice, text string, rawKeys, forcePaste bool) (
	simbridge.TextRoute, error,
) {
	var keyboard simbridge.ProbedKeyboard
	// Nothing to establish when the caller has already said which route they
	// want, and the probe costs a subprocess on the device.
	if !rawKeys && !forcePaste {
		keyboard.Mode, keyboard.Err = simkeyboard.Probe(ctx, c.deps.CommandOutput, device.UDID)
	}
	route, err := simbridge.PlanText(text, keyboard, simbridge.TextOptions{RawKeys: rawKeys, Paste: forcePaste})
	if err != nil {
		return simbridge.TextRoute{}, usageError{err}
	}
	return route, nil
}

// runSimPaste delivers the text through the guest pasteboard, under the same
// gesture hold every other way of touching this device takes.
func (c *commandContext) runSimPaste(
	cmd *cobra.Command, opts simTouchOptions, device simDevice, text string, route simbridge.TextRoute,
) error {
	ctx := cmd.Context()
	sessionID, err := simSessionID("`ao sim type`")
	if err != nil {
		return err
	}
	driver, err := c.simDriver()
	if err != nil {
		return err
	}

	result, err := simpaste.Run(ctx, &cliSimHolder{ctx: c, sessionID: sessionID, device: device}, driver,
		simpaste.Simctl{Run: c.deps.CommandOutput}, device.UDID, text)
	if err != nil {
		return c.explainSimPasteFailure(ctx, device, route, err)
	}

	out := simGestureResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Action:            "paste",
		Detail:            strconv.Itoa(len([]rune(text))) + " characters",
		Keyboard:          route.Keyboard.Identifier,
		Route:             route.Why,
		Note:              simSharedDeviceNote,
	}
	if !result.Restored {
		out.PasteboardLeftBehind = true
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	return writeSimPaste(cmd.OutOrStdout(), out, result.RestoreErr)
}

// explainSimPasteFailure says what went wrong AND what state the field is in,
// because those are different questions and the answer to the second decides
// whether there is anything to clean up.
func (c *commandContext) explainSimPasteFailure(
	ctx context.Context, device simDevice, route simbridge.TextRoute, err error,
) error {
	if errors.Is(err, simpaste.ErrNotDelivered) {
		// "Nothing on screen changed" is the one place a gesture command knows
		// for a fact that it changed nothing, and an app that cannot answer at
		// all looks exactly like a field that never had focus. Ask before
		// sending the caller to check the field.
		if note := c.blockedSimAppNote(ctx, device); note != "" {
			return fmt.Errorf("nothing was typed into %s, and the field is not the reason.\n%s",
				device.Label(), note)
		}
		return fmt.Errorf("nothing was typed into %s (%s, so the text was sent through the pasteboard): %w\n"+
			"Tap the field first with `ao sim tap` so it has keyboard focus. Some apps refuse paste outright - "+
			"for those, fix the simulator's input mode and use key presses instead",
			device.Label(), route.Why, err)
	}
	return fmt.Errorf("`ao sim type` failed on %s (%s, so the text was sent through the pasteboard): %w",
		device.Label(), route.Why, err)
}

func newSimButtonCommand(ctx *commandContext) *cobra.Command {
	opts := simTouchOptions{}
	cmd := &cobra.Command{
		Use:   "button <name>",
		Short: "Press a hardware button on a claimed simulator",
		Long: "Press one of the device's buttons: " + strings.Join(simbridge.ButtonNames(), ", ") + ".\n\n" +
			"`home` performs the swipe-up home gesture - the way back to a known screen when " +
			"an app has wandered somewhere unexpected. The list is deliberately short: only " +
			"buttons observed to change a real device are offered, because the mechanism " +
			"reports success for ones that do nothing at all.\n\n" +
			"The device must be claimed by this session (`ao sim claim`) first.",
		Example: `  ao sim button home
  ao sim button app-switcher`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			events, err := simbridge.Button(args[0])
			if err != nil {
				return usageError{err}
			}
			return ctx.runSimGesture(cmd, opts, simGesture{action: "button", detail: args[0], events: events})
		},
	}
	opts.bind(cmd)
	return cmd
}

type simTouchOptions struct {
	udid string
	json bool
}

func (o *simTouchOptions) bind(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&o.udid, "udid", "", "Drive this simulator instead of the booted one")
	f.BoolVar(&o.json, "json", false, "Output the result as JSON")
}

// simGesture is one composed gesture, ready to run.
type simGesture struct {
	action string
	detail string
	events []simbridge.Event
	// keyboard is the simulator input mode `type` established before composing
	// these events. Empty for every other gesture, none of which depends on one.
	keyboard string
	// last is where the finger would be if the gesture died in flight, and so
	// where a recovery lift has to land.
	last simbridge.Point
}

// runSimGesture is the one path every touch takes: resolve the device, then
// hand the gesture to the shared sequence that brackets it with a hold. There
// is deliberately no second route to the driver - and the desktop app's
// Simulator tab goes through the same sequence from the daemon side, so a click
// there is arbitrated exactly like this command.
func (c *commandContext) runSimGesture(cmd *cobra.Command, opts simTouchOptions, gesture simGesture) error {
	// Before the machine is asked anything, for the same reason `ao sim type`
	// checks first: a caller with no session may not drive a device, and
	// "there is no simulator" is the wrong thing to tell them about.
	if _, err := simSessionID("`ao sim " + gesture.action + "`"); err != nil {
		return err
	}
	device, err := c.resolveBootedSimDevice(cmd.Context(), opts.udid)
	if err != nil {
		return err
	}
	return c.runSimGestureOn(cmd, opts, device, gesture)
}

// runSimGestureOn is runSimGesture for a device the caller has already
// resolved. `type` needs that split: it has to ask the device which keyboard it
// is using before it knows whether there is a gesture to run at all, and
// resolving the device twice would pay for a second `simctl list` - the single
// most expensive thing in a touch.
func (c *commandContext) runSimGestureOn(
	cmd *cobra.Command, opts simTouchOptions, device simDevice, gesture simGesture,
) error {
	ctx := cmd.Context()
	sessionID, err := simSessionID("`ao sim " + gesture.action + "`")
	if err != nil {
		return err
	}
	driver, err := c.simDriver()
	if err != nil {
		return err
	}

	result, err := simgesture.Run(ctx, &cliSimHolder{ctx: c, sessionID: sessionID, device: device}, driver, device.UDID,
		simgesture.Gesture{Action: gesture.action, Detail: gesture.detail, Events: gesture.events, Last: gesture.last})
	if err != nil {
		return c.explainSimGestureFailure(device, err)
	}

	out := simGestureResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Action:            gesture.action,
		Detail:            gesture.detail,
		Rescued:           result.Lifted,
		Keyboard:          gesture.keyboard,
		Note:              simSharedDeviceNote,
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	return writeSimGesture(cmd.OutOrStdout(), out)
}

// explainSimGestureFailure phrases the shared sequence's outcome for a person
// reading a terminal. What it may never do is lose the distinction the sequence
// draws: a gesture whose recovery release also failed leaves a device that may
// be wedged, and that has to be the loudest thing on screen.
func (c *commandContext) explainSimGestureFailure(device simDevice, err error) error {
	var failed *simgesture.FailedError
	if !errors.As(err, &failed) {
		return err
	}
	if !failed.Lifted {
		return fmt.Errorf("`ao sim %s` failed on %s: %w", failed.Action, device.Label(), failed.Cause)
	}
	if failed.LiftErr != nil {
		return fmt.Errorf("`ao sim %s` failed on %s: %w\n"+
			"The follow-up release ALSO failed (%v), so the device may have a finger held down: "+
			"a stuck touch wedges input until the simulator is rebooted. Check it before using it again",
			failed.Action, device.Label(), failed.Cause, failed.LiftErr)
	}
	return fmt.Errorf("`ao sim %s` failed on %s: %w\n"+
		"The touch was released afterwards, so the device is not left with a finger down. "+
		"Run `ao sim ax` to see where the screen ended up",
		failed.Action, device.Label(), failed.Cause)
}

// cliSimHolder takes the gesture hold the way a CLI must: over the daemon's
// HTTP API, because the CLI is a thin client and the hold lives in the daemon's
// database. A refusal is turned into advice here rather than in the shared
// sequence, which has no business knowing about terminals.
type cliSimHolder struct {
	ctx       *commandContext
	sessionID string
	device    simDevice
}

func (h *cliSimHolder) Acquire(ctx context.Context, udid string, ttl time.Duration) (string, error) {
	path := "sessions/" + url.PathEscape(h.sessionID) + "/sim-leases/" + url.PathEscape(udid) + "/hold"
	body := acquireSimHoldRequest{HoldSeconds: int(ttl.Seconds())}
	var res simHoldResponse
	if err := h.ctx.postJSON(ctx, path, body, &res); err != nil {
		return "", h.ctx.explainSimHoldRefusal(h.device, err)
	}
	return res.Hold.Token, nil
}

// Release gives the finger back. A failure here is worth saying out loud but
// must not fail a gesture that already happened: the hold lapses on its own
// within a minute either way.
//
// performed is passed on as the `performed` query parameter, which the daemon
// defaults to true when it is absent - so it is only sent when false, keeping
// the common case's request unchanged.
func (h *cliSimHolder) Release(ctx context.Context, udid, token string, performed bool) {
	if token == "" {
		return
	}
	path := "sessions/" + url.PathEscape(h.sessionID) + "/sim-leases/" + url.PathEscape(udid) +
		"/hold/" + url.PathEscape(token)
	if !performed {
		path += "?performed=false"
	}
	if err := h.ctx.deleteJSON(ctx, path, nil); err != nil {
		_, _ = fmt.Fprintf(h.ctx.deps.Err, "warning: could not hand the device's gesture hold back (%v); it lapses on its own shortly.\n", err)
	}
}

// explainSimHoldRefusal turns the daemon's 409 into the one sentence that says
// what to do about it. The three reasons need three different answers, which is
// why the daemon reports them apart.
func (c *commandContext) explainSimHoldRefusal(device simDevice, err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "SIM_DEVICE_BUSY" {
		return fmt.Errorf("could not take the gesture hold on %s, so nothing was sent to the device: %w",
			device.Label(), err)
	}
	reason, _ := apiErr.ErrorBody.Details["reason"].(string)
	switch reason {
	case "busy":
		return fmt.Errorf("%s is mid-gesture: another command holds the finger right now, and nothing was sent.\n"+
			"Two overlapping gestures merge into one touch on this device, so this command waits for nobody. Retry in a moment",
			device.Label())
	case "leased_by_other":
		holder, _ := apiErr.ErrorBody.Details["holder"].(string)
		left := ""
		if raw, ok := apiErr.ErrorBody.Details["expiresAt"].(string); ok {
			if expiresAt, parseErr := time.Parse(time.RFC3339, raw); parseErr == nil {
				expiresAt = expiresAt.UTC()
				left = fmt.Sprintf(" for another %s", simRemaining(&expiresAt, c.deps.Now().UTC()))
			}
		}
		return fmt.Errorf("%s is leased by @%s%s, so nothing was sent to the device.\n"+
			"`ao sim ax` and `ao sim shot` are read-only and still work. Wait for the lease to lapse, or ask @%s to run `ao sim release`",
			device.Label(), holder, left, holder)
	default:
		return fmt.Errorf("this session has not claimed %s, so nothing was sent to the device.\n"+
			"Run `ao sim claim` first - AO cannot see whether a human is driving the same simulator from Xcode, "+
			"so it never takes a device on your behalf",
			device.Label())
	}
}

// parseSimPoint reads one coordinate pair. Bad input is CLI misuse (exit 2) and
// is caught before anything reaches the device.
func parseSimPoint(rawX, rawY string) (simbridge.Point, error) {
	x, err := strconv.ParseFloat(strings.TrimSpace(rawX), 64)
	if err != nil {
		return simbridge.Point{}, usageError{fmt.Errorf("invalid x coordinate %q: use a number between 0 and 1", rawX)}
	}
	y, err := strconv.ParseFloat(strings.TrimSpace(rawY), 64)
	if err != nil {
		return simbridge.Point{}, usageError{fmt.Errorf("invalid y coordinate %q: use a number between 0 and 1", rawY)}
	}
	return simbridge.Point{X: x, Y: y}, nil
}

// writeSimPaste reports a paste. It says PASTED rather than typed, and says why
// that route was taken: an app that reacts to each keystroke behaves differently
// when it receives one paste instead, and a caller who is debugging that needs
// to know which of the two happened without having to guess.
func writeSimPaste(out io.Writer, result simGestureResult, restoreErr error) error {
	if _, err := fmt.Fprintf(out, "Pasted %s into %s (%s, %s)\n%s, so the text went through the simulator's "+
		"pasteboard rather than its keyboard, and was checked on screen afterwards.\n",
		result.Detail, result.Name, result.Runtime, result.UDID,
		strings.ToUpper(result.Route[:1])+result.Route[1:]); err != nil {
		return err
	}
	if result.PasteboardLeftBehind {
		if _, err := fmt.Fprintf(out, "WARNING: the simulator's pasteboard could NOT be put back (%v), so the "+
			"text is still on it and any app on that device can read it. Clear it before moving on.\n",
			restoreErr); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out,
		"An app that reacts to each keystroke sees one paste instead - use `--raw-keys` if it needs real key presses.")
	return err
}

func writeSimGesture(out io.Writer, result simGestureResult) error {
	verb := map[string]string{
		"tap":    "Tapped",
		"swipe":  "Swiped",
		"type":   "Typed",
		"button": "Pressed",
	}[result.Action]
	if _, err := fmt.Fprintf(out, "%s %s on %s (%s, %s)\n",
		verb, result.Detail, result.Name, result.Runtime, result.UDID); err != nil {
		return err
	}
	if err := writeSimTapMatch(out, result.Target); err != nil {
		return err
	}
	if result.Rescued {
		if _, err := fmt.Fprintln(out,
			"Note: the bridge released a touch this gesture left down. The device is fine, but the gesture did not end cleanly."); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "Read the result with `ao sim ax` - never assume a touch changed what you expected.")
	return err
}

// writeSimTapMatch says how the name became this element. A contains-match is
// the tool's inference rather than the name the caller gave, and reading
// "Tapped ..." without that distinction is how a caller concludes the thing
// they named was found.
func writeSimTapMatch(out io.Writer, target *simTapMatch) error {
	if target == nil {
		return nil
	}
	if target.MatchedBy == string(simbridge.MatchExact) {
		_, err := fmt.Fprintf(out, "Matched the %s %q exactly  [%s]\n", target.Kind, target.Selector, target.Path)
		return err
	}
	_, err := fmt.Fprintf(out, "No element has the %s %q exactly; this was the only one containing it  [%s]\n",
		target.Kind, target.Selector, target.Path)
	return err
}
