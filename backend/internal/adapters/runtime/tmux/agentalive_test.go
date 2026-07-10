package tmux

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// newAgentAliveRuntime wires a runtime whose pane-pid lookup is faked via the
// runner and whose child-process probe is faked via hasLiveChild.
func newAgentAliveRuntime(paneOut []byte, runErr error, child bool, childErr error) *Runtime {
	r, fr := newTestRuntime(0)
	fr.outputs = [][]byte{paneOut}
	fr.err = runErr
	r.hasLiveChild = func(_ context.Context, _ int) (bool, error) { return child, childErr }
	return r
}

func TestAgentAliveTrueWhenPaneHasLiveChild(t *testing.T) {
	r := newAgentAliveRuntime([]byte("40123\n"), nil, true, nil)
	alive, err := r.AgentAlive(context.Background(), ports.RuntimeHandle{ID: "review-worker-1"})
	if err != nil {
		t.Fatalf("AgentAlive error: %v", err)
	}
	if !alive {
		t.Fatal("AgentAlive = false, want true (pane has a live agent child)")
	}
}

func TestAgentAliveFalseWhenPaneIsBareShell(t *testing.T) {
	// pane_pid resolves but the keep-alive shell has no child => agent exited.
	r := newAgentAliveRuntime([]byte("40123\n"), nil, false, nil)
	alive, err := r.AgentAlive(context.Background(), ports.RuntimeHandle{ID: "review-worker-1"})
	if err != nil {
		t.Fatalf("AgentAlive error: %v", err)
	}
	if alive {
		t.Fatal("AgentAlive = true, want false (bare keep-alive shell, no agent child)")
	}
}

func TestAgentAliveFalseWhenSessionMissing(t *testing.T) {
	// A definitively-missing session is a clean false, nil (not a probe error).
	missing := &fakeRunner{err: &exec.ExitError{}, outputs: [][]byte{[]byte("can't find session: review-worker-1")}}
	r, _ := newTestRuntime(0)
	r.runner = missing
	alive, err := r.AgentAlive(context.Background(), ports.RuntimeHandle{ID: "review-worker-1"})
	if err != nil {
		t.Fatalf("AgentAlive error: %v", err)
	}
	if alive {
		t.Fatal("AgentAlive = true, want false for a missing session")
	}
}

func TestAgentAliveProbeErrorSurfaces(t *testing.T) {
	// An unexpected tmux failure (not a missing-session signal) is a probe error,
	// never a silent death — callers must not reap on a failed probe.
	r, fr := newTestRuntime(0)
	fr.err = errors.New("boom")
	fr.outputs = [][]byte{[]byte("some transient tmux noise")}
	_, err := r.AgentAlive(context.Background(), ports.RuntimeHandle{ID: "review-worker-1"})
	if err == nil {
		t.Fatal("AgentAlive error = nil, want a probe error on an ambiguous tmux failure")
	}
}

// A create colliding with a stale DEAD same-named session (an orphan left by a
// terminated/restarted session — e.g. re-import→open Orchestrator) reaps the
// stale session and retries, instead of failing with "duplicate session".
func TestCreateReapsStaleDeadSessionOnDuplicate(t *testing.T) {
	r, fr := newTestRuntime(0)
	fr.errQueue = []error{&exec.ExitError{}, nil, nil, nil, nil, nil, nil}
	fr.outputs = [][]byte{[]byte("duplicate session: sess"), []byte("123\n")}
	r.hasLiveChild = func(context.Context, int) (bool, error) { return false, nil }

	handle, err := r.Create(context.Background(), ports.RuntimeConfig{
		SessionID: "sess", WorkspacePath: "/ws", Argv: []string{"claude"},
	})
	if err != nil {
		t.Fatalf("Create should reap the dead stale session and succeed: %v", err)
	}
	if handle.ID != "sess" {
		t.Fatalf("handle = %q, want sess", handle.ID)
	}
	var killed, created int
	for _, c := range fr.calls {
		if len(c.args) > 0 {
			switch c.args[0] {
			case "kill-session":
				killed++
			case "new-session":
				created++
			}
		}
	}
	if killed != 1 || created != 2 {
		t.Fatalf("expected 1 kill + 2 new-session (retry), got kill=%d new=%d", killed, created)
	}
}

// A create colliding with a LIVE same-named session must NOT clobber it.
func TestCreateRefusesToClobberLiveDuplicate(t *testing.T) {
	r, fr := newTestRuntime(0)
	fr.errQueue = []error{&exec.ExitError{}, nil}
	fr.outputs = [][]byte{[]byte("duplicate session: sess"), []byte("123\n")}
	r.hasLiveChild = func(context.Context, int) (bool, error) { return true, nil }

	if _, err := r.Create(context.Background(), ports.RuntimeConfig{
		SessionID: "sess", WorkspacePath: "/ws", Argv: []string{"claude"},
	}); err == nil {
		t.Fatal("Create must refuse to clobber a live-agent duplicate session")
	}
}

// Compile-time assertion that the tmux runtime satisfies the optional capability.
var _ ports.AgentLivenessProber = (*Runtime)(nil)
