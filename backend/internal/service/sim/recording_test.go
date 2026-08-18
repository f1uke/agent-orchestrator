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
	snap simbridge.Snapshot
	// script, when set, is returned one entry per call, with the last entry
	// repeating - which is how a screen that arrives late is expressed. It
	// lives here rather than in a second reader type so that "how many times
	// was AX called" stays one counter with one meaning.
	script []simbridge.Snapshot
	err    error
	calls  int
}

func (f *fakeScreenReader) AX(_ context.Context, _ string) (simbridge.Snapshot, error) {
	f.calls++
	if f.err != nil {
		return simbridge.Snapshot{}, f.err
	}
	if len(f.script) > 0 {
		if f.calls <= len(f.script) {
			return f.script[f.calls-1], nil
		}
		return f.script[len(f.script)-1], nil
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
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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

// The escaping half of that rule, which the test above cannot see because
// "Continue" has no metacharacters in it: an ambiguous by-name tap must go
// through the SAME escaping a unique match gets. Maestro matches text as a
// regex, so a stored "Continue." would emit `- tapOn: "Continue."` and also
// match "Continue!" - over-matching on the one path that already could not
// tell its candidates apart.
func TestRecordIntent_AmbiguousByNameTapEscapesTheNameItRecords(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.app.a"},
		Elements: []simbridge.Element{
			{
				Path: "0", Label: "Continue.", Enabled: true,
				Box: &simbridge.Box{X1: 0.1, Y1: 0.1, X2: 0.5, Y2: 0.3},
				Tap: &simbridge.Point{X: 0.3, Y: 0.2},
			},
			{
				Path: "1", Label: "Continue.", Enabled: true,
				Box: &simbridge.Box{X1: 0.1, Y1: 0.5, X2: 0.5, Y2: 0.7},
				Tap: &simbridge.Point{X: 0.3, Y: 0.6},
			},
		},
	}}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, sim.GestureIntent{Kind: "tap", Label: "Continue."})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].Selector != `Continue\.` {
		t.Fatalf("selector = %q, want the escaped name %q", steps[0].Selector, `Continue\.`)
	}
	if steps[0].Ambiguity != 2 || steps[0].SelectorRung != int64(simflow.RungText) {
		t.Fatalf("step = %+v, want RungText with ambiguity 2", steps[0])
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
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{}); err != nil {
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

// A drag takes its hold on `drag-begin`, whose intent carries a start and no
// end at all. Without the release carrying the real end point, the step is
// appended with ToX/ToY of zero and the emitted flow reads
// `end: "0%,0%"` - a coordinate nobody ever touched, in the one capability
// this whole feature exists to serve.
func TestReleaseHold_ADragsEndPointComesFromTheRelease(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0,
		sim.GestureIntent{Kind: "drag-begin", X: 0.3, Y: 0.2})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	end := simbridge.Point{X: 0.42, Y: 0.88}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token,
		sim.GestureOutcome{Performed: true, End: &end}); err != nil {
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
	if step.X != 0.3 || step.Y != 0.2 {
		t.Fatalf("start = (%v,%v), want the point the finger went down at (0.3,0.2)", step.X, step.Y)
	}
	if step.ToX != end.X || step.ToY != end.Y {
		t.Fatalf("end = (%v,%v), want the point the release reported (%v,%v) - never a fabricated 0,0",
			step.ToX, step.ToY, end.X, end.Y)
	}
}

// The other half of that rule: a drag that was abandoned rather than ended
// deliberately is released as not performed, so no step is written at all.
// There is therefore no case where a recorded step exists without a true end.
func TestReleaseHold_AnAbandonedDragWritesNoStepAtAll(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0,
		sim.GestureIntent{Kind: "drag-begin", X: 0.3, Y: 0.2})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	// Where the watchdog lifted it is known, but the human never ended the
	// drag there, so it is not performed.
	lifted := simbridge.Point{X: 0.31, Y: 0.21}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token,
		sim.GestureOutcome{End: &lifted}); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps = %d, want 0 for a drag nobody finished", len(steps))
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

	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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

	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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
		if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release 1: %v", err)
	}

	// The tap navigated to a new screen: a different app is now frontmost.
	reader.snap = snapshotWithButton("com.app.b", "Done")
	hold, err = svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold 2: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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

// The case the real-app measurement was itself fooled by: a screen whose
// content arrives late. The first read has nothing under the finger, so the
// recorder must read again and describe the step from the screen that turned
// up - not record a step it could not resolve.
func TestRecordIntent_SettlesWhenNothingResolvedUnderTheGesture(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	arrived := snapshotWithButton("com.app.a", "Continue")
	// An empty tree first: the app is up, the screen is not.
	loading := simbridge.Snapshot{Frontmost: simbridge.Frontmost{BundleID: "com.app.a"}}
	reader := &fakeScreenReader{script: []simbridge.Snapshot{loading, arrived, arrived}}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
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
		t.Fatalf("selector = %q, want the element from the settled screen", steps[0].Selector)
	}
	if reader.calls < 2 {
		t.Errorf("AX calls = %d; an unresolved gesture must trigger another read", reader.calls)
	}
}

// The other half of the same decision, and the reason it is conditional: this
// read sits between a hold being granted and the gesture being performed, so
// a screen that is already up must cost exactly one read. If this ever fails,
// every tap a human makes while recording just got a full AX read slower.
func TestRecordIntent_SettledScreenCostsNoExtraRead(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent()); err != nil {
		t.Fatalf("hold: %v", err)
	}

	if reader.calls != 1 {
		t.Errorf("AX calls = %d, want 1 - settling must not run on a screen that resolved", reader.calls)
	}
}

// A screen that never settles must not hold the gesture open indefinitely: the
// step is recorded from the last read, exactly as it would have been if
// settling did not exist.
func TestRecordIntent_UnsettleableScreenStillRecordsAStep(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	// Every read differs and none has anything under the finger.
	reader := &fakeScreenReader{script: []simbridge.Snapshot{
		{Frontmost: simbridge.Frontmost{BundleID: "com.app.a"}},
		snapshotWithButton("com.app.a", "One"),
		snapshotWithButton("com.app.a", "Two"),
	}}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("a screen that will not settle must not fail the hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want the step recorded anyway", len(steps))
	}
	// One first read plus the settle's own budget, and not one more: this
	// number is what stops an animating screen from holding a gesture open.
	if reader.calls > sim.RecorderSettleMaxReads {
		t.Errorf("AX calls = %d; settling must be bounded at %d", reader.calls, sim.RecorderSettleMaxReads)
	}
}

// An anchored selector is only worth resolving if it survives to the flow.
// Rung, anchor and relation all have to come back out of the store, or
// re-emitting silently falls back to the index the anchor exists to replace.
func TestRecordIntent_AnchoredSelectorRoundTripsThroughTheStore(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	// Two identically labelled buttons with a unique heading between them.
	snap := simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.app.a"},
		Screen:    simbridge.Size{Width: 400, Height: 800},
		Elements: []simbridge.Element{
			recordedRow("0.0", "Buy", 0, 100),
			recordedRow("0.1", "Second Section", 0, 300),
			recordedRow("0.2", "Buy", 0, 500),
		},
	}
	reader := &fakeScreenReader{snap: snap}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	// Tap the second "Buy": centre of the row at y=500 on an 800-point screen.
	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0,
		sim.GestureIntent{Kind: "tap", X: 0.125, Y: (500 + 10) / 800.0})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop recording: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].SelectorRung != int64(simflow.RungTextAnchor) {
		t.Fatalf("rung = %d, want RungTextAnchor (%d)", steps[0].SelectorRung, simflow.RungTextAnchor)
	}
	if steps[0].SelectorAnchor != "Second Section" {
		t.Errorf("anchor = %q, want the unique nearby label", steps[0].SelectorAnchor)
	}
	if steps[0].SelectorAnchorRel != string(simflow.RelBelow) {
		t.Errorf("relation = %q, want below", steps[0].SelectorAnchorRel)
	}
}

// recordedRow is an element with a real frame and a box, which is what
// hitTest and the anchor rung both need.
func recordedRow(path, label string, x, y float64) simbridge.Element {
	const w, h, sw, sh = 100.0, 20.0, 400.0, 800.0
	return simbridge.Element{
		Path:  path,
		Label: label,
		Frame: simbridge.Rect{X: x, Y: y, Width: w, Height: h},
		Tap:   &simbridge.Point{X: (x + w/2) / sw, Y: (y + h/2) / sh},
		Box:   &simbridge.Box{X1: x / sw, Y1: y / sh, X2: (x + w) / sw, Y2: (y + h) / sh},
	}
}
