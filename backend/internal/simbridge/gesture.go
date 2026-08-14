package simbridge

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
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
//     nothing - and so is the subtler version of the same sin, a key press the
//     guest would deliver as a DIFFERENT character (see Type).

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
	// MaxTypeRunes bounds one `type` so the gesture cannot outlive the hold that
	// makes it exclusive: a hold that lapses mid-gesture is a window for another
	// command to take the finger while this one is still using it. At keyStep a
	// run this long takes a few seconds, well inside the hold's ceiling.
	MaxTypeRunes = 2000
	// perEventOverhead is the allowance added per non-sleep event when estimating
	// how long a gesture will take. Deliberately generous: an estimate that comes
	// out short is the failure that matters.
	perEventOverhead = 2 * time.Millisecond
)

// Duration estimates how long a gesture will take on the device. Callers size
// the gesture hold from it, so it must never be optimistic.
func Duration(events []Event) time.Duration {
	var total time.Duration
	for _, e := range events {
		if e.Kind == "sleep" {
			total += time.Duration(e.MS) * time.Millisecond
			continue
		}
		total += perEventOverhead
	}
	return total
}

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
	return Path([]Point{from, to}, duration)
}

// Path drags through a route of points, holding one finger down the whole way.
//
// It is what a swipe cannot express: a scroll that changes direction, a drag
// onto a target, anything an app distinguishes from a flick. Composing it as
// back-to-back swipes would lift between legs, and an app reads three lifts as
// three separate gestures - so a route is one gesture here, with a single
// matched begin and end, and every waypoint actually visited rather than cut
// across.
//
// Two points is a swipe, and Swipe is exactly this: one implementation means
// one set of timings to keep in step with what the device responds to.
func Path(points []Point, duration time.Duration) ([]Event, error) {
	if len(points) < 2 {
		return nil, fmt.Errorf("a drag needs at least a start and an end, got %d point(s)", len(points))
	}
	for i, p := range points {
		if err := validatePoint(fmt.Sprintf("drag point %d", i+1), p); err != nil {
			return nil, err
		}
	}
	if duration <= 0 || duration > MaxSwipeDuration {
		return nil, fmt.Errorf("drag duration must be above zero and at most %s, got %s", MaxSwipeDuration, duration)
	}

	legs := len(points) - 1
	steps := int(duration / swipeStep)
	if steps < 3 {
		steps = 3
	}
	if steps > maxSwipeSteps {
		steps = maxSwipeSteps
	}
	// Every leg needs a step of its own, or a waypoint would be skipped.
	if steps < legs {
		steps = legs
	}
	pause := int((duration / time.Duration(steps)).Milliseconds())
	if pause < 1 {
		pause = 1
	}

	events := []Event{{Kind: "touch", Type: "begin", X: points[0].X, Y: points[0].Y}}
	for leg, stepsHere := range shareSteps(points, steps) {
		from, to := points[leg], points[leg+1]
		for i := 1; i <= stepsHere; i++ {
			fraction := float64(i) / float64(stepsHere)
			events = append(events,
				Event{Kind: "sleep", MS: pause},
				Event{Kind: "touch", Type: "move",
					X: from.X + (to.X-from.X)*fraction,
					Y: from.Y + (to.Y-from.Y)*fraction,
				})
		}
	}
	last := points[len(points)-1]
	return append(events, Event{Kind: "touch", Type: "end", X: last.X, Y: last.Y}), nil
}

// shareSteps splits a step budget between the legs of a route by how far each
// one travels, so the finger moves at one speed rather than crawling across a
// long leg and jumping a short one. Every leg keeps at least one step: the last
// step of a leg is what lands exactly on its waypoint.
func shareSteps(points []Point, steps int) []int {
	legs := len(points) - 1
	lengths := make([]float64, legs)
	var total float64
	for i := range lengths {
		dx, dy := points[i+1].X-points[i].X, points[i+1].Y-points[i].Y
		lengths[i] = math.Hypot(dx, dy)
		total += lengths[i]
	}

	share := make([]int, legs)
	assigned := 0
	for i := range share {
		share[i] = 1
		if total > 0 {
			// -1 because the step every leg already has is part of its share.
			if extra := int(float64(steps)*lengths[i]/total) - 1; extra > 0 {
				share[i] += extra
			}
		}
		assigned += share[i]
	}
	// Rounding leaves a few steps unspent - fewer than there are legs. They go
	// to the longest legs first, which is where one more step is least visible.
	order := make([]int, legs)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return lengths[order[a]] > lengths[order[b]] })
	for i := 0; assigned < steps; i++ {
		share[order[i%legs]]++
		assigned++
	}
	return share
}

// RemapError is `type` refusing to send keys the guest would turn into
// different characters. It carries the mode that was in the way so each surface
// can name its own way past it - `--raw-keys` for the CLI, `rawKeys` for the
// daemon route - without either restating what the problem is.
type RemapError struct{ Mode simkeyboard.Mode }

func (e *RemapError) Error() string {
	return fmt.Sprintf("this simulator's keyboard input mode is %s, so the key presses `type` sends "+
		"would arrive as different characters - nothing was sent.\n"+
		"The keys are US-keyboard key presses and the GUEST decides what each one produces, so only a US "+
		"hardware layout is promised - even a UK one turns shift-2 into a quote rather than an at-sign, "+
		"which is enough to corrupt every email address. Simulator.app ships with I/O > Keyboard > "+
		"\"Use the Same Keyboard Language as macOS\" ticked, so a Mac on any other input source gives the "+
		"simulator one too.\n"+
		"Fix it in the Simulator window - press Ctrl-Space until the input mode is U.S. English (en_US), "+
		"or untick that menu item, or switch the Mac's own input source - then run this again",
		e.Mode.Describe())
}

// Type sends text as US-keyboard key events, and refuses when the guest would
// turn those key presses into something else.
//
// The mode has to be passed in rather than looked up here because this package
// composes events and never talks to a device. Making it a parameter is the
// point: both surfaces that type - the CLI and the daemon's gesture route -
// have to have established the mapping before they can call this at all, so
// "we forgot to check" is not a state the code can be in.
func Type(text string, mode simkeyboard.Mode) ([]Event, error) {
	events, err := TypeRaw(text)
	if err != nil {
		return nil, err
	}
	// After composing, not before: a payload this keyboard could never send is
	// the caller's own mistake and stays the first thing they are told about,
	// whatever the guest is set to.
	if !mode.SendsUSASCII() {
		return nil, &RemapError{Mode: mode}
	}
	return events, nil
}

// TypeRaw sends the key presses without promising what they will produce.
//
// It exists because on a guest set to Thai these usages ARE how Thai text is
// entered, and a QA session driving a Thai app may want exactly that. What it
// does not waive is the HID path's own limit: a character no US keyboard can
// send has no usage to send it with, so it is still refused rather than
// dropped.
func TypeRaw(text string) ([]Event, error) {
	if text == "" {
		return nil, fmt.Errorf("nothing to type")
	}
	if n := len([]rune(text)); n > MaxTypeRunes {
		return nil, fmt.Errorf("cannot type %d characters at once: keep it to %d or shorter, "+
			"so one `type` stays inside the gesture hold that keeps other commands off the device", n, MaxTypeRunes)
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

// ValidatePoint is exported because a drag's points arrive one at a time rather
// than inside a composed gesture, and they have to be refused by the same rule.
func ValidatePoint(what string, p Point) error { return validatePoint(what, p) }

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
// a US layout - the only layout this table can speak for. Whether the guest
// reading these usages agrees is a separate question, and the one Type asks
// before sending any of them.
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
