package simbridge

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
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
	events, err := Type("aA1!\n\t")
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
		_, err := Type(text)
		if err == nil {
			t.Fatalf("Type(%q) must be refused", text)
		}
		if !strings.Contains(err.Error(), "US keyboard") {
			t.Fatalf("Type(%q) error must explain the limit, got %v", text, err)
		}
	}
	if _, err := Type(""); err == nil {
		t.Fatal("typing nothing is a mistake worth reporting, not a no-op")
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

	typing, err := Type(strings.Repeat("a", 500))
	if err != nil {
		t.Fatalf("type: %v", err)
	}
	if got := Duration(typing); got < 500*keyStep {
		t.Fatalf("typing duration = %s, want at least %s", got, 500*keyStep)
	}
}

func TestType_RefusesTextLongerThanAGestureHoldCanCover(t *testing.T) {
	_, err := Type(strings.Repeat("a", MaxTypeRunes+1))
	if err == nil {
		t.Fatal("text long enough to outlive its own gesture hold must be refused, not sent")
	}
	if !strings.Contains(err.Error(), "shorter") {
		t.Fatalf("the refusal must say what to do, got %v", err)
	}
	if _, err := Type(strings.Repeat("a", MaxTypeRunes)); err != nil {
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
