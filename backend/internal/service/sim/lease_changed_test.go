package sim_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
)

// The XCTest runners follow the lease table; the nudge is what makes a claim
// or a release take effect at once instead of on their next tick. It must
// fire for every change that passes through the service, and only for those.
func TestLeaseChanged_NudgesOnEveryChangeAndNoRefusal(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	var nudges atomic.Int32
	svc, store := newServiceWithOpts(t, fixedClock(now), sim.WithLeaseChanged(func() { nudges.Add(1) }))
	first, second := newSession(t, store, now), newSession(t, store, now)
	ctx := context.Background()

	step := func(what string, want int32, err error) {
		t.Helper()
		if got := nudges.Load(); got != want {
			t.Fatalf("after %s: %d nudges, want %d (err %v)", what, got, want, err)
		}
	}
	_, err := svc.Acquire(ctx, first, udidProMax, 0)
	step("a claim", 1, err)
	_, err = svc.Acquire(ctx, second, udidProMax, 0)
	step("a refused claim", 1, err)
	_, err = svc.TakeOver(ctx, second, udidProMax, 0)
	step("a take-over", 2, err)
	err = svc.Release(ctx, first, udidProMax)
	step("a refused release", 2, err)
	err = svc.Release(ctx, second, udidProMax)
	step("a release", 3, err)
}
