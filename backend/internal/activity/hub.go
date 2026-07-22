// Package activity is the in-memory push path for the per-session agent
// activity feed.
//
// It sits deliberately OUTSIDE the CDC pipeline. CDC carries durable changes:
// change_log is written by triggers, its event_type is a closed allow-list, and
// the sessions trigger fires only when activity_state actually CHANGES — so a
// hundred consecutive "active" tool ticks produce exactly one CDC row. That is
// right for a status pill and useless for a live activity feed. Per-tool
// activity is ephemeral state with a lifetime of seconds; writing every Read and
// Edit into SQLite to fire a trigger would put write amplification on the hot
// path of every tool call for data nobody wants to keep.
//
// The precedent is notify.Hub, which this mirrors: in-memory pub/sub, per
// subscriber buffered channel, and lossy by design so a slow consumer can never
// stall an agent's hook. A dropped tick is a bubble that skipped a frame —
// nobody is lied to, because every event carries its own TTL and the coarse
// level it decays to (see domain.ActivityEvent).
package activity

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// subscriberBuffer matches notify.Hub's: enough to absorb a burst of tool ticks,
// small enough that a stalled consumer is dropped rather than accumulating.
const subscriberBuffer = 64

// coalesceWindow caps how often ONE session may push a routine tool frame. A
// busy agent fires several tool hooks per second; a bubble that changes five
// times a second is unreadable and burns CPU on an always-on-top overlay.
// Throttling here caps the redraw rate for every consumer at once.
const coalesceWindow = 200 * time.Millisecond

type subscription struct {
	sessionID domain.SessionID
	ch        chan domain.ActivityEvent
}

// Hub is an in-process publisher for activity-feed SSE subscribers.
type Hub struct {
	mu     sync.RWMutex
	nextID int
	subs   map[int]subscription
	// lastTool is the last routine tool frame published per session, used for
	// throttling. Entries are dropped when a session reports it has exited.
	lastTool map[domain.SessionID]time.Time
	now      func() time.Time
}

// NewHub constructs an empty activity Hub.
func NewHub() *Hub {
	return &Hub{
		subs:     map[int]subscription{},
		lastTool: map[domain.SessionID]time.Time{},
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// Subscribe registers a live activity subscriber. An empty sessionID receives
// every session, which is what a desktop overlay watching all sessions wants.
func (h *Hub) Subscribe(sessionID domain.SessionID) (<-chan domain.ActivityEvent, func()) {
	if h == nil {
		ch := make(chan domain.ActivityEvent)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan domain.ActivityEvent, subscriberBuffer)
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subs[id] = subscription{sessionID: sessionID, ch: ch}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if sub, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(sub.ch)
		}
		h.mu.Unlock()
	}
}

// Publish pushes a curated activity event to matching subscribers without ever
// blocking the caller — this runs on the hook path of every tool call.
//
// Routine tool frames are throttled per session (see coalesceWindow). Failures,
// messages and coarse-level changes are never throttled: they are what keep the
// consumer's decay ladder truthful.
func (h *Hub) Publish(_ context.Context, ev domain.ActivityEvent) error {
	if h == nil {
		return nil
	}
	if ev.At.IsZero() {
		ev.At = h.now()
	}

	h.mu.Lock()
	if h.throttled(ev) {
		h.mu.Unlock()
		return nil
	}
	if isRoutineToolFrame(ev) {
		h.lastTool[ev.SessionID] = ev.At
	}
	if ev.Coarse == domain.CoarseExited {
		delete(h.lastTool, ev.SessionID)
	}
	h.mu.Unlock()

	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, sub := range h.subs {
		if sub.sessionID != "" && sub.sessionID != ev.SessionID {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
		}
	}
	return nil
}

// throttled reports whether a routine tool frame arrived too soon after the
// previous one for the same session. Callers must hold the write lock.
func (h *Hub) throttled(ev domain.ActivityEvent) bool {
	if !isRoutineToolFrame(ev) {
		return false
	}
	last, seen := h.lastTool[ev.SessionID]
	return seen && ev.At.Sub(last) < coalesceWindow
}

// isRoutineToolFrame is true for the high-volume, low-stakes tool ticks. A
// failure is excluded: it is rare and worth every frame.
func isRoutineToolFrame(ev domain.ActivityEvent) bool {
	return ev.Kind == domain.ActivityEventToolStart || ev.Kind == domain.ActivityEventToolEnd
}
