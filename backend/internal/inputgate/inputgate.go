// Package inputgate coordinates injected-message delivery with live user typing
// in a session's terminal pane.
//
// `ao send`, worker->orchestrator lifecycle nudges, and review nudges all deliver
// a message by typing it into the target pane's single, shared input line and
// then pressing Enter (see the runtime SendMessage adapters). That pane is the
// same one the human types into through the attached terminal, so if a message
// is injected while the user is mid-keystroke it concatenates onto their unsent
// line and the trailing Enter submits the merged text as a command.
//
// The terminal mux already funnels every client keystroke through the daemon, so
// the daemon can observe *when* a pane was last typed into. The Gate records that
// per pane (NoteInput) and lets a sender block until a typing gap opens
// (WaitForQuiet) before injecting. It cannot read the TUI's input buffer, so a
// user who types a partial line and then pauses longer than the quiet window is
// still exposed; that residual is bounded and documented on WaitForQuiet.
package inputgate

import (
	"context"
	"sync"
	"time"
)

const (
	// DefaultQuietWindow is how long a pane must go without user input before an
	// injected message is considered safe to deliver. Longer than the gap between
	// keystrokes in a typing burst, so it fires in the pause after the user stops.
	DefaultQuietWindow = 800 * time.Millisecond
	// DefaultMaxDefer caps how long delivery is held while the user keeps typing,
	// so an important nudge is never starved by a user who never stops. Well under
	// the 60s REST request timeout that bounds an `ao send` HTTP call.
	DefaultMaxDefer = 8 * time.Second
)

// Gate tracks per-pane user-input recency and blocks message injection until a
// typing gap opens. The zero value is not usable; construct with New. A nil
// *Gate is a safe no-op for both methods, so wiring that has no gate degrades to
// the old always-inject behavior rather than panicking.
type Gate struct {
	quietWindow time.Duration
	maxDefer    time.Duration
	now         func() time.Time
	sleep       func(ctx context.Context, d time.Duration) bool

	mu        sync.Mutex
	lastInput map[string]time.Time
}

// Option configures a Gate.
type Option func(*Gate)

// WithQuietWindow overrides the no-input window (default DefaultQuietWindow).
func WithQuietWindow(d time.Duration) Option {
	return func(g *Gate) {
		if d > 0 {
			g.quietWindow = d
		}
	}
}

// WithMaxDefer overrides the delivery-hold cap (default DefaultMaxDefer).
func WithMaxDefer(d time.Duration) Option {
	return func(g *Gate) {
		if d > 0 {
			g.maxDefer = d
		}
	}
}

// withClock and withSleep are unexported test seams so unit tests can drive the
// wait loop deterministically without real time.
func withClock(now func() time.Time) Option { return func(g *Gate) { g.now = now } }
func withSleep(s func(ctx context.Context, d time.Duration) bool) Option {
	return func(g *Gate) { g.sleep = s }
}

// New builds a Gate with the default quiet window and max-defer cap unless
// overridden by options.
func New(opts ...Option) *Gate {
	g := &Gate{
		quietWindow: DefaultQuietWindow,
		maxDefer:    DefaultMaxDefer,
		now:         time.Now,
		sleep:       realSleep,
		lastInput:   map[string]time.Time{},
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// NoteInput records that the user just typed into pane id. Fed by the terminal
// mux on every inbound client input frame. Safe on a nil Gate or empty id.
func (g *Gate) NoteInput(id string) {
	if g == nil || id == "" {
		return
	}
	g.mu.Lock()
	g.lastInput[id] = g.now()
	g.mu.Unlock()
}

// WaitForQuiet blocks until pane id has seen no user input for the quiet window,
// or maxDefer elapses, or ctx is cancelled — whichever comes first. It returns
// as soon as it is safe (or as safe as it is going to get) to inject a message.
//
// A pane with no observed input returns immediately, so the common
// autonomous-delivery case (nobody attached, or nobody typing) is never delayed.
// The residual: a user who types then pauses longer than the quiet window before
// resuming is treated as idle and can still be clobbered; the gate has no way to
// read the pane's input buffer to detect a paused-but-non-empty line.
func (g *Gate) WaitForQuiet(ctx context.Context, id string) {
	if g == nil || id == "" {
		return
	}
	start := g.now()
	for {
		g.mu.Lock()
		last, ok := g.lastInput[id]
		g.mu.Unlock()
		if !ok {
			return // no user input ever observed for this pane
		}
		now := g.now()
		if now.Sub(last) >= g.quietWindow {
			return // a typing gap opened — safe to inject
		}
		if now.Sub(start) >= g.maxDefer {
			return // held long enough; deliver anyway rather than starve the message
		}
		// Sleep until the earliest of (last+quietWindow) and (start+maxDefer), then
		// re-check: a keystroke landing during the sleep pushes `last` forward and
		// extends the wait, up to the maxDefer cap.
		wake := last.Add(g.quietWindow)
		if deadline := start.Add(g.maxDefer); wake.After(deadline) {
			wake = deadline
		}
		d := wake.Sub(now)
		if d <= 0 {
			return
		}
		if !g.sleep(ctx, d) {
			return // ctx cancelled
		}
	}
}

// realSleep waits d or returns false early if ctx is cancelled.
func realSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
