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
// # Why there is no longer a rate cap or a repeat filter
//
// Both belonged to a JPEG-per-frame stream, where every frame stood alone and
// dropping one cost nothing but that frame. The stream is H.264 now, measured
// at a twenty-fifth of the bytes and under half the CPU for the same frames,
// and its frames are not independent: a delta means nothing without the
// keyframe before it. Hashing for repeats would find none (a still screen
// re-encodes to different bytes), and capping the rate would silently corrupt
// the picture rather than merely thin it.
//
// What replaces them is one rule with one field behind it: **a viewer receives
// frames only from a complete starting point onwards** - the avcC description,
// then a keyframe. Until it has both it is sent nothing, and anything that
// could have left it out of step (joining mid-stream, falling behind far enough
// to lose a frame) puts it back to waiting for one and asks the encoder for a
// fresh picture group. A viewer is therefore never shown bytes it cannot
// decode, and no frame is ever dropped from a viewer that is keeping up.
//
// Frames never touch the disk. The only frame retained is the current avcC
// description, a few dozen bytes, so a viewer that connects has something to
// configure its decoder with without waiting for the next group.
//
// # The exception: a device that cannot be encoded at all
//
// Some framebuffers VideoToolbox refuses outright (see
// simbridge.codecFallbackAfter), and a capture that has produced nothing is
// moved to JPEG-per-frame rather than left silent. Those frames arrive as
// simbridge.FrameImage and every rule above simply does not apply to them: each
// one stands alone, so there is no starting point to wait for and nothing a
// viewer can be out of step with.
package simstream

import (
	"context"
	"errors"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// viewerBuffer is how many frames a single viewer may fall behind before it
// loses one. A viewer that loses one is resynchronized rather than fed a gap,
// so this only has to be deep enough to absorb an ordinary scheduling hiccup on
// a loopback socket: H.264 deltas are a few KB each, so the whole buffer is
// smaller than one of the JPEGs this stream used to carry.
const viewerBuffer = 8

// Event is one thing that happened on a device's stream: a frame, or the reason
// the stream is ending. Exactly one of the two is set.
type Event struct {
	Frame *simbridge.Frame
	Err   error
}

// Hub owns every running capture.
type Hub struct {
	capturer simbridge.Capturer

	mu      sync.Mutex
	devices map[string]*device
	closed  bool
}

// New builds a hub over a capture mechanism. A nil capturer is a machine that
// cannot capture at all: Subscribe refuses rather than hanging.
func New(capturer simbridge.Capturer) *Hub {
	return &Hub{capturer: capturer, devices: map[string]*device{}}
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
	// keyframes carries requests for a fresh picture group. Depth one on
	// purpose: two viewers joining together need one group between them, so a
	// request that arrives while one is pending is the same request.
	keyframes chan struct{}

	mu          sync.Mutex
	viewers     map[*viewer]struct{}
	description *simbridge.Frame
	ended       bool
}

// viewerState is how much of a starting point a viewer has been given. It only
// ever moves forward by receiving frames, and back to the beginning by losing
// one - which is what keeps "was sent something it cannot decode" impossible.
type viewerState uint8

const (
	needsDescription viewerState = iota
	needsKeyframe
	ready
)

type viewer struct {
	ch chan Event
	// once because both endings can reach a viewer at the same moment: the
	// viewer's own context finishing, and the capture failing under it.
	once sync.Once
	// state is guarded by the device's mutex, like the viewer set itself.
	state viewerState
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
			udid:      udid,
			cancel:    cancel,
			ctx:       captureCtx,
			viewers:   map[*viewer]struct{}{},
			stopped:   make(chan struct{}),
			keyframes: make(chan struct{}, 1),
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
	// The description a running encoder has already emitted will not be emitted
	// again on its own, so a viewer that arrives afterwards is handed the one
	// that is current.
	if dev.description != nil {
		send(dev, v, *dev.description)
	}
	dev.mu.Unlock()

	if existing {
		// An encoder already running is somewhere in the middle of a picture
		// group, and nothing in the middle of one decodes on its own. A capture
		// that is only just starting opens with a fresh group of its own accord,
		// so asking would only make it restart one it has not made yet.
		requestKeyframe(dev)
	} else {
		go h.run(dev.ctx, dev)
	}

	go func() {
		<-ctx.Done()
		h.detach(dev, v)
	}()
	return v.ch, nil
}

// requestKeyframe asks the capture for a fresh picture group without ever
// blocking the caller: a pending request already covers this one.
func requestKeyframe(dev *device) {
	select {
	case dev.keyframes <- struct{}{}:
	default:
	}
}

// run streams one device until its last viewer leaves or the capture fails.
func (h *Hub) run(ctx context.Context, dev *device) {
	defer close(dev.stopped)
	err := h.capturer.Capture(ctx, dev.udid, dev.keyframes, func(frame simbridge.Frame) {
		h.publish(dev, frame)
	})
	// A capture that ended on its own - the device went away, the addon stopped
	// loading - is reported once and not retried. Retrying against a device that
	// is not there is how a viewer-driven stream becomes a background poller.
	h.finish(dev, err)
}

// publish forwards a frame to every viewer that can currently use it.
func (h *Hub) publish(dev *device, frame simbridge.Frame) {
	dev.mu.Lock()
	if dev.ended {
		dev.mu.Unlock()
		return
	}
	kept := frame
	switch frame.Kind {
	case simbridge.FrameDescription:
		// Kept because a viewer that connects later needs it and the encoder
		// will not repeat it until a group restarts.
		dev.description = &kept
		for v := range dev.viewers {
			if v.state == needsDescription {
				v.state = needsKeyframe
			}
			send(dev, v, kept)
		}
	case simbridge.FrameKeyframe:
		for v := range dev.viewers {
			// A keyframe without the description that goes with it configures
			// nothing, so a viewer still waiting for one skips this group.
			if v.state == needsDescription {
				continue
			}
			v.state = ready
			send(dev, v, kept)
		}
	case simbridge.FrameDelta:
		for v := range dev.viewers {
			if v.state != ready {
				continue
			}
			send(dev, v, kept)
		}
	case simbridge.FrameImage:
		// A whole JPEG needs nothing before it, so the rule that gates the
		// H.264 kinds has nothing to gate: every viewer can decode this one,
		// including the one that just arrived. The viewer's state is left
		// where it is rather than moved to ready - a stream that fell back to
		// images sends no deltas, and one that has not fallen back sends no
		// images, so it can never carry the wrong meaning into the other.
		for v := range dev.viewers {
			offer(v, kept)
		}
	}
	dev.mu.Unlock()
}

// offer hands one frame to one viewer and drops it if the viewer is behind,
// with nothing to put right afterwards. It is what a whole image gets: the next
// one is a complete picture too, so a viewer that missed this one has lost a
// frame and nothing else. Asking for a fresh start here would restart the
// device's subscription for a frame nobody needed replaced - which under load
// is a restart per dropped frame.
//
// Called with the device's mutex held, like send, for the same reason: a send
// on a closed channel is a panic rather than a dropped frame.
func offer(v *viewer, frame simbridge.Frame) {
	select {
	case v.ch <- Event{Frame: &frame}:
	default:
	}
}

// send hands one frame to one viewer, or - if that viewer has fallen far enough
// behind to lose it - puts the viewer back to waiting for a starting point and
// asks for one. Called with the device's mutex held, which is what stops a send
// racing a viewer's close: a send on a closed channel is a panic, not a dropped
// frame. It cannot stall the capture because the send is non-blocking.
func send(dev *device, v *viewer, frame simbridge.Frame) {
	select {
	case v.ch <- Event{Frame: &frame}:
		return
	default:
	}
	// Losing any frame breaks the chain, and the description this viewer holds
	// may not be the one the next group is encoded against, so it goes all the
	// way back rather than merely waiting for the next keyframe.
	v.state = needsDescription
	requestKeyframe(dev)
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
