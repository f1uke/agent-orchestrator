package simbridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
)

// Reading a simulator screen continuously, for a human to watch.
//
// This is the one continuous cost in the whole `ao sim` surface, so it is
// deliberately the narrowest thing that can work:
//
//   - it runs only while somebody is subscribed, and dies with them;
//   - it owns exactly one child process, addressed by the pid we started, with
//     no port, no discovery, no scan of the machine's process table and nothing
//     to SIGKILL by port - the failure modes vendored serve-sim's own helper had;
//   - the child's stdin is its heartbeat: if this process dies, the child reads
//     EOF and exits, so a capture cannot outlive the daemon that started it.
//
// Frames never touch the disk. One frame is in flight at a time, and the only
// retained copy is the newest one, held by the hub for a viewer that just
// connected.
//
// Why not a screenshot loop, which needs none of this: measured on the machine
// this was built for, `simctl io screenshot` manages 0.94 fps and costs 22% of
// a core on the simulator side, against 4.9 fps for 5% here. The simple
// mechanism was the expensive one.

// Frame is one encoded screen, exactly as the device produced it.
type Frame struct {
	// JPEG is a complete image. Frames are never partially delivered.
	JPEG []byte
	// Width and Height are the framebuffer's own pixel size, not a display size:
	// a viewer needs them to keep the aspect ratio honest and to turn a click
	// back into the normalized coordinate the HID layer takes.
	Width  int
	Height int
}

const (
	// frameHeaderSize is a big-endian u32 payload length followed by u16 width
	// and u16 height.
	frameHeaderSize = 8
	// maxFrameBytes bounds one frame. A full-screen iPhone JPEG measures around
	// 430 KB; 16 MiB is far above any real frame and stops a corrupt length
	// prefix from becoming an allocation.
	maxFrameBytes = 16 << 20
)

// CaptureSession is one running capture process. Close must stop the process,
// wait for it, and leave Frames unblocked - a reader parked on the frame
// descriptor has to come back once Close has run, or a viewer walking away
// could not stop the capture. Close is called exactly once, on every path out
// of Capture.
type CaptureSession interface {
	// Frames is the descriptor the capture script writes frames to. It is
	// deliberately not stdout: the native addon prints to stdout itself, and one
	// stray line on a shared channel would desynchronize the frame stream for
	// good.
	Frames() io.Reader
	Close() error
}

// CaptureStarter launches the capture process. Injectable so the framing, the
// bounds and the lifecycle can be tested without Node, a mac or a device.
type CaptureStarter func(ctx context.Context, node string, args []string, request []byte) (CaptureSession, error)

// Capturer is the streaming half of the screen surface, kept apart from Driver
// because their lifetimes are nothing alike: a Driver call is one gesture that
// ends, a Capturer runs for as long as a person is looking.
type Capturer interface {
	// Capture streams frames until ctx is cancelled or the stream ends. It
	// returns only once the capture process is gone.
	Capture(ctx context.Context, udid string, onFrame func(Frame)) error
}

// NodeCapturer runs our capture script under the machine's own `node`.
type NodeCapturer struct {
	Toolchain Toolchain
	NodePath  string
	Start     CaptureStarter
}

// NewNodeCapturer builds a capturer, installing the bridge under dataDir. It
// shares the toolchain with the gesture driver, so there is one vendored addon
// on disk and one place its version is decided.
func NewNodeCapturer(dataDir string, lookPath func(string) (string, error), start CaptureStarter) (*NodeCapturer, error) {
	if runtime.GOOS != "darwin" {
		return nil, &Error{
			Message: "watching an iOS Simulator screen only works on macOS",
			Advice:  "This machine is not a mac, so there is no simulator to watch.",
		}
	}
	node, err := lookPath("node")
	if err != nil {
		return nil, &Error{
			Message: "node was not found on PATH",
			Advice: "The live simulator view runs a small capture script under Node.js (20 or newer).\n" +
				"Install Node (for example `brew install node`) and retry. `ao sim shot` does not need it.",
		}
	}
	tc, err := Install(dataDir)
	if err != nil {
		return nil, err
	}
	if start == nil {
		start = startCaptureProcess
	}
	return &NodeCapturer{Toolchain: tc, NodePath: node, Start: start}, nil
}

type captureRequest struct {
	AddonPath string `json:"addonPath"`
	UDID      string `json:"udid"`
}

// Capture runs one capture to completion. The process is closed on every exit
// path, including a panic in onFrame: a capture that outlived its viewer would
// be exactly the CPU burn this design exists to avoid.
func (c *NodeCapturer) Capture(ctx context.Context, udid string, onFrame func(Frame)) error {
	body, err := json.Marshal(captureRequest{AddonPath: c.Toolchain.Addon, UDID: udid})
	if err != nil {
		return err
	}
	session, err := c.Start(ctx, c.NodePath, []string{c.Toolchain.Capture}, body)
	if err != nil {
		return fmt.Errorf("the simulator capture did not start: %w", err)
	}

	// Closing is what actually stops the child, so it happens once, here, for
	// every way this function can end.
	var closeOnce sync.Once
	var closeErr error
	stop := func() { closeOnce.Do(func() { closeErr = session.Close() }) }
	defer stop()

	// The reader parks on a descriptor that only produces when the device does,
	// so a cancelled context cannot be noticed by the reader itself. Closing the
	// process is what unblocks it, and both paths below then wait for the reader
	// to finish - so no frame is ever delivered after Capture has returned.
	var stopping atomic.Bool
	done := make(chan struct{})
	var readErr error
	go func() {
		defer close(done)
		readErr = readFrames(session.Frames(), func(f Frame) {
			if stopping.Load() {
				return
			}
			onFrame(f)
		})
	}()

	select {
	case <-ctx.Done():
		// Stopping because nobody is watching any more is the normal ending.
		stopping.Store(true)
		stop()
		<-done
		return nil
	case <-done:
	}
	stop()

	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return fmt.Errorf("the simulator capture ended: %w", closeErr)
	}
	return nil
}

// readFrames turns the wire into frames. A short read is an error rather than a
// partial frame, because half an image rendered as a screen is worse than a gap
// in the stream.
func readFrames(r io.Reader, onFrame func(Frame)) error {
	head := make([]byte, frameHeaderSize)
	for {
		if _, err := io.ReadFull(r, head); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("the simulator capture stream ended mid-frame: %w", err)
		}
		size := binary.BigEndian.Uint32(head[0:4])
		if size == 0 || size > maxFrameBytes {
			return fmt.Errorf("the simulator capture sent an impossible frame size (%d bytes)", size)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return fmt.Errorf("the simulator capture stream ended mid-frame: %w", err)
		}
		onFrame(Frame{
			JPEG:   payload,
			Width:  int(binary.BigEndian.Uint16(head[4:6])),
			Height: int(binary.BigEndian.Uint16(head[6:8])),
		})
	}
}

// processSession is the production CaptureSession: one child process, its
// frames on descriptor 3, and its stdin held open as the heartbeat that kills
// it if we die.
type processSession struct {
	cmd    *exec.Cmd
	frames *os.File
	stdin  io.WriteCloser
	diag   *diagnosticBuffer
}

func (s *processSession) Frames() io.Reader { return s.frames }

func (s *processSession) Close() error {
	// Closing stdin asks the child to stop the capture and exit on its own,
	// which is what lets it lift the capture engine down cleanly.
	_ = s.stdin.Close()
	_ = s.frames.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	err := s.cmd.Wait()
	if err != nil && s.diag.String() != "" {
		return fmt.Errorf("%w: %s", err, s.diag.String())
	}
	return nil
}

func startCaptureProcess(ctx context.Context, node string, args []string, request []byte) (CaptureSession, error) {
	readFrom, writeTo, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, node, args...)
	// Descriptor 3 in the child: stdout stays a diagnostic channel because the
	// addon writes to it unbidden.
	cmd.ExtraFiles = []*os.File{writeTo}
	diag := &diagnosticBuffer{}
	cmd.Stdout = diag
	cmd.Stderr = diag
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = readFrom.Close()
		_ = writeTo.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = readFrom.Close()
		_ = writeTo.Close()
		return nil, err
	}
	// The parent must not keep the write end open, or reads never see EOF when
	// the child exits.
	_ = writeTo.Close()
	if _, err := stdin.Write(append(request, '\n')); err != nil {
		_ = readFrom.Close()
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	return &processSession{cmd: cmd, frames: readFrom, stdin: stdin, diag: diag}, nil
}

// diagnosticBuffer keeps the tail of what the child said, bounded: the addon is
// chatty and a long-running capture must not accumulate its log.
type diagnosticBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const maxDiagnosticBytes = 4096

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > maxDiagnosticBytes {
		b.buf = b.buf[len(b.buf)-maxDiagnosticBytes:]
	}
	return len(p), nil
}

func (b *diagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return firstLines(b.buf)
}
