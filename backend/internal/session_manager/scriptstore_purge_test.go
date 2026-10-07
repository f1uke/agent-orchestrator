package sessionmanager

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scriptstore"
)

func TestPurge_ScriptsStoreRefusesUnpublishedScriptsUnlessForced(t *testing.T) {
	m, st, _, _, scripts := newScriptsManager(t, "")
	rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	scripts.blocked = true
	if err := m.PurgeSession(ctx, rec.ID, false); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("purge = %v, want refused like a dirty tree", err)
	}
	if _, ok := st.sessions[rec.ID]; !ok {
		t.Fatal("a refused purge deleted the session row")
	}
	if err := m.PurgeSession(ctx, rec.ID, true); err != nil {
		t.Fatalf("forced purge: %v", err)
	}
	if last := scripts.settled[len(scripts.settled)-1]; last.policy != scriptstore.SettleDiscard {
		t.Fatalf("forced purge settled with %v, want a discard", last.policy)
	}
}
