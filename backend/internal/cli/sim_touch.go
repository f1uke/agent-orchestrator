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

// gestureHoldSlack is added to a gesture's own duration when asking for the
// hold. The hold has to outlive the gesture, and only just: it is the ceiling
// on how long a command killed mid-gesture keeps the device.
const gestureHoldSlack = 15 * time.Second

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
	Rescued bool   `json:"rescued,omitempty"`
	Note    string `json:"note"`
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
	cmd := &cobra.Command{
		Use:   "tap <x> <y>",
		Short: "Tap a point on a claimed simulator",
		Long: "Tap the screen at a normalized 0..1 coordinate.\n\n" +
			"The coordinates are exactly the `tap` values `ao sim ax` prints for each " +
			"element, so the way to tap something is to read the tree and copy its point - " +
			"never to estimate one from a screenshot.\n\n" +
			"The device must be claimed by this session (`ao sim claim`) first.",
		Example: `  ao sim ax
  ao sim tap 0.5 0.934`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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
	opts.bind(cmd)
	return cmd
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
				action:   "swipe",
				detail:   fmt.Sprintf("(%.3f, %.3f) to (%.3f, %.3f) over %s", from.X, from.Y, to.X, to.Y, duration),
				events:   events,
				last:     to,
				duration: duration,
			})
		},
	}
	cmd.Flags().DurationVar(&duration, "duration", 300*time.Millisecond, "How long the drag takes")
	opts.bind(cmd)
	return cmd
}

func newSimTypeCommand(ctx *commandContext) *cobra.Command {
	opts := simTouchOptions{}
	cmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type text into a claimed simulator's focused field",
		Long: "Send text to whatever has keyboard focus.\n\n" +
			"Tap the field first: this types, it does not choose where the text goes. " +
			"The keys sent are US-keyboard key presses, so anything a US keyboard cannot " +
			"send is refused rather than silently dropped - but the simulator's own active " +
			"input source decides what those key presses produce. If the device is set to a " +
			"non-US keyboard the characters that appear will differ, so read the field back " +
			"with `ao sim ax` rather than assuming.\n\n" +
			"The device must be claimed by this session (`ao sim claim`) first.",
		Example: `  ao sim tap 0.5 0.125
  ao sim type "hello@example.com"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			events, err := simbridge.Type(text)
			if err != nil {
				return usageError{err}
			}
			return ctx.runSimGesture(cmd, opts, simGesture{
				action: "type",
				detail: strconv.Itoa(len([]rune(text))) + " characters",
				events: events,
			})
		},
	}
	opts.bind(cmd)
	return cmd
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
	// last is where the finger would be if the gesture died in flight, and so
	// where a recovery lift has to land.
	last     simbridge.Point
	duration time.Duration
}

// runSimGesture is the one path every touch takes: resolve the device, take the
// hold, run the gesture, give the hold back. There is deliberately no second
// route to the driver.
func (c *commandContext) runSimGesture(cmd *cobra.Command, opts simTouchOptions, gesture simGesture) error {
	ctx := cmd.Context()
	sessionID, err := simSessionID("`ao sim " + gesture.action + "`")
	if err != nil {
		return err
	}
	device, err := c.resolveBootedSimDevice(ctx, opts.udid)
	if err != nil {
		return err
	}
	driver, err := c.simDriver()
	if err != nil {
		return err
	}

	token, err := c.acquireSimHold(ctx, sessionID, device, gesture.duration)
	if err != nil {
		return err
	}
	// The hold is given back on every path out of here, including a panic, so a
	// failed gesture can never leave the device claimed by a command that is
	// already gone.
	defer c.releaseSimHold(ctx, sessionID, device.UDID, token)

	result, performErr := driver.Perform(ctx, device.UDID, gesture.events)
	if performErr != nil {
		return c.recoverSimGesture(ctx, driver, device, gesture, performErr)
	}

	out := simGestureResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Action:            gesture.action,
		Detail:            gesture.detail,
		Rescued:           result.Lifted,
		Note:              simSharedDeviceNote,
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	return writeSimGesture(cmd.OutOrStdout(), out)
}

// recoverSimGesture makes sure a gesture that failed in flight does not leave a
// finger down. A release with nothing held is harmless; a finger left down
// wedges the device until it is rebooted, so the lift is always attempted and
// its outcome is always reported.
func (c *commandContext) recoverSimGesture(ctx context.Context, driver simbridge.Driver, device simDevice, gesture simGesture, cause error) error {
	if !gestureTouches(gesture.events) {
		return fmt.Errorf("`ao sim %s` failed on %s: %w", gesture.action, device.label(), cause)
	}
	if _, liftErr := driver.Perform(ctx, device.UDID, simbridge.Lift(gesture.last)); liftErr != nil {
		return fmt.Errorf("`ao sim %s` failed on %s: %w\n"+
			"The follow-up release ALSO failed (%v), so the device may have a finger held down: "+
			"a stuck touch wedges input until the simulator is rebooted. Check it before using it again",
			gesture.action, device.label(), cause, liftErr)
	}
	return fmt.Errorf("`ao sim %s` failed on %s: %w\n"+
		"The touch was released afterwards, so the device is not left with a finger down. "+
		"Run `ao sim ax` to see where the screen ended up",
		gesture.action, device.label(), cause)
}

func gestureTouches(events []simbridge.Event) bool {
	for _, e := range events {
		if e.Kind == "touch" {
			return true
		}
	}
	return false
}

// acquireSimHold takes the finger for this gesture and turns a refusal into
// advice. The refusal is never something a caller can proceed past: no hold, no
// events.
func (c *commandContext) acquireSimHold(ctx context.Context, sessionID string, device simDevice, gestureDuration time.Duration) (string, error) {
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-leases/" + url.PathEscape(device.UDID) + "/hold"
	body := acquireSimHoldRequest{HoldSeconds: int((gestureDuration + gestureHoldSlack).Seconds())}
	var res simHoldResponse
	if err := c.postJSON(ctx, path, body, &res); err != nil {
		return "", c.explainSimHoldRefusal(device, err)
	}
	return res.Hold.Token, nil
}

// releaseSimHold gives the finger back. A failure here is worth saying out loud
// but must not fail a gesture that already happened: the hold lapses on its own
// within a minute either way.
func (c *commandContext) releaseSimHold(ctx context.Context, sessionID, udid, token string) {
	if token == "" {
		return
	}
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-leases/" + url.PathEscape(udid) +
		"/hold/" + url.PathEscape(token)
	if err := c.deleteJSON(ctx, path, nil); err != nil {
		_, _ = fmt.Fprintf(c.deps.Err, "warning: could not hand the device's gesture hold back (%v); it lapses on its own shortly.\n", err)
	}
}

// explainSimHoldRefusal turns the daemon's 409 into the one sentence that says
// what to do about it. The three reasons need three different answers, which is
// why the daemon reports them apart.
func (c *commandContext) explainSimHoldRefusal(device simDevice, err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "SIM_DEVICE_BUSY" {
		return fmt.Errorf("could not take the gesture hold on %s, so nothing was sent to the device: %w",
			device.label(), err)
	}
	reason, _ := apiErr.ErrorBody.Details["reason"].(string)
	switch reason {
	case "busy":
		return fmt.Errorf("%s is mid-gesture: another command holds the finger right now, and nothing was sent.\n"+
			"Two overlapping gestures merge into one touch on this device, so this command waits for nobody. Retry in a moment",
			device.label())
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
			device.label(), holder, left, holder)
	default:
		return fmt.Errorf("this session has not claimed %s, so nothing was sent to the device.\n"+
			"Run `ao sim claim` first - AO cannot see whether a human is driving the same simulator from Xcode, "+
			"so it never takes a device on your behalf",
			device.label())
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
	if result.Rescued {
		if _, err := fmt.Fprintln(out,
			"Note: the bridge released a touch this gesture left down. The device is fine, but the gesture did not end cleanly."); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "Read the result with `ao sim ax` - never assume a touch changed what you expected.")
	return err
}
