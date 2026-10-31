package chatusage

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

func usage(pct float64) chatproto.RateLimit {
	return chatproto.RateLimit{FiveHour: &chatproto.RateLimitWindow{Utilization: pct, ResetsAt: time.Unix(1793374200, 0).UTC(), Status: "allowed"}}
}

func TestStoreKeepsTheNewestEvent(t *testing.T) {
	var s Store
	if _, ok := s.Latest(); ok {
		t.Fatal("an empty store reports usage")
	}
	t0 := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	s.Record(usage(0.2), t0.Add(time.Minute))
	s.Record(usage(0.1), t0)
	got, ok := s.Latest()
	if !ok || got.FiveHour.Utilization != 0.2 || !got.UpdatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("after an older event from another session: %+v, %v; want the newer 20%%", got, ok)
	}
	s.Record(usage(0.3), t0.Add(2*time.Minute))
	if got, _ := s.Latest(); got.FiveHour.Utilization != 0.3 {
		t.Errorf("a newer event did not replace the old one: %+v", got)
	}
}
