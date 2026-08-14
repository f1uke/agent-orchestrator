package simgesture_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
)

type step struct {
	what string
	arg  string
}

// recorder captures the exact order of hold and device operations, which is the
// only thing this package exists to get right.
type recorder struct {
	mu         sync.Mutex
	holdErr    error
	steps      []step
	acquireErr error
	performErr error
	liftErr    error
	ttl        time.Duration
	performed  [][]simbridge.Event
}

func (r *recorder) Acquire(_ context.Context, udid string, ttl time.Duration) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, step{"acquire", udid})
	r.ttl = ttl
	if r.acquireErr != nil {
		return "", r.acquireErr
	}
	return "token-1", nil
}

func (r *recorder) Release(_ context.Context, udid, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, step{"release", udid + "/" + token})
}

func (r *recorder) AX(context.Context, string) (simbridge.Snapshot, error) {
	return simbridge.Snapshot{}, errors.New("not used")
}

func (r *recorder) Perform(_ context.Context, _ string, events []simbridge.Event) (simbridge.PerformResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.performed = append(r.performed, events)
	if len(events) == 1 && events[0].Kind == "touch" && events[0].Type == "end" {
		r.steps = append(r.steps, step{"lift", ""})
		if r.liftErr != nil {
			return simbridge.PerformResult{}, r.liftErr
		}
		return simbridge.PerformResult{}, nil
	}
	r.steps = append(r.steps, step{"perform", ""})
	if r.performErr != nil {
		return simbridge.PerformResult{}, r.performErr
	}
	return simbridge.PerformResult{}, nil
}

func (r *recorder) Hold(_ context.Context, _ string, events []simbridge.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.performed = append(r.performed, events)
	r.steps = append(r.steps, step{"hold", events[0].Type})
	return r.holdErr
}

func (r *recorder) order() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []string{}
	for _, s := range r.steps {
		out = append(out, s.what)
	}
	return strings.Join(out, ",")
}

func tapGesture(t *testing.T) simgesture.Gesture {
	t.Helper()
	at := simbridge.Point{X: 0.5, Y: 0.8}
	events, err := simbridge.Tap(at)
	if err != nil {
		t.Fatalf("Tap: %v", err)
	}
	return simgesture.Gesture{Action: "tap", Detail: "(0.500, 0.800)", Events: events, Last: at}
}

func TestRun_HoldsForTheWholeGestureAndGivesItBack(t *testing.T) {
	rec := &recorder{}
	if _, err := simgesture.Run(context.Background(), rec, rec, "UDID-A", tapGesture(t)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rec.order(); got != "acquire,perform,release" {
		t.Fatalf("order was %q; the hold must bracket the gesture", got)
	}
}

// The hold has to outlive the gesture. A hold that lapsed mid-gesture is
// exactly the window another command needs to take the finger while this one is
// still touching the screen.
func TestRun_HoldOutlastsTheGestureItCovers(t *testing.T) {
	rec := &recorder{}
	gesture := tapGesture(t)
	if _, err := simgesture.Run(context.Background(), rec, rec, "UDID-A", gesture); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.ttl <= simbridge.Duration(gesture.Events) {
		t.Fatalf("hold ttl %s does not cover a gesture of %s", rec.ttl, simbridge.Duration(gesture.Events))
	}
}

// A refused hold must leave the device untouched. This is the case that makes a
// click in the desktop app as safe as a command: no hold, no events.
func TestRun_RefusedHoldSendsNothingToTheDevice(t *testing.T) {
	rec := &recorder{acquireErr: errors.New("busy")}
	_, err := simgesture.Run(context.Background(), rec, rec, "UDID-A", tapGesture(t))
	if err == nil {
		t.Fatal("a refused hold must fail the gesture")
	}
	if len(rec.performed) != 0 {
		t.Fatalf("nothing may reach the device without a hold, got %d calls", len(rec.performed))
	}
	if got := rec.order(); got != "acquire" {
		t.Fatalf("order was %q; a hold that was never taken must not be released", got)
	}
}

// A gesture that dies in flight must not leave a finger down: a stuck touch
// wedges input until the simulator is rebooted.
func TestRun_FailedGestureIsFollowedByALiftAndTheHoldComesBack(t *testing.T) {
	rec := &recorder{performErr: errors.New("bridge exploded")}
	_, err := simgesture.Run(context.Background(), rec, rec, "UDID-A", tapGesture(t))
	var failed *simgesture.FailedError
	if !errors.As(err, &failed) {
		t.Fatalf("want *FailedError, got %v", err)
	}
	if !failed.Lifted || failed.LiftErr != nil {
		t.Fatalf("a recovered gesture must report the lift: %+v", failed)
	}
	if got := rec.order(); got != "acquire,perform,lift,release" {
		t.Fatalf("order was %q", got)
	}
}

// The worst case has to be legible: the device may be left with a finger down.
func TestRun_LiftThatAlsoFailsIsReportedAsSuch(t *testing.T) {
	rec := &recorder{performErr: errors.New("bridge exploded"), liftErr: errors.New("still gone")}
	_, err := simgesture.Run(context.Background(), rec, rec, "UDID-A", tapGesture(t))
	var failed *simgesture.FailedError
	if !errors.As(err, &failed) {
		t.Fatalf("want *FailedError, got %v", err)
	}
	if failed.LiftErr == nil {
		t.Fatal("a failed recovery lift must be reported, not swallowed")
	}
	if got := rec.order(); got != "acquire,perform,lift,release" {
		t.Fatalf("order was %q; the hold must come back even when recovery failed", got)
	}
}

// A gesture that never touched the screen has no finger to lift, and attempting
// one would send a stray touch to a device nobody touched.
func TestRun_NonTouchGestureIsNotFollowedByALift(t *testing.T) {
	events, err := simbridge.Type("hi")
	if err != nil {
		t.Fatalf("Type: %v", err)
	}
	rec := &recorder{performErr: errors.New("bridge exploded")}
	_, runErr := simgesture.Run(context.Background(), rec, rec, "UDID-A",
		simgesture.Gesture{Action: "type", Detail: `"hi"`, Events: events})
	if runErr == nil {
		t.Fatal("want the failure surfaced")
	}
	if got := rec.order(); got != "acquire,perform,release" {
		t.Fatalf("order was %q; a keyboard gesture has no finger to lift", got)
	}
}

func TestRun_RescueIsReportedOnSuccessToo(t *testing.T) {
	rec := &lifter{}
	result, err := simgesture.Run(context.Background(), rec, rec, "UDID-A", tapGesture(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Lifted {
		t.Fatal("a gesture the bridge had to rescue must say so rather than reading as clean")
	}
}

type lifter struct{ recorder }

func (l *lifter) Perform(context.Context, string, []simbridge.Event) (simbridge.PerformResult, error) {
	return simbridge.PerformResult{Lifted: true, LiftReason: "gesture ended without a lift"}, nil
}
