package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// A message AO types into a pane reaches the transcript looking exactly like the
// human typed it. The messenger records who wrote each one - for a project that
// learns from sessions, and only for one.
func TestSessionMessenger_RecordsWhoWroteADelivery(t *testing.T) {
	for _, learnOn := range []bool{true, false} {
		store, err := sqlite.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		ctx := context.Background()
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/repo/p", RegisteredAt: time.Now(), Config: domain.ProjectConfig{LearnFromSessions: learnOn}}); err != nil {
			t.Fatal(err)
		}
		rec, err := store.CreateSession(ctx, domain.SessionRecord{
			ProjectID: "p", Kind: domain.KindWorker,
			Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()},
			Metadata: domain.SessionMetadata{RuntimeHandleID: "ao-1/terminal_0"},
		})
		if err != nil {
			t.Fatal(err)
		}
		messenger := newSessionMessenger(store, &captureRuntimeSender{}, nil, nil)
		sends := []struct {
			trigger, body string
			want          domain.DeliveryAuthor
		}{
			{"", "keep the PR small", domain.DeliveryAuthorHuman},
			{msgdelivery.TriggerSend, "[from @p-2] qa passed", domain.DeliveryAuthorAgent},
			{msgdelivery.TriggerNudge, "CI is failing on PR #1.", domain.DeliveryAuthorAO},
		}
		for _, s := range sends {
			sendCtx := ctx
			if s.trigger != "" {
				sendCtx = msgdelivery.WithOrigin(ctx, msgdelivery.Origin{Trigger: s.trigger})
			}
			if _, err := messenger.Send(sendCtx, rec.ID, s.body); err != nil {
				t.Fatal(err)
			}
		}
		got, err := store.ListDeliveredFingerprints(ctx, rec.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !learnOn {
			if len(got) != 0 {
				t.Errorf("learning off: %d deliveries recorded, want none", len(got))
			}
			continue
		}
		if len(got) != len(sends) {
			t.Fatalf("recorded %d deliveries, want %d", len(got), len(sends))
		}
		for i, s := range sends {
			if got[i].Author != s.want {
				t.Errorf("%q recorded as %s, want %s", s.body, got[i].Author, s.want)
			}
		}
	}
}
