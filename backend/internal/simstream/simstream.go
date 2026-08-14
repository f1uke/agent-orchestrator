// Package simstream fans one simulator's screen out to the people watching it.
//
// The rule the whole package exists to enforce is negative: **a capture may not
// exist without a viewer**. Not "is stopped soon after", not "is throttled when
// idle" - cannot exist. A device's capture is started by its first subscriber
// and its process is gone before the last subscriber's Unsubscribe returns, so
// the expensive thing is structurally impossible to leave running. This repo
// has already shipped a poller that burned a core and a preview poller that
// walked worktrees four times a second; the fix for both was noticed late,
// which is why this one is a property of the shape rather than a guard.
//
// Two cheap filters sit between the device and the wire:
//
//   - repeats are dropped. The capture engine re-encodes a still screen at a
//     5 fps idle floor; measured, eight seconds of a still screen produced 39
//     frames with one distinct image. Hashing costs 0.32 ms a frame and takes
//     idle traffic to zero.
//   - a rate cap bounds what a busy screen can cost, whatever the device does.
//
// Frames never touch the disk, and only the newest one is retained - handed to
// a viewer that connects between changes so it sees the screen at once instead
// of a blank pane.
package simstream

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// DefaultMinInterval caps forwarding at 10 frames a second. The capture engine
// idles at 5 and rises above it only while the screen is actually moving, so
// this bounds the busy case without touching the common one.
const DefaultMinInterval = 100 * time.Millisecond

// viewerBuffer is how many frames a single viewer may fall behind before its
// frames start being dropped. Small on purpose: for a live view the newest
// frame is the only one worth having, and a queue would turn a slow reader into
// unbounded memory.
const viewerBuffer = 2

// Event is one thing that happened on a device's stream: a frame, or the reason
// the stream is ending. Exactly one of the two is set.
type Event struct {
	Frame *simbridge.Frame
	Err   error
}

// Hub owns every running capture.
type Hub struct {
	capturer    simbridge.Capturer
	minInterval time.Duration

	mu      sync.Mutex
	devices map[string]*device
	closed  bool
}

// Option customizes a Hub.
type Option func(*Hub)

// WithMinInterval sets the shortest gap between forwarded frames. Zero forwards
// every distinct frame.
func WithMinInterval(d time.Duration) Option {
	return func(h *Hub) { h.minInterval = d }
}

// New builds a hub over a capture mechanism. A nil capturer is a machine that
// cannot capture at all: Subscribe refuses rather than hanging.
func New(capturer simbridge.Capturer, opts ...Option) *Hub {
	h := &Hub{capturer: capturer, minInterval: DefaultMinInterval, devices: map[string]*device{}}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// ErrUnavailable is a hub with no capture mechanism - a machine that is not a
// mac, or one without Node.
var ErrUnavailable = errors.New("simstream: no capture mechanism on this machine")

// device is one running capture and the viewers attached to it.
type device struct {
	udid    string
	ctx     context.Context
	cancel  context.CancelFunc
	stopped chan struct{}

	mu       sync.Mutex
	viewers  map[*viewer]struct{}
	last     *simbridge.Frame
	lastHash [sha256.Size]byte
	lastSent time.Time
	ended    bool
}

type viewer struct {
	ch chan Event
	// once because both endings can reach a viewer at the same moment: the
	// viewer's own context finishing, and the capture failing under it.
	once sync.Once
}

// Subscribe attaches to a device's screen for as long as ctx lives. The
// returned channel is closed when the viewer goes away or the stream ends; the
// capture process is gone by then if this was the last viewer.
func (h *Hub) Subscribe(ctx context.Context, udid string) (<-chan Event, error) {
	if h.capturer == nil {
		return nil, ErrUnavailable
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrUnavailable
	}
	dev, existing := h.devices[udid]
	if !existing {
		// The capture outlives the subscriber that started it, so it gets its
		// own lifetime here rather than borrowing the first viewer's. Both the
		// cancel and the stopped channel are set before the device is visible to
		// anyone else, so a second subscriber can never find a half-built one.
		captureCtx, cancel := context.WithCancel(context.Background())
		dev = &device{
			udid:    udid,
			cancel:  cancel,
			ctx:     captureCtx,
			viewers: map[*viewer]struct{}{},
			stopped: make(chan struct{}),
		}
		h.devices[udid] = dev
	}
	h.mu.Unlock()

	v := &viewer{ch: make(chan Event, viewerBuffer)}
	dev.mu.Lock()
	if dev.ended {
		dev.mu.Unlock()
		close(v.ch)
		return v.ch, nil
	}
	dev.viewers[v] = struct{}{}
	// A viewer arriving between changes gets the screen as it is now rather
	// than a blank pane until something moves.
	if dev.last != nil {
		v.ch <- Event{Frame: dev.last}
	}
	dev.mu.Unlock()

	if !existing {
		go h.run(dev.ctx, dev)
	}

	go func() {
		<-ctx.Done()
		h.detach(dev, v)
	}()
	return v.ch, nil
}

// run streams one device until its last viewer leaves or the capture fails.
func (h *Hub) run(ctx context.Context, dev *device) {
	defer close(dev.stopped)
	err := h.capturer.Capture(ctx, dev.udid, func(frame simbridge.Frame) {
		h.publish(dev, frame)
	})
	// A capture that ended on its own - the device went away, the addon stopped
	// loading - is reported once and not retried. Retrying against a device that
	// is not there is how a viewer-driven stream becomes a background poller.
	h.finish(dev, err)
}

// publish forwards a frame if it is worth forwarding.
func (h *Hub) publish(dev *device, frame simbridge.Frame) {
	sum := sha256.Sum256(frame.JPEG)
	now := time.Now()

	dev.mu.Lock()
	if dev.ended {
		dev.mu.Unlock()
		return
	}
	// The newest frame is kept whether or not it is forwarded, so a viewer that
	// connects later is handed the current screen and not a stale one.
	kept := frame
	dev.last = &kept
	if sum == dev.lastHash {
		dev.mu.Unlock()
		return
	}
	if h.minInterval > 0 && !dev.lastSent.IsZero() && now.Sub(dev.lastSent) < h.minInterval {
		dev.mu.Unlock()
		return
	}
	dev.lastHash = sum
	dev.lastSent = now
	// Sending inside the lock is what stops a frame racing a viewer's close -
	// a send on a closed channel is a panic, not a dropped frame. It cannot
	// stall the capture because every send below is non-blocking.
	for v := range dev.viewers {
		// Drop, never queue: for a live view the newest frame is the only one
		// worth having, and a viewer that stalled must not stall the others.
		select {
		case v.ch <- Event{Frame: &kept}:
		default:
		}
	}
	dev.mu.Unlock()
}

// finish ends a device's stream, telling whoever is still attached why.
func (h *Hub) finish(dev *device, cause error) {
	h.mu.Lock()
	if h.devices[dev.udid] == dev {
		delete(h.devices, dev.udid)
	}
	h.mu.Unlock()

	dev.mu.Lock()
	if dev.ended {
		dev.mu.Unlock()
		return
	}
	dev.ended = true
	for v := range dev.viewers {
		if cause != nil {
			// The reason has to get through even to a viewer that is behind, so
			// it displaces a queued frame rather than being dropped.
			select {
			case v.ch <- Event{Err: cause}:
			default:
				select {
				case <-v.ch:
				default:
				}
				select {
				case v.ch <- Event{Err: cause}:
				default:
				}
			}
		}
		closeViewer(v)
	}
	dev.viewers = map[*viewer]struct{}{}
	dev.mu.Unlock()
}

// detach removes one viewer and, when it was the last, stops the capture before
// returning. Waiting for the process here is what makes "no viewer, no capture"
// true rather than eventual.
func (h *Hub) detach(dev *device, v *viewer) {
	dev.mu.Lock()
	if _, ok := dev.viewers[v]; !ok {
		dev.mu.Unlock()
		return
	}
	delete(dev.viewers, v)
	last := len(dev.viewers) == 0 && !dev.ended
	closeViewer(v)
	dev.mu.Unlock()

	if !last {
		return
	}
	h.mu.Lock()
	if h.devices[dev.udid] == dev {
		delete(h.devices, dev.udid)
	}
	h.mu.Unlock()
	dev.cancel()
	<-dev.stopped
}

func closeViewer(v *viewer) {
	v.once.Do(func() { close(v.ch) })
}

// Shutdown stops every capture. It is the daemon going away, and it waits: a
// capture process that outlived the daemon would have nobody left to stop it.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	h.closed = true
	devices := make([]*device, 0, len(h.devices))
	for _, dev := range h.devices {
		devices = append(devices, dev)
	}
	h.devices = map[string]*device{}
	h.mu.Unlock()

	for _, dev := range devices {
		dev.cancel()
		<-dev.stopped
	}
}
