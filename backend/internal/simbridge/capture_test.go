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
func framed(payload []byte, w, h uint16) []byte {
	var buf bytes.Buffer
	head := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint32(head[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint16(head[4:6], w)
	binary.BigEndian.PutUint16(head[6:8], h)
	buf.Write(head)
	buf.Write(payload)
	return buf.Bytes()
}

// fakeSession replays a canned frame stream and records that it was closed.
type fakeSession struct {
	r io.Reader
	// unblock honours the CaptureSession contract: Close leaves Frames
	// unblocked, which is what lets a viewer walking away stop the capture.
	unblock io.Closer
	mu      sync.Mutex
	closed  int
	err     error
}

func (f *fakeSession) Frames() io.Reader { return f.r }
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
	stream := append(framed([]byte("jpeg-one"), 1320, 2868), framed([]byte("jpeg-two"), 1320, 2868)...)
	session := &fakeSession{r: bytes.NewReader(stream)}
	got := []Frame{}
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", func(f Frame) {
		got = append(got, f)
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 frames, got %d", len(got))
	}
	if string(got[0].JPEG) != "jpeg-one" || got[0].Width != 1320 || got[0].Height != 2868 {
		t.Fatalf("first frame wrong: %q %dx%d", got[0].JPEG, got[0].Width, got[0].Height)
	}
	if string(got[1].JPEG) != "jpeg-two" {
		t.Fatalf("second frame wrong: %q", got[1].JPEG)
	}
	if session.closeCount() != 1 {
		t.Fatalf("the capture process must be closed exactly once, got %d", session.closeCount())
	}
}

// A stream that dies mid-frame must never surface the bytes it did get as a
// frame: half a JPEG rendered as a screen is worse than a gap.
func TestCapture_TruncatedFrameIsNotDelivered(t *testing.T) {
	full := framed([]byte("jpeg-one"), 100, 200)
	stream := append(full, framed([]byte("truncated"), 100, 200)[:frameHeaderSize+3]...)
	session := &fakeSession{r: bytes.NewReader(stream)}
	got := 0
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", func(Frame) { got++ })
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
	session := &fakeSession{r: bytes.NewReader(head)}
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", func(Frame) {
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
		done <- capturerFor(t, session, nil).Capture(ctx, "UDID", func(Frame) {})
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
		Capture(context.Background(), "UDID", func(Frame) {})
	if err == nil || !strings.Contains(err.Error(), "node exploded") {
		t.Fatalf("want the start failure surfaced, got %v", err)
	}
}

// A capture that ends cleanly still reports what the process said if it exited
// badly - the addon dlopens private frameworks, so "it stopped working after an
// Xcode upgrade" has to be legible.
func TestCapture_ProcessExitDiagnosticSurvives(t *testing.T) {
	session := &fakeSession{r: bytes.NewReader(nil), err: errors.New("addon_load_failed: image not found")}
	err := capturerFor(t, session, nil).Capture(context.Background(), "UDID", func(Frame) {})
	if err == nil || !strings.Contains(err.Error(), "image not found") {
		t.Fatalf("want the process diagnostic, got %v", err)
	}
}
