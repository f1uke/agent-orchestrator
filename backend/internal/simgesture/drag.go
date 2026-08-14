package simgesture

import (
	"context"
	"errors"
	"fmt"
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
// The whole risk of that is a finger left down: the simulator's HID layer has
// one finger and no caller identity, so a drag that never ends wedges input
// until the device is rebooted. Three things stop it:
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
	at        simbridge.Point
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
	udid, sessionID string, at simbridge.Point,
) error {
	d.mu.Lock()
	existing := d.open[udid]
	if existing != nil && existing.sessionID != sessionID {
		d.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDragHeldByOther, udid)
	}
	d.mu.Unlock()

	// A drag of our own still open means the previous one's end never arrived.
	// Recovering it - rather than refusing until the watchdog fires - is the
	// same instinct as lifting a finger a failed gesture left down.
	if existing != nil {
		_ = d.finish(ctx, existing, existing.at)
	}

	token, err := holder.Acquire(ctx, udid, DragHoldTTL)
	if err != nil {
		return err
	}
	if err := driver.Hold(ctx, udid, []simbridge.Event{
		{Kind: "touch", Type: "begin", X: at.X, Y: at.Y},
	}); err != nil {
		// The touch never landed, so there is nothing to lift - but the hold was
		// granted and must not be kept.
		holder.Release(ctx, udid, token)
		return &FailedError{Action: "drag", Cause: err}
	}

	held := &drag{
		token: token, sessionID: sessionID, holder: holder, driver: driver,
		udid: udid, at: at, started: time.Now(),
	}
	d.mu.Lock()
	d.open[udid] = held
	d.mu.Unlock()
	d.arm(held)
	return nil
}

// Move follows the finger. It is the only call in this package that touches a
// device without taking a hold, because the hold it runs under was taken by
// Begin and has not been given back.
func (d *Drags) Move(ctx context.Context, driver simbridge.Driver, udid, sessionID string, at simbridge.Point) error {
	d.mu.Lock()
	held := d.open[udid]
	switch {
	case held == nil:
		d.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNoDrag, udid)
	case held.sessionID != sessionID:
		d.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrDragHeldByOther, udid)
	}
	overrun := time.Since(held.started) > d.ceiling
	d.mu.Unlock()

	if overrun {
		_ = d.finish(ctx, held, at)
		return fmt.Errorf("%w: %s", ErrNoDrag, udid)
	}

	if err := driver.Hold(ctx, udid, []simbridge.Event{
		{Kind: "touch", Type: "move", X: at.X, Y: at.Y},
	}); err != nil {
		// A move that failed leaves a finger down that only this side can lift.
		_ = d.finish(ctx, held, at)
		return &FailedError{Action: "drag", Cause: err, Lifted: true}
	}

	d.mu.Lock()
	held.at = at
	d.mu.Unlock()
	d.arm(held)
	return nil
}

// End lifts the finger and gives the hold back.
//
// An end with no drag open is not an error: the watchdog may have lifted it
// already, and that race is ordinary rather than a bug. Reporting it would turn
// a touch that completed into a failure the human has to read.
func (d *Drags) End(ctx context.Context, udid, sessionID string, at simbridge.Point) error {
	d.mu.Lock()
	held := d.open[udid]
	if held == nil || held.sessionID != sessionID {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	return d.finish(ctx, held, at)
}

// Shutdown lifts every finger. The daemon calls it on the way out: a touch left
// down outlives the process that could have lifted it.
func (d *Drags) Shutdown() {
	d.mu.Lock()
	open := make([]*drag, 0, len(d.open))
	for _, held := range d.open {
		open = append(open, held)
	}
	d.mu.Unlock()
	for _, held := range open {
		_ = d.finish(context.Background(), held, held.at)
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
		_ = d.finish(context.Background(), held, held.at)
	})
}

// finish lifts the finger and gives the hold back, once, whoever gets here
// first.
func (d *Drags) finish(ctx context.Context, held *drag, at simbridge.Point) error {
	d.mu.Lock()
	if held.done {
		d.mu.Unlock()
		return nil
	}
	held.done = true
	if held.watchdog != nil {
		held.watchdog.Stop()
	}
	if d.open[held.udid] == held {
		delete(d.open, held.udid)
	}
	d.mu.Unlock()

	// The lift goes through Perform, not Hold: Perform's own rule is to leave no
	// finger down, so a lift that half-worked is still followed by one.
	_, err := held.driver.Perform(ctx, held.udid, []simbridge.Event{
		{Kind: "touch", Type: "end", X: at.X, Y: at.Y},
	})
	// The hold is given back whether or not the lift worked. A hold kept because
	// the lift failed would leave the device unusable by anyone, on top of a
	// finger that is already down.
	held.holder.Release(ctx, held.udid, held.token)
	if err != nil {
		return &FailedError{Action: "drag", Cause: err, LiftErr: err}
	}
	return nil
}
