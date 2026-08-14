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
	mu       sync.Mutex
	started  int
	running  int
	udids    []string
	emit     chan simbridge.Frame
	endWith  error
	blockEnd chan struct{}
}

func newFakeCapturer() *fakeCapturer {
	return &fakeCapturer{emit: make(chan simbridge.Frame, 16), blockEnd: make(chan struct{})}
}

func (f *fakeCapturer) Capture(ctx context.Context, udid string, onFrame func(simbridge.Frame)) error {
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

func frame(payload string) simbridge.Frame {
	return simbridge.Frame{JPEG: []byte(payload), Width: 100, Height: 200}
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

	capturer.emit <- frame("one")
	if got := recv(t, a); string(got.Frame.JPEG) != "one" {
		t.Fatalf("viewer a got %q", got.Frame.JPEG)
	}
	if got := recv(t, b); string(got.Frame.JPEG) != "one" {
		t.Fatalf("viewer b got %q", got.Frame.JPEG)
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

// The idle floor of the capture engine re-encodes a still screen five times a
// second. Forwarding those would cost 2 MB/s to say nothing changed.
func TestHub_IdenticalFramesAreSentOnce(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer, simstream.WithMinInterval(0))
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })

	capturer.emit <- frame("still")
	capturer.emit <- frame("still")
	capturer.emit <- frame("moved")

	if got := recv(t, sub); string(got.Frame.JPEG) != "still" {
		t.Fatalf("first frame %q", got.Frame.JPEG)
	}
	// If the repeat were forwarded, this would read "still" a second time.
	if got := recv(t, sub); string(got.Frame.JPEG) != "moved" {
		t.Fatalf("a byte-identical repeat must not be forwarded; got %q", got.Frame.JPEG)
	}
}

// A viewer that arrives between changes must not stare at nothing until the
// screen next moves.
func TestHub_NewViewerGetsTheLastFrameAtOnce(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer, simstream.WithMinInterval(0))
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })
	capturer.emit <- frame("current")
	recv(t, first)

	late, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if got := recv(t, late); string(got.Frame.JPEG) != "current" {
		t.Fatalf("a late viewer must be handed the current screen, got %q", got.Frame.JPEG)
	}
}

// A viewer that stops reading must lose frames, not stall the device's other
// viewers. The reading viewer sees every frame in turn: if the hub blocked on
// the stalled one, it would stop receiving as soon as the stalled viewer's small
// buffer filled.
func TestHub_SlowViewerDropsFramesInsteadOfBlocking(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer, simstream.WithMinInterval(0))
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Nobody ever reads this one.
	if _, err := hub.Subscribe(ctx, "UDID-A"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	fast, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })

	for i := range 8 {
		want := "frame-" + string(rune('a'+i))
		capturer.emit <- frame(want)
		if got := recv(t, fast); string(got.Frame.JPEG) != want {
			t.Fatalf("frame %d: the reading viewer got %q, want %q - a stalled viewer must not hold the stream up",
				i, got.Frame.JPEG, want)
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

// The rate capturer bounds what a busy screen can cost, independently of how fast
// the device produces frames.
func TestHub_MinIntervalCapsTheForwardedRate(t *testing.T) {
	capturer := newFakeCapturer()
	hub := simstream.New(capturer, simstream.WithMinInterval(time.Hour))
	t.Cleanup(hub.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub, err := hub.Subscribe(ctx, "UDID-A")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitFor(t, "the capture to start", func() bool { _, running := capturer.counts(); return running == 1 })
	capturer.emit <- frame("one")
	recv(t, sub)
	capturer.emit <- frame("two")

	var got atomic.Bool
	go func() {
		if _, ok := <-sub; ok {
			got.Store(true)
		}
	}()
	time.Sleep(150 * time.Millisecond)
	if got.Load() {
		t.Fatal("a frame arriving inside the minimum interval must not be forwarded")
	}
}
