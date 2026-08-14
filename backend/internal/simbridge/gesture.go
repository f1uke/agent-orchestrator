package simbridge

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Gestures are composed here, in Go, and not in the bridge script: this is
// where the rules that decide whether a touch actually lands can be tested
// without a simulator.
//
// Two properties matter more than any other and both are structural:
//
//   - every gesture that puts the finger down ends with the matching lift, in
//     the same event list, so the "stuck finger that wedges input until the
//     device reboots" case cannot be composed by accident;
//   - anything the device would silently ignore (a coordinate off the screen, a
//     character a US keyboard cannot send, a button name the addon does not
//     know) is refused here rather than reported as a success that changed
//     nothing.

// Timings, chosen to match what the simulator's HID layer actually responds to.
const (
	// tapHold is how long the finger stays down for a tap. Too short and the
	// press is not registered as a tap at all.
	tapHold = 40 * time.Millisecond
	// swipeStep is the interval between intermediate move events. A swipe with
	// no intermediate moves is read as a flick to nowhere.
	swipeStep = 16 * time.Millisecond
	// keyStep paces key events; the guest drops keystrokes sent faster.
	keyStep = 4 * time.Millisecond
	// MaxSwipeDuration bounds a gesture so it can never outlive the hold that
	// makes it exclusive.
	MaxSwipeDuration = 5 * time.Second
	// maxSwipeSteps caps event count for long swipes.
	maxSwipeSteps = 120
)

// Tap presses and releases one point.
func Tap(at Point) ([]Event, error) {
	if err := validatePoint("tap", at); err != nil {
		return nil, err
	}
	return []Event{
		{Kind: "touch", Type: "begin", X: at.X, Y: at.Y},
		{Kind: "sleep", MS: int(tapHold.Milliseconds())},
		{Kind: "touch", Type: "end", X: at.X, Y: at.Y},
	}, nil
}

// Swipe drags from one point to another over duration.
func Swipe(from, to Point, duration time.Duration) ([]Event, error) {
	if err := validatePoint("swipe start", from); err != nil {
		return nil, err
	}
	if err := validatePoint("swipe end", to); err != nil {
		return nil, err
	}
	if duration <= 0 || duration > MaxSwipeDuration {
		return nil, fmt.Errorf("swipe duration must be above zero and at most %s, got %s", MaxSwipeDuration, duration)
	}
	steps := int(duration / swipeStep)
	if steps < 3 {
		steps = 3
	}
	if steps > maxSwipeSteps {
		steps = maxSwipeSteps
	}
	pause := int((duration / time.Duration(steps)).Milliseconds())
	if pause < 1 {
		pause = 1
	}

	events := []Event{{Kind: "touch", Type: "begin", X: from.X, Y: from.Y}}
	for i := 1; i <= steps; i++ {
		fraction := float64(i) / float64(steps)
		events = append(events,
			Event{Kind: "sleep", MS: pause},
			Event{Kind: "touch", Type: "move",
				X: from.X + (to.X-from.X)*fraction,
				Y: from.Y + (to.Y-from.Y)*fraction,
			})
	}
	return append(events, Event{Kind: "touch", Type: "end", X: to.X, Y: to.Y}), nil
}

// Type sends text as US-keyboard key events.
func Type(text string) ([]Event, error) {
	if text == "" {
		return nil, fmt.Errorf("nothing to type")
	}
	events := make([]Event, 0, len(text)*4)
	for _, r := range text {
		if r == '\r' {
			continue
		}
		key, ok := usKeyboard[r]
		if !ok {
			return nil, fmt.Errorf("cannot type %q: the simulator's HID keyboard is a US keyboard, so only "+
				"ASCII letters, digits, space, tab, newline and standard punctuation can be sent", string(r))
		}
		if key.shift {
			events = append(events, Event{Kind: "key", Type: "down", Usage: usageLeftShift})
		}
		events = append(events,
			Event{Kind: "key", Type: "down", Usage: key.usage},
			Event{Kind: "key", Type: "up", Usage: key.usage},
		)
		if key.shift {
			events = append(events, Event{Kind: "key", Type: "up", Usage: usageLeftShift})
		}
		events = append(events, Event{Kind: "sleep", MS: int(keyStep.Milliseconds())})
	}
	return events, nil
}

// Button presses a hardware button.
func Button(name string) ([]Event, error) {
	native, ok := buttons[name]
	if !ok {
		return nil, fmt.Errorf("unknown button %q: use one of %s", name, strings.Join(ButtonNames(), ", "))
	}
	return []Event{{Kind: "button", Name: native}}, nil
}

// Lift is the recovery gesture: a bare release at a point, sent when a gesture
// died in a way that may have left the finger down. A release with nothing held
// is harmless; a finger left down wedges the device.
func Lift(at Point) []Event {
	return []Event{{Kind: "touch", Type: "end", X: clamp01(at.X), Y: clamp01(at.Y)}}
}

// ButtonNames lists what Button accepts, in a stable order.
func ButtonNames() []string {
	names := make([]string, 0, len(buttons))
	for name := range buttons {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validatePoint(what string, p Point) error {
	if p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 {
		return fmt.Errorf("%s coordinates must be normalized 0..1 of the screen (the `tap` values `ao sim ax` "+
			"prints for each element), got x=%g y=%g", what, p.X, p.Y)
	}
	return nil
}

// buttons maps the name a person types to the addon's own spelling.
//
// The list is short because every entry on it was checked against a real device
// and observably changed the screen. The mechanism offers more - a lock button,
// volume, Siri, and a "home" that relaunches SpringBoard rather than performing
// the home gesture - but the addon logs an unknown or inapplicable button and
// returns anyway, so a button that does nothing is indistinguishable from one
// that worked. Shipping those would mean shipping "reports success, changes
// nothing", which is the exact failure this command surface exists to avoid.
//
// `home` is deliberately the swipe-up home gesture: on this device generation
// the addon's own "home" (a SpringBoard relaunch) left the foreground app
// unchanged, while the gesture returned to the home screen every time.
var buttons = map[string]string{
	"home":         "swipe_home",
	"app-switcher": "app_switcher",
}

// usageLeftShift is the USB HID usage for left shift.
const usageLeftShift = 225

type keystroke struct {
	usage int
	shift bool
}

// usKeyboard maps a character to the USB HID keyboard usage that produces it on
// a US layout - the only layout the simulator's HID path speaks.
var usKeyboard = buildUSKeyboard()

func buildUSKeyboard() map[rune]keystroke {
	m := map[rune]keystroke{
		'\n': {usage: 40}, // return
		'\t': {usage: 43},
		' ':  {usage: 44},
	}
	for r := 'a'; r <= 'z'; r++ {
		usage := 4 + int(r-'a')
		m[r] = keystroke{usage: usage}
		m[r-32] = keystroke{usage: usage, shift: true} // 'A'..'Z'
	}
	for r := '1'; r <= '9'; r++ {
		m[r] = keystroke{usage: 30 + int(r-'1')}
	}
	m['0'] = keystroke{usage: 39}
	// Shifted digits, in keyboard order for 1..9 then 0.
	for i, r := range []rune{'!', '@', '#', '$', '%', '^', '&', '*', '('} {
		m[r] = keystroke{usage: 30 + i, shift: true}
	}
	m[')'] = keystroke{usage: 39, shift: true}
	punctuation := map[rune]struct {
		plain   int
		shifted rune
	}{
		'-':  {45, '_'},
		'=':  {46, '+'},
		'[':  {47, '{'},
		']':  {48, '}'},
		'\\': {49, '|'},
		';':  {51, ':'},
		'\'': {52, '"'},
		'`':  {53, '~'},
		',':  {54, '<'},
		'.':  {55, '>'},
		'/':  {56, '?'},
	}
	for plain, pair := range punctuation {
		m[plain] = keystroke{usage: pair.plain}
		m[pair.shifted] = keystroke{usage: pair.plain, shift: true}
	}
	return m
}
