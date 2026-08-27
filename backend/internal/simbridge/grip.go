package simbridge

import "fmt"

// Grip is what is touching the screen right now: one finger, or the two of a
// pinch.
//
// It exists because the HELD path - down, then a move whenever the caller knows
// where the fingers went, then up - is one path whether one contact is down or
// two. Only the HID frame each step becomes differs, and the addon composes
// that frame itself: `touch` carries one point, `multiTouch` carries both in a
// single frame, which is what makes two contacts SIMULTANEOUS rather than two
// touches in a row.
//
// So the number of fingers is a property of the grip and of nothing else. The
// alternative - a point for the one-finger path and a separate pair type beside
// it - would be two watchdogs, two registries, two recovery lifts and two places
// to forget one, and the failure mode of forgetting is a contact left down,
// which wedges the device until it is rebooted.
//
// 🗝 Every gesture in this package is built on this vocabulary, including the
// one-shot ones: `Pinch` is begin/move/end on a two-finger grip, exactly what a
// live pinch that tracks a human's fingers would send, so there is no second
// definition of "how two fingers move" to drift from the first.
type Grip struct {
	// a is the first contact; b is the second, and only a pair has one. Two is
	// the ceiling rather than a simplification: the addon's frame carries two
	// points, so a third finger has nowhere to go.
	a, b Point
	pair bool
}

// OneFinger is the grip every gesture but a pinch uses.
func OneFinger(at Point) Grip { return Grip{a: at} }

// TwoFingers is the grip a pinch uses: both contacts land, move and lift
// together, in one frame each.
func TwoFingers(a, b Point) Grip { return Grip{a: a, b: b, pair: true} }

// Pair reports whether two contacts are down. A held touch may not change it
// mid-way: the contact that disappeared was never lifted.
func (g Grip) Pair() bool { return g.pair }

// Points is where the contacts are - one, or two.
func (g Grip) Points() []Point {
	if g.pair {
		return []Point{g.a, g.b}
	}
	return []Point{g.a}
}

// At is the single point that describes this grip: the finger, or the midpoint
// between two. It is what a recording writes down and what a report prints -
// never what a release is composed from, which is Event's job, because the
// midpoint of a pinch is a place no finger ever was.
func (g Grip) At() Point {
	if g.pair {
		return Point{X: (g.a.X + g.b.X) / 2, Y: (g.a.Y + g.b.Y) / 2}
	}
	return g.a
}

// Event is one step of the held path: "begin", "move" or "end".
//
// This is the primitive. A continuous two-finger drag is these three in the
// order a caller learns them, and `Pinch` is these three composed in advance -
// the same events either way, which is the whole point of there being one
// constructor for them.
func (g Grip) Event(phase string) Event {
	if g.pair {
		return Event{Kind: "multitouch", Type: phase, X: g.a.X, Y: g.a.Y, X2: g.b.X, Y2: g.b.Y}
	}
	return Event{Kind: "touch", Type: phase, X: g.a.X, Y: g.a.Y}
}

// Validate refuses a grip the device would not land: a contact off the screen
// never arrives, and two contacts on the same spot arrive as one - which reads
// exactly like a pinch that worked while nothing zoomed.
func (g Grip) Validate(what string) error {
	for i, p := range g.Points() {
		name := what
		if g.pair {
			name = fmt.Sprintf("%s finger %d", what, i+1)
		}
		if err := validatePoint(name, p); err != nil {
			return err
		}
	}
	if g.pair {
		if gap := g.b.X - g.a.X; gap < MinPinchSpan && gap > -MinPinchSpan {
			return fmt.Errorf("%s puts the two fingers %g apart, which is closer than %g of the screen's width: "+
				"any closer and they land as one touch, which no app reads as two", what, gap, MinPinchSpan)
		}
	}
	return nil
}

// Clamped is this grip with every contact pulled back onto the screen. It is
// only for a RELEASE: a lift must land somewhere rather than be refused, and a
// release a pixel off where the finger was still releases it.
func (g Grip) Clamped() Grip {
	out := Grip{a: Point{X: clamp01(g.a.X), Y: clamp01(g.a.Y)}, pair: g.pair}
	if g.pair {
		out.b = Point{X: clamp01(g.b.X), Y: clamp01(g.b.Y)}
	}
	return out
}
