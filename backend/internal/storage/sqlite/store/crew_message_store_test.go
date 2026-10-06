package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLatestDeliveredCrewMessageFrom_SkipsRefusalsAndEarlierRounds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "crw")
	qa, err := s.CreateSession(ctx, sampleRecord("crw"))
	if err != nil {
		t.Fatalf("create qa: %v", err)
	}
	dev, err := s.CreateSession(ctx, sampleRecord("crw"))
	if err != nil {
		t.Fatalf("create dev: %v", err)
	}
	t0 := time.Now().UTC().Truncate(time.Second)

	if _, ok, err := s.LatestDeliveredCrewMessageFrom(ctx, qa.ID, time.Time{}); err != nil || ok {
		t.Fatalf("before any message: ok=%v err=%v", ok, err)
	}

	for _, m := range []domain.CrewMessage{
		{ID: "m1", From: qa.ID, To: dev.ID, Subject: "1a2b3c4", CreatedAt: t0},
		{ID: "m2", From: qa.ID, To: dev.ID, Subject: "5d6e7f8", CreatedAt: t0.Add(time.Minute)},
		{ID: "m3", From: qa.ID, To: dev.ID, Subject: "9a8b7c6", RefusedReason: "capped", CreatedAt: t0.Add(2 * time.Minute)},
		{ID: "m4", From: dev.ID, To: qa.ID, Subject: "5d6e7f8", CreatedAt: t0.Add(3 * time.Minute)},
	} {
		m.CrewID, m.ProjectID = dev.ID, "crw"
		if err := s.InsertCrewMessage(ctx, m); err != nil {
			t.Fatalf("insert %s: %v", m.ID, err)
		}
	}

	got, ok, err := s.LatestDeliveredCrewMessageFrom(ctx, qa.ID, time.Time{})
	if err != nil || !ok {
		t.Fatalf("latest delivered: ok=%v err=%v", ok, err)
	}
	if got.ID != "m2" || got.Subject != "5d6e7f8" || !got.CreatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("latest delivered = %+v, want m2 (the refusal after it and dev's reply do not count)", got)
	}

	if got, ok, err := s.LatestDeliveredCrewMessageFrom(ctx, qa.ID, t0.Add(time.Minute)); err != nil || !ok || got.ID != "m2" {
		t.Fatalf("since the moment m2 was sent: got=%+v ok=%v err=%v, want m2", got, ok, err)
	}
	if _, ok, err := s.LatestDeliveredCrewMessageFrom(ctx, qa.ID, t0.Add(90*time.Second)); err != nil || ok {
		t.Fatalf("since a round that began after the last delivery: ok=%v err=%v, want none", ok, err)
	}
}
