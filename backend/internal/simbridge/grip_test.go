package simbridge

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The claim that makes `ao sim pinch` and a live two-finger drag one feature
// rather than two: the one-shot gesture is composed from exactly the frames the
// held path sends, so there is no second definition of "two fingers move" to
// drift from the first.
func TestPinch_IsTheHeldPrimitiveComposedInAdvance(t *testing.T) {
	center := Point{X: 0.5, Y: 0.5}
	events, err := Pinch(center, 0.2, 0.6, 400*time.Millisecond)
	if err != nil {
		t.Fatalf("pinch: %v", err)
	}

	var frames []Event
	for _, e := range events {
		if e.Kind != "sleep" {
			frames = append(frames, e)
		}
	}
	if got := frames[0]; !reflect.DeepEqual(got, PinchGrip(center, 0.2).Event("begin")) {
		t.Fatalf("the first frame is %+v, not the primitive's own begin", got)
	}
	if got := frames[len(frames)-1]; !reflect.DeepEqual(got, PinchGrip(center, 0.6).Event("end")) {
		t.Fatalf("the last frame is %+v, not the primitive's own end", got)
	}
	// Every step in between is a grip's move at some span, which is what a
	// caller tracking a human's fingers would send one at a time. Compared with
	// a tolerance because recovering the span by subtraction and rebuilding the
	// grip from it is not bit-identical arithmetic; what is being asserted is
	// the SHAPE, not the last bit of a float.
	for i, frame := range frames[1 : len(frames)-1] {
		want := PinchGrip(center, frame.X2-frame.X).Event("move")
		if frame.Kind != want.Kind || frame.Type != want.Type {
			t.Fatalf("move %d is %+v, want the primitive's %+v", i, frame, want)
		}
		if math.Abs(frame.X-want.X) > 1e-9 || math.Abs(frame.X2-want.X2) > 1e-9 ||
			frame.Y != want.Y || frame.Y2 != want.Y2 {
			t.Fatalf("move %d is %+v, want the primitive's %+v", i, frame, want)
		}
	}
}

func TestGrip_ComposesTheFrameTheContactCountNeeds(t *testing.T) {
	one := OneFinger(Point{X: 0.4, Y: 0.6}).Event("begin")
	if one.Kind != "touch" || one.X != 0.4 || one.Y != 0.6 {
		t.Fatalf("one finger = %+v, want a single-point touch", one)
	}
	if one.X2 != 0 || one.Y2 != 0 {
		t.Fatalf("one finger carries a second contact: %+v", one)
	}
	two := TwoFingers(Point{X: 0.3, Y: 0.5}, Point{X: 0.7, Y: 0.5}).Event("move")
	if two.Kind != "multitouch" || two.X != 0.3 || two.X2 != 0.7 {
		t.Fatalf("two fingers = %+v, want both contacts in one frame", two)
	}
}

// At is what a recording writes down and what a report prints. For a pinch that
// is the point it was centred on - and never a point a release is composed from,
// because no finger was ever there.
func TestGrip_AtDescribesThePairByItsCentre(t *testing.T) {
	grip := PinchGrip(Point{X: 0.4, Y: 0.7}, 0.3)
	if got := grip.At(); got.X != 0.4 || got.Y != 0.7 {
		t.Fatalf("At = %+v, want the centre the pinch was about", got)
	}
	if release := Release(grip); release[0].X == release[0].X2 {
		t.Fatalf("the release collapsed both contacts onto one point: %+v", release[0])
	}
}

func TestGrip_RefusesContactsTheDeviceWouldNotLand(t *testing.T) {
	for _, tc := range []struct {
		name string
		grip Grip
		want string
	}{
		{"a finger off the screen", OneFinger(Point{X: 1.4, Y: 0.5}), "normalized 0..1"},
		{"a second contact off the screen", TwoFingers(Point{X: 0.5, Y: 0.5}, Point{X: 1.4, Y: 0.5}), "finger 2"},
		{"two contacts on the same spot", TwoFingers(Point{X: 0.5, Y: 0.5}, Point{X: 0.5, Y: 0.5}), "land as one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.grip.Validate("grip")
			if err == nil {
				t.Fatal("must be refused: a contact that never lands looks exactly like one that did")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not say %q", err, tc.want)
			}
		})
	}
	if err := TwoFingers(Point{X: 0.3, Y: 0.5}, Point{X: 0.7, Y: 0.5}).Validate("grip"); err != nil {
		t.Fatalf("a landable pair must be accepted: %v", err)
	}
}

// A release must land somewhere rather than be refused - a contact left down is
// worse than one released a pixel from where it was.
func TestGrip_ReleaseIsClampedOntoTheScreen(t *testing.T) {
	release := Release(TwoFingers(Point{X: -0.2, Y: 0.5}, Point{X: 1.3, Y: 0.5}))
	if len(release) != 1 || release[0].X != 0 || release[0].X2 != 1 {
		t.Fatalf("release = %+v, want both contacts pulled onto the screen", release)
	}
}

// The held path may hand in a pair separated any way at all, so "are these two
// contacts effectively one touch" is a DISTANCE, not a horizontal gap. Measuring
// only X refuses a vertical pair that is half a screen apart.
func TestGrip_TooCloseIsMeasuredAsADistance(t *testing.T) {
	vertical := TwoFingers(Point{X: 0.5, Y: 0.2}, Point{X: 0.5, Y: 0.8})
	if err := vertical.Validate("grip"); err != nil {
		t.Fatalf("two fingers 0.6 apart vertically must be accepted: %v", err)
	}
	diagonal := TwoFingers(Point{X: 0.500, Y: 0.500}, Point{X: 0.505, Y: 0.505})
	if err := diagonal.Validate("grip"); err == nil {
		t.Fatal("two contacts a hair apart on a diagonal still land as one touch and must be refused")
	}
}
