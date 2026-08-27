// Package simgesture is the one sequence every touch on a simulator goes
// through, whoever asked for it.
//
// There are two askers now - an agent running `ao sim tap`, and a human
// clicking the desktop app's Simulator tab - and they reach the device by
// different routes: the CLI takes its hold over daemon HTTP, the daemon takes
// it straight from the lease service. What must not differ is what happens
// around the gesture, because that is where the device's one caller-less finger
// is protected:
//
//	take a hold sized to this gesture
//	  run the gesture
//	  if it failed and it touched the screen, release the touch
//	give the hold back, on every path out
//
// Two copies of that would be two places to forget the lift. So the sequence
// lives here and the routes differ only in how they say "take a hold" - which
// is the Holder interface and nothing else.
package simgesture

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// HoldSlack is added to a gesture's own duration when asking for the hold. The
// hold has to outlive the gesture, and only just: it is the ceiling on how long
// a caller killed mid-gesture keeps the device.
const HoldSlack = 15 * time.Second

// Outcome is what a release says about the gesture its hold covered.
type Outcome struct {
	// Performed says whether the gesture actually reached the device - not
	// merely whether it was attempted. It is what a session recording gestures
	// on this device uses to decide whether to keep the step it stashed when
	// the hold was taken: a gesture that failed, or a drag abandoned rather
	// than completed, must not leave a step behind that never really happened.
	Performed bool
	// End is where the gesture finished, for a gesture whose end was not known
	// when the hold was taken. Only a drag sets it: every other gesture in this
	// package is composed in full before the hold is asked for, so its end
	// already travelled with the intent and repeating it here would be a second
	// copy of the same fact. A drag's hold, by contrast, is taken on the finger
	// going DOWN, and where it comes back up is only knowable when it does.
	End *simbridge.Point
}

// Holder takes and gives back the device's gesture hold. The CLI implements it
// over daemon HTTP; the daemon implements it over the lease service. Release
// returns nothing on purpose: a hold that could not be handed back has already
// stopped mattering (it lapses within a minute) and must never turn a gesture
// that happened into a reported failure.
type Holder interface {
	Acquire(ctx context.Context, udid string, ttl time.Duration) (token string, err error)
	Release(ctx context.Context, udid, token string, outcome Outcome)
}

// Gesture is one composed gesture, ready to run.
type Gesture struct {
	// Action and Detail are for the caller's own reporting; this package only
	// carries them into errors.
	Action string
	Detail string
	Events []simbridge.Event
	// Last is where the finger would be if the gesture died in flight, and so
	// where a recovery lift has to land. A gesture that holds two fingers says
	// where they are in its own events instead - see simbridge.Recover.
	Last simbridge.Point
}

// FailedError is a gesture that did not complete, and what was done about it.
// Callers phrase it for their own audience; what they may not do is drop
// LiftErr, which is the difference between "the device is fine" and "the device
// may have a finger held down".
type FailedError struct {
	Action string
	Cause  error
	// Lifted: the gesture touched the screen and a recovery release was sent.
	Lifted bool
	// LiftErr: the recovery release itself failed. The device may be wedged.
	LiftErr error
}

func (e *FailedError) Error() string {
	return fmt.Sprintf("`%s` failed: %v", e.Action, e.Cause)
}

func (e *FailedError) Unwrap() error { return e.Cause }

// ScreenRead is the allowance a composed gesture's hold gets for reading the
// screen before it knows what it is going to do. The first accessibility read
// on a device can take a second or two while the translator attaches, and a
// hold that lapsed halfway through would hand the device away between the read
// and the touch it decided on.
const ScreenRead = 10 * time.Second

// Run performs one gesture under a hold. Nothing reaches the device unless the
// hold was granted, and the hold is given back on every path out - including
// one where the gesture failed and had to be recovered from.
func Run(ctx context.Context, holder Holder, driver simbridge.Driver, udid string, gesture Gesture) (simbridge.PerformResult, error) {
	// The hold is sized from the gesture itself, not from a flag: a hold that
	// lapsed mid-gesture would be exactly the window another caller needs to
	// take the finger while this one is still touching the screen.
	_, result, err := run(ctx, holder, driver, udid, simbridge.Duration(gesture.Events)+HoldSlack,
		func(context.Context) (Gesture, error) { return gesture, nil })
	return result, err
}

// RunComposed is Run for a gesture that cannot be composed until the device has
// been looked at - a tap on an element named rather than pointed at.
//
// The composing happens UNDER the hold, and that ordering is the whole reason
// this exists: reading the screen first and taking the hold second leaves a
// window in which another command moves the screen, and the coordinate this one
// then touches belongs to a screen that is no longer there. It returns the
// gesture it composed so the caller can report what it actually did.
func RunComposed(
	ctx context.Context, holder Holder, driver simbridge.Driver, udid string,
	compose func(context.Context) (Gesture, error),
) (Gesture, simbridge.PerformResult, error) {
	return run(ctx, holder, driver, udid, ScreenRead+HoldSlack, compose)
}

func run(
	ctx context.Context, holder Holder, driver simbridge.Driver, udid string,
	ttl time.Duration, compose func(context.Context) (Gesture, error),
) (Gesture, simbridge.PerformResult, error) {
	token, err := holder.Acquire(ctx, udid, ttl)
	if err != nil {
		return Gesture{}, simbridge.PerformResult{}, err
	}
	// performed starts false and is only ever raised on the one path where the
	// gesture actually reached the device: driver.Perform returning cleanly. A
	// gesture that failed - even one that touched the screen and had to be
	// recovered - never earns a recorded step, because it is not the gesture a
	// caller asked for.
	performed := false
	// No End: a composed gesture's end travelled with the intent that took the
	// hold, so there is nothing here the recorder does not already have.
	defer func() { holder.Release(ctx, udid, token, Outcome{Performed: performed}) }()

	gesture, err := compose(ctx)
	if err != nil {
		// Nothing was sent: composing is what decides whether there is anything
		// to send at all.
		return gesture, simbridge.PerformResult{}, err
	}

	result, performErr := driver.Perform(ctx, udid, gesture.Events)
	if performErr == nil {
		performed = true
		return gesture, result, nil
	}

	failed := &FailedError{Action: gesture.Action, Cause: performErr}
	// What to release is a question about the gesture, not about this sequence:
	// a keyboard gesture pressed nothing, and a pinch left TWO fingers down, so
	// simbridge composes the release from what the events actually did.
	release := simbridge.Recover(gesture.Events, gesture.Last)
	if len(release) == 0 {
		return gesture, simbridge.PerformResult{}, failed
	}
	// A release with nothing held is harmless; a finger left down wedges the
	// device until it is rebooted, so the lift is always attempted and its
	// outcome is always reported.
	failed.Lifted = true
	if _, liftErr := driver.Perform(ctx, udid, release); liftErr != nil {
		failed.LiftErr = liftErr
	}
	return gesture, simbridge.PerformResult{}, failed
}
