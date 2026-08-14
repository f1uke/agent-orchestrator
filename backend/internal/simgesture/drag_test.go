package simgesture_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
)

func at(x, y float64) simbridge.Point { return simbridge.Point{X: x, Y: y} }

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
