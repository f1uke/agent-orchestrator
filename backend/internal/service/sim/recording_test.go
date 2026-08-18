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
	// The recorder's screen read is asynchronous in production because being
	// off the gesture's critical path is the whole point of it. Here it runs
	// inline, so a test can say what the recorder has seen without racing a
	// goroutine - and so mutating the fake screen between gestures is a
	// sequence rather than a data race.
	svc, store := newServiceWithOpts(t, fixedClock(now), sim.WithRecorder(reader),
		sim.WithScreenRefreshRunner(func(f func()) { f() }))
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
	// The tap navigated to a new screen: a different app is now frontmost by
	// the time the finger lifts. The ordering matters and is the production
	// one - the touch is performed between acquiring the hold and releasing
	// it, so the screen has already changed when the recorder looks again.
	reader.snap = snapshotWithButton("com.app.b", "Done")
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release 1: %v", err)
	}

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
	// Twice loading, because there are now two reads BEFORE any settling: the
	// one that primes the screen when the recording opens, and the one the
	// gesture falls back to when the primed screen has nothing under the
	// finger. If the screen arrived on either of those, this test would pass
	// without the settle ever running - which is exactly what it did until a
	// mutation check disabled the settle and nothing failed.
	reader := &fakeScreenReader{script: []simbridge.Snapshot{loading, loading, arrived, arrived}}
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
	if reader.calls < 3 {
		t.Errorf("AX calls = %d; an unresolved gesture must settle rather than describe a step from a screen that is not there", reader.calls)
	}
}

// The other half of the same decision, and the reason settling is conditional.
//
// This is about the SLOW path - the one a gesture takes when there is no
// usable maintained screen - because that path is still in front of the
// human's finger. A read that resolved must cost exactly one read there; if it
// ever settles anyway, every such gesture just got three more AX reads slower.
// (That a gesture with a maintained screen costs NO read is a different
// property, pinned by
// TestAcquireHold_GestureDoesNotReadTheScreenWhenOneIsAlreadyKnown.)
func TestRecordIntent_SettledScreenCostsNoExtraReadOnTheSlowPath(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	clock := now
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, store := newServiceWithOpts(t, func() time.Time { return clock }, sim.WithRecorder(reader),
		sim.WithScreenRefreshRunner(func(f func()) { f() }))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}
	// Age the maintained screen out, so the gesture has to read for itself.
	clock = now.Add(2 * time.Minute)
	before := reader.calls

	if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent()); err != nil {
		t.Fatalf("hold: %v", err)
	}

	if got := reader.calls - before; got != 1 {
		t.Errorf("AX calls during the gesture = %d, want 1 - settling must not run on a screen that resolved", got)
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

// --- the screen read is off the gesture's critical path ----------------------
//
// The defect these pin: with a recording open, a tap went from 45 ms to 616 ms
// and a drag lost every one of its intermediate moves, because the recorder
// read the accessibility tree between the finger going down and the touch
// reaching the device. The bridge serializes, so that read is time the finger
// spends in the air - and the drag stream, which sends one request at a time,
// collapsed a whole scroll into a touch-down and a touch-up with no motion
// between them.
//
// ⚠ Timing itself cannot be asserted here. What IS structurally checkable is
// the thing that causes it: how many reads a gesture performs. That is what
// these pin; the milliseconds are in the record, measured on a device.

// The property the fix rests on: once the recorder has seen the screen, a
// gesture performs NO read at all.
func TestAcquireHold_GestureDoesNotReadTheScreenWhenOneIsAlreadyKnown(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}
	// Starting a recording primes the screen, off any gesture's path.
	primed := reader.calls
	if primed == 0 {
		t.Fatal("starting a recording must prime the screen, or the first gesture pays for it")
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if reader.calls != primed {
		t.Errorf("acquiring the hold performed %d read(s); a gesture must perform none", reader.calls-primed)
	}

	// The read happens on release instead - after the touch, with nobody
	// waiting for it - and it is what the NEXT gesture describes itself from.
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if reader.calls != primed+1 {
		t.Errorf("reads after the gesture = %d, want exactly one refresh", reader.calls-primed)
	}
}

// A whole drag takes ONE hold, so the count that matters is per drag, not per
// request - and it must not read while the finger is moving. This is the
// scroll failure stated as a count: a read during the drag is the stall that
// swallowed every move.
func TestAcquireHold_ADragPerformsNoReadWhileTheFingerIsDown(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}
	before := reader.calls

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0,
		sim.GestureIntent{Kind: "drag-begin", X: 0.3, Y: 0.2})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if reader.calls != before {
		t.Fatalf("drag-begin performed %d read(s); every one of them is motion the device never sees",
			reader.calls-before)
	}

	// And the end coordinates still arrive, which is the property #208 fixed
	// and this must not trade back.
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{
		Performed: true, End: &simbridge.Point{X: 0.3, Y: 0.9},
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].X != 0.3 || steps[0].Y != 0.2 || steps[0].ToX != 0.3 || steps[0].ToY != 0.9 {
		t.Errorf("drag recorded as %.2f,%.2f -> %.2f,%.2f, want 0.30,0.20 -> 0.30,0.90",
			steps[0].X, steps[0].Y, steps[0].ToX, steps[0].ToY)
	}
}

// The guard that keeps the maintained screen honest: a finger that lands where
// the remembered tree has nothing means the remembered tree is not this
// screen, so the recorder reads rather than describing the step from it.
func TestAcquireHold_ReadsAgainWhenTheRememberedScreenCannotDescribeTheGesture(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}
	before := reader.calls

	// The app moved on by itself, and the finger lands on something the
	// remembered screen has nothing at.
	reader.snap = snapshotWithButton("com.app.b", "Done")
	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0,
		sim.GestureIntent{Kind: "tap", X: 0.9, Y: 0.9})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if reader.calls == before {
		t.Fatal("nothing resolved under the finger and the recorder did not look again")
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// ⚠ The trade this design makes, pinned so it is a decision rather than a
// surprise.
//
// A step is described from the screen as of the end of the PREVIOUS gesture.
// Nothing the human does can change it in between - their only way to touch
// the device is through this same path - but the app can, by finishing a load
// or running an animation. When it does, AND the remembered screen still
// resolves the gesture, the step is described from the older screen.
//
// This is the cost of the recorder not making the tab unusable. The record
// says why no cheaper check is sound: proving the screen has not changed needs
// a read, which is the thing being avoided.
func TestAcquireHold_DescribesFromTheRememberedScreenWhenTheAppMovedOnUnderneath(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	// The app relabelled the control in place, after the recorder last looked.
	// The finger still lands on it, so nothing tells the recorder to look
	// again - and the step carries the label that was there before.
	reader.snap = snapshotWithButton("com.app.a", "Done")
	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].Selector != "Continue" {
		t.Errorf("selector = %q, want %q - if this changed, the trade-off described above changed with it",
			steps[0].Selector, "Continue")
	}
}

// A screen nobody has driven for a long time is not described from. The TTL
// does not make a stale screen safe - only bounds how stale one may be when a
// recording is left open and returned to.
func TestAcquireHold_ReadsAgainWhenTheRememberedScreenIsOld(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	clock := now
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, store := newServiceWithOpts(t, func() time.Time { return clock }, sim.WithRecorder(reader),
		sim.WithScreenRefreshRunner(func(f func()) { f() }))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}
	before := reader.calls

	clock = now.Add(2 * time.Minute)
	if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent()); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if reader.calls == before {
		t.Error("a screen nobody has looked at for two minutes was described from without checking it")
	}
}

// Nothing is read when no recording is open - the property that pays for the
// whole design - and that has to stay true of the refresh too, or every
// gesture on an unrecorded device would start one.
func TestReleaseHold_DoesNotRefreshTheScreenWithoutARecording(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if reader.calls != 0 {
		t.Errorf("a device with no recording open was read %d time(s)", reader.calls)
	}
}

// ⚠ At most one background read may be outstanding per device, and the reason
// is the same one the whole design rests on: the bridge SERIALIZES reads and
// touches. Two refreshes queued on it are up to a second the human's next
// touch waits behind - which is the bug this fix removes, reintroduced through
// the back door.
//
// The runner is captured rather than run, so the guard is exercised without a
// goroutine deciding the outcome.
func TestRefreshScreen_OnlyOneReadIsOutstandingPerDevice(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	var queued []func()
	svc, store := newServiceWithOpts(t, fixedClock(now), sim.WithRecorder(reader),
		sim.WithScreenRefreshRunner(func(f func()) { queued = append(queued, f) }))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	gesture := func() {
		hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, buttonIntent())
		if err != nil {
			t.Fatalf("hold: %v", err)
		}
		if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
			t.Fatalf("release: %v", err)
		}
	}

	gesture()
	gesture()
	gesture()
	if len(queued) != 1 {
		t.Fatalf("%d reads queued behind one another; at most one may be outstanding", len(queued))
	}

	// Once the outstanding one finishes, the next gesture may start another.
	queued[0]()
	gesture()
	if len(queued) != 2 {
		t.Errorf("after the outstanding read finished, a later gesture queued %d; want a second", len(queued)-1)
	}
}

// A gesture that was attempted and failed still leaves the screen wherever it
// left it, so it must refresh too. Skipping it would let one refused touch
// strand the maintained screen on a view of the device that has moved on.
func TestReleaseHold_RefreshesEvenWhenTheGestureDidNotHappen(t *testing.T) {
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
	before := reader.calls

	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: false}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if reader.calls == before {
		t.Error("a gesture that failed left the maintained screen untouched; the next one describes itself from a stale view")
	}

	// And the step itself is still not recorded - a gesture that did not
	// happen must never be written down as if it had.
	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("steps = %d, want 0 for a gesture that never happened", len(steps))
	}
}

// --- typing and keys are recorded like anything else ------------------------

// A recorded login that silently omitted the typing produces a flow that
// cannot replay, so typed text and the keys around it are captured exactly the
// way a tap is - through the same hold, off the same path.
func TestAcquireHold_RecordsTypedTextAndKeys(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	for _, intent := range []sim.GestureIntent{
		{Kind: "type", Text: "hello"},
		{Kind: "key", Name: "backspace"},
		// Non-ASCII is the human's real case, and it must survive being stored
		// and read back exactly as typed.
		{Kind: "type", Text: "สวัสดี"},
		{Kind: "key", Name: "enter"},
	} {
		hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, intent)
		if err != nil {
			t.Fatalf("hold %+v: %v", intent, err)
		}
		if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
			t.Fatalf("release: %v", err)
		}
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(steps) != 4 {
		t.Fatalf("steps = %d, want 4", len(steps))
	}
	if steps[0].Kind != "type" || steps[0].Text != "hello" {
		t.Errorf("step 1 = %q %q, want the typed text", steps[0].Kind, steps[0].Text)
	}
	if steps[1].Kind != "key" || steps[1].Detail != "backspace" {
		t.Errorf("step 2 = %q %q, want the key", steps[1].Kind, steps[1].Detail)
	}
	if steps[2].Text != "สวัสดี" {
		t.Errorf("step 3 text = %q, want it unchanged", steps[2].Text)
	}
	if steps[3].Detail != "enter" {
		t.Errorf("step 4 = %q, want enter", steps[3].Detail)
	}
}

// ⚠ #209 removed the accessibility read from between the finger going down and
// the touch reaching the device. Typing must not put work back on that path:
// a key press is not a screen read, and neither is starting one.
func TestAcquireHold_TypingAndKeysAddNoReadToTheGesturePath(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	reader := &fakeScreenReader{snap: snapshotWithButton("com.app.a", "Continue")}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}
	primed := reader.calls

	for _, intent := range []sim.GestureIntent{
		{Kind: "type", Text: "hello"},
		{Kind: "key", Name: "enter"},
		buttonIntent(),
	} {
		hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, intent)
		if err != nil {
			t.Fatalf("hold %+v: %v", intent, err)
		}
		// Checked BEFORE the release, because the release is where the read is
		// allowed to happen - after the gesture, with nobody waiting.
		if reader.calls != primed {
			t.Fatalf("%s performed %d read(s) while the gesture was in flight", intent.Kind, reader.calls-primed)
		}
		if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
			t.Fatalf("release: %v", err)
		}
		primed = reader.calls
	}
}

// A drag DOES target something: it starts on an element, and that element is
// what its selector and its screen-change detection are built from. Typing and
// key presses are the ones that target nothing.
//
// ⚠ Found by a mutation check: dropping the drag kinds from targetsAnElement
// broke nothing, because every other test in this file drives taps. A drag
// recorded with no selector loses both the wait stanza in the emitted flow and
// the fingerprint that says whether the screen changed - silently.
func TestAcquireHold_ADragStillResolvesWhatItStartedOn(t *testing.T) {
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
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{
		Performed: true, End: &simbridge.Point{X: 0.3, Y: 0.9},
	}); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].Selector != "Continue" {
		t.Errorf("selector = %q, want the element the drag started on", steps[0].Selector)
	}
}

// The other half of the same rule, stated directly: typing targets nothing, so
// it must not carry a selector describing whatever sits in the top-left corner.
func TestAcquireHold_TypingCarriesNoSelector(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	// A tree whose only element IS in the corner, so a hit-test at (0,0) would
	// find something to wrongly attach to the step.
	corner := simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.app.a"},
		Elements: []simbridge.Element{{
			Path: "0", Label: "Clock",
			Box: &simbridge.Box{X1: 0, Y1: 0, X2: 0.2, Y2: 0.1},
			Tap: &simbridge.Point{X: 0.05, Y: 0.02},
		}},
	}
	reader := &fakeScreenReader{snap: corner}
	svc, _, owner := newRecordingService(t, now, reader)
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0,
		sim.GestureIntent{Kind: "type", Text: "hello"})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}

	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	if steps[0].Selector != "" {
		t.Errorf("selector = %q; typing targets no element and must not borrow the corner's", steps[0].Selector)
	}
	if steps[0].Text != "hello" {
		t.Errorf("text = %q, want it recorded", steps[0].Text)
	}
}
