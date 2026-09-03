package simstream_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
)

// fakeCapturer stands in for the capture process. It records how many captures
// were ever started and how many are running right now - the two numbers the
// no-polling-when-nobody-looks rule is made of.
type fakeCapturer struct {
	mu        sync.Mutex
	started   int
	running   int
	udids     []string
	requested int
	emit      chan simbridge.Frame
	endWith   error
	blockEnd  chan struct{}
}

func newFakeCapturer() *fakeCapturer {
	return &fakeCapturer{emit: make(chan simbridge.Frame, 64), blockEnd: make(chan struct{})}
}

func (f *fakeCapturer) Capture(
	ctx context.Context, udid string, keyframes <-chan struct{}, onFrame func(simbridge.Frame),
) error {
	f.mu.Lock()
	f.started++
	f.running++
	f.udids = append(f.udids, udid)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-keyframes:
			f.mu.Lock()
			f.requested++
			f.mu.Unlock()
		case frame := <-f.emit:
			onFrame(frame)
		case <-f.blockEnd:
			return f.endWith
		}
	}
}

func (f *fakeCapturer) counts() (started, running int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.running
}

func (f *fakeCapturer) keyframeRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requested
}

func chunk(kind simbridge.FrameKind, payload string) simbridge.Frame {
	return simbridge.Frame{Data: []byte(payload), Kind: kind, Width: 100, Height: 200}
}

// group is the shortest thing a viewer can start from: the decoder
// configuration followed by a keyframe.
func group(capturer *fakeCapturer, name string) {
	capturer.emit <- chunk(simbridge.FrameDescription, "avcC-"+name)
	capturer.emit <- chunk(simbridge.FrameKeyframe, "idr-"+name)
}

func delta(payload string) simbridge.Frame { return chunk(simbridge.FrameDelta, payload) }

// startedStream drains the opening description and keyframe so a test can talk
// about the frames it actually cares about.
func startedStream(t *testing.T, capturer *fakeCapturer, sub <-chan simstream.Event, name string) {
	t.Helper()
	group(capturer, name)
	if got := recv(t, sub); got.Frame.Kind != simbridge.FrameDescription {
		t.Fatalf("want the description first, got kind %v", got.Frame.Kind)
	}
	if got := recv(t, sub); got.Frame.Kind != simbridge.FrameKeyframe {
		t.Fatalf("want the keyframe second, got kind %v", got.Frame.Kind)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
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

func recv(t *testing.T, ch <-chan simstream.Event) simstream.Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("stream closed while a frame was expected")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame")
		return simstream.Event{}
	}
}

// The CPU-safety story in one test: nothing captures until somebody subscribes,
// and the capture ends when the last of them goes away.
func TestHub_CaptureRunsOnlyWhileSomebodyIsWatching(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)

	if started, running := capturer.counts(); started != 0 || running != 0 {
		t.Fatalf("a hub with no subscribers must capture nothing, got started=%d running=%d", started, running)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if _, err := hub.Subscribe(ctx, "UDID-A"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })

	cancel()
	waitFor(t, "the capture to stop", func() bool { _, running := capturer.counts(); return running == 0 })
	if started, _ := capturer.counts(); started != 1 {
		t.Fatalf("want exactly one capture over the whole test, got %d", started)
	}
}

func TestHub_TwoViewersOfOneDeviceShareOneCapture(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)

	first, cancelFirst := context.WithCancel(context.Background())
	second, cancelSecond := context.WithCancel(context.Background())
	a, err := hub.Subscribe(first, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	b, err := hub.Subscribe(second, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "one capture", func() bool { started, running := capturer.counts(); return started == 1 && running == 1 })

	startedStream(t, capturer, a, "one")
	if got := recv(t, b); string(got.Frame.Data) != "avcC-one" {
		t.Fatalf("viewer b got %q", got.Frame.Data)
	}
	if got := recv(t, b); string(got.Frame.Data) != "idr-one" {
		t.Fatalf("viewer b got %q", got.Frame.Data)
	}
	capturer.emit <- delta("p1")
	if got := recv(t, a); string(got.Frame.Data) != "p1" {
		t.Fatalf("viewer a got %q", got.Frame.Data)
	}
	if got := recv(t, b); string(got.Frame.Data) != "p1" {
		t.Fatalf("viewer b got %q", got.Frame.Data)
	}

	// One viewer leaving must not stop the other's stream.
	cancelFirst()
	waitFor(t, "the first viewer to drop", func() bool { _, ok := <-a; return !ok })
	if _, running := capturer.counts(); running != 1 {
		t.Fatal("the capture must survive while a second viewer is still watching")
	}
	cancelSecond()
	waitFor(t, "the capture to stop", func() bool { _, running := capturer.counts(); return running == 0 })
}

func TestHub_SeparateDevicesGetSeparateCaptures(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := hub.Subscribe(ctx, "UDID-A"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := hub.Subscribe(ctx, "UDID-B"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "two captures", func() bool { started, running := capturer.counts(); return started == 2 && running == 2 })
}

// A delta is meaningless without the group it belongs to, so a viewer is sent
// nothing at all until it has both halves of a starting point. Shipping the
// deltas anyway is what turns an H.264 pane into a smear.
func TestHub_ViewerGetsNothingBeforeACompleteStartingPoint(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })

	// An encoder that is mid-group when the viewer attaches.
	capturer.emit <- delta("orphan-1")
	capturer.emit <- delta("orphan-2")
	group(capturer, "first")
	capturer.emit <- delta("p1")

	if got := recv(t, sub); string(got.Frame.Data) != "avcC-first" {
		t.Fatalf("the first thing a viewer receives must be a description, got %q", got.Frame.Data)
	}
	if got := recv(t, sub); string(got.Frame.Data) != "idr-first" {
		t.Fatalf("want the keyframe next, got %q", got.Frame.Data)
	}
	if got := recv(t, sub); string(got.Frame.Data) != "p1" {
		t.Fatalf("want the delta that follows the group, got %q", got.Frame.Data)
	}
}

// A keyframe on its own configures nothing. A viewer that has not been given a
// description yet has to sit the group out rather than start from bytes its
// decoder is not set up for.
func TestHub_KeyframeWithoutADescriptionIsNotAStartingPoint(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })

	capturer.emit <- chunk(simbridge.FrameKeyframe, "idr-orphan")
	capturer.emit <- delta("p-orphan")
	group(capturer, "real")

	if got := recv(t, sub); string(got.Frame.Data) != "avcC-real" {
		t.Fatalf("a keyframe with no description must not start a viewer, got %q", got.Frame.Data)
	}
}

// The capture is already mid-group when a second viewer arrives, and nothing in
// the middle of one decodes on its own. Asking for a fresh group is the only
// thing that can give the new viewer a picture.
func TestHub_ViewerJoiningAStreamAsksForAFreshGroup(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })
	startedStream(t, capturer, first, "one")
	if capturer.keyframeRequests() != 0 {
		t.Fatal("the first viewer starts the capture, which opens a group by itself - asking would restart it")
	}

	late, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "a keyframe request", func() bool { return capturer.keyframeRequests() >= 1 })

	// The description already emitted is handed over at once, but a viewer is
	// only live once the fresh group arrives.
	if got := recv(t, late); string(got.Frame.Data) != "avcC-one" {
		t.Fatalf("a late viewer must be handed the current description, got %q", got.Frame.Data)
	}
	capturer.emit <- delta("mid-group")
	group(capturer, "two")
	if got := recv(t, late); string(got.Frame.Data) != "avcC-two" {
		t.Fatalf("a late viewer must not be fed mid-group deltas, got %q", got.Frame.Data)
	}
}

// A viewer that stops reading must lose frames rather than stall the device's
// other viewers - and must then be resynchronized rather than handed a stream
// with a hole in it. The reading viewer sees every frame in turn: if the hub
// blocked on the stalled one, it would stop receiving as soon as the stalled
// viewer's buffer filled.
func TestHub_SlowViewerIsResynchronizedAndDoesNotBlockTheOthers(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Nobody ever reads this one.
	stalled, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	fast, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })
	startedStream(t, capturer, fast, "one")
	// Taken after both viewers are attached: the second one joining asks for a
	// group of its own, and counting from zero would let that request stand in
	// for the resync this test is actually about.
	joined := capturer.keyframeRequests()

	for i := range 32 {
		want := "delta-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		capturer.emit <- delta(want)
		if got := recv(t, fast); string(got.Frame.Data) != want {
			t.Fatalf("frame %d: the reading viewer got %q, want %q - a stalled viewer must not hold the stream up",
				i, got.Frame.Data, want)
		}
	}

	// Overflowing the stalled viewer must have asked for a group it can
	// actually start from, rather than leaving it with a gap.
	waitFor(t, "a resync request for the stalled viewer", func() bool { return capturer.keyframeRequests() > joined })

	// And what it does eventually read never contains a delta it cannot use:
	// every delta in its buffer is preceded by a complete starting point.
	seen := 0
	for {
		select {
		case ev, ok := <-stalled:
			if !ok {
				return
			}
			if ev.Frame == nil {
				continue
			}
			if seen == 0 && ev.Frame.Kind != simbridge.FrameDescription {
				t.Fatalf("the stalled viewer's stream must open with a description, got kind %v", ev.Frame.Kind)
			}
			seen++
		default:
			if seen == 0 {
				t.Fatal("the stalled viewer received nothing at all")
			}
			return
		}
	}
}

// Nothing throttles the stream any more: the frames a device produces are the
// frames a viewer that is keeping up receives. A cap would drop deltas, and a
// dropped delta is a corrupt picture rather than a thinner one.
func TestHub_EveryFrameReachesAViewerThatKeepsUp(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })
	startedStream(t, capturer, sub, "one")

	// Far more frames than any interval-based cap would let through, back to
	// back, read one at a time so nothing is lost to the buffer.
	for i := range 40 {
		want := "delta-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		capturer.emit <- delta(want)
		if got := recv(t, sub); string(got.Frame.Data) != want {
			t.Fatalf("frame %d: got %q, want %q - no frame may be withheld from a viewer keeping up",
				i, got.Frame.Data, want)
		}
	}
}

// A device that goes away mid-view has to say so and stop, not retry forever
// against a device that is not there.
func TestHub_CaptureFailureReachesTheViewerAndEndsTheStream(t *testing.T) {
	capturer := newFakeCapturer()
	capturer.endWith = errors.New("device is gone")
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })
	close(capturer.blockEnd)

	ev := recv(t, sub)
	if ev.Err == nil {
		t.Fatal("a failed capture must reach the viewer as an error")
	}
	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("the stream must end after the failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end after the failure")
	}
	time.Sleep(50 * time.Millisecond)
	if started, _ := capturer.counts(); started != 1 {
		t.Fatalf("a failed capture must not be retried in a loop, started %d times", started)
	}
}

// Shutdown is the daemon going away: every capture stops, whatever its viewers
// are doing.
func TestHub_ShutdownStopsEveryCapture(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := hub.Subscribe(ctx, "UDID-A"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := hub.Subscribe(ctx, "UDID-B"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "two captures", func() bool { _, running := capturer.counts(); return running == 2 })
	hub.Shutdown()
	if _, running := capturer.counts(); running != 0 {
		t.Fatalf("Shutdown must leave no capture running, got %d", running)
	}
}

func TestHub_WithoutACapturerSubscribeFails(t *testing.T) {
	hub := simstream.New(nil)
	t.Cleanup(hub.Shutdown)
	if _, err := hub.Subscribe(context.Background(), "UDID-A"); err == nil {
		t.Fatal("a hub with no capture mechanism must refuse rather than hang")
	}
}

// closableDriver stands in for the resident gesture bridge, which holds a node
// child that could have a finger down on a device.
type closableDriver struct {
	mu     sync.Mutex
	closed int
}

func (c *closableDriver) AX(context.Context, string) (simbridge.Snapshot, error) {
	return simbridge.Snapshot{}, nil
}

func (c *closableDriver) Hold(context.Context, string, []simbridge.Event) error { return nil }

func (c *closableDriver) Perform(context.Context, string, []simbridge.Event) (simbridge.PerformResult, error) {
	return simbridge.PerformResult{}, nil
}

func (c *closableDriver) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return nil
}

func (c *closableDriver) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// The daemon going away has to take the gesture bridge with it. A bridge left
// running is a node child with an injector attached to a device and nobody left
// who can lift its finger.
func TestScreen_ShutdownClosesTheGestureBridge(t *testing.T) {
	driver := &closableDriver{}
	screen := simstream.NewScreenForTest(driver, nil, nil)

	if _, err := screen.Driver(context.Background()); err != nil {
		t.Fatalf("driver: %v", err)
	}
	screen.Shutdown()

	if driver.closeCount() != 1 {
		t.Fatalf("the gesture bridge was closed %d times on shutdown, want 1", driver.closeCount())
	}
}

// `xcrun simctl list` costs most of a second, and the gesture route reads the
// device list before a touch. Paying that per event is what a human feels as
// lag, so a listing this recent is reused outright.
func TestScreen_DeviceListingIsReusedForABurstOfGestures(t *testing.T) {
	var runs atomic.Int64
	clock := time.Now()
	now := func() time.Time { return clock }
	screen := simstream.NewScreenForTest(nil, countingLister(&runs, nil), now)

	for range 6 {
		if _, err := screen.Devices(context.Background()); err != nil {
			t.Fatalf("devices: %v", err)
		}
	}
	if runs.Load() != 1 {
		t.Fatalf("simctl ran %d times for six reads inside the cache window, want 1", runs.Load())
	}
}

// The bug this shape exists to avoid: a plain expiry makes whichever caller
// arrives just after it pay the whole second. During a drag that was a visible
// stall every couple of seconds. A listing that is merely getting old is served
// at once and refreshed behind the caller.
func TestScreen_AStaleListingIsServedAtOnceAndRefreshedBehind(t *testing.T) {
	var runs atomic.Int64
	release := make(chan struct{})
	// started carries one value per refresh that actually began, so "only one
	// runs at a time" can be checked without racing the goroutine that runs it.
	started := make(chan int64, 16)
	clock := time.Now()
	now := func() time.Time { return clock }
	// Every read after the first blocks until the test lets it go, standing in
	// for the second `xcrun simctl list` really takes.
	screen := simstream.NewScreenForTest(nil, countingLister(&runs, func(n int64) {
		if n > 1 {
			started <- n
			<-release
		}
	}), now)

	if _, err := screen.Devices(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	clock = clock.Add(simstream.DevicesTTL + time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := screen.Devices(context.Background()); err != nil {
			t.Errorf("stale read: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a caller waited on a refresh instead of being served the listing it already had")
	}

	// And the refresh really was started, exactly once however many callers
	// arrive while it is running.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("a stale listing was served but never refreshed behind the caller")
	}
	for range 5 {
		if _, err := screen.Devices(context.Background()); err != nil {
			t.Fatalf("during refresh: %v", err)
		}
	}
	select {
	case n := <-started:
		t.Fatalf("refresh %d started while one was already running: a burst of callers must share one", n)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
}

// Old enough and there is nothing worth serving: the caller waits rather than
// being told about simulators that may all be gone.
func TestScreen_AListingPastItsMaxAgeIsFetchedBeforeAnswering(t *testing.T) {
	var runs atomic.Int64
	clock := time.Now()
	now := func() time.Time { return clock }
	screen := simstream.NewScreenForTest(nil, countingLister(&runs, nil), now)

	if _, err := screen.Devices(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	clock = clock.Add(simstream.DevicesMaxAge + time.Second)
	if _, err := screen.Devices(context.Background()); err != nil {
		t.Fatalf("second: %v", err)
	}
	if runs.Load() != 2 {
		t.Fatalf("simctl ran %d times, want a listing this old fetched before answering", runs.Load())
	}
}

// A machine that could not answer must be asked again, not remembered as
// broken: a daemon that started before Xcode was ready would otherwise report
// no simulators for as long as it lived.
func TestScreen_AFailedListingIsNotCached(t *testing.T) {
	var runs atomic.Int64
	screen := simstream.NewScreenForTest(nil, func(context.Context, string, ...string) ([]byte, error) {
		runs.Add(1)
		return nil, errors.New("xcrun: not found")
	}, time.Now)

	for range 3 {
		if _, err := screen.Devices(context.Background()); err == nil {
			t.Fatal("a listing that failed must be reported, not cached as empty")
		}
	}
	if runs.Load() != 3 {
		t.Fatalf("simctl ran %d times, want every failed read retried", runs.Load())
	}
}

// countingLister stands in for `xcrun simctl list`, counting calls and letting
// a test hold one open the way the real one holds a caller for most of a second.
func countingLister(runs *atomic.Int64, block func(int64)) simctl.Runner {
	return func(context.Context, string, ...string) ([]byte, error) {
		n := runs.Add(1)
		if block != nil {
			block(n)
		}
		return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-3":[` +
			`{"udid":"UDID-A","name":"iPhone","state":"Booted","isAvailable":true}]}}`), nil
	}
}

// --- the keyboard input mode ------------------------------------------------
//
// 🗝 Why this is cached at all, when the probe itself says it must not be.
//
// Asking a guest which input mode it is in costs a process spawned INSIDE the
// simulator, measured at 909-960 ms on the machine this was built for, and it
// used to run synchronously in front of every keystroke: a character typed in
// the Device tab took 1164-1181 ms to reach the device, of which ~935 ms was
// this one question and 6 ms was the device. Typing that arrives in a lump
// after a pause reads as broken even when every character is right.
//
// What makes reuse safe is not the clock, it is what the browser already did.
// The pane sends the CHARACTER the human meant (`event.key`), so a human who
// switches their Mac to Thai starts sending Thai runes - and PlanText routes
// those to the pasteboard from the TEXT alone, before the input mode is
// consulted at all. So a stale "US" cannot corrupt Thai. What it could still
// get wrong is a switch to another LATIN layout (AZERTY, QWERTZ, UK) while
// typing ASCII, which is what the freshness window below is sized for.

// countingKeyboard stands in for `simctl spawn <udid> defaults read`, counting
// only the keyboard probes so a test can tell them from device listings.
func countingKeyboard(probes *atomic.Int64, block func(int64), identifier string) simctl.Runner {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		isProbe := false
		for _, a := range args {
			if a == "defaults" {
				isProbe = true
			}
		}
		if !isProbe {
			return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-3":[` +
				`{"udid":"UDID-A","name":"iPhone","state":"Booted","isAvailable":true}]}}`), nil
		}
		n := probes.Add(1)
		if block != nil {
			block(n)
		}
		return []byte("(\n    \"" + identifier + "\"\n)\n"), nil
	}
}

// The whole point: a burst of typing asks the guest once, not once per key.
//
// ⚠ It asserts that no further probe is even STARTED, not merely that the count
// has not moved yet. Counting alone passed with the fresh window disabled
// entirely - the stale branch answers from the same remembered value and kicks
// a refresh behind the caller, and the count is read before that goroutine
// gets there. The distinction is the point: inside the window nothing is asked
// at all, so a burst of typing costs the guest nothing.
func TestScreen_KeyboardModeIsReusedForABurstOfKeystrokes(t *testing.T) {
	var probes atomic.Int64
	started := make(chan int64, 8)
	clock := time.Now()
	now := func() time.Time { return clock }
	screen := simstream.NewScreenForTest(nil, countingKeyboard(&probes, func(n int64) {
		if n > 1 {
			started <- n
		}
	}, "en_US@sw=QWERTY;hw=Automatic"), now)

	for range 6 {
		mode, err := screen.Keyboard(context.Background(), "UDID-A")
		if err != nil {
			t.Fatalf("keyboard: %v", err)
		}
		if !mode.SendsUSASCII() {
			t.Fatal("a US guest must still be reported as one; the cache may not change the answer")
		}
	}
	select {
	case n := <-started:
		t.Fatalf("the guest was asked again (probe %d) inside the window, where nothing should be asked", n)
	case <-time.After(200 * time.Millisecond):
	}
	if probes.Load() != 1 {
		t.Fatalf("the guest was asked %d times for six keystrokes inside the window, want 1", probes.Load())
	}
}

// Serving a mode that is merely getting old, and refreshing behind the caller,
// is what keeps a keystroke mid-session from being the unlucky one that pays
// the second. Same shape as the device listing, for the same reason.
func TestScreen_AStaleKeyboardModeIsServedAtOnceAndRefreshedBehind(t *testing.T) {
	var probes atomic.Int64
	release := make(chan struct{})
	started := make(chan int64, 16)
	clock := time.Now()
	now := func() time.Time { return clock }
	screen := simstream.NewScreenForTest(nil, countingKeyboard(&probes, func(n int64) {
		if n > 1 {
			started <- n
			<-release
		}
	}, "en_US@sw=QWERTY;hw=Automatic"), now)

	if _, err := screen.Keyboard(context.Background(), "UDID-A"); err != nil {
		t.Fatalf("first: %v", err)
	}
	clock = clock.Add(simstream.KeyboardTTL + time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := screen.Keyboard(context.Background(), "UDID-A"); err != nil {
			t.Errorf("stale read: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a mode that is only getting old must be served at once, not waited for")
	}
	<-started
	close(release)
}

// Past a minute the speed is not worth it. A recording left open while somebody
// did something else comes back to a machine that may have changed language.
func TestScreen_AKeyboardModePastItsMaxAgeIsProbedBeforeAnswering(t *testing.T) {
	var probes atomic.Int64
	clock := time.Now()
	now := func() time.Time { return clock }
	screen := simstream.NewScreenForTest(nil, countingKeyboard(&probes, nil, "en_US@sw=QWERTY;hw=Automatic"), now)

	if _, err := screen.Keyboard(context.Background(), "UDID-A"); err != nil {
		t.Fatalf("first: %v", err)
	}
	clock = clock.Add(simstream.KeyboardMaxAge + time.Second)
	if _, err := screen.Keyboard(context.Background(), "UDID-A"); err != nil {
		t.Fatalf("second: %v", err)
	}
	if probes.Load() != 2 {
		t.Fatalf("the guest was asked %d times, want a mode this old established before answering", probes.Load())
	}
}

// Two devices are two guests with two input modes. Sharing one answer between
// them would be the layout bug wearing a cache.
func TestScreen_EachDeviceKeepsItsOwnKeyboardMode(t *testing.T) {
	var probes atomic.Int64
	clock := time.Now()
	now := func() time.Time { return clock }
	screen := simstream.NewScreenForTest(nil, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		udid := ""
		for i, a := range args {
			if a == "spawn" && i+1 < len(args) {
				udid = args[i+1]
			}
		}
		if udid == "" {
			return []byte(`{"devices":{}}`), nil
		}
		probes.Add(1)
		if udid == "UDID-THAI" {
			return []byte("(\n    \"th_TH@sw=Thai;hw=Automatic\"\n)\n"), nil
		}
		return []byte("(\n    \"en_US@sw=QWERTY;hw=Automatic\"\n)\n"), nil
	}, now)

	us, err := screen.Keyboard(context.Background(), "UDID-US")
	if err != nil {
		t.Fatalf("us: %v", err)
	}
	thai, err := screen.Keyboard(context.Background(), "UDID-THAI")
	if err != nil {
		t.Fatalf("thai: %v", err)
	}
	if !us.SendsUSASCII() {
		t.Fatal("the US device must send US ASCII")
	}
	if thai.SendsUSASCII() {
		t.Fatal("a Thai guest must never be reported as sending US ASCII - that is the corruption bug")
	}
	if probes.Load() != 2 {
		t.Fatalf("the guest was asked %d times for two devices, want one each", probes.Load())
	}
}

// A guest that would not answer is asked again. Remembering "unknown" would
// route every later keystroke through the pasteboard for as long as the daemon
// lived, which is slow rather than wrong - but it is also never revisited.
func TestScreen_AFailedKeyboardProbeIsNotCached(t *testing.T) {
	var probes atomic.Int64
	screen := simstream.NewScreenForTest(nil, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		for _, a := range args {
			if a == "defaults" {
				probes.Add(1)
				return nil, errors.New("device not booted")
			}
		}
		return []byte(`{"devices":{}}`), nil
	}, time.Now)

	for range 3 {
		if _, err := screen.Keyboard(context.Background(), "UDID-A"); err == nil {
			t.Fatal("a guest that would not say must be reported, not cached as an answer")
		}
	}
	if probes.Load() != 3 {
		t.Fatalf("the guest was asked %d times, want every failed probe retried", probes.Load())
	}
}

// 🗝 What the daemon has to be able to answer before it touches anything.
//
// A simulator's input is driven through a client bound to ONE boot of the
// device. The udid outlives a reboot and that client does not, and the
// simulator accepts events into the dead one without complaint - so a daemon
// whose bridge outlives a device's boot went on reporting taps as delivered
// while the screen never moved. Naming the boot is what lets the bridge notice.
func TestScreen_BootNamesTheRunOfADeviceNotTheDevice(t *testing.T) {
	lister := func(state, lastBootedAt string) simctl.Runner {
		return func(context.Context, string, ...string) ([]byte, error) {
			return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-3":[` +
				`{"udid":"UDID-A","name":"iPhone","state":"` + state + `","isAvailable":true,` +
				`"lastBootedAt":"` + lastBootedAt + `"}]}}`), nil
		}
	}

	booted := simstream.NewScreenForTest(nil, lister("Booted", "2026-08-31T04:37:26Z"), nil)
	boot, err := booted.Boot(context.Background(), "udid-a")
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	// Matched the way every other lookup here matches - a udid a human typed is
	// not required to carry simctl's own casing.
	if boot != "2026-08-31T04:37:26Z" {
		t.Fatalf("boot = %q, want the device's lastBootedAt", boot)
	}

	// A device that is not booted has no run to name, and the bridge refuses to
	// touch it rather than posting events a shut-down simulator swallows.
	off := simstream.NewScreenForTest(nil, lister("Shutdown", "2026-08-31T04:37:26Z"), nil)
	if boot, err := off.Boot(context.Background(), "UDID-A"); err != nil || boot != "" {
		t.Fatalf("a shut-down device named boot %q (err %v), want empty", boot, err)
	}

	// So does one this machine has never heard of.
	if boot, err := booted.Boot(context.Background(), "UDID-GONE"); err != nil || boot != "" {
		t.Fatalf("an unknown device named boot %q (err %v), want empty", boot, err)
	}
}

// A whole JPEG stands on its own, so the rule that holds an H.264 viewer back
// until it has a description and a keyframe must not hold it back at all.
//
// This is the hub's half of the iPhone 14 Pro bug: a device whose framebuffer
// VideoToolbox refuses is streamed as images, and there is no description
// coming - not late, not ever - so a viewer gated on one would sit at
// "connecting" with a working capture running underneath it.
func TestHub_AWholeImageReachesAViewerThatHasNoDescription(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := hub.Subscribe(ctx, "UDID-OF-A-MODEL-NOBODY-LISTED")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	capturer.emit <- chunk(simbridge.FrameImage, "jpeg-one")
	got := recv(t, sub)
	if got.Frame == nil || got.Frame.Kind != simbridge.FrameImage || string(got.Frame.Data) != "jpeg-one" {
		t.Fatalf("want the image delivered as-is, got %+v", got.Frame)
	}

	// And it keeps arriving: an image stream has no picture group to fall out
	// of, so nothing about the second frame depends on the first.
	capturer.emit <- chunk(simbridge.FrameImage, "jpeg-two")
	if next := recv(t, sub); string(next.Frame.Data) != "jpeg-two" {
		t.Fatalf("want the second image, got %+v", next.Frame)
	}
}

// A second viewer joining an image stream needs nothing replayed and nothing
// restarted - it can decode the very next frame.
func TestHub_ASecondViewerOfAnImageStreamGetsTheNextImage(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first, err := hub.Subscribe(ctx, "UDID")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	capturer.emit <- chunk(simbridge.FrameImage, "jpeg-one")
	recv(t, first)

	second, err := hub.Subscribe(ctx, "UDID")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	capturer.emit <- chunk(simbridge.FrameImage, "jpeg-two")
	for _, sub := range []<-chan simstream.Event{first, second} {
		if got := recv(t, sub); string(got.Frame.Data) != "jpeg-two" {
			t.Fatalf("every viewer must get the image, got %+v", got.Frame)
		}
	}
}

// A viewer that falls behind on an H.264 stream has to be resynchronized,
// because the frames it missed are what the next ones are encoded against. An
// image stream has no such chain: the next whole picture puts it right by
// itself. Asking the device for a fresh start anyway would restart its
// subscription once per dropped frame, which is a thrash on exactly the device
// that is already the expensive one to stream.
func TestHub_ABehindViewerOfAnImageStreamIsNotResynchronized(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer)
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := hub.Subscribe(ctx, "UDID")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// Far more than one viewer can hold, and nothing is read while they arrive.
	for i := 0; i < 40; i++ {
		capturer.emit <- chunk(simbridge.FrameImage, fmt.Sprintf("jpeg-%d", i))
	}
	waitFor(t, "the images to be published", func() bool { return len(capturer.emit) == 0 })
	if got := capturer.keyframeRequests(); got != 0 {
		t.Fatalf("a dropped image must not restart the device's subscription, %d restarts asked for", got)
	}

	// And the viewer is still being served: what it holds is whole pictures.
	if got := recv(t, sub); got.Frame == nil || got.Frame.Kind != simbridge.FrameImage {
		t.Fatalf("want an image, got %+v", got.Frame)
	}
}
