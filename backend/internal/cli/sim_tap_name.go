package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
)

// Tapping the thing you read rather than the number beside it.
//
// `ao sim ax` already prints a tap point per element, so the loop never
// involved coordinate maths - but it still asked a caller to carry a number
// from one command to another, and a number is easy to copy off the wrong line.
// --label and --id take the name that was being read anyway.
//
// What this must never do is turn a name into a tap nobody asked for. Every
// answer that is not exactly one reachable element is a refusal that says what
// to do instead: two controls with one name, a name nothing answers to, an
// element below the fold, a disabled control. Guessing between any of those
// puts a finger somewhere the caller did not choose.

// maxSimTapAlternatives caps the "here is what IS on screen" list. Enough to
// find the name that was meant, short of reprinting the tree.
const maxSimTapAlternatives = 15

// simTapMatch is what a named tap resolved to. It is additive to the shipped
// `--json` shape and absent for a tap by coordinate, which has no name to
// report.
type simTapMatch struct {
	Selector  string  `json:"selector"`
	Kind      string  `json:"kind"`
	MatchedBy string  `json:"matchedBy"`
	Path      string  `json:"path"`
	Label     string  `json:"label,omitempty"`
	ID        string  `json:"id,omitempty"`
	Type      string  `json:"type,omitempty"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
}

// simUnreadableScreenError is the screen coming back with nothing while the
// hold is in hand. It is carried out of the gesture rather than explained on
// the spot: explaining costs a second of sampling the app, and doing that under
// the device's gesture hold would keep the finger from everybody else for no
// reason - nothing is being touched by then.
type simUnreadableScreenError struct{ front simbridge.Frontmost }

func (e *simUnreadableScreenError) Error() string { return "the screen could not be read" }

func (c *commandContext) runSimTapByName(cmd *cobra.Command, opts simTouchOptions, selector simbridge.Selector) error {
	ctx := cmd.Context()
	// Before the machine is asked anything: a caller with no session may not
	// drive a device, and "there is no simulator" is the wrong thing to tell
	// them about.
	sessionID, err := simSessionID("`ao sim tap`")
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

	var matched simbridge.Match
	gesture, _, err := simgesture.RunComposed(ctx,
		&cliSimHolder{ctx: c, sessionID: sessionID, device: device}, driver, device.UDID,
		func(ctx context.Context) (simgesture.Gesture, error) {
			// Under the hold, deliberately: reading first and holding second
			// leaves a window in which another command moves the screen, and
			// the point this one then touches belongs to a screen that is gone.
			snapshot, axErr := driver.AX(ctx, device.UDID)
			if axErr != nil {
				return simgesture.Gesture{}, axErr
			}
			if !snapshot.Usable() {
				return simgesture.Gesture{}, &simUnreadableScreenError{front: snapshot.Frontmost}
			}
			found, selErr := simbridge.Select(snapshot, selector)
			if selErr != nil {
				return simgesture.Gesture{}, explainSimSelect(device, selector, selErr)
			}
			if reachErr := simTapReachable(found.Element); reachErr != nil {
				return simgesture.Gesture{}, reachErr
			}
			matched = found
			events, evErr := simbridge.Tap(*found.Element.Tap)
			if evErr != nil {
				return simgesture.Gesture{}, evErr
			}
			return simgesture.Gesture{
				Action: "tap",
				// The point as well as the name: a caller checking that the
				// tool agreed with them needs the coordinate it acted on, and
				// it is the one to re-run by hand if it did not.
				Detail: fmt.Sprintf("%s at (%.3f, %.3f)",
					simTapTargetLabel(found.Element), found.Element.Tap.X, found.Element.Tap.Y),
				Events: events,
				Last:   *found.Element.Tap,
			}, nil
		})
	if err != nil {
		var unreadable *simUnreadableScreenError
		if errors.As(err, &unreadable) {
			// Outside the hold now: this is where the app itself gets asked
			// whether it can answer at all.
			return c.explainEmptySimTree(ctx, device, unreadable.front)
		}
		return c.explainSimGestureFailure(device, err)
	}

	tap := *matched.Element.Tap
	out := simGestureResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Action:            "tap",
		Detail:            gesture.Detail,
		Note:              simSharedDeviceNote,
		Target: &simTapMatch{
			Selector:  strings.TrimSpace(selector.Text),
			Kind:      string(selector.Kind),
			MatchedBy: string(matched.How),
			Path:      matched.Element.Path,
			Label:     matched.Element.Label,
			ID:        matched.Element.ID,
			Type:      matched.Element.Type,
			X:         tap.X,
			Y:         tap.Y,
		},
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	return writeSimGesture(cmd.OutOrStdout(), out)
}

// simTapReachable refuses the two elements that exist but must not be tapped.
// Both would otherwise report success and change nothing, which is the failure
// this whole command set is built to avoid.
func simTapReachable(e simbridge.Element) error {
	if e.Tap == nil {
		var b strings.Builder
		fmt.Fprintf(&b, "%s is off screen, so there is nowhere to touch it.", simTapTargetLabel(e))
		if e.Box != nil {
			fmt.Fprintf(&b, "\nIts edges are %.3f,%.3f->%.3f,%.3f in the same units as a tap point, so it is %.2f screens down.",
				e.Box.X1, e.Box.Y1, e.Box.X2, e.Box.Y2, e.Box.Y1-1)
		}
		b.WriteString("\nScroll it into view with `ao sim drag 0.5 0.8 0.5 0.4`, read again with `ao sim ax`, then tap.")
		return errors.New(b.String())
	}
	if !e.Enabled {
		return fmt.Errorf("%s is disabled, so tapping it would report success and change nothing.\n"+
			"If the app is wrong about that, tap the point itself: `ao sim tap %.3f %.3f`",
			simTapTargetLabel(e), e.Tap.X, e.Tap.Y)
	}
	return nil
}

// explainSimSelect phrases the two ways a name fails to pick one element. Both
// refuse; neither is a dead end.
func explainSimSelect(device simDevice, selector simbridge.Selector, err error) error {
	var ambiguous *simbridge.AmbiguousMatchError
	var missing *simbridge.NoMatchError
	switch {
	case errors.As(err, &ambiguous):
		var b strings.Builder
		fmt.Fprintf(&b, "%d elements on %s answer to %s, so there is no unambiguous target and nothing was tapped. Re-run with one of:",
			len(ambiguous.Matches), device.Label(), selector)
		for _, e := range ambiguous.Matches {
			if e.Tap == nil {
				fmt.Fprintf(&b, "\n  (off screen)              # %s  [%s]", simTapTargetLabel(e), e.Path)
				continue
			}
			fmt.Fprintf(&b, "\n  ao sim tap %.3f %.3f   # %s  [%s]", e.Tap.X, e.Tap.Y, simTapTargetLabel(e), e.Path)
		}
		return errors.New(b.String())
	case errors.As(err, &missing):
		var b strings.Builder
		fmt.Fprintf(&b, "nothing on %s answers to %s, so nothing was tapped.", device.Label(), selector)
		if len(missing.OnScreen) == 0 {
			b.WriteString("\nNothing on this screen carries a name at all. Read it with `ao sim ax`.")
			return errors.New(b.String())
		}
		b.WriteString("\nThese elements can be tapped right now:")
		for i, e := range missing.OnScreen {
			if i == maxSimTapAlternatives {
				fmt.Fprintf(&b, "\n  ... and %d more - read them all with `ao sim ax`", len(missing.OnScreen)-i)
				break
			}
			fmt.Fprintf(&b, "\n  %s  tap %.3f %.3f  [%s]", simTapTargetLabel(e), e.Tap.X, e.Tap.Y, e.Path)
		}
		return errors.New(b.String())
	case errors.Is(err, simbridge.ErrEmptySelector):
		return usageError{err}
	default:
		return err
	}
}

// simTapTargetLabel names an element the way `ao sim ax` prints it, so what a
// message says matches the line the caller read it from.
func simTapTargetLabel(e simbridge.Element) string {
	name := e.Label
	if name == "" {
		name = e.Value
	}
	switch {
	case name != "" && e.Type != "":
		return fmt.Sprintf("%s %q", e.Type, name)
	case name != "":
		return fmt.Sprintf("%q", name)
	case e.ID != "":
		return fmt.Sprintf("%s (id %s)", e.Type, e.ID)
	default:
		return e.Type
	}
}
