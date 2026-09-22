package daemon

import (
	"context"
	goruntime "runtime"
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

// Agent liveness is how every consumer tells a live agent from the shell a pane
// keeps after its agent exits. Losing it is INVISIBLE at runtime - the queue
// waits on a timer instead of a signal, the session manager's reap-safety check
// says "dead" for everything, Resume adopts a pane with no agent in it - so pin
// that the input gate carries the capability through rather than hiding it.
func TestGatedRuntimeKeepsAgentLiveness(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("conpty cannot report agent liveness; the queue falls back to a bounded wait there")
	}
	adapter := runtimeselect.New(nil, runtimeselect.Options{})
	if agentLivenessProber(adapter) == nil {
		t.Fatal("the selected runtime must expose AgentAlive, or queued messages lose their readiness signal")
	}
	if _, ok := newGatedRuntime(adapter, nil).(ports.AgentLivenessProber); !ok {
		t.Fatal("the input gate hides AgentAlive; the session manager would read every pane as agentless")
	}
}

// probeOnlyInner is an inner runtime that can report agent liveness.
type probeOnlyInner struct {
	runtimeselect.Runtime
	alive bool
}

func (p probeOnlyInner) AgentAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return p.alive, nil
}

// The wrapper forwards the inner runtime's answer, and a runtime without the
// capability (conpty) stays without it, so its consumers keep their fallback.
func TestGatedRuntimeForwardsAgentLivenessOnlyWhenInnerHasIt(t *testing.T) {
	g := newGatedRuntime(probeOnlyInner{alive: true}, nil)
	prober, ok := g.(ports.AgentLivenessProber)
	if !ok {
		t.Fatal("capability lost through the gate")
	}
	if alive, err := prober.AgentAlive(context.Background(), ports.RuntimeHandle{ID: "h"}); err != nil || !alive {
		t.Fatalf("AgentAlive = %v, %v; want the inner runtime's true", alive, err)
	}
	if _, ok := newGatedRuntime(&fakeInnerRuntime{}, nil).(ports.AgentLivenessProber); ok {
		t.Fatal("a runtime without the capability gained one through the gate")
	}
}
