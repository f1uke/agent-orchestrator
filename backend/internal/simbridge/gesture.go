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
	// pinchSettle is how long both fingers stay still after they land, before
	// they start travelling.
	//
	// 🗝 Measured, not guessed. A pinch recognizer only starts scaling once it
	// has latched BOTH touches, and the frames it spends doing that are frames
	// whose movement it never counts - so a pinch that starts moving
	// immediately arrives at a smaller scale than it asked for, and by a margin
	// that grows with how fast it moves. On mobile Safari, `x3.00` over 400 ms
	// delivered x2.20 with no settle and x2.69 with this one, measured off the
	// page's own `visualViewport.scale`; twice this length bought another 0.07
	// and four times bought nothing more, which is why it is 120 ms and not
	// longer. It is not part of `duration`, which is how long the fingers
	// TRAVEL; Duration counts it, so the hold still covers it.
	pinchSettle = 120 * time.Millisecond
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

// MinPinchSpan is the smallest gap allowed between the two fingers of a pinch,
// as a fraction of the screen's width.
//
// Below it the two contacts are close enough to be one touch, and a gesture
// that lands as a single finger is the failure this package exists to refuse:
// it sends events, it changes nothing, and it reads exactly like a pinch that
// worked. Two percent of a 393-point phone is about 8 points - already tight,
// and the floor rather than a recommendation.
const MinPinchSpan = 0.02

// Pinch moves two fingers along the horizontal line through center, from `from`
// apart to `to` apart, over duration. `to` greater than `from` spreads the
// fingers (zoom in); smaller pinches them together (zoom out).
//
// 🗝 It is composed FROM the held two-finger primitive rather than beside it:
// every event here is Grip.Event on a TwoFingers grip, the same call a live
// pinch tracking a human's fingers would make. That is deliberate. A one-shot
// pinch with its own idea of what a two-finger frame looks like would be a
// second definition to drift from the first, and the drift would show up as a
// contact left down on a device somebody else is using.
//
// The reason two fingers are possible at all is that the vendored addon carries
// BOTH contacts in a single HID frame (`multiTouch`) rather than making us
// interleave two touches that have no identity to tell them apart.
//
// Where the fingers sit for a given gap is PinchGrip, which says why one axis.
func Pinch(center Point, from, to float64, duration time.Duration) ([]Event, error) {
	if err := validatePoint("pinch centre", center); err != nil {
		return nil, err
	}
	for _, span := range []struct {
		what string
		v    float64
	}{{"start", from}, {"end", to}} {
		if span.v < MinPinchSpan {
			return nil, fmt.Errorf("the %s gap between the fingers must be at least %g of the screen's width, got %g: "+
				"any closer and the two touches land as one, which no app reads as a pinch",
				span.what, MinPinchSpan, span.v)
		}
		if half := span.v / 2; center.X-half < 0 || center.X+half > 1 {
			return nil, fmt.Errorf("a %s gap of %g does not fit around x=%g: the fingers would leave the screen. "+
				"The widest gap that fits there is %g",
				span.what, span.v, center.X, widestPinchSpan(center.X))
		}
	}
	// The advice above is about the ARGUMENTS - a span, a centre - which is why
	// it can name the gap that would have fitted. The grip's own rule is still
	// the authority on whether two contacts land, so it is asked as well: a
	// pinch that skipped it would be the one path composing a grip nothing
	// checked.
	for _, span := range []float64{from, to} {
		if err := PinchGrip(center, span).Validate("pinch"); err != nil {
			return nil, err
		}
	}
	if from == to {
		return nil, fmt.Errorf("a pinch from %g to %g never changes the distance between the fingers, "+
			"so nothing would zoom: give a different end gap", from, to)
	}
	if duration <= 0 || duration > MaxSwipeDuration {
		return nil, fmt.Errorf("pinch duration must be above zero and at most %s, got %s", MaxSwipeDuration, duration)
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

	events := []Event{
		PinchGrip(center, from).Event("begin"),
		{Kind: "sleep", MS: int(pinchSettle.Milliseconds())},
	}
	for i := 1; i <= steps; i++ {
		grip := PinchGrip(center, from+(to-from)*float64(i)/float64(steps))
		events = append(events, Event{Kind: "sleep", MS: pause}, grip.Event("move"))
	}
	return append(events, PinchGrip(center, to).Event("end")), nil
}

// PinchGrip is where two fingers sit when they are `span` apart about center.
//
// It is exported because it is what a CONTINUOUS pinch needs: a caller tracking
// a human's fingers - or an agent stepping a zoom in stages - turns each span it
// learns into a grip here and hands it to the held path, and gets the same
// geometry `Pinch` composes in advance rather than a second version of it.
//
// The fingers stay on ONE AXIS. A normalized coordinate is a fraction of its own
// axis and a phone screen is not square, so a diagonal pair would travel a
// different number of POINTS sideways than up, and the scale an app computed
// would not be the scale that was asked for. On one axis the normalization
// cancels and the ratio of the spans IS the scale the recognizer sees.
func PinchGrip(center Point, span float64) Grip {
	half := span / 2
	return TwoFingers(
		Point{X: center.X - half, Y: center.Y},
		Point{X: center.X + half, Y: center.Y},
	)
}

// widestPinchSpan is the largest gap that still fits both fingers on screen
// around x. It is in the refusal rather than silently clamped: a pinch quietly
// narrowed to fit is a smaller scale than the caller asked for, reported as the
// one they asked for.
func widestPinchSpan(x float64) float64 {
	return 2 * math.Min(x, 1-x)
}

// PinchScale is what a pinch from one gap to another asks an app to scale by.
// Reported rather than taken as input, so the number on screen is derived from
// the fingers that actually moved.
func PinchScale(from, to float64) float64 { return to / from }

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
	// Forwarded: the keys a person pressed were sent as themselves, so what
	// arrives is the guest's reading of those keys rather than a promise about
	// characters. Reported rather than inferred from Events being non-empty:
	// the two routes compose the same shape and mean different things.
	Forwarded bool
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
	// Keys are the physical keys a person pressed to produce this text on THIS
	// Mac, in the same order. When they are all forwardable they are sent as
	// themselves and nothing about the guest's input mode has to be asked or
	// planned around - see ForwardKeys for why that is correct for a human and
	// wrong for an agent.
	//
	// ⚠ Only a caller that watched a person press these keys may set them. A
	// string an agent chose has no keys behind it, and inventing some would be
	// the layout bug of #198 with extra steps.
	Keys []KeyPress
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
	if len(opts.Keys) > 0 {
		if opts.RawKeys || opts.Paste {
			return TextRoute{}, errors.New(
				"forwarded key presses already say how to reach the device, so they cannot be combined with " +
					"raw keys or paste. Send the keys, or send the text and pick a route for it")
		}
		// The two must describe the same keystrokes. If they do not, one of them
		// is wrong and there is no way to tell which - and the text is what a
		// recording keeps, so a mismatch would write down something that was
		// never typed.
		if n := len([]rune(text)); n != len(opts.Keys) {
			return TextRoute{}, fmt.Errorf(
				"%d key press(es) were forwarded for %d character(s): the keys and the text must describe "+
					"the same keystrokes, in the same order", len(opts.Keys), n)
		}
		events, err := ForwardKeys(opts.Keys)
		if err == nil {
			return TextRoute{Events: events, Forwarded: true}, nil
		}
		// A position with no usage is not a failure. The text still says which
		// characters the human meant, and the ordinary planner can deliver
		// those - slower, and never wrong.
		var unknown *UnknownKeyError
		if !errors.As(err, &unknown) {
			return TextRoute{}, err
		}
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

// KeyPress is one physical key a person pressed on their own keyboard, named
// the way the browser names it.
//
// 🗝 `KeyboardEvent.code` is a POSITION on the keyboard, not a character: it is
// defined as where that key sits on a US layout, whatever the layout in force
// prints on it. That is the same thing a USB HID usage is, which is why a
// keystroke a human actually performed can be forwarded to the device without
// anything in between having to know what character it means.
type KeyPress struct {
	// Code is `KeyboardEvent.code`, e.g. "KeyF", "Digit1", "Semicolon".
	Code string
	// Shift was held. It is part of the key press rather than of the character:
	// on a Thai keyboard shift produces a different Thai letter, and the guest
	// applies its own layout to the pair exactly as the Mac did.
	Shift bool
}

// ForwardKeys composes the key presses for keys a person actually pressed.
//
// 🗝 This is the interactive counterpart of PlanText, and it is a different
// promise rather than a faster version of the same one. PlanText answers "make
// these CHARACTERS arrive", which is an agent's question: the agent chose a
// string, and the guest's input mode stands between that string and the field,
// so the route has to be planned and proven. Forwarding answers "the human
// pressed THIS KEY", where the guest's input mode is not an obstacle but the
// point: it is the same layout the Mac just used to decide which character the
// human saw themselves type, so whatever the guest makes of the key is what
// they meant. It is also exactly what Simulator.app does, which is why typing
// there has never felt slow.
//
// What it does not promise is characters. A guest whose input mode has drifted
// away from the Mac's - Simulator's I/O > Keyboard > "Use the Same Keyboard
// Language as macOS" unticked, or a field that forced the guest to an
// ASCII-capable mode - will produce something else, exactly as Simulator.app
// would. The human is watching the screen, and that is the check.
func ForwardKeys(keys []KeyPress) ([]Event, error) {
	if len(keys) == 0 {
		return nil, errors.New("no keys to forward")
	}
	if len(keys) > MaxTypeRunes {
		return nil, fmt.Errorf("cannot forward %d key presses at once: keep it to %d or fewer, "+
			"so one gesture stays inside the hold that keeps other commands off the device", len(keys), MaxTypeRunes)
	}
	events := make([]Event, 0, len(keys)*5)
	for _, k := range keys {
		usage, ok := keyPositions[k.Code]
		if !ok {
			return nil, &UnknownKeyError{Code: k.Code}
		}
		if k.Shift {
			events = append(events, Event{Kind: "key", Type: "down", Usage: usageLeftShift})
		}
		events = append(events,
			Event{Kind: "key", Type: "down", Usage: usage},
			Event{Kind: "key", Type: "up", Usage: usage},
		)
		if k.Shift {
			events = append(events, Event{Kind: "key", Type: "up", Usage: usageLeftShift})
		}
		events = append(events, Event{Kind: "sleep", MS: int(keyStep.Milliseconds())})
	}
	return events, nil
}

// ForwardableKeys reports whether every one of these key positions has a usage
// to send it with.
//
// It exists so a caller can tell, WITHOUT touching the device, that it is about
// to take the forwarding route and therefore does not need to ask the guest
// which input mode it is in - the read that costs about a second and is the
// only reason typing was ever slow.
func ForwardableKeys(keys []KeyPress) bool {
	if len(keys) == 0 || len(keys) > MaxTypeRunes {
		return false
	}
	for _, k := range keys {
		if _, ok := keyPositions[k.Code]; !ok {
			return false
		}
	}
	return true
}

// UnknownKeyError is a key position with no usage to send it with - an F-key, a
// layout-specific extra key, or an event that carried no position at all. It is
// a type rather than a message because it selects a route: the key cannot be
// forwarded, but the caller still knows which CHARACTER the human meant, and
// PlanText can deliver that instead.
type UnknownKeyError struct{ Code string }

func (e *UnknownKeyError) Error() string {
	if e.Code == "" {
		return "that keystroke carried no key position, so there is no key to forward"
	}
	return fmt.Sprintf("no simulator key matches the position %q", e.Code)
}

// keyPositions maps a `KeyboardEvent.code` to the HID usage for that position.
//
// It is built from usKeyboard rather than written out, because the two say the
// same thing: `code` is defined by the character that position carries on a US
// keyboard, and usKeyboard is what usage sends that character. One table means
// one place for the usages to be right.
//
// Only positions that carry a character are here. The keys that do not - Enter,
// Backspace, Tab, the arrows - are the `key` gesture's business (see keyUsages),
// and modified keystrokes never reach here at all: a chord is a shortcut on the
// Mac, not something a person is typing into a field.
var keyPositions = buildKeyPositions()

func buildKeyPositions() map[string]int {
	m := make(map[string]int, 48)
	add := func(code string, r rune) {
		key, ok := usKeyboard[r]
		if !ok {
			panic("simbridge: no US keyboard usage for " + string(r))
		}
		m[code] = key.usage
	}
	for r := 'a'; r <= 'z'; r++ {
		add("Key"+string(r-32), r)
	}
	for r := '0'; r <= '9'; r++ {
		add("Digit"+string(r), r)
	}
	for code, r := range map[string]rune{
		"Minus": '-', "Equal": '=', "BracketLeft": '[', "BracketRight": ']',
		"Backslash": '\\', "Semicolon": ';', "Quote": '\'', "Backquote": '`',
		"Comma": ',', "Period": '.', "Slash": '/', "Space": ' ',
	} {
		add(code, r)
	}
	return m
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

// keyUsages is the set of keyboard keys that are NOT characters.
//
// 🗝 This table is the reason live typing can send these as key presses at all,
// while a character has to be planned by PlanText. The guest remaps
// CHARACTER-producing keys according to its input mode - that is the whole
// `ao sim type` layout bug - but these produce no character, so there is
// nothing for a layout to remap them into. It is the same property that makes
// Command-V work on a Thai guest (see Paste): the guest matches these against
// the key, not against the letter a layout would print.
//
// ⚠ That is a claim about a real device, so it is verified on one rather than
// reasoned about - see the record. A key whose effect could not be observed on
// a device does not belong in this table.
var keyUsages = map[string]int{
	"enter":       40,
	"backspace":   42,
	"tab":         43,
	"arrow-right": 79,
	"arrow-left":  80,
	"arrow-down":  81,
	"arrow-up":    82,
}

// Key presses one named keyboard key.
//
// It is separate from Type because it promises something different. Type
// promises CHARACTERS and has to choose a route to keep that promise; this
// promises a key, and the keys it accepts are exactly the ones whose meaning
// does not pass through the guest's input mode.
func Key(name string) ([]Event, error) {
	usage, ok := keyUsages[name]
	if !ok {
		return nil, fmt.Errorf("unknown key %q: use one of %s", name, strings.Join(KeyNames(), ", "))
	}
	return []Event{
		{Kind: "key", Type: "down", Usage: usage},
		{Kind: "key", Type: "up", Usage: usage},
	}, nil
}

// KeyNames lists what Key accepts, in a stable order.
func KeyNames() []string {
	names := make([]string, 0, len(keyUsages))
	for name := range keyUsages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
func Lift(at Point) []Event { return Release(OneFinger(at)) }

// Release is the recovery gesture for a grip: whatever is touching the screen,
// let go of it the way it went down. Releasing ONE contact of a pair leaves the
// other held, which is the same wedge as a stuck finger reached by being
// half-right, so a pair is always released as a pair.
func Release(grip Grip) []Event {
	return []Event{grip.Clamped().Event("end")}
}

// Recover is the release to send when a gesture died in flight: whatever these
// events left touching the screen, released the way it went down.
//
// It exists because "lift the finger" stopped being a complete description the
// moment a gesture could hold two. Releasing ONE contact of a pinch leaves the
// other held, which is the wedge this package spends its whole guard budget
// avoiding - so the pair is released as a pair, in one frame, at the points the
// last multitouch event put them.
//
// at is where the caller believes the finger ended, and is used for the
// one-finger case because a drag's end is only known to the caller (see
// simgesture.Gesture.Last). Nothing is returned when the gesture never touched
// the screen: sending a stray touch to recover from a keyboard gesture would be
// worse than the failure it is recovering from.
func Recover(events []Event, at Point) []Event {
	for i := len(events) - 1; i >= 0; i-- {
		switch e := events[i]; e.Kind {
		case "multitouch":
			return Release(TwoFingers(Point{X: e.X, Y: e.Y}, Point{X: e.X2, Y: e.Y2}))
		case "touch":
			return Lift(at)
		}
	}
	return nil
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
