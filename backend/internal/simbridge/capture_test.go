package simbridge

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// framed builds the wire the capture script writes on its own descriptor.
func framed(payload []byte, w, h uint16, kind FrameKind) []byte {
	var buf bytes.Buffer
	head := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(head[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint16(head[4:6], w)
	binary.BigEndian.PutUint16(head[6:8], h)
	head[8] = byte(kind)
	buf.Write(head)
	buf.Write(payload)
	return buf.Bytes()
}

// delta is the ordinary frame kind, and the one most tests do not care about.
func framedDelta(payload []byte, w, h uint16) []byte { return framed(payload, w, h, FrameDelta) }

// fakeSession replays a canned frame stream and records that it was closed.
type fakeSession struct {
	r io.Reader
	// unblock honours the CaptureSession contract: Close leaves Frames
	// unblocked, which is what lets a viewer walking away stop the capture.
	unblock  io.Closer
	mu       sync.Mutex
	closed   int
	requests [][]byte
	err      error
}

func (f *fakeSession) Frames() io.Reader { return f.r }
func (f *fakeSession) Request(line []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, append([]byte(nil), line...))
	return nil
}
func (f *fakeSession) sentRequests() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.requests...)
}
func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	if f.unblock != nil {
		_ = f.unblock.Close()
	}
	return f.err
}
func (f *fakeSession) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func capturerFor(t *testing.T, session CaptureSession, startErr error) *NodeCapturer {
	t.Helper()
	return &NodeCapturer{
		Toolchain: Toolchain{Script: "capture.mjs", Addon: "addon.node"},
		NodePath:  "/usr/bin/node",
		Start: func(context.Context, string, []string, []byte) (CaptureSession, error) {
			return session, startErr
		},
	}
}

func TestCapture_DeliversFramesInOrderWithTheirSize(t *testing.T) {
	stream := append(framedDelta([]byte("nal-one"), 1320, 2868), framedDelta([]byte("nal-two"), 1320, 2868)...)
	session := &fakeSession{r: bytes.NewReader(stream)}
	got := []Frame{}
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", nil, func(f Frame) {
		got = append(got, f)
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 frames, got %d", len(got))
	}
	if string(got[0].Data) != "nal-one" || got[0].Width != 1320 || got[0].Height != 2868 {
		t.Fatalf("first frame wrong: %q %dx%d", got[0].Data, got[0].Width, got[0].Height)
	}
	if string(got[1].Data) != "nal-two" {
		t.Fatalf("second frame wrong: %q", got[1].Data)
	}
	if session.closeCount() != 1 {
		t.Fatalf("the capture process must be closed exactly once, got %d", session.closeCount())
	}
}

// The kind is the difference between bytes a decoder can start from and bytes
// that only mean something after them. Losing it on the wire would leave the
// viewer unable to tell a keyframe from a delta, which is how an H.264 stream
// turns into a smear.
func TestCapture_FrameKindSurvivesTheWire(t *testing.T) {
	stream := framed([]byte("avcC"), 1320, 2868, FrameDescription)
	stream = append(stream, framed([]byte("idr"), 1320, 2868, FrameKeyframe)...)
	stream = append(stream, framed([]byte("p"), 1320, 2868, FrameDelta)...)
	stream = append(stream, framed([]byte("jpeg"), 1179, 2556, FrameImage)...)
	session := &fakeSession{r: bytes.NewReader(stream)}
	var kinds []FrameKind
	if err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", nil, func(f Frame) {
		kinds = append(kinds, f.Kind)
	}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	want := []FrameKind{FrameDescription, FrameKeyframe, FrameDelta, FrameImage}
	if len(kinds) != len(want) {
		t.Fatalf("want %d frames, got %d", len(want), len(kinds))
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("frame %d: kind %v, want %v", i, kinds[i], want[i])
		}
	}
}

// A kind this build does not know means the stream and the reader disagree
// about the wire. Guessing would deliver bytes to a decoder that cannot use
// them, so it ends the stream instead.
func TestCapture_UnknownFrameKindEndsTheStream(t *testing.T) {
	session := &fakeSession{r: bytes.NewReader(framed([]byte("x"), 1, 1, FrameKind(9)))}
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", nil, func(Frame) {
		t.Fatal("a frame of an unknown kind must not be delivered")
	})
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("want an unknown-kind error, got %v", err)
	}
}

// A viewer that joins mid-stream cannot decode deltas from an encoder that is
// already running, so the hub asks for a fresh keyframe. That request has to
// reach the capture process.
func TestCapture_KeyframeRequestReachesTheProcess(t *testing.T) {
	pr, pw := io.Pipe()
	session := &fakeSession{r: pr, unblock: pr}
	keyframes := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- capturerFor(t, session, nil).Capture(ctx, "UDID", keyframes, func(Frame) {}) }()

	keyframes <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(session.sentRequests()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	sent := session.sentRequests()
	if len(sent) == 0 {
		t.Fatal("a keyframe request must be forwarded to the capture process")
	}
	if !strings.Contains(string(sent[0]), "keyframe") {
		t.Fatalf("unexpected control line %q", sent[0])
	}
	cancel()
	<-done
	_ = pw.Close()
}

// A stream that dies mid-frame must never surface the bytes it did get as a
// frame: half a JPEG rendered as a screen is worse than a gap.
func TestCapture_TruncatedFrameIsNotDelivered(t *testing.T) {
	full := framedDelta([]byte("nal-one"), 100, 200)
	stream := append(full, framedDelta([]byte("truncated"), 100, 200)[:frameHeaderSize+3]...)
	session := &fakeSession{r: bytes.NewReader(stream)}
	got := 0
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", nil, func(Frame) { got++ })
	if err == nil {
		t.Fatal("a truncated stream must be reported, not treated as a clean end")
	}
	if got != 1 {
		t.Fatalf("only the complete frame may be delivered, got %d", got)
	}
}

// The frame cap is what stops a corrupt or hostile length prefix from turning
// into an unbounded allocation.
func TestCapture_OversizeFrameIsRefusedNotAllocated(t *testing.T) {
	head := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(head[0:4], maxFrameBytes+1)
	head[8] = byte(FrameDelta)
	session := &fakeSession{r: bytes.NewReader(head)}
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", nil, func(Frame) {
		t.Fatal("an oversize frame must not be delivered")
	})
	if err == nil || !strings.Contains(err.Error(), "frame") {
		t.Fatalf("want a frame-size error, got %v", err)
	}
	if session.closeCount() != 1 {
		t.Fatal("the process must still be closed after a refused frame")
	}
}

// The whole CPU-safety story rests on this: when the caller stops watching, the
// capture process goes away. Not eventually - as part of Capture returning.
func TestCapture_ContextCancellationClosesTheProcess(t *testing.T) {
	pr, pw := io.Pipe()
	session := &fakeSession{r: pr, unblock: pr}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- capturerFor(t, session, nil).Capture(ctx, "UDID", nil, func(Frame) {})
	}()
	// A live stream that is producing nothing must still stop on cancel.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Capture did not return after its context was cancelled")
	}
	if session.closeCount() != 1 {
		t.Fatalf("the process must be closed on cancellation, closed %d times", session.closeCount())
	}
	_ = pw.Close()
}

func TestCapture_StartFailureIsReported(t *testing.T) {
	err := capturerFor(t, &fakeSession{r: bytes.NewReader(nil)}, errors.New("node exploded")).
		Capture(context.Background(), "UDID", nil, func(Frame) {})
	if err == nil || !strings.Contains(err.Error(), "node exploded") {
		t.Fatalf("want the start failure surfaced, got %v", err)
	}
}

// A capture that ends cleanly still reports what the process said if it exited
// badly - the addon dlopens private frameworks, so "it stopped working after an
// Xcode upgrade" has to be legible.
func TestCapture_ProcessExitDiagnosticSurvives(t *testing.T) {
	session := &fakeSession{r: bytes.NewReader(nil), err: errors.New("addon_load_failed: image not found")}
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", nil, func(Frame) {})
	if err == nil || !strings.Contains(err.Error(), "image not found") {
		t.Fatalf("want the process diagnostic, got %v", err)
	}
}

// A device whose framebuffer VideoToolbox will not encode produces NOTHING -
// no description, no keyframe, no delta, and no error either, because the
// encoder fails inside the addon and the capture process is none the wiser. On
// this machine that is any simulator whose width is odd - 1125x2436 and
// 1179x2556, seven models between them - and the pane sat on "connecting" for
// as long as anybody was willing to watch it.
//
// The trigger is silence, not the model: a list of models is a list somebody
// has to remember to add to, and this asserts the behaviour for a device the
// code has never heard of.
func TestCapture_ADeviceThatEncodesNothingIsMovedToImages(t *testing.T) {
	pr, pw := io.Pipe()
	session := &fakeSession{r: pr, unblock: pr}
	capturer := capturerFor(t, session, nil)
	capturer.fallbackAfter = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- capturer.Capture(ctx, "UDID-OF-A-MODEL-NOBODY-LISTED", nil, func(Frame) {}) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(session.sentRequests()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	sent := session.sentRequests()
	if len(sent) == 0 {
		t.Fatal("a capture that has produced no frames at all must be asked for images")
	}
	if !strings.Contains(string(sent[0]), "mjpeg") {
		t.Fatalf("unexpected control line %q", sent[0])
	}
	cancel()
	<-done
	_ = pw.Close()
}

// The other half of the same rule: a device that IS encoding must be left on
// H.264. Falling back on a working stream would cost twenty-five times the
// bytes for a picture that was already fine.
func TestCapture_ADeviceThatIsEncodingStaysOnH264(t *testing.T) {
	pr, pw := io.Pipe()
	session := &fakeSession{r: pr, unblock: pr}
	capturer := capturerFor(t, session, nil)
	capturer.fallbackAfter = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	seen := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- capturer.Capture(ctx, "UDID", nil, func(Frame) {
			select {
			case seen <- struct{}{}:
			default:
			}
		})
	}()
	go func() { _, _ = pw.Write(framed([]byte("avcC"), 1320, 2868, FrameDescription)) }()

	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("the frame never arrived")
	}
	// Well past the fallback: if it were going to fire, it has had its chance.
	time.Sleep(100 * time.Millisecond)
	if got := session.sentRequests(); len(got) != 0 {
		t.Fatalf("a stream that is producing frames must not be switched, got %q", got)
	}
	cancel()
	<-done
	_ = pw.Close()
}
