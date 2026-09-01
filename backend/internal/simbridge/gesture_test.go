package simbridge

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
)

// The two guest keyboard mappings that matter: one that turns the usages we
// send into the characters they stand for, and one that does not. Both
// identifiers were read off a real device.
var (
	usMode   = simkeyboard.ParseMode("en_US@sw=QWERTY;hw=Automatic")
	thaiMode = simkeyboard.ParseMode("th_TH@sw=Thai;hw=Automatic")
)

func TestTap_IsAMatchedDownAndUp(t *testing.T) {
	events, err := Tap(Point{X: 0.5, Y: 0.25})
	if err != nil {
		t.Fatalf("tap: %v", err)
	}
	if got := touchTypes(events); got != "begin,end" {
		t.Fatalf("touch sequence = %q, want begin,end", got)
	}
	first, last := events[0], events[len(events)-1]
	if first.X != 0.5 || first.Y != 0.25 || last.X != 0.5 || last.Y != 0.25 {
		t.Fatalf("down and up must land on the same point: %+v / %+v", first, last)
	}
}

func TestGestures_RefuseCoordinatesOffTheScreen(t *testing.T) {
	for _, p := range []Point{{X: -0.1, Y: 0.5}, {X: 0.5, Y: 1.5}} {
		if _, err := Tap(p); err == nil {
			t.Fatalf("tap %+v must be refused: a touch outside the screen simply never lands", p)
		}
	}
	if _, err := Swipe(Point{X: 0.5, Y: 0.5}, Point{X: 0.5, Y: 2}, 300*time.Millisecond); err == nil {
		t.Fatal("swipe off the screen must be refused")
	}
}

func TestSwipe_MovesBetweenTheEnds(t *testing.T) {
	from, to := Point{X: 0.5, Y: 0.8}, Point{X: 0.5, Y: 0.2}
	events, err := Swipe(from, to, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("swipe: %v", err)
	}
	types := touchTypes(events)
	if !strings.HasPrefix(types, "begin,move") || !strings.HasSuffix(types, "end") {
		t.Fatalf("swipe sequence = %q, want begin, moves, end", types)
	}
	if strings.Count(types, "move") < 2 {
		t.Fatalf("a swipe with no intermediate moves is a tap the system reads as a flick: %q", types)
	}
	touches := touchEvents(events)
	if touches[0].Y != from.Y || touches[len(touches)-1].Y != to.Y {
		t.Fatalf("swipe must start at %+v and end at %+v: %+v", from, to, touches)
	}
	// Monotonic travel: every step moves toward the target, never past it.
	for i := 1; i < len(touches); i++ {
		if touches[i].Y > touches[i-1].Y+1e-9 {
			t.Fatalf("step %d went backwards: %+v", i, touches)
		}
	}
}

func TestSwipe_DurationBounds(t *testing.T) {
	if _, err := Swipe(Point{X: 0.5, Y: 0.5}, Point{X: 0.5, Y: 0.2}, MaxSwipeDuration+time.Second); err == nil {
		t.Fatal("an unbounded swipe would outlive its gesture hold")
	}
	if _, err := Swipe(Point{X: 0.5, Y: 0.5}, Point{X: 0.5, Y: 0.2}, -time.Second); err == nil {
		t.Fatal("a negative duration must be refused")
	}
}

func TestType_ProducesShiftedAndUnshiftedKeys(t *testing.T) {
	events, err := TypeRaw("aA1!\n\t")
	if err != nil {
		t.Fatalf("type: %v", err)
	}
	var keys []Event
	for _, e := range events {
		if e.Kind == "key" {
			keys = append(keys, e)
		}
	}
	// a = usage 4 unshifted; A = shift + 4; 1 = 30; ! = shift + 30;
	// newline = 40 (return); tab = 43.
	want := []struct {
		usage int
		typ   string
	}{
		{4, "down"}, {4, "up"},
		{225, "down"}, {4, "down"}, {4, "up"}, {225, "up"},
		{30, "down"}, {30, "up"},
		{225, "down"}, {30, "down"}, {30, "up"}, {225, "up"},
		{40, "down"}, {40, "up"},
		{43, "down"}, {43, "up"},
	}
	if len(keys) != len(want) {
		t.Fatalf("got %d key events, want %d: %+v", len(keys), len(want), keys)
	}
	for i, w := range want {
		if keys[i].Usage != w.usage || keys[i].Type != w.typ {
			t.Fatalf("key %d = usage %d %s, want usage %d %s", i, keys[i].Usage, keys[i].Type, w.usage, w.typ)
		}
	}
}

func TestType_RefusesWhatTheKeyboardCannotSend(t *testing.T) {
	// The HID path is a US keyboard. Silently dropping a character would type
	// something different from what was asked for - worse than refusing.
	for _, text := range []string{"ก", "café", "emoji 🎉"} {
		_, err := TypeRaw(text)
		if err == nil {
			t.Fatalf("Type(%q) must be refused", text)
		}
		if !strings.Contains(err.Error(), "US keyboard") {
			t.Fatalf("Type(%q) error must explain the limit, got %v", text, err)
		}
	}
	if _, err := TypeRaw(""); err == nil {
		t.Fatal("typing nothing is a mistake worth reporting, not a no-op")
	}
}

func TestPlanText_UsesKeysOnlyWhenTheGuestCanBeTrusted(t *testing.T) {
	route, err := PlanText("fa12345", ProbedKeyboard{Mode: usMode}, TextOptions{})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Paste {
		t.Fatal("a guest that sends US ASCII must be typed at, not pasted into: keys are the truer simulation")
	}
	if len(route.Events) == 0 {
		t.Fatal("the keyboard route must carry its events")
	}
}

func TestPlanText_PastesRatherThanTypingCharactersTheGuestWouldRemap(t *testing.T) {
	// The bug, and the fix in one assertion: the same call that used to put
	// "ดฟๅ/_ภถ" in the field now routes around the keyboard entirely.
	route, err := PlanText("fa12345", ProbedKeyboard{Mode: thaiMode}, TextOptions{})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if !route.Paste {
		t.Fatal("a guest that would remap the keys must not be typed at")
	}
	if !strings.Contains(route.Why, "th_TH") {
		t.Fatalf("Why = %q, must name what made the keyboard unusable", route.Why)
	}
}

func TestPlanText_PastesWhenTheGuestWouldNotSayWhatItsKeyboardIs(t *testing.T) {
	// Unknown is not US. The pasteboard does not care what the input mode is,
	// so it is the honest way through rather than a refusal.
	route, err := PlanText("fa12345", ProbedKeyboard{Err: errors.New("device is not booted")}, TextOptions{})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if !route.Paste {
		t.Fatal("an unverified keyboard must never be treated as a US one")
	}
	// Routing the same way is not the same as diagnosing the same way. "The
	// device would not answer" and "the device answered th_TH" send a reader to
	// two different places, and the zero mode reaching the layout check would
	// silently report the second for the first.
	if !strings.Contains(route.Why, "would not say") {
		t.Fatalf("Why = %q, want it to name the unanswered probe rather than a layout", route.Why)
	}
}

func TestPlanText_PastesCharactersNoKeyCanSend(t *testing.T) {
	// A capability the key path never had at all.
	route, err := PlanText("สวัสดี", ProbedKeyboard{Mode: usMode}, TextOptions{})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if !route.Paste {
		t.Fatal("there is no US keyboard usage for these, so the pasteboard is the only mechanism")
	}
}

func TestPlanText_RawKeysTakesTheKeyboardWhateverTheGuestWouldMakeOfIt(t *testing.T) {
	route, err := PlanText("fa12345", ProbedKeyboard{Mode: thaiMode}, TextOptions{RawKeys: true})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Paste {
		t.Fatal("--raw-keys asked for key presses")
	}
	keys, err := TypeRaw("fa12345")
	if err != nil {
		t.Fatalf("TypeRaw: %v", err)
	}
	if !reflect.DeepEqual(route.Events, keys) {
		t.Fatal("the escape hatch must send exactly the keys, only without the promise")
	}
}

func TestPlanText_RawKeysStillCannotSendWhatHasNoKey(t *testing.T) {
	// --raw-keys waives the guest's mapping, not the limits of the HID path.
	if _, err := PlanText("ก", ProbedKeyboard{Mode: usMode}, TextOptions{RawKeys: true}); err == nil {
		t.Fatal("a character with no usage has no key to send it with")
	}
}

func TestPlanText_PasteFlagForcesTheRoute(t *testing.T) {
	route, err := PlanText("fa12345", ProbedKeyboard{Mode: usMode}, TextOptions{Paste: true})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if !route.Paste {
		t.Fatal("--paste must win over a keyboard that would have worked")
	}
}

func TestPlanText_RefusesContradictoryRoutesAndEmptyText(t *testing.T) {
	if _, err := PlanText("hi", ProbedKeyboard{Mode: usMode}, TextOptions{RawKeys: true, Paste: true}); err == nil {
		t.Fatal("asking for both routes at once has no answer")
	}
	if _, err := PlanText("", ProbedKeyboard{Mode: usMode}, TextOptions{}); err == nil {
		t.Fatal("typing nothing is a mistake worth reporting, not a no-op")
	}
}

func TestPaste_IsCommandVWithMatchedReleases(t *testing.T) {
	// Verified against a real device: the guest matches this shortcut WITHOUT
	// putting it through the input mode, so it pastes on a Thai guest where the
	// same `v` key would type "อ". That is the whole reason the paste path can
	// rescue a keyboard the key path cannot speak for.
	events := Paste()
	var keys []Event
	for _, e := range events {
		if e.Kind == "key" {
			keys = append(keys, e)
		}
	}
	want := []struct {
		usage int
		typ   string
	}{
		{usageLeftGUI, "down"}, {usageV, "down"},
		{usageV, "up"}, {usageLeftGUI, "up"},
	}
	if len(keys) != len(want) {
		t.Fatalf("got %d key events, want %d: %+v", len(keys), len(want), keys)
	}
	for i, w := range want {
		if keys[i].Usage != w.usage || keys[i].Type != w.typ {
			t.Fatalf("key %d = usage %d %s, want usage %d %s", i, keys[i].Usage, keys[i].Type, w.usage, w.typ)
		}
	}
	// A modifier left held down is the keyboard's version of a stuck finger:
	// every later keystroke on that device arrives with Command applied.
	for _, e := range events {
		if e.Kind == "touch" {
			t.Fatalf("pasting must not touch the screen: %+v", events)
		}
	}
}

func TestButton_OnlyKnownNames(t *testing.T) {
	events, err := Button("home")
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	// `home` is the swipe-up gesture, not the addon's SpringBoard relaunch: on a
	// modern device the relaunch reports success and leaves the foreground app
	// exactly where it was.
	if len(events) != 1 || events[0].Kind != "button" || events[0].Name != "swipe_home" {
		t.Fatalf("events = %+v", events)
	}

	// An unknown name must not reach the device: the addon logs and does
	// nothing, which would report success while changing nothing.
	_, err = Button("hoem")
	if err == nil {
		t.Fatal("an unknown button name must be refused, never silently dropped")
	}
	if !strings.Contains(err.Error(), "home") {
		t.Fatalf("the refusal must list what is available, got %v", err)
	}
}

func TestButtonNames_AreSorted(t *testing.T) {
	names := ButtonNames()
	if len(names) < 2 {
		t.Fatalf("names = %v", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("button names must be listed in a stable order: %v", names)
		}
	}
}

func touchEvents(events []Event) []Event {
	var out []Event
	for _, e := range events {
		if e.Kind == "touch" {
			out = append(out, e)
		}
	}
	return out
}

func touchTypes(events []Event) string {
	var parts []string
	for _, e := range touchEvents(events) {
		parts = append(parts, e.Type)
	}
	return strings.Join(parts, ",")
}

func TestDuration_CoversEveryGesture(t *testing.T) {
	// The hold has to outlive the gesture it protects. If it lapses mid-gesture,
	// another command can take the finger while this one is still touching - the
	// exact interleave the hold exists to prevent - so the estimate must never
	// come out under the sleeps the gesture actually performs.
	tap, err := Tap(Point{X: 0.5, Y: 0.5})
	if err != nil {
		t.Fatalf("tap: %v", err)
	}
	if got := Duration(tap); got < tapHold {
		t.Fatalf("tap duration = %s, want at least the %s hold", got, tapHold)
	}

	swipe, err := Swipe(Point{X: 0.5, Y: 0.8}, Point{X: 0.5, Y: 0.2}, 2*time.Second)
	if err != nil {
		t.Fatalf("swipe: %v", err)
	}
	if got := Duration(swipe); got < 2*time.Second {
		t.Fatalf("swipe duration = %s, want at least the 2s it was asked for", got)
	}

	typing, err := TypeRaw(strings.Repeat("a", 500))
	if err != nil {
		t.Fatalf("type: %v", err)
	}
	if got := Duration(typing); got < 500*keyStep {
		t.Fatalf("typing duration = %s, want at least %s", got, 500*keyStep)
	}
}

func TestType_RefusesTextLongerThanAGestureHoldCanCover(t *testing.T) {
	_, err := TypeRaw(strings.Repeat("a", MaxTypeRunes+1))
	if err == nil {
		t.Fatal("text long enough to outlive its own gesture hold must be refused, not sent")
	}
	if !strings.Contains(err.Error(), "shorter") {
		t.Fatalf("the refusal must say what to do, got %v", err)
	}
	if _, err := TypeRaw(strings.Repeat("a", MaxTypeRunes)); err != nil {
		t.Fatalf("text at the limit must still be typable: %v", err)
	}
}

// A path is one touch, not several. Composing a route as back-to-back swipes
// lifts the finger between legs, and an app reads that as three separate flicks
// rather than one drag - which is exactly the difference between "scrolls" and
// "scrolls, stops, scrolls, stops".
func TestPath_IsOneFingerThroughEveryWaypoint(t *testing.T) {
	points := []Point{{X: 0.5, Y: 0.8}, {X: 0.5, Y: 0.5}, {X: 0.2, Y: 0.5}}
	events, err := Path(points, 600*time.Millisecond)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	types := touchTypes(events)
	if strings.Count(types, "begin") != 1 || strings.Count(types, "end") != 1 {
		t.Fatalf("a path must put the finger down once and lift it once: %q", types)
	}
	if !strings.HasPrefix(types, "begin,move") || !strings.HasSuffix(types, "end") {
		t.Fatalf("path sequence = %q, want begin, moves, end", types)
	}
	touches := touchEvents(events)
	if touches[0] != (Event{Kind: "touch", Type: "begin", X: points[0].X, Y: points[0].Y}) {
		t.Fatalf("path must start at %+v: %+v", points[0], touches[0])
	}
	last := touches[len(touches)-1]
	if last.X != points[2].X || last.Y != points[2].Y {
		t.Fatalf("path must lift at %+v: %+v", points[2], last)
	}
	// Every waypoint is actually visited: a route that cuts the corner is not
	// the route that was asked for.
	for _, want := range points[1:] {
		visited := false
		for _, e := range touches {
			if math.Abs(e.X-want.X) < 1e-9 && math.Abs(e.Y-want.Y) < 1e-9 {
				visited = true
			}
		}
		if !visited {
			t.Fatalf("waypoint %+v was never reached: %+v", want, touches)
		}
	}
}

// The hold is sized from Duration, so a path that takes longer than it was
// asked for would outlive the exclusivity that protects it.
func TestPath_KeepsToTheTimeItWasGiven(t *testing.T) {
	events, err := Path([]Point{{X: 0.1, Y: 0.1}, {X: 0.9, Y: 0.9}, {X: 0.1, Y: 0.9}}, time.Second)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	var slept time.Duration
	for _, e := range events {
		if e.Kind == "sleep" {
			slept += time.Duration(e.MS) * time.Millisecond
		}
	}
	if slept > time.Second {
		t.Fatalf("path slept %s for a 1s drag", slept)
	}
	if slept < 900*time.Millisecond {
		t.Fatalf("path slept %s: a drag that finishes early is not the drag that was asked for", slept)
	}
}

// Two points is a swipe. They are the same gesture, so they are the same code -
// a second implementation is a second set of timings to keep in step.
func TestPath_OfTwoPointsIsExactlyASwipe(t *testing.T) {
	from, to := Point{X: 0.5, Y: 0.8}, Point{X: 0.5, Y: 0.2}
	swipe, err := Swipe(from, to, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("swipe: %v", err)
	}
	path, err := Path([]Point{from, to}, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if !reflect.DeepEqual(swipe, path) {
		t.Fatalf("a two-point path and a swipe must be one gesture:\nswipe=%+v\npath =%+v", swipe, path)
	}
}

func TestPath_RefusesWhatWouldNotLand(t *testing.T) {
	ok := []Point{{X: 0.5, Y: 0.5}, {X: 0.5, Y: 0.2}}
	for name, bad := range map[string][]Point{
		"nothing at all":  {},
		"a single point":  {{X: 0.5, Y: 0.5}},
		"off the screen":  {{X: 0.5, Y: 0.5}, {X: 0.5, Y: 1.5}},
		"a start off it":  {{X: -0.1, Y: 0.5}, {X: 0.5, Y: 0.5}},
		"a middle off it": {{X: 0.5, Y: 0.5}, {X: 2, Y: 0.5}, {X: 0.5, Y: 0.5}},
	} {
		if _, err := Path(bad, 300*time.Millisecond); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
	if _, err := Path(ok, MaxSwipeDuration+time.Second); err == nil {
		t.Fatal("a path longer than its own hold must be refused")
	}
	if _, err := Path(ok, 0); err == nil {
		t.Fatal("a path with no duration must be refused")
	}
}

func TestForwardKeys_SendsThePositionAndItsShift(t *testing.T) {
	// The keys a Thai Mac uses for "ดฟ" are the positions US keyboards print
	// `f` and `a` on, and shift is part of the press rather than of the letter.
	events, err := ForwardKeys([]KeyPress{{Code: "KeyF"}, {Code: "KeyA", Shift: true}})
	if err != nil {
		t.Fatalf("ForwardKeys: %v", err)
	}
	var keys []Event
	for _, e := range events {
		if e.Kind == "key" {
			keys = append(keys, e)
		}
	}
	want := []struct {
		usage int
		typ   string
	}{
		{9, "down"}, {9, "up"},
		{225, "down"}, {4, "down"}, {4, "up"}, {225, "up"},
	}
	if len(keys) != len(want) {
		t.Fatalf("got %d key events, want %d: %+v", len(keys), len(want), keys)
	}
	for i, w := range want {
		if keys[i].Usage != w.usage || keys[i].Type != w.typ {
			t.Fatalf("key %d = usage %d %s, want usage %d %s", i, keys[i].Usage, keys[i].Type, w.usage, w.typ)
		}
	}
}

func TestForwardKeys_IsTheSameUsagesTheUSTableWouldSend(t *testing.T) {
	// 🗝 What `KeyboardEvent.code` MEANS, written out independently of how the
	// table is built: each position is named for the character a US keyboard
	// prints on it. So forwarding a position must compose exactly what typing
	// that character composes - every position, not a sample. A single wrong
	// entry here is a key that lands somewhere else on the device, which on a
	// Thai guest is a different letter and in a secure field is invisible.
	positions := map[string]string{
		"Digit1": "1", "Digit2": "2", "Digit3": "3", "Digit4": "4", "Digit5": "5",
		"Digit6": "6", "Digit7": "7", "Digit8": "8", "Digit9": "9", "Digit0": "0",
		"Minus": "-", "Equal": "=", "BracketLeft": "[", "BracketRight": "]", "Backslash": `\`,
		"Semicolon": ";", "Quote": "'", "Backquote": "`", "Comma": ",", "Period": ".",
		"Slash": "/", "Space": " ",
	}
	for r := 'a'; r <= 'z'; r++ {
		positions["Key"+string(r-32)] = string(r)
	}
	if len(positions) != len(keyPositions) {
		t.Fatalf("the table has %d positions and this test knows %d", len(keyPositions), len(positions))
	}
	for code, char := range positions {
		forwarded, err := ForwardKeys([]KeyPress{{Code: code}})
		if err != nil {
			t.Fatalf("ForwardKeys(%s): %v", code, err)
		}
		typed, err := TypeRaw(char)
		if err != nil {
			t.Fatalf("TypeRaw(%q): %v", char, err)
		}
		if !reflect.DeepEqual(forwarded, typed) {
			t.Fatalf("%s does not send what typing %q sends: %+v vs %+v", code, char, forwarded, typed)
		}
	}
	// And shifted: the position is the same key, held with shift, which is what
	// the US table does for the shifted character.
	forwarded, err := ForwardKeys([]KeyPress{{Code: "KeyA", Shift: true}, {Code: "Digit1", Shift: true}})
	if err != nil {
		t.Fatalf("ForwardKeys: %v", err)
	}
	typed, err := TypeRaw("A!")
	if err != nil {
		t.Fatalf("TypeRaw: %v", err)
	}
	if !reflect.DeepEqual(forwarded, typed) {
		t.Fatalf("a shifted position must send the shifted key: %+v vs %+v", forwarded, typed)
	}
}

func TestForwardKeys_RefusesAPositionItHasNoUsageFor(t *testing.T) {
	// Refused rather than dropped: a keystroke that silently sends nothing is
	// the failure this package exists to make impossible.
	for _, code := range []string{"F5", "IntlRo", ""} {
		_, err := ForwardKeys([]KeyPress{{Code: code}})
		var unknown *UnknownKeyError
		if !errors.As(err, &unknown) {
			t.Fatalf("ForwardKeys(%q) must report an unknown position, got %v", code, err)
		}
	}
	if _, err := ForwardKeys(nil); err == nil {
		t.Fatal("forwarding nothing is a mistake worth reporting, not a no-op")
	}
	long := make([]KeyPress, MaxTypeRunes+1)
	for i := range long {
		long[i] = KeyPress{Code: "KeyA"}
	}
	if _, err := ForwardKeys(long); err == nil {
		t.Fatal("a run longer than a gesture hold can cover must be refused")
	}
}

func TestPlanText_ForwardsTheKeysAPersonPressedWhenTheGuestReadsThemAsTyped(t *testing.T) {
	// The case forwarding exists for: the person pressed the key that prints
	// `f`, the guest reads US key presses, so the key goes as itself and the
	// app sees a real keystroke rather than a paste.
	route, err := PlanText("fa", ProbedKeyboard{Mode: usMode},
		TextOptions{Keys: []KeyPress{{Code: "KeyF"}, {Code: "KeyA"}}})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Paste {
		t.Fatal("a key a person pressed that the guest reads as typed must be forwarded, not pasted")
	}
	if !route.Forwarded {
		t.Fatal("the route must say it forwarded keys")
	}
	keys, err := ForwardKeys([]KeyPress{{Code: "KeyF"}, {Code: "KeyA"}})
	if err != nil {
		t.Fatalf("ForwardKeys: %v", err)
	}
	if !reflect.DeepEqual(route.Events, keys) {
		t.Fatal("forwarding must send exactly the keys that were pressed")
	}
}

func TestPlanText_ShiftIsPartOfThePressAndStillForwards(t *testing.T) {
	route, err := PlanText("A!", ProbedKeyboard{Mode: usMode},
		TextOptions{Keys: []KeyPress{{Code: "KeyA", Shift: true}, {Code: "Digit1", Shift: true}}})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if !route.Forwarded {
		t.Fatal("a shifted press produces the shifted character on a US guest, so it is faithful")
	}
}

func TestPlanText_DoesNotForwardAKeyTheGuestWouldReadAsAnotherCharacter(t *testing.T) {
	// 🗝 Bug #277, in one assertion. `KeyF` is the key a Thai Mac prints "ด"
	// on; a guest sitting on en_US reads that same position as "f" and the
	// person gets a character they never pressed, reported as success.
	// Observed on a real device: `ดฟ` arrived as "Fa".
	route, err := PlanText("ดฟ", ProbedKeyboard{Mode: usMode},
		TextOptions{Keys: []KeyPress{{Code: "KeyF"}, {Code: "KeyA"}}})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Forwarded {
		t.Fatal("a US guest would read these positions as \"fa\", so the keys must not be sent as themselves")
	}
	if !route.Paste {
		t.Fatal("the characters the person typed must still arrive, by the route that can carry them")
	}
	if !strings.Contains(route.Why, "ด") {
		t.Fatalf("Why = %q, must name what forced the pasteboard", route.Why)
	}
}

func TestPlanText_DoesNotForwardIntoAGuestThatWouldNotSayWhatItsKeyboardIs(t *testing.T) {
	// An unanswered probe is not a US keyboard - the assumption this package
	// exists to refuse - and forwarding is not a door around that.
	route, err := PlanText("ด", ProbedKeyboard{Err: errors.New("device is not booted")},
		TextOptions{Keys: []KeyPress{{Code: "KeyF"}}})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Forwarded {
		t.Fatal("a guest that would not say what it reads must never have keys sent to it as themselves")
	}
	if !route.Paste {
		t.Fatal("the character must still arrive")
	}
}

func TestPlanText_DoesNotForwardIntoAGuestThatWouldRemapTheKeys(t *testing.T) {
	// The mirror of the reported bug: an English typist on a Thai guest. The
	// positions are the ones that print "fa", and this guest would make
	// "ดฟ" of them.
	route, err := PlanText("fa", ProbedKeyboard{Mode: thaiMode},
		TextOptions{Keys: []KeyPress{{Code: "KeyF"}, {Code: "KeyA"}}})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Forwarded {
		t.Fatal("a guest that remaps these positions must not be sent them as themselves")
	}
	if !route.Paste || !strings.Contains(route.Why, "th_TH") {
		t.Fatalf("route = %+v, want the pasteboard with the guest's mode named", route)
	}
}

func TestPlanText_CapsLockIsCaughtByTheCharacterNotByTheModifier(t *testing.T) {
	// 🗝 With Caps Lock on, the Mac produces "F" from an unshifted press and
	// the guest would produce "f" from the same position. The pane no longer
	// has to notice - which matters, because on a Mac that uses Caps Lock to
	// SWITCH INPUT SOURCE the modifier state is never set at all, and that is
	// exactly the Mac this bug was reported from. The characters simply do not
	// line up, so the keys are dropped.
	route, err := PlanText("F", ProbedKeyboard{Mode: usMode}, TextOptions{Keys: []KeyPress{{Code: "KeyF"}}})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Forwarded {
		t.Fatal("an unshifted position cannot account for a capital letter")
	}
	// And the character is still typed rather than pasted: "F" has a US key.
	if route.Paste {
		t.Fatal("a character the US keyboard can send does not need the pasteboard")
	}
	events, err := TypeRaw("F")
	if err != nil {
		t.Fatalf("TypeRaw: %v", err)
	}
	if !reflect.DeepEqual(route.Events, events) {
		t.Fatal("the character the person saw is what has to be sent")
	}
}

func TestPositionRunes_AgreeWithTheUSKeyboardTable(t *testing.T) {
	// The two tables are one fact - `code` is defined by the character the
	// position carries on a US keyboard - so every position must round-trip
	// back to the usage that sends it.
	for code := range keyPositions {
		for _, shift := range []bool{false, true} {
			r, ok := positionRune(KeyPress{Code: code, Shift: shift})
			if !ok {
				t.Fatalf("%s (shift=%v) has a usage but no character", code, shift)
			}
			key, ok := usKeyboard[r]
			if !ok {
				t.Fatalf("%s (shift=%v) reads as %q, which the US table cannot send", code, shift, string(r))
			}
			if key.usage != keyPositions[code] {
				t.Fatalf("%s (shift=%v) reads as %q, which is sent by another position", code, shift, string(r))
			}
			// Space is the one key with nothing on its shifted half.
			if code != "Space" && key.shift != shift {
				t.Fatalf("%s (shift=%v) reads as %q, which needs shift=%v", code, shift, string(r), key.shift)
			}
		}
	}
	if _, ok := positionRune(KeyPress{Code: "F5"}); ok {
		t.Fatal("a position with no usage has no character either")
	}
}

func TestPlanText_FallsBackToTheTextWhenAKeyCannotBeForwarded(t *testing.T) {
	// The position is one this package has no usage for - so it cannot be
	// forwarded and cannot be read either. The CHARACTER is still known, so
	// the ordinary planner delivers it. Slower, never wrong.
	route, err := PlanText("ด", ProbedKeyboard{Mode: usMode}, TextOptions{Keys: []KeyPress{{Code: "IntlRo"}}})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Forwarded {
		t.Fatal("a position with no usage cannot have been forwarded")
	}
	if !route.Paste {
		t.Fatal("the character must still reach the device, by the route that can carry it")
	}
}

func TestPlanText_RefusesKeysThatDoNotMatchTheText(t *testing.T) {
	// The text is what a recording keeps. If it disagrees with the keys, one of
	// them is a lie and there is no way to tell which.
	_, err := PlanText("ดฟ", ProbedKeyboard{Mode: usMode}, TextOptions{Keys: []KeyPress{{Code: "KeyF"}}})
	if err == nil {
		t.Fatal("one key press cannot account for two characters")
	}
	if !strings.Contains(err.Error(), "same keystrokes") {
		t.Fatalf("error = %q, must say what has to line up", err)
	}
}

func TestPlanText_RefusesForwardedKeysCombinedWithARouteFlag(t *testing.T) {
	for _, opts := range []TextOptions{
		{Keys: []KeyPress{{Code: "KeyF"}}, Paste: true},
		{Keys: []KeyPress{{Code: "KeyF"}}, RawKeys: true},
	} {
		if _, err := PlanText("ด", ProbedKeyboard{Mode: usMode}, opts); err == nil {
			t.Fatalf("%+v asks for two routes at once and has no answer", opts)
		}
	}
}

func TestPlanText_WithoutKeysIsUnchanged(t *testing.T) {
	// The agent-facing promise: a caller that did not watch a person press
	// anything still gets the planned, proven route. Locked here so a future
	// change to forwarding cannot quietly become the default.
	route, err := PlanText("fa12345", ProbedKeyboard{Mode: thaiMode}, TextOptions{})
	if err != nil {
		t.Fatalf("PlanText: %v", err)
	}
	if route.Forwarded || !route.Paste {
		t.Fatal("text with no keys behind it must still be planned from the guest's input mode")
	}
}

// A pinch is the one gesture here that is not one finger, so what the tests owe
// it is different: not "did the finger land" but "were there two of them, at
// once, and did the gap between them end up where it was asked to".

func multiTouchEvents(events []Event) []Event {
	var out []Event
	for _, e := range events {
		if e.Kind == "multitouch" {
			out = append(out, e)
		}
	}
	return out
}

func multiTouchTypes(events []Event) string {
	var parts []string
	for _, e := range multiTouchEvents(events) {
		parts = append(parts, e.Type)
	}
	return strings.Join(parts, ",")
}

func TestPinch_MovesTwoFingersToTheGapItWasAskedFor(t *testing.T) {
	center := Point{X: 0.5, Y: 0.4}
	events, err := Pinch(center, 0.2, 0.6, 400*time.Millisecond)
	if err != nil {
		t.Fatalf("pinch: %v", err)
	}
	if len(touchEvents(events)) != 0 {
		t.Fatal("a pinch that emits a one-finger touch would leave a contact behind when the pair is released")
	}
	types := multiTouchTypes(events)
	if !strings.HasPrefix(types, "begin,move") || !strings.HasSuffix(types, "end") {
		t.Fatalf("pinch sequence = %q, want begin, moves, end", types)
	}
	if strings.Count(types, "move") < 2 {
		t.Fatalf("a pinch with no intermediate moves gives a recognizer nothing to scale from: %q", types)
	}

	contacts := multiTouchEvents(events)
	first, last := contacts[0], contacts[len(contacts)-1]
	if math.Abs((first.X2-first.X)-0.2) > 1e-9 {
		t.Fatalf("fingers start %g apart, want 0.2", first.X2-first.X)
	}
	if math.Abs((last.X2-last.X)-0.6) > 1e-9 {
		t.Fatalf("fingers end %g apart, want 0.6", last.X2-last.X)
	}
	for i, e := range contacts {
		if e.Y != center.Y || e.Y2 != center.Y {
			t.Fatalf("event %d left the horizontal line through the centre: %+v", i, e)
		}
		// Symmetric about the centre, or the pinch also drags the content.
		if mid := (e.X + e.X2) / 2; math.Abs(mid-center.X) > 1e-9 {
			t.Fatalf("event %d is centred on %g, want %g", i, mid, center.X)
		}
		if e.X > e.X2 {
			t.Fatalf("event %d crossed the fingers over: %+v", i, e)
		}
	}
}

func TestPinch_ClosingIsTheSameGestureBackwards(t *testing.T) {
	events, err := Pinch(Point{X: 0.5, Y: 0.5}, 0.6, 0.2, 400*time.Millisecond)
	if err != nil {
		t.Fatalf("pinch: %v", err)
	}
	contacts := multiTouchEvents(events)
	first, last := contacts[0], contacts[len(contacts)-1]
	if first.X2-first.X <= last.X2-last.X {
		t.Fatal("a pinch from a wide gap to a narrow one must bring the fingers together")
	}
	if got := PinchScale(0.6, 0.2); math.Abs(got-(1.0/3.0)) > 1e-9 {
		t.Fatalf("scale = %g, want a third", got)
	}
}

func TestPinch_RefusesWhatWouldNotBeAPinch(t *testing.T) {
	center := Point{X: 0.5, Y: 0.5}
	for _, tc := range []struct {
		name       string
		center     Point
		from, to   float64
		duration   time.Duration
		wantSubstr string
	}{
		{"a gap too small to be two touches", center, 0.005, 0.4, 400 * time.Millisecond, "land as one"},
		{"a gap that runs off the screen", Point{X: 0.2, Y: 0.5}, 0.2, 0.9, 400 * time.Millisecond, "leave the screen"},
		{"no change in the gap at all", center, 0.3, 0.3, 400 * time.Millisecond, "nothing would zoom"},
		{"a centre off the screen", Point{X: 1.4, Y: 0.5}, 0.2, 0.4, 400 * time.Millisecond, "normalized 0..1"},
		{"no duration", center, 0.2, 0.4, 0, "duration"},
		{"a duration past the hold", center, 0.2, 0.4, MaxSwipeDuration + time.Second, "duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Pinch(tc.center, tc.from, tc.to, tc.duration)
			if err == nil {
				t.Fatal("must be refused: a pinch that sends events and changes nothing reads exactly like one that worked")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q does not say %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestPinch_RefusalNamesTheGapThatWouldFit(t *testing.T) {
	_, err := Pinch(Point{X: 0.2, Y: 0.5}, 0.2, 0.9, 400*time.Millisecond)
	if err == nil {
		t.Fatal("a gap that does not fit must be refused, not narrowed")
	}
	// 0.2 from the left edge leaves 0.4 for the whole gap.
	if !strings.Contains(err.Error(), "0.4") {
		t.Fatalf("error %q does not say the widest gap that fits", err)
	}
}

func TestDuration_CoversAPinch(t *testing.T) {
	const want = 400 * time.Millisecond
	events, err := Pinch(Point{X: 0.5, Y: 0.5}, 0.2, 0.6, want)
	if err != nil {
		t.Fatalf("pinch: %v", err)
	}
	// The hold is sized from this, and a hold that lapses mid-pinch is the
	// window another caller needs to take the screen with two fingers on it.
	if got := Duration(events); got < want {
		t.Fatalf("duration = %s, want at least the %s the pinch takes", got, want)
	}
}

func TestRecover_ReleasesWhatTheGestureActuallyHeld(t *testing.T) {
	pinch, err := Pinch(Point{X: 0.5, Y: 0.5}, 0.2, 0.6, 400*time.Millisecond)
	if err != nil {
		t.Fatalf("pinch: %v", err)
	}
	release := Recover(pinch, Point{X: 0.5, Y: 0.5})
	if len(release) != 1 || release[0].Kind != "multitouch" || release[0].Type != "end" {
		t.Fatalf("a pinch must be recovered as a pair, not a finger: %+v", release)
	}
	// At the points the pinch actually left them, or the release lands
	// somewhere the fingers never were.
	last := multiTouchEvents(pinch)[len(multiTouchEvents(pinch))-1]
	if release[0].X != last.X || release[0].X2 != last.X2 {
		t.Fatalf("release at %g/%g, want the last contact points %g/%g", release[0].X, release[0].X2, last.X, last.X2)
	}

	swipe, err := Swipe(Point{X: 0.5, Y: 0.8}, Point{X: 0.5, Y: 0.2}, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("swipe: %v", err)
	}
	at := Point{X: 0.5, Y: 0.2}
	if got := Recover(swipe, at); !reflect.DeepEqual(got, Lift(at)) {
		t.Fatalf("a one-finger gesture must still be recovered with a bare lift: %+v", got)
	}

	keys, err := TypeRaw("hi")
	if err != nil {
		t.Fatalf("type: %v", err)
	}
	if got := Recover(keys, at); got != nil {
		t.Fatalf("nothing touched the screen, so nothing may be sent to recover it: %+v", got)
	}
}
