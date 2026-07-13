package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSetSessionTokenUsage_RoundTrips(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A fresh session has no telemetry yet.
	if got, _, _ := s.GetSession(ctx, rec.ID); got.TokensUpdatedAt != (time.Time{}) || got.TokenUsage != (domain.TokenUsage{}) {
		t.Fatalf("fresh session has telemetry: usage=%+v at=%v", got.TokenUsage, got.TokensUpdatedAt)
	}

	usage := domain.TokenUsage{Input: 82010, CacheCreation: 2525549, CacheRead: 152740511, Output: 998731, Turns: 602}
	parsedAt := time.Now().UTC().Truncate(time.Second)
	ok, err := s.SetSessionTokenUsage(ctx, rec.ID, usage, parsedAt)
	if err != nil {
		t.Fatalf("set token usage: %v", err)
	}
	if !ok {
		t.Fatal("SetSessionTokenUsage reported no row updated")
	}

	got, found, err := s.GetSession(ctx, rec.ID)
	if err != nil || !found {
		t.Fatalf("get: err=%v found=%v", err, found)
	}
	if got.TokenUsage != usage {
		t.Fatalf("usage = %+v, want %+v", got.TokenUsage, usage)
	}
	if !got.TokensUpdatedAt.Equal(parsedAt) {
		t.Fatalf("tokensUpdatedAt = %v, want %v", got.TokensUpdatedAt, parsedAt)
	}
	// The telemetry write must NOT bump updated_at (it would re-sort the Done bar).
	if !got.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Fatalf("updated_at changed by a telemetry write: got %v, want %v", got.UpdatedAt, rec.UpdatedAt)
	}
}

func TestSetSessionTokenUsage_UnknownSessionReturnsFalse(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	ok, err := s.SetSessionTokenUsage(ctx, "nope", domain.TokenUsage{Input: 1}, time.Now())
	if err != nil {
		t.Fatalf("set token usage: %v", err)
	}
	if ok {
		t.Fatal("ok = true for an unknown session, want false")
	}
}
