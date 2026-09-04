package msgorigin_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/msgorigin"
)

func TestSenderRoundTrips(t *testing.T) {
	ctx := msgorigin.WithSender(context.Background(), "agent-orchestrator-105")
	if got := msgorigin.Sender(ctx); got != "agent-orchestrator-105" {
		t.Fatalf("Sender = %q, want the session that was set", got)
	}
}

// "Nobody in particular" has to stay distinguishable from a session id, because
// that is what makes the transport name AO itself instead of an agent.
func TestSenderIsEmptyWhenNoSessionAuthoredTheMessage(t *testing.T) {
	if got := msgorigin.Sender(context.Background()); got != "" {
		t.Fatalf("Sender on a bare context = %q, want empty", got)
	}
	if got := msgorigin.Sender(msgorigin.WithSender(context.Background(), "")); got != "" {
		t.Fatalf("Sender after setting an empty session = %q, want empty", got)
	}
}
