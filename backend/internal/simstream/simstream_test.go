package simstream_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
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
// device list before every touch to refuse a device that is not booted. Paying
// that per click is what a human feels as lag, so a listing this recent is
// reused.
func TestScreen_DeviceListingIsReusedForABurstOfGestures(t *testing.T) {
	var runs atomic.Int64
	clock := time.Now()
	now := func() time.Time { return clock }
	screen := simstream.NewScreenForTest(nil, func(context.Context, string, ...string) ([]byte, error) {
		runs.Add(1)
		return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-3":[` +
			`{"udid":"UDID-A","name":"iPhone","state":"Booted","isAvailable":true}]}}`), nil
	}, now)

	for range 6 {
		if _, err := screen.Devices(context.Background()); err != nil {
			t.Fatalf("devices: %v", err)
		}
	}
	if runs.Load() != 1 {
		t.Fatalf("simctl ran %d times for six reads inside the cache window, want 1", runs.Load())
	}

	// And a listing old enough to have missed a simulator being booted is not
	// reused: the cache is a burst filter, not a memory.
	clock = clock.Add(simstream.DevicesTTL + time.Millisecond)
	if _, err := screen.Devices(context.Background()); err != nil {
		t.Fatalf("devices: %v", err)
	}
	if runs.Load() != 2 {
		t.Fatalf("simctl ran %d times, want a second run once the listing went stale", runs.Load())
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
