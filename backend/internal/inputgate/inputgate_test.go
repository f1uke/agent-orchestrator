package inputgate

import (
	"context"
	"sync"
	"testing"
	"time"
)

// testClock is a manually-advanced clock so the wait loop is deterministic.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Unix(1_000_000, 0)} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestWaitForQuiet_NoInputReturnsImmediately(t *testing.T) {
	clock := newTestClock()
	slept := 0
	g := New(withClock(clock.now), withSleep(func(context.Context, time.Duration) bool {
		slept++
		return true
	}))
	g.WaitForQuiet(context.Background(), "pane") // never typed here
	if slept != 0 {
		t.Fatalf("a pane with no observed input must not wait; slept %d times", slept)
	}
}

func TestWaitForQuiet_WaitsForQuietWindowAfterInput(t *testing.T) {
	clock := newTestClock()
	var total time.Duration
	g := New(
		WithQuietWindow(800*time.Millisecond),
		WithMaxDefer(8*time.Second),
		withClock(clock.now),
		withSleep(func(_ context.Context, d time.Duration) bool {
			total += d
			clock.advance(d)
			return true // no further input during the sleep
		}),
	)
	g.NoteInput("pane")
	g.WaitForQuiet(context.Background(), "pane")
	if total < 800*time.Millisecond {
		t.Fatalf("waited %s after one keystroke, want >= quiet window 800ms", total)
	}
	if total > 900*time.Millisecond {
		t.Fatalf("waited %s, want ~800ms (should not overshoot the quiet window)", total)
	}
}

func TestWaitForQuiet_ContinuousTypingCapsAtMaxDefer(t *testing.T) {
	clock := newTestClock()
	var total time.Duration
	g := New(WithQuietWindow(800*time.Millisecond), WithMaxDefer(3*time.Second), withClock(clock.now))
	// The user keeps typing throughout: every sleep advances the clock AND records
	// another keystroke, so the quiet window never opens and the maxDefer cap must
	// be what releases the wait.
	g.sleep = func(_ context.Context, d time.Duration) bool {
		total += d
		clock.advance(d)
		g.NoteInput("pane")
		return true
	}
	g.NoteInput("pane")
	g.WaitForQuiet(context.Background(), "pane")
	if total < 3*time.Second {
		t.Fatalf("waited %s under continuous typing, want >= maxDefer 3s", total)
	}
	if total > 3800*time.Millisecond {
		t.Fatalf("waited %s, want ~maxDefer (one quiet-window granularity of slack)", total)
	}
}

func TestWaitForQuiet_ContextCancelReturns(t *testing.T) {
	clock := newTestClock()
	g := New(withClock(clock.now), withSleep(func(context.Context, time.Duration) bool {
		return false // simulate ctx cancelled inside the sleep
	}))
	g.NoteInput("pane")
	done := make(chan struct{})
	go func() {
		g.WaitForQuiet(context.Background(), "pane")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitForQuiet did not return when the sleep reported cancellation")
	}
}

func TestWaitForQuiet_RealContextCancelUnblocks(t *testing.T) {
	// End-to-end with the real sleep: a cancelled context must unblock a wait that
	// is holding for an actively-typing pane.
	g := New(WithQuietWindow(time.Hour), WithMaxDefer(time.Hour))
	g.NoteInput("pane")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		g.WaitForQuiet(ctx, "pane")
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForQuiet did not return after real context cancel")
	}
}

func TestNilGateIsSafe(t *testing.T) {
	var g *Gate
	g.NoteInput("x")                          // must not panic
	g.WaitForQuiet(context.Background(), "x") // must not panic
}

func TestNoteInputEmptyIDIgnored(t *testing.T) {
	g := New()
	g.NoteInput("")
	g.mu.Lock()
	n := len(g.lastInput)
	g.mu.Unlock()
	if n != 0 {
		t.Fatalf("empty id must not be recorded; map has %d entries", n)
	}
}
