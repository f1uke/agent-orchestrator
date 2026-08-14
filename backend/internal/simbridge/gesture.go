package simbridge

import (
	"errors"
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
	// pasteHold is how long Command-V stays down. Long enough that the guest
	// registers the chord rather than treating it as key noise.
	pasteHold = 60 * time.Millisecond
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

// UnsendableError is a character the HID keyboard has no key for. It is a type
// rather than a message because it is the signal that decides a route: there is
// no key that sends it, but the PASTEBOARD can carry it, so a caller that has
// that option should take it instead of giving up.
type UnsendableError struct{ Rune rune }

func (e *UnsendableError) Error() string {
	return fmt.Sprintf("cannot type %q with key presses: the simulator's HID keyboard is a US keyboard, so only "+
		"ASCII letters, digits, space, tab, newline and standard punctuation have a key to send them",
		string(e.Rune))
}

// TextRoute is how a piece of text will be delivered to a device.
//
// Deciding this is the whole fix, so it is a pure function of what is being
// sent and what the guest reported - no device access, no I/O, one place. Both
// surfaces that type (the CLI, and the daemon route behind the Device tab) plan
// with it, because two implementations of "is the keyboard safe" would
// eventually disagree, and disagreeing about THIS means one of them silently
// types the wrong characters again.
type TextRoute struct {
	// Paste: deliver through the guest's pasteboard instead of the keyboard.
	Paste bool
	// Why names the reason the pasteboard was chosen, for the caller to report.
	// Taking a different route than expected is fine; doing it silently is not.
	Why string
	// Events is the composed keystrokes, when the keyboard route is taken.
	Events []Event
	// Keyboard is the input mode that was established, when one was.
	Keyboard simkeyboard.Mode
}

// ProbedKeyboard is what a device answered when asked which input mode it uses,
// or why it would not say.
//
// The two travel together as one value on purpose. Held apart, the natural
// mistake is to read a failed probe as a zero Mode and let it fall through the
// same branch as a known-bad layout - which routes the same way today but
// diagnoses it wrongly, telling a person their keyboard is set to something
// when the truth is the device never answered.
type ProbedKeyboard struct {
	Mode simkeyboard.Mode
	// Err is why the mode is not known. Never nil-checked away into "US": an
	// unverified keyboard is the thing this whole package refuses to assume.
	Err error
}

// TextOptions are the caller's overrides.
type TextOptions struct {
	// RawKeys: send the key presses whatever the guest makes of them. On a Thai
	// guest that is how Thai text is entered at all, so the capability stays -
	// it just has to be asked for, and then only key presses are promised.
	RawKeys bool
	// Paste: use the pasteboard regardless.
	Paste bool
}

// PlanText decides how to deliver text so that the characters asked for are the
// characters that arrive.
//
// The default is neither "keys" nor "paste" but "whichever one can tell the
// truth". Keys are preferred wherever they are provably faithful, because they
// are the truer simulation: an app watching per-keystroke events - a live
// validator, a character counter, a masked field - sees what a person would.
// The pasteboard takes over exactly where keys stop being able to promise
// anything: a guest whose input mode would remap them, a guest that will not
// say what its input mode is, or text no US keyboard has a key for at all.
//
// keyboard is what the device reported when asked; a caller that could not ask
// passes the reason, and gets the pasteboard - which does not care what the
// input mode is.
func PlanText(text string, keyboard ProbedKeyboard, opts TextOptions) (TextRoute, error) {
	if opts.RawKeys && opts.Paste {
		return TextRoute{}, errors.New(
			"raw keys and paste ask for different routes to the device: raw keys sends the key presses and " +
				"accepts whatever the simulator makes of them, paste delivers the exact characters through " +
				"the pasteboard. Pick one")
	}
	if text == "" {
		return TextRoute{}, errors.New("nothing to type")
	}
	if opts.Paste {
		return TextRoute{Paste: true, Why: "the pasteboard was asked for", Keyboard: keyboard.Mode}, nil
	}

	events, keyErr := TypeRaw(text)
	if opts.RawKeys {
		if keyErr != nil {
			return TextRoute{}, keyErr
		}
		return TextRoute{Events: events}, nil
	}

	// A character with no key at all is the one case where the pasteboard is
	// not a fallback but the only mechanism that exists.
	var unsendable *UnsendableError
	if errors.As(keyErr, &unsendable) {
		return TextRoute{Paste: true, Keyboard: keyboard.Mode,
			Why: fmt.Sprintf("no US keyboard key can send %q", string(unsendable.Rune))}, nil
	}
	if keyErr != nil {
		// Anything else wrong with the text is the caller's own mistake and is
		// not fixed by changing route.
		return TextRoute{}, keyErr
	}

	switch {
	case keyboard.Err != nil:
		// Deliberately not propagated. A device that would not say which
		// keyboard it has has not failed this call - it has answered the only
		// question that matters here, which is "can the key presses be
		// trusted", and the answer is no. The pasteboard does not care what the
		// input mode is, so there is a route left to take.
		//nolint:nilerr // an unknown keyboard selects a route; it is not an error to report
		return TextRoute{Paste: true, Why: "the simulator would not say which keyboard input mode it is using"}, nil
	case !keyboard.Mode.SendsUSASCII():
		return TextRoute{Paste: true, Keyboard: keyboard.Mode,
			Why: "the simulator's keyboard input mode is " + keyboard.Mode.Describe() +
				", which would remap the key presses"}, nil
	}
	return TextRoute{Events: events, Keyboard: keyboard.Mode}, nil
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
			return nil, &UnsendableError{Rune: r}
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

// Paste sends Command-V.
//
// It is the one keystroke on this device whose meaning does NOT depend on the
// guest's input mode. Verified on a real device set to Thai: the same `v` usage
// that types "อ" as a character still pastes when Command is held, because the
// guest matches keyboard shortcuts against the key rather than the character
// the layout would produce. That is what makes the pasteboard a way in when the
// key path has been refused - including into a secure field, which no other
// mechanism here can fill correctly.
//
// The releases are part of the same list for the same reason a touch's lift is:
// a modifier left held down is the keyboard's stuck finger, and every later
// keystroke on the device would silently arrive with Command applied.
func Paste() []Event {
	return []Event{
		{Kind: "key", Type: "down", Usage: usageLeftGUI},
		{Kind: "key", Type: "down", Usage: usageV},
		{Kind: "sleep", MS: int(pasteHold.Milliseconds())},
		{Kind: "key", Type: "up", Usage: usageV},
		{Kind: "key", Type: "up", Usage: usageLeftGUI},
	}
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

// USB HID usages for the modifiers and keys composed here by name rather than
// by number.
const (
	usageLeftShift = 225
	usageLeftGUI   = 227 // Command
	usageV         = 25
)

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
