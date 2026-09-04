package claudepeer

import (
	"crypto/sha256"
	"sync"
	"time"
)

// The receiver runs its own guards over inbound peer messages and drops what
// they catch WITHOUT telling the sender. The two a supervisor trips in normal
// use are duplicate suppression (an identical body from the same sender inside
// a 30s window) and a token bucket (capacity 30, refilling 0.5/s).
//
// So AO mirrors them, deliberately stricter than the values it read, and sends
// anything the mirror would catch through the pane instead - where no such
// guard exists. Being stricter is always safe: it can only move a message onto
// the path that is known to deliver. The receiver's values are also
// server-overridable, which is a second reason not to sit on their edge.
const (
	mirrorDedupeWindow = 45 * time.Second // receiver: 30s
	mirrorBucketSize   = 20.0             // receiver: 30
	mirrorRefillPerSec = 0.4              // receiver: 0.5
)

// guard mirrors the receiver's per-sender state. AO's daemon is a single
// long-lived process, so from every receiver's point of view all of AO's
// messages come from one sender: one mirror entry per target session tracks it
// exactly.
type guard struct {
	now func() time.Time

	mu      sync.Mutex
	targets map[string]*guardState
}

type guardState struct {
	lastBody   [sha256.Size]byte
	lastBodyAt time.Time
	hasBody    bool

	tokens     float64
	lastRefill time.Time
}

func newGuard(now func() time.Time) *guard {
	return &guard{now: now, targets: make(map[string]*guardState)}
}

// admit reports whether the receiver would accept this message, and returns a
// release func the caller MUST call: release(true) keeps the state change,
// release(false) rolls it back. The rollback matters - a send that fell back to
// the pane never reached the receiver's guards, so leaving our mirror advanced
// would make it disagree with the thing it mirrors.
func (g *guard) admit(target, message string) (release func(committed bool), ok bool) {
	body := sha256.Sum256([]byte(message))
	now := g.now()

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.targets[target]
	if state == nil {
		state = &guardState{tokens: mirrorBucketSize, lastRefill: now}
		g.targets[target] = state
	}
	if state.hasBody && state.lastBody == body && now.Sub(state.lastBodyAt) < mirrorDedupeWindow {
		return nil, false
	}
	state.tokens = refill(state.tokens, state.lastRefill, now)
	state.lastRefill = now
	if state.tokens < 1 {
		return nil, false
	}

	prev := *state
	state.tokens--
	state.lastBody = body
	state.lastBodyAt = now
	state.hasBody = true

	return func(committed bool) {
		if committed {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if current := g.targets[target]; current == state {
			*state = prev
		}
	}, true
}

func refill(tokens float64, since, now time.Time) float64 {
	elapsed := now.Sub(since).Seconds()
	if elapsed <= 0 {
		return tokens
	}
	if refilled := tokens + elapsed*mirrorRefillPerSec; refilled < mirrorBucketSize {
		return refilled
	}
	return mirrorBucketSize
}
