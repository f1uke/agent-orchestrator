package simgesture

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// Keyboard is what was done about the software keyboard after a gesture that
// pressed hardware keys - which minimize it for every field after, not just
// this one (see simbridge.ShowKeyboard). A caller reports it: the keyboard is
// the device's, and the next command on it depends on whether it comes up.
type Keyboard struct {
	// Shown: the toggle that brings it back was sent after the keys.
	Shown bool
	// Seen: a field had keyboard focus afterwards and the keyboard was on
	// screen with it.
	Seen bool
	// Unseen is why it could not be looked at, when it could not: nothing
	// has keyboard focus, or the reader cannot see the keyboard. Shown is
	// then the whole of what is known.
	Unseen string
	// Problem is a keyboard that was looked at and is still not on screen,
	// after a second toggle. The next field tapped will have no keyboard.
	Problem string
}

// String says what happened to the keyboard, for a caller to print as it is.
func (k Keyboard) String() string {
	switch {
	case k.Problem != "":
		return "WARNING: " + k.Problem
	case k.Seen && k.Shown:
		return "the key presses minimized the on-screen keyboard (a hardware key press does), and it was shown " +
			"again - it is on screen with the focused field"
	case k.Seen:
		return "the on-screen keyboard is still up with the focused field"
	case k.Shown:
		return "the key presses minimized the on-screen keyboard (a hardware key press does), and it was shown " +
			"again; " + k.Unseen + ", so the next field tapped is where it shows"
	}
	return ""
}

// keyboardSettle is how long the device is given after the key presses, and
// after a toggle, before the screen is read. The toggle travels apart from
// the key events and is not ordered after them: sent straight after a key it
// was seen to land FIRST (the key then minimized the keyboard it had just
// shown) and to swallow the key outright, so it is never sent until the keys
// have had their effect.
const keyboardSettle = 500 * time.Millisecond

// keyboardRead is the allowance for each read that looks at the keyboard.
// Less than a ScreenRead: only the XCTest reader can see the keyboard, and it
// reads in 0.05-0.3 s; a slower reader is not looked through a second time.
// It is also what keeps a paste that falls back to Command-V inside the
// gesture hold's one-minute ceiling.
const keyboardRead = 5 * time.Second

// KeyboardCheck is the hold ShowKeyboardAfter needs on top of the gesture: two
// reads and two settles.
const KeyboardCheck = 2*keyboardRead + 2*keyboardSettle

// ShowKeyboardAfter brings the software keyboard back after events that
// pressed hardware keys, and says what it did. Events that pressed no key
// that minimizes it answer nil.
//
// Where a field has focus the keyboard can be SEEN, and it is toggled only if
// it is seen minimized, then looked at again. Where nothing has focus there is
// nothing to see, and the toggle is sent on what is known about iOS rather
// than on a reading - a key that is not a modifier always leaves the keyboard
// minimized, so one toggle brings it back - and that is said rather than
// claimed.
func ShowKeyboardAfter(ctx context.Context, driver simbridge.Driver, udid string, events []simbridge.Event) *Keyboard {
	if !simbridge.MinimizesKeyboard(events) {
		return nil
	}
	result := &Keyboard{}
	snap, unseen := lookAtKeyboard(ctx, driver, udid)
	if unseen == "" && snap.Keyboard != nil {
		result.Seen = true
		return result
	}
	if _, err := driver.Perform(ctx, udid, simbridge.ShowKeyboard()); err != nil {
		result.Problem = fmt.Sprintf("the hardware key presses minimized the on-screen keyboard, and showing it "+
			"again failed (%v) - the next field tapped will have no keyboard until it is shown", err)
		return result
	}
	result.Shown = true
	if unseen != "" {
		result.Unseen = unseen
		return result
	}
	snap, unseen = lookAtKeyboard(ctx, driver, udid)
	switch {
	case unseen != "":
		result.Unseen = unseen
	case snap.Keyboard != nil:
		result.Seen = true
	default:
		result.Problem = "the hardware key presses minimized the on-screen keyboard, and it is still minimized " +
			"after it was told to show - the next field tapped will have no keyboard until it is shown"
	}
	return result
}

// lookAtKeyboard reads the screen after the keyboard has settled, and says
// why the keyboard cannot be judged from it when it cannot.
func lookAtKeyboard(ctx context.Context, driver simbridge.Driver, udid string) (simbridge.Snapshot, string) {
	select {
	case <-time.After(keyboardSettle):
	case <-ctx.Done():
		return simbridge.Snapshot{}, "the command ended before the screen could be read"
	}
	snap, err := driver.AX(ctx, udid)
	switch {
	case err != nil:
		return snap, fmt.Sprintf("the screen could not be read to check it (%v)", err)
	case snap.Reader == nil || snap.Reader.Source != simbridge.SourceXCTest:
		// Only the XCTest reader sees the keyboard and which field has focus;
		// the accessibility bridge sees the frontmost app alone.
		return snap, "the screen was read without the XCTest runner, which is what sees the keyboard"
	case !anyElement(snap.Elements, func(e simbridge.Element) bool { return e.Focused }):
		return snap, "no field has keyboard focus"
	case snap.Keyboard == nil && !anyElement(snap.Elements, func(e simbridge.Element) bool { return e.Type == "Keyboard" }):
		// A minimized keyboard is still in the tree, below the screen. A
		// focused element with no keyboard anywhere has an input view of its
		// own (a picker), and toggling there would minimize the real one.
		return snap, "the focused field takes its input from something other than the keyboard"
	}
	return snap, ""
}

func anyElement(elements []simbridge.Element, match func(simbridge.Element) bool) bool {
	for _, e := range elements {
		if match(e) || anyElement(e.Children, match) {
			return true
		}
	}
	return false
}
