package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/inputgate"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeInnerRuntime implements just enough of runtimeselect.Runtime for the gate
// test: it records SendMessage calls. The embedded nil interface panics if any
// other method is exercised, which none are here.
type fakeInnerRuntime struct {
	runtimeselect.Runtime
	mu   sync.Mutex
	sent []string
}

func (f *fakeInnerRuntime) SendMessage(_ context.Context, _ ports.RuntimeHandle, message string) error {
	f.mu.Lock()
	f.sent = append(f.sent, message)
	f.mu.Unlock()
	return nil
}

func (f *fakeInnerRuntime) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func TestGatedRuntime_DeliversImmediatelyWhenPaneIdle(t *testing.T) {
	inner := &fakeInnerRuntime{}
	gate := inputgate.New() // no NoteInput -> WaitForQuiet returns at once
	g := newGatedRuntime(inner, gate)

	if err := g.SendMessage(context.Background(), ports.RuntimeHandle{ID: "pane"}, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if inner.sentCount() != 1 {
		t.Fatalf("message not forwarded to inner runtime")
	}
}

func TestGatedRuntime_DefersWhileUserTyping(t *testing.T) {
	inner := &fakeInnerRuntime{}
	// A short but observable quiet window and cap for a real-time test.
	gate := inputgate.New(inputgate.WithQuietWindow(120*time.Millisecond), inputgate.WithMaxDefer(5*time.Second))
	g := newGatedRuntime(inner, gate)

	gate.NoteInput("pane") // user is mid-keystroke
	start := time.Now()
	done := make(chan struct{})
	go func() {
		_ = g.SendMessage(context.Background(), ports.RuntimeHandle{ID: "pane"}, "nudge")
		close(done)
	}()

	// While within the quiet window, delivery must NOT have happened yet.
	time.Sleep(40 * time.Millisecond)
	if inner.sentCount() != 0 {
		t.Fatalf("message injected while the user was still typing")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SendMessage never completed after the typing gap opened")
	}
	if elapsed := time.Since(start); elapsed < 120*time.Millisecond {
		t.Fatalf("delivered after %s, want >= quiet window 120ms", elapsed)
	}
	if inner.sentCount() != 1 {
		t.Fatalf("message not delivered after the quiet window")
	}
}
