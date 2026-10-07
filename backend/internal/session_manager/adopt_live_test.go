package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAdoptLiveAgent_TakesBackATerminatedSessionWhoseAgentRuns(t *testing.T) {
	m, st, rt, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", RuntimeHandleID: "live-1"})
	rt.aliveByHandle = map[string]bool{"live-1": true}
	rt.agentAliveByHandle = map[string]bool{"live-1": true}

	got, adopted, err := m.AdoptLiveAgent(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if !adopted || got.IsTerminated {
		t.Fatalf("adopted=%v terminated=%v, want the running agent taken back", adopted, got.IsTerminated)
	}
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("runtime created=%d destroyed=%d, want it left exactly as it runs", rt.created, rt.destroyed)
	}
}

// Unlike Restore, a session whose agent is gone is left terminated: the caller
// asked only for an agent that is still there.
func TestAdoptLiveAgent_LeavesASessionWhoseAgentIsGone(t *testing.T) {
	m, st, rt, _ := newManager()
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", RuntimeHandleID: "live-1"})
	rt.aliveByHandle = map[string]bool{"live-1": true}

	got, adopted, err := m.AdoptLiveAgent(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if adopted || !got.IsTerminated || rt.created != 0 {
		t.Fatalf("adopted=%v terminated=%v created=%d, want nothing changed", adopted, got.IsTerminated, rt.created)
	}
}
