package claudecode

// End-to-end wiring test for token telemetry: the REAL observer + REAL sqlite store
// + REAL claude-code reader (claudecode.ReadSessionUsage) against a fixture
// transcript — the exact composition daemon.startTokenUsageObserver builds. It
// proves a claude-code session's parsed totals land on its session row through the
// observer's own poll, not just through the pieces in isolation.

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/tokenusage"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestObserverPersistsClaudeUsageEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workspace := seedTranscript(t, "native-e2e", transcript)

	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: workspace, RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "p1",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{WorkspacePath: workspace, AgentSessionID: "native-e2e"},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Start the observer with a fast tick; StartPollLoop runs an immediate first poll.
	obs := tokenusage.New(store, ReadSessionUsage, tokenusage.Config{Tick: 5 * time.Millisecond})
	done := obs.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	var got domain.SessionRecord
	for time.Now().Before(deadline) {
		got, _, err = store.GetSession(ctx, rec.ID)
		if err != nil {
			t.Fatalf("get session: %v", err)
		}
		if !got.TokensUpdatedAt.IsZero() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if got.TokensUpdatedAt.IsZero() {
		t.Fatal("observer never persisted token usage")
	}
	if got.TokenUsage != wantUsage {
		t.Fatalf("persisted usage = %+v, want %+v", got.TokenUsage, wantUsage)
	}
}
