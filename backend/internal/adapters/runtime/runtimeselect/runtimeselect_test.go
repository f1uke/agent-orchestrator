package runtimeselect

import (
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New must not hide tmux's optional agent-liveness capability behind the
// claudepeer wrapper: the daemon and the review launcher reach AgentAlive by
// type assertion, and losing it silently downgrades queued-message delivery
// from "wait for the agent" to "wait a while and hope".
func TestSelectedRuntimeStillReportsAgentLiveness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("conpty does not implement AgentAlive")
	}
	if _, ok := New(nil, Options{}).(ports.AgentLivenessProber); !ok {
		t.Fatal("the selected runtime no longer implements ports.AgentLivenessProber")
	}
}
