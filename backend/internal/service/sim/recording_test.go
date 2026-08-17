package sim_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// newServiceWithOpts is newService (see sim_test.go) with room for extra
// options such as WithRecorder - kept separate so the many hold/lease tests
// that do not care about recording stay untouched.
func newServiceWithOpts(t *testing.T, now func() time.Time, opts ...sim.Option) (*sim.Service, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", RegisteredAt: now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	allOpts := append([]sim.Option{sim.WithClock(now)}, opts...)
	return sim.New(store, allOpts...), store
}

// fakeScreenReader is a ScreenReader whose snapshot and error a test can swap
// between calls, and that counts how many times AX was actually called - the
// property TestAcquireHold_DoesNotReadTheScreenWhenNoRecordingIsOpen exists to
// pin.
type fakeScreenReader struct {
	snap  simbridge.Snapshot
	err   error
	calls int
}

func (f *fakeScreenReader) AX(_ context.Context, _ string) (simbridge.Snapshot, error) {
	f.calls++
	if f.err != nil {
		return simbridge.Snapshot{}, f.err
	}
	return f.snap, nil
}

// snapshotWithButton is a one-element screen: a labeled button whose box
// covers [0.1,0.1]-[0.5,0.3], normalized like every other coordinate this
// package takes.
func snapshotWithButton(bundleID, label string) simbridge.Snapshot {
	return simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: bundleID},
		Elements: []simbridge.Element{
			{
				Path:  "0",
				Label: label,
				Box:   &simbridge.Box{X1: 0.1, Y1: 0.1, X2: 0.5, Y2: 0.3},
				Tap:   &simbridge.Point{X: 0.3, Y: 0.2},
			},
		},
	}
}

// buttonIntent is a tap landing inside snapshotWithButton's element.
func buttonIntent() sim.GestureIntent {
	return sim.GestureIntent{Kind: "tap", X: 0.3, Y: 0.2}
}

// newRecordingService builds a service with a lease already acquired for
// owner on udidProMax, wired to reader, plus the store for further setup.
func newRecordingService(t *testing.T, now time.Time, reader *fakeScreenReader) (*sim.Service, *sqlite.Store, domain.SessionID) {
	t.Helper()
	svc, store := newServiceWithOpts(t, fixedClock(now), sim.WithRecorder(reader))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	return svc, store, owner
}

// The property that pays for this whole design: a device with no recording
// open must not be read. An AX read costs over a second, and the overwhelming
// majority of gestures happen with no recording open anywhere.
func TestAcquireHold_DoesNotReadTheScreenWhenNoRecordingIsOpen(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)

	if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent()); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if reader.calls != 0 {
		t.Fatalf("AX was called %d times with no recording open; want 0", reader.calls)
	}
}

func TestAcquireHold_ReadsTheScreenAndResolvesASelectorWhenRecording(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if reader.calls != 1 {
		t.Fatalf("AX was called %d times with a recording open; want 1", reader.calls)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].Selector != "Continue" {
		t.Fatalf("selector = %q, want %q", steps[0].Selector, "Continue")
	}
	if steps[0].SelectorRung != int64(simflow.RungText) {
		t.Fatalf("rung = %d, want RungText (%d)", steps[0].SelectorRung, simflow.RungText)
	}
	if steps[0].Kind != "tap" {
		t.Fatalf("kind = %q, want %q", steps[0].Kind, "tap")
	}
}

// The recorded selector for a by-name tap is the name the caller gave, not
// whatever a coordinate hit-test rediscovered - here there IS no coordinate
// at all (Label is the only thing the intent carries), so a step only comes
// out right if recordIntent actually resolves by name rather than falling
// through to hitTest(0, 0).
func TestRecordIntent_ByNameTapRecordsTheRequestedSelector(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, sim.GestureIntent{Kind: "tap", Label: "Continue"})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].Selector != "Continue" {
		t.Fatalf("selector = %q, want %q (the requested label, not a hit-tested coordinate)", steps[0].Selector, "Continue")
	}
	if steps[0].SelectorRung != int64(simflow.RungText) {
		t.Fatalf("rung = %d, want RungText (%d)", steps[0].SelectorRung, simflow.RungText)
	}
}

// The id form of the same path, over a screen with no label at all - proving
// elementFor's ID branch is exercised too, not just Label's.
func TestRecordIntent_ByIDTapRecordsTheRequestedSelector(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.app.a"},
		Elements: []simbridge.Element{{
			Path: "0", ID: "continue-button",
			Box: &simbridge.Box{X1: 0.1, Y1: 0.1, X2: 0.5, Y2: 0.3},
			Tap: &simbridge.Point{X: 0.3, Y: 0.2},
		}},
	}}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, sim.GestureIntent{Kind: "tap", ID: "continue-button"})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].Selector != "continue-button" {
		t.Fatalf("selector = %q, want %q", steps[0].Selector, "continue-button")
	}
	if steps[0].SelectorRung != int64(simflow.RungID) {
		t.Fatalf("rung = %d, want RungID (%d)", steps[0].SelectorRung, simflow.RungID)
	}
}

// An ambiguous by-name tap is under-determined, not unaddressable. The
// recorded step must say so: the name searched for, the count of candidates,
// and NO invented index - guessing one would be exactly the wrong-element bug
// 0039_sim_recording_step_index.sql exists to fix. Before this, the step came
// back as RungNone (Ambiguity 0), which Emit renders as "this element cannot
// be addressed" - false; there were two reachable candidates, they just could
// not be told apart.
func TestRecordIntent_AmbiguousByNameTapRecordsTheNameAndTheCount(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.app.a"},
		Elements: []simbridge.Element{
			{
				Path: "0", Label: "Continue", Enabled: true,
				Box: &simbridge.Box{X1: 0.1, Y1: 0.1, X2: 0.5, Y2: 0.3},
				Tap: &simbridge.Point{X: 0.3, Y: 0.2},
			},
			{
				Path: "1", Label: "Continue", Enabled: true,
				Box: &simbridge.Box{X1: 0.1, Y1: 0.5, X2: 0.5, Y2: 0.7},
				Tap: &simbridge.Point{X: 0.3, Y: 0.6},
			},
		},
	}}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, sim.GestureIntent{Kind: "tap", Label: "Continue"})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	step := steps[0]
	if step.Selector != "Continue" {
		t.Fatalf("selector = %q, want %q - the name searched for must not be dropped", step.Selector, "Continue")
	}
	if step.SelectorRung != int64(simflow.RungText) {
		t.Fatalf("rung = %d, want RungText (%d) - under-determined, not RungNone/unaddressable", step.SelectorRung, simflow.RungText)
	}
	if step.Ambiguity != 2 {
		t.Fatalf("ambiguity = %d, want 2", step.Ambiguity)
	}
	if step.SelectorIndex != 0 {
		t.Fatalf("selectorIndex = %d, want 0 - no index may ever be invented here", step.SelectorIndex)
	}
}

// A gesture that was attempted and failed is not a step. Recording at acquire
// alone would write it down as if it had happened.
func TestReleaseHold_PerformedFalseAppendsNothing(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, false); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps = %d, want 0 for a gesture that did not happen", len(steps))
	}
}

func TestReleaseHold_PerformedTrueAppendsTheStashedStep(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	// Nothing is written until the step is earned: a caller reading the
	// recording between acquire and release must not see a step that has not
	// happened yet.
	_, mid, _, err := svc.GetRecording(context.Background(), udidProMax)
	if err != nil {
		t.Fatalf("get recording: %v", err)
	}
	if len(mid) != 0 {
		t.Fatalf("steps before release = %d, want 0", len(mid))
	}

	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, ok, err := svc.GetRecording(context.Background(), udidProMax)
	if err != nil {
		t.Fatalf("get recording: %v", err)
	}
	if !ok {
		t.Fatal("recording should exist")
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
}

// A drag holds one token across many begin/move/end requests - none of which
// touch AcquireHold again - and must produce exactly ONE recorded step. If the
// stash were keyed by udid instead of by token, any writer touching that same
// device key between acquire and release could clobber the gesture's own
// stashed step; keying by token means the step earned by THIS hold is the only
// one ReleaseHold(token) can ever earn.
func TestAcquireHold_OneTokenProducesOneStepAcrossManyRequests(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	// Stand-ins for the many begin/move/move/end HID events a drag sends while
	// the SAME hold is down: none of them call AcquireHold or ReleaseHold, so
	// the recorder never sees them at all.
	for i := 0; i < 5; i++ {
		_, _, _, err := svc.GetRecording(context.Background(), udidProMax)
		if err != nil {
			t.Fatalf("get recording mid-drag %d: %v", i, err)
		}
	}

	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want exactly 1 for one drag under one token", len(steps))
	}
}

// The screen may be unreadable exactly when a gesture is most interesting. A
// recorder that fails must not take the device down with it.
func TestAcquireHold_ScreenReadFailureStillGrantsTheHold(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{err: errors.New("the accessibility service did not answer")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("a screen-read failure must not fail the hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps = %d, want 0: an unreadable screen must lose the step, not the hold", len(steps))
	}
}

func TestStartRecording_RefusedWithoutALease(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newServiceWithOpts(t, fixedClock(now), sim.WithRecorder(&fakeScreenReader{}))
	owner := newSession(t, store, now)

	_, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow")
	var refused *sim.RecordingRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want a *RecordingRefusedError", err)
	}
	if refused.Reason != sim.RecordingRefusedNotLeased {
		t.Fatalf("reason = %q, want %q", refused.Reason, sim.RecordingRefusedNotLeased)
	}
}

func TestStopRecording_ReturnsTheStepsInOrder(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	for i := 0; i < 3; i++ {
		hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
		if err != nil {
			t.Fatalf("hold %d: %v", i, err)
		}
		if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(steps))
	}
	for i, step := range steps {
		if step.Seq != int64(i+1) {
			t.Fatalf("steps[%d].Seq = %d, want %d", i, step.Seq, i+1)
		}
	}
}

// screen_change is computed here, not by Emit: Emit never sees a tree.
func TestAcquireHold_MarksScreenChangeWhenTheForegroundAppChanged(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	// First step: nothing to compare against, so no screen change.
	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold 1: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release 1: %v", err)
	}

	// The tap navigated to a new screen: a different app is now frontmost.
	reader.snap = snapshotWithButton("com.app.b", "Done")
	hold, err = svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold 2: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, true); err != nil {
		t.Fatalf("release 2: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	if steps[0].ScreenChange {
		t.Fatal("the first step has nothing to compare against and must not be marked as a screen change")
	}
	if !steps[1].ScreenChange {
		t.Fatal("the second step's foreground app differs from the first's; it must be marked as a screen change")
	}
}
