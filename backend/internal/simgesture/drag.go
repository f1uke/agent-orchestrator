package simgesture

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// A drag that follows a human's finger, rather than one that is replayed after
// it has been let go.
//
// Every other gesture is known in full before it starts, so it takes the hold,
// runs, and gives the hold back inside one call. A drag is not: where the
// finger goes next is not knowable until it goes there. Sending it as one swipe
// on release - which is what this used to do - means the screen starts moving
// after the human has stopped, which is exactly the lag they reported against
// serve-sim, where the content tracks the finger.
//
// So the touch spans several calls, and the thing that spans them is the hold.
// One hold is taken when the drag starts and given back when it ends, which
// makes the arbitration *stronger* than a per-gesture hold, not weaker: for as
// long as a finger is down on a device, no other session can take it.
//
// What is held is a simbridge.Grip - one finger, or the two of a pinch - rather
// than a point, because the held path is the same path either way: down, a move
// whenever the caller learns where the fingers went, up. Only the HID frame each
// step becomes differs, and the grip composes that itself. Two registries, one
// per finger count, would be two watchdogs and two places to forget the lift.
//
// The whole risk of that is a contact left down: the simulator's HID layer has
// no caller identity, so a drag that never ends wedges input until the device is
// rebooted. Three things stop it:
//
//   - a watchdog. A drag with no movement for DragIdleTimeout is lifted and its
//     hold given back, so a browser tab that closed mid-drag costs two seconds.
//   - a ceiling. However much it moves, a drag is lifted after DragMaxDuration.
//   - the bridge's own process-level lifts, which still fire on a signal, on the
//     daemon going away, and on the reply channel breaking.
//
// The hold's own TTL is the backstop under all three: it lapses on its own.

const (
	// DragIdleTimeout is how long a drag may go without a move before it is
	// lifted. Short enough that a client that vanished mid-drag does not hold
	// the device, long enough to survive a stall between two pointer moves.
	DragIdleTimeout = 2 * time.Second
	// DragMaxDuration is the ceiling on one drag however much it moves. A human
	// dragging for twenty seconds is not a case worth keeping a device for.
	DragMaxDuration = 20 * time.Second
	// DragHoldTTL sizes the hold. It has to outlive DragMaxDuration so the hold
	// never lapses under a drag that is still going.
	DragHoldTTL = 30 * time.Second
)

// ErrNoDrag is a move for a touch that is not down. Nothing is sent: a move
// with no begin before it would be a stray event on a device somebody else may
// be using.
var ErrNoDrag = errors.New("no drag is in progress on this device")

// ErrDragHeldByOther is a drag another session started. Watching is always
// allowed; taking the finger out from under someone is not.
var ErrDragHeldByOther = errors.New("another session is mid-drag on this device")

// ErrGripChanged is a step that puts a different number of fingers on the screen
// than the one that is already down - a two-finger drag continued with one
// point, or the other way round.
//
// It is refused rather than adapted to, because there is no honest adaptation:
// the contact that disappeared was never lifted, and the one that appeared never
// landed. The drag it interrupted is cut off and released, so the device is left
// clean rather than half-held.
var ErrGripChanged = errors.New("a held touch cannot change how many fingers are on the screen")

// key normalizes a udid so a drag can be found without asking the machine what
// devices it has. A move belongs to a touch that is already down, and that
// touch was opened against a device this daemon resolved moments ago - asking
// again per move is what put a `xcrun simctl list` in the middle of a drag.
func key(udid string) string { return strings.ToUpper(udid) }

// Drags is every touch currently held down, one per device.
type Drags struct {
	idle    time.Duration
	ceiling time.Duration

	mu   sync.Mutex
	open map[string]*drag
}

type drag struct {
	token     string
	sessionID string
	holder    Holder
	driver    simbridge.Driver
	udid      string
	grip      simbridge.Grip
	started   time.Time
	watchdog  *time.Timer
	// done is set by whichever of the end, the watchdog and the shutdown gets
	// there first, so the finger is lifted once and the hold released once.
	done bool
}

// NewDrags builds the registry the daemon keeps for the life of the process.
func NewDrags() *Drags {
	return &Drags{idle: DragIdleTimeout, ceiling: DragMaxDuration, open: map[string]*drag{}}
}

// NewDragsForTest builds one with timeouts a test can actually wait for.
func NewDragsForTest(idle, ceiling time.Duration) *Drags {
	return &Drags{idle: idle, ceiling: ceiling, open: map[string]*drag{}}
}

// Begin puts the finger down and keeps it there.
func (d *Drags) Begin(
	ctx context.Context, holder Holder, driver simbridge.Driver,
	udid, sessionID string, grip simbridge.Grip,
) error {
	d.mu.Lock()
	existing := d.open[key(udid)]
	if existing != nil && existing.sessionID != sessionID {
		d.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDragHeldByOther, udid)
	}
	d.mu.Unlock()

	// A drag of our own still open means the previous one's end never arrived.
	// Recovering it - rather than refusing until the watchdog fires - is the
	// same instinct as lifting a finger a failed gesture left down. It was
	// abandoned, not completed, so it is not performed.
	if existing != nil {
		_ = d.finish(ctx, existing, existing.grip, false)
	}

	token, err := holder.Acquire(ctx, udid, DragHoldTTL)
	if err != nil {
		return err
	}
	if err := driver.Hold(ctx, udid, []simbridge.Event{grip.Event("begin")}); err != nil {
		// The touch never landed, so there is nothing to lift - but the hold was
		// granted and must not be kept. Nothing reached the device, so this was
		// not performed, and a drag that never started has no end to report.
		holder.Release(ctx, udid, token, Outcome{})
		return &FailedError{Action: "drag", Cause: err}
	}

	held := &drag{
		token: token, sessionID: sessionID, holder: holder, driver: driver,
		udid: udid, grip: grip, started: time.Now(),
	}
	d.mu.Lock()
	d.open[key(udid)] = held
	d.mu.Unlock()
	d.arm(held)
	return nil
}

// Move follows the finger. It is the only call in this package that touches a
// device without taking a hold, because the hold it runs under was taken by
// Begin and has not been given back.
func (d *Drags) Move(ctx context.Context, driver simbridge.Driver, udid, sessionID string, grip simbridge.Grip) error {
	d.mu.Lock()
	held := d.open[key(udid)]
	switch {
	case held == nil:
		d.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNoDrag, udid)
	case held.sessionID != sessionID:
		d.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDragHeldByOther, udid)
	}
	overrun := time.Since(held.started) > d.ceiling
	changed := held.grip.Pair() != grip.Pair()
	lastGrip := held.grip
	d.mu.Unlock()

	if changed {
		// Lifted at the grip that is actually down, not the one the caller
		// described: the point of the refusal is that they disagree.
		_ = d.finish(ctx, held, lastGrip, false)
		return fmt.Errorf("%w: %s", ErrGripChanged, udid)
	}
	if overrun {
		// Cut off at the ceiling rather than let go by the caller: abandoned, not
		// completed.
		_ = d.finish(ctx, held, grip, false)
		return fmt.Errorf("%w: %s", ErrNoDrag, udid)
	}

	if err := driver.Hold(ctx, udid, []simbridge.Event{grip.Event("move")}); err != nil {
		// A move that failed leaves a contact down that only this side can lift.
		// The drag did not complete, so it is not performed.
		_ = d.finish(ctx, held, grip, false)
		return &FailedError{Action: "drag", Cause: err, Lifted: true}
	}

	d.mu.Lock()
	held.grip = grip
	d.mu.Unlock()
	d.arm(held)
	return nil
}

// End lifts the finger and gives the hold back.
//
// An end with no drag open is not an error: the watchdog may have lifted it
// already, and that race is ordinary rather than a bug. Reporting it would turn
// a touch that completed into a failure the human has to read.
func (d *Drags) End(ctx context.Context, udid, sessionID string, grip simbridge.Grip) error {
	d.mu.Lock()
	held := d.open[key(udid)]
	if held == nil || held.sessionID != sessionID {
		d.mu.Unlock()
		return nil
	}
	changed := held.grip.Pair() != grip.Pair()
	lastGrip := held.grip
	d.mu.Unlock()

	if changed {
		// An end that describes a different number of fingers cannot say where
		// the ones that ARE down came up, so they are lifted where they were
		// last seen. Nothing is left holding the screen, and the drag is not
		// performed: what the caller described is not what happened.
		_ = d.finish(ctx, held, lastGrip, false)
		return fmt.Errorf("%w: %s", ErrGripChanged, udid)
	}
	// This is the caller's own deliberate end - the only path a drag counts as
	// performed. It reached the device, which is all "performed" answers: the
	// app under test saw it and moved. Whether the final lift itself lands is a
	// separate fact about device hygiene, not about what happened, and it has
	// its own loud reporting path (a failed recovery lift warns the finger may
	// still be down) - folding it into "performed" would silently drop a step
	// the human actually did.
	return d.finish(ctx, held, grip, true)
}

// Shutdown lifts every finger. The daemon calls it on the way out: a touch left
// down outlives the process that could have lifted it, and a drag cut off by
// the process exiting was abandoned, not completed.
func (d *Drags) Shutdown() {
	d.mu.Lock()
	open := make([]*drag, 0, len(d.open))
	for _, held := range d.open {
		open = append(open, held)
	}
	d.mu.Unlock()
	for _, held := range open {
		_ = d.finish(context.Background(), held, held.grip, false)
	}
}

// arm resets the watchdog. Called on every move, so the timeout measures
// silence rather than the drag's own length.
func (d *Drags) arm(held *drag) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if held.done {
		return
	}
	if held.watchdog != nil {
		held.watchdog.Stop()
	}
	held.watchdog = time.AfterFunc(d.idle, func() {
		// Silence, not a caller ending it - abandoned, not completed.
		d.mu.Lock()
		grip := held.grip
		d.mu.Unlock()
		_ = d.finish(context.Background(), held, grip, false)
	})
}

// finish lifts the finger and gives the hold back, once, whoever gets here
// first.
//
// completed is whether this is the caller's own deliberate End - as opposed to
// a stale drag being recovered, the ceiling, a failed move, the watchdog or
// shutdown - all of which cut a drag off rather than let it finish, and are
// genuinely not performed: nothing the caller asked for actually happened. A
// completed drag, by contrast, is told to Release as performed regardless of
// whether the final lift below succeeds - performed answers "did this reach
// the device", and a completed drag already did, moving whatever app was
// under it. Whether the finger also came back up afterwards is a separate,
// loudly-reported fact (see the FailedError below), not this one.
func (d *Drags) finish(ctx context.Context, held *drag, grip simbridge.Grip, completed bool) error {
	d.mu.Lock()
	if held.done {
		d.mu.Unlock()
		return nil
	}
	held.done = true
	if held.watchdog != nil {
		held.watchdog.Stop()
	}
	if d.open[key(held.udid)] == held {
		delete(d.open, key(held.udid))
	}
	d.mu.Unlock()

	// The lift goes through Perform, not Hold: Perform's own rule is to leave no
	// contact down, so a lift that half-worked is still followed by one. Release
	// composes it from the grip, so a pinch comes up as a pair rather than
	// leaving its second contact on the screen.
	_, err := held.driver.Perform(ctx, held.udid, simbridge.Release(grip))
	// The hold is given back whether or not the lift worked. A hold kept because
	// the lift failed would leave the device unusable by anyone, on top of a
	// finger that is already down.
	//
	// The end point is where the grip actually came up - the finger, or the
	// midpoint between two - and it is carried back with the release because this is the first moment anybody knows it: the hold was
	// taken on the finger going down, so a recording holds a step with a start
	// and no end until here. For a completed drag it is the end the caller
	// asked for; for an abandoned one the release is not performed and the
	// stashed step is dropped rather than written, so the point is never used
	// to describe a gesture nobody finished.
	end := grip.At()
	held.holder.Release(ctx, held.udid, held.token, Outcome{Performed: completed, End: &end})
	if err != nil {
		return &FailedError{Action: "drag", Cause: err, LiftErr: err}
	}
	return nil
}
