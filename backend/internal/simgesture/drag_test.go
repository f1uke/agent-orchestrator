package simgesture_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
)

// at is one finger, which is what the Device tab's pointer is and what every
// test below drags with. A pinch's two-finger grip has its own tests.
func at(x, y float64) simbridge.Grip { return simbridge.OneFinger(simbridge.Point{X: x, Y: y}) }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The whole point of a drag: the finger goes down once, follows, and comes up
// once - and the hold spans all of it rather than being taken and given back
// per move.
func TestDrags_FollowTheFingerUnderOneHold(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)
	ctx := context.Background()

	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.8)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, y := range []float64{0.7, 0.6, 0.5} {
		if err := drags.Move(ctx, rec, "UDID-A", "p-1", at(0.5, y)); err != nil {
			t.Fatalf("move: %v", err)
		}
	}
	if err := drags.End(ctx, "UDID-A", "p-1", at(0.5, 0.5)); err != nil {
		t.Fatalf("end: %v", err)
	}

	if got := rec.order(); got != "acquire,hold,hold,hold,hold,lift,release" {
		t.Fatalf("order = %q; want one hold taken up front, the touch followed, then one lift and one release", got)
	}
	if performed, ok := rec.lastReleasePerformed(); !ok || !performed {
		t.Fatalf("a drag the caller ended must be released as performed: performed=%v ok=%v", performed, ok)
	}
	// The hold was taken when the finger went down, so nothing upstream knows
	// where the drag ended until it does. The release is what carries that
	// back; without it a recording keeps the begin's own point as the end.
	outcome, ok := rec.lastRelease()
	if !ok || outcome.End == nil {
		t.Fatalf("a drag must report where it ended when its hold is released: %+v", outcome)
	}
	if *outcome.End != at(0.5, 0.5).At() {
		t.Fatalf("released end = %+v, want the point the drag ended at (0.5,0.5)", *outcome.End)
	}
}

// A move is the only thing in this package that reaches a device without taking
// a hold, so a move with no touch down must reach nothing at all.
func TestDrags_AMoveWithNothingDownTouchesTheDevice(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)

	err := drags.Move(context.Background(), rec, "UDID-A", "p-1", at(0.5, 0.5))
	if !errors.Is(err, simgesture.ErrNoDrag) {
		t.Fatalf("err = %v, want ErrNoDrag", err)
	}
	if got := rec.order(); got != "" {
		t.Fatalf("a stray move reached the device: %q", got)
	}
}

// The arbitration this replaces a per-gesture hold with: while a finger is down
// no other session may take the device, and must not be able to move it.
func TestDrags_AnotherSessionCannotTakeTheFingerMidDrag(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)
	ctx := context.Background()
	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.8)); err != nil {
		t.Fatalf("begin: %v", err)
	}

	if err := drags.Move(ctx, rec, "UDID-A", "other-7", at(0.5, 0.5)); !errors.Is(err, simgesture.ErrDragHeldByOther) {
		t.Fatalf("move by another session: err = %v, want ErrDragHeldByOther", err)
	}
	if err := drags.Begin(ctx, rec, rec, "UDID-A", "other-7", at(0.1, 0.1)); !errors.Is(err, simgesture.ErrDragHeldByOther) {
		t.Fatalf("begin by another session: err = %v, want ErrDragHeldByOther", err)
	}
	// And an end from the wrong session must not lift somebody else's finger.
	if err := drags.End(ctx, "UDID-A", "other-7", at(0.5, 0.5)); err != nil {
		t.Fatalf("end by another session: %v", err)
	}
	if got := rec.order(); got != "acquire,hold" {
		t.Fatalf("order = %q; another session reached the device mid-drag", got)
	}
}

// The guarantee that makes a held finger safe at all: a client that goes away
// mid-drag costs the device a couple of seconds, not a reboot.
func TestDrags_AQuietDragIsLiftedAndTheHoldGivenBack(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(20*time.Millisecond, time.Minute)

	if err := drags.Begin(context.Background(), rec, rec, "UDID-A", "p-1", at(0.5, 0.8)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	eventually(t, "the watchdog to lift the finger", func() bool {
		return rec.order() == "acquire,hold,lift,release"
	})

	// And the device is free again: a new drag may start.
	if err := drags.Begin(context.Background(), rec, rec, "UDID-A", "p-2", at(0.2, 0.2)); err != nil {
		t.Fatalf("a device whose drag was lifted must be usable again: %v", err)
	}
}

// A drag the watchdog lifted was abandoned, not completed - the client that
// started it never said it was done. A session recording gestures must not
// write down a drag nobody actually finished.
func TestDrags_AnAbandonedDragIsReleasedAsNotPerformed(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(20*time.Millisecond, time.Minute)

	if err := drags.Begin(context.Background(), rec, rec, "UDID-A", "p-1", at(0.5, 0.8)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	eventually(t, "the watchdog to lift the finger", func() bool {
		return rec.order() == "acquire,hold,lift,release"
	})
	if performed, ok := rec.lastReleasePerformed(); !ok || performed {
		t.Fatalf("a drag abandoned to the watchdog must be released as not performed: performed=%v ok=%v", performed, ok)
	}
}

// A completed drag reached the device: the app saw it and moved. Whether the
// finger came back up is a separate fact with its own warning, and folding the
// two together would silently drop a step the human actually performed.
func TestDrag_CompletedDragIsPerformedEvenWhenTheFinalLiftFails(t *testing.T) {
	rec := &recorder{liftErr: errors.New("still gone")}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)
	ctx := context.Background()

	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.8)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	err := drags.End(ctx, "UDID-A", "p-1", at(0.5, 0.5))
	var failed *simgesture.FailedError
	if !errors.As(err, &failed) || failed.LiftErr == nil {
		t.Fatalf("a lift that failed must still be reported loudly: %v", err)
	}
	performed, ok := rec.lastReleasePerformed()
	if !ok {
		t.Fatal("the hold must still be released even though the lift failed")
	}
	if !performed {
		t.Fatal("a drag the caller deliberately ended reached the device and must be released as performed, " +
			"even though its final lift failed - that failure is reported separately and must not erase the step")
	}
}

// A drag that keeps moving must not be lifted under the human's finger.
func TestDrags_MovingKeepsTheWatchdogBack(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(60*time.Millisecond, time.Minute)
	ctx := context.Background()
	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.9)); err != nil {
		t.Fatalf("begin: %v", err)
	}

	for range 6 {
		time.Sleep(20 * time.Millisecond)
		if err := drags.Move(ctx, rec, "UDID-A", "p-1", at(0.5, 0.5)); err != nil {
			t.Fatalf("a drag that is still moving was lifted under the finger: %v", err)
		}
	}
	if err := drags.End(ctx, "UDID-A", "p-1", at(0.5, 0.5)); err != nil {
		t.Fatalf("end: %v", err)
	}
}

// However much it moves, a drag does not own a device for ever.
func TestDrags_ADragThatNeverEndsIsLiftedAtItsCeiling(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(time.Second, 30*time.Millisecond)
	ctx := context.Background()
	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.9)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := drags.Move(ctx, rec, "UDID-A", "p-1", at(0.5, 0.5)); !errors.Is(err, simgesture.ErrNoDrag) {
		t.Fatalf("err = %v, want the drag to have been ended at its ceiling", err)
	}
	if got := rec.order(); got != "acquire,hold,lift,release" {
		t.Fatalf("order = %q; want the finger lifted and the hold given back", got)
	}
}

// The end may arrive after the watchdog already lifted. That race is ordinary,
// and must not be reported as a touch that failed.
func TestDrags_AnEndAfterTheWatchdogIsNotAFailure(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(15*time.Millisecond, time.Minute)
	ctx := context.Background()
	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.9)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	eventually(t, "the watchdog", func() bool { return rec.order() == "acquire,hold,lift,release" })

	if err := drags.End(ctx, "UDID-A", "p-1", at(0.5, 0.5)); err != nil {
		t.Fatalf("a late end must not read as a failed touch: %v", err)
	}
	if got := rec.order(); got != "acquire,hold,lift,release" {
		t.Fatalf("order = %q; a late end lifted a finger that was already up", got)
	}
}

// A begin whose touch never landed still took a hold, and keeping it would lock
// the device out for everyone including the caller.
func TestDrags_ABeginThatCouldNotTouchGivesTheHoldBack(t *testing.T) {
	rec := &recorder{holdErr: errors.New("bridge exploded")}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)

	if err := drags.Begin(context.Background(), rec, rec, "UDID-A", "p-1", at(0.5, 0.9)); err == nil {
		t.Fatal("a begin that could not touch the device must be reported")
	}
	if got := rec.order(); got != "acquire,hold,release" {
		t.Fatalf("order = %q; want the hold given back", got)
	}
}

// A move that failed leaves a finger down that only this side can lift.
func TestDrags_AFailedMoveLiftsTheFinger(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)
	ctx := context.Background()
	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.9)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	rec.holdErr = errors.New("bridge exploded")

	if err := drags.Move(ctx, rec, "UDID-A", "p-1", at(0.5, 0.5)); err == nil {
		t.Fatal("a move that failed must be reported")
	}
	if got := rec.order(); got != "acquire,hold,hold,lift,release" {
		t.Fatalf("order = %q; want the finger lifted and the hold given back", got)
	}
}

// The daemon going away must not leave a finger down on a device: nothing else
// is left that could lift it.
func TestDrags_ShutdownLiftsEveryFinger(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)
	ctx := context.Background()
	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.9)); err != nil {
		t.Fatalf("begin: %v", err)
	}

	drags.Shutdown()

	if got := rec.order(); got != "acquire,hold,lift,release" {
		t.Fatalf("order = %q; want the finger lifted and the hold given back on shutdown", got)
	}
}

// Losing the end for one drag must not refuse the next: the recovery is to lift
// what is down and start again, the same instinct as rescuing a stuck finger.
func TestDrags_ANewDragRecoversOneWhoseEndNeverArrived(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)
	ctx := context.Background()
	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.5, 0.9)); err != nil {
		t.Fatalf("begin: %v", err)
	}

	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", at(0.2, 0.2)); err != nil {
		t.Fatalf("a second begin from the same session must recover, not refuse: %v", err)
	}
	if got := rec.order(); got != "acquire,hold,lift,release,acquire,hold" {
		t.Fatalf("order = %q; want the stale touch lifted and its hold given back first", got)
	}
	if err := drags.End(ctx, "UDID-A", "p-1", at(0.2, 0.2)); err != nil {
		t.Fatalf("end: %v", err)
	}
}

// --- two fingers, held ------------------------------------------------------

// pinchAt is the two-finger grip a live pinch holds: the same one
// `simbridge.Pinch` composes in advance, so the held path and the one-shot
// command cannot disagree about where two fingers are.
func pinchAt(x, y, span float64) simbridge.Grip {
	return simbridge.PinchGrip(simbridge.Point{X: x, Y: y}, span)
}

// The capability the whole two-touch protocol buys: a pinch that follows a
// human's fingers rather than being replayed after they let go. Down once, moved
// as the gap is learned, up once - and both contacts throughout.
func TestDrags_HoldTwoFingersThroughAContinuousPinch(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(time.Second, time.Minute)
	ctx := context.Background()

	if err := drags.Begin(ctx, rec, rec, "UDID-A", "p-1", pinchAt(0.5, 0.5, 0.2)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	for _, span := range []float64{0.3, 0.45, 0.6} {
		if err := drags.Move(ctx, rec, "UDID-A", "p-1", pinchAt(0.5, 0.5, span)); err != nil {
			t.Fatalf("move: %v", err)
		}
	}
	if err := drags.End(ctx, "UDID-A", "p-1", pinchAt(0.5, 0.5, 0.6)); err != nil {
		t.Fatalf("end: %v", err)
	}

	if got := rec.order(); got != "acquire,hold,hold,hold,hold,lift,release" {
		t.Fatalf("order = %q; want one hold, the fingers followed, then one lift and one release", got)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for i, events := range rec.performed {
		if len(events) != 1 || events[0].Kind != "multitouch" {
			t.Fatalf("step %d sent %+v, want a single two-finger frame: one contact per step would "+
				"leave the other down", i, events)
		}
	}
	// The lift is the last call, and it releases BOTH contacts.
	last := rec.performed[len(rec.performed)-1][0]
	if last.Type != "end" || last.X == last.X2 {
		t.Fatalf("the drag was released as %+v, want both contacts up at the points they held", last)
	}
}

// A held touch that changes how many fingers are down is a caller bug with a
// physical consequence: the contact that vanished was never lifted. It is
// refused, and the device is left clean rather than half-held.
func TestDrags_AHeldTouchCannotChangeItsFingerCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		step func(*simgesture.Drags, *recorder) error
	}{
		{"a move with one finger", func(d *simgesture.Drags, rec *recorder) error {
			return d.Move(context.Background(), rec, "UDID-A", "p-1", at(0.5, 0.5))
		}},
		{"an end with one finger", func(d *simgesture.Drags, rec *recorder) error {
			return d.End(context.Background(), "UDID-A", "p-1", at(0.5, 0.5))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			drags := simgesture.NewDragsForTest(time.Second, time.Minute)
			if err := drags.Begin(context.Background(), rec, rec, "UDID-A", "p-1", pinchAt(0.5, 0.5, 0.2)); err != nil {
				t.Fatalf("begin: %v", err)
			}
			if err := tc.step(drags, rec); !errors.Is(err, simgesture.ErrGripChanged) {
				t.Fatalf("err = %v, want ErrGripChanged", err)
			}
			if got := rec.order(); got != "acquire,hold,lift,release" {
				t.Fatalf("order = %q; the refused step must still leave the screen released", got)
			}
			rec.mu.Lock()
			lift := rec.performed[len(rec.performed)-1][0]
			rec.mu.Unlock()
			if lift.Kind != "multitouch" || lift.X == lift.X2 {
				t.Fatalf("lift = %+v, want both contacts released where they were last seen", lift)
			}
			if performed, ok := rec.lastReleasePerformed(); !ok || performed {
				t.Fatal("a drag cut off by a step that did not describe it was not performed")
			}
		})
	}
}

// The watchdog's whole job is that a client which vanished costs seconds rather
// than a reboot. Two contacts must not be the case where it only lifts one.
func TestDrags_AQuietPinchIsLiftedAsAPair(t *testing.T) {
	rec := &recorder{}
	drags := simgesture.NewDragsForTest(20*time.Millisecond, time.Minute)

	if err := drags.Begin(context.Background(), rec, rec, "UDID-A", "p-1", pinchAt(0.5, 0.5, 0.3)); err != nil {
		t.Fatalf("begin: %v", err)
	}
	eventually(t, "the watchdog to lift both fingers", func() bool {
		return rec.order() == "acquire,hold,lift,release"
	})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	lift := rec.performed[len(rec.performed)-1][0]
	if lift.Kind != "multitouch" || lift.Type != "end" || lift.X == lift.X2 {
		t.Fatalf("watchdog lift = %+v, want both contacts up in one frame", lift)
	}
}
