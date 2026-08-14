package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// newHoldStore is a store with a project to hang sessions off.
func newHoldStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	return s
}

// runConcurrently starts n goroutines that all block on one barrier, so they
// hit the database at the same moment rather than one after another.
func runConcurrently(n int, body func(i int)) {
	var start, done sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			body(i)
		}(i)
	}
	start.Done()
	done.Wait()
}

// A lease keeps other SESSIONS off a device. A hold is the finger itself: it is
// what makes one gesture (begin..end) atomic, including against a second
// command from the session that legitimately holds the lease. These tests are
// about that second thing.

func simHold(udid string, owner domain.SessionID, token string, at time.Time, ttl time.Duration) domain.SimHold {
	return domain.SimHold{
		UDID:      udid,
		SessionID: owner,
		Token:     token,
		ExpiresAt: at.Add(ttl),
	}
}

func TestAcquireSimHold_GrantedToTheLeaseHolder(t *testing.T) {
	ctx := context.Background()
	s := newHoldStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	out, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok-a", now, time.Minute), now)
	if err != nil {
		t.Fatalf("acquire hold: %v", err)
	}
	if !out.Granted {
		t.Fatalf("the lease holder must be able to take the finger, got %+v", out)
	}
	if out.Hold.Token != "tok-a" {
		t.Fatalf("hold token = %q, want tok-a", out.Hold.Token)
	}
}

func TestAcquireSimHold_RefusedWithoutTheLease(t *testing.T) {
	ctx := context.Background()
	s := newHoldStore(t)
	holder := seedSession(t, s, "mer")
	other := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, simLease(testUDID, holder, now, 10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	out, err := s.AcquireSimHold(ctx, simHold(testUDID, other, "tok-b", now, time.Minute), now)
	if err != nil {
		t.Fatalf("acquire hold: %v", err)
	}
	if out.Granted {
		t.Fatal("a session without the lease must never get the finger")
	}
	if !out.Leased || out.Lease.SessionID != holder {
		t.Fatalf("the refusal must name the real lease holder, got %+v", out)
	}
	if out.Busy {
		t.Fatal("nobody was mid-gesture, so the refusal must not blame a hold")
	}
}

func TestAcquireSimHold_RefusedWhileTheSAMESessionIsMidGesture(t *testing.T) {
	// The case a lease alone cannot cover: one session, two concurrent commands,
	// both legitimately holding the lease. Without this, `ao sim tap` and
	// `ao sim swipe` from one agent interleave into a single teleporting finger.
	ctx := context.Background()
	s := newHoldStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if _, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok-first", now, time.Minute), now); err != nil {
		t.Fatalf("first hold: %v", err)
	}

	out, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok-second", now, time.Minute), now)
	if err != nil {
		t.Fatalf("second hold: %v", err)
	}
	if out.Granted {
		t.Fatal("two gestures from one session must not overlap on one device")
	}
	if !out.Busy {
		t.Fatalf("the refusal must say the device is mid-gesture, got %+v", out)
	}
}

func TestAcquireSimHold_TakesOverAHoldThatOutlivedItsCommand(t *testing.T) {
	// A command killed between begin and end must not wedge the device forever.
	ctx := context.Background()
	s := newHoldStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if _, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok-dead", now, time.Minute), now); err != nil {
		t.Fatalf("first hold: %v", err)
	}

	later := now.Add(2 * time.Minute)
	out, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok-next", later, time.Minute), later)
	if err != nil {
		t.Fatalf("acquire hold: %v", err)
	}
	if !out.Granted {
		t.Fatalf("an expired hold must be takeable, got %+v", out)
	}
}

func TestAcquireSimHold_RefusedWhenTheLeaseItselfHasLapsed(t *testing.T) {
	ctx := context.Background()
	s := newHoldStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	later := now.Add(2 * time.Minute)
	out, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok", later, time.Minute), later)
	if err != nil {
		t.Fatalf("acquire hold: %v", err)
	}
	if out.Granted {
		t.Fatal("a lapsed lease must not still let its old owner touch the screen")
	}
	if out.Leased {
		t.Fatalf("a lapsed lease is not a lease, got %+v", out)
	}
}

func TestAcquireSimHold_RefusedWhenNobodyHoldsTheDevice(t *testing.T) {
	ctx := context.Background()
	s := newHoldStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	out, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok", now, time.Minute), now)
	if err != nil {
		t.Fatalf("acquire hold: %v", err)
	}
	if out.Granted {
		t.Fatal("an unclaimed device must not be touchable: claim it first")
	}
	if out.Leased {
		t.Fatal("there is no lease to report")
	}
}

func TestReleaseSimHold_OnlyTheMatchingTokenReleases(t *testing.T) {
	ctx := context.Background()
	s := newHoldStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if _, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok-mine", now, time.Minute), now); err != nil {
		t.Fatalf("hold: %v", err)
	}

	released, err := s.ReleaseSimHold(ctx, testUDID, "tok-someone-else", now)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released {
		t.Fatal("a stale command must not be able to drop the live gesture's hold")
	}

	released, err = s.ReleaseSimHold(ctx, testUDID, "tok-mine", now)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !released {
		t.Fatal("the owner of the hold must be able to give the finger back")
	}
	// And the device is immediately touchable again.
	out, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok-next", now, time.Minute), now)
	if err != nil {
		t.Fatalf("acquire hold: %v", err)
	}
	if !out.Granted {
		t.Fatalf("released hold must free the device, got %+v", out)
	}
}

func TestReleaseSimHold_KeepsTheLease(t *testing.T) {
	// Giving the finger back is not giving the device back.
	ctx := context.Background()
	s := newHoldStore(t)
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if _, err := s.AcquireSimHold(ctx, simHold(testUDID, owner, "tok", now, time.Minute), now); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := s.ReleaseSimHold(ctx, testUDID, "tok", now); err != nil {
		t.Fatalf("release hold: %v", err)
	}

	leases, err := s.ListSimLeases(ctx, now)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(leases) != 1 || leases[0].SessionID != owner {
		t.Fatalf("the lease must survive its gesture, got %+v", leases)
	}
}

func TestAcquireSimHold_ConcurrentHoldsInOneSessionHaveExactlyOneWinner(t *testing.T) {
	// Genuinely concurrent, and deliberately across separate *Store values on
	// one database file: a single Store serializes writes behind its own mutex,
	// which would hide a check-then-act implementation and let this test pass
	// for the wrong reason. Separate stores share no lock, so only the SQL
	// predicate can decide the winner.
	dir := t.TempDir()
	ctx := context.Background()

	const racers = 4
	pool := make([]*sqlite.Store, racers)
	for i := range pool {
		s, err := sqlite.Open(dir)
		if err != nil {
			t.Fatalf("open store %d: %v", i, err)
		}
		t.Cleanup(func() { _ = s.Close() })
		pool[i] = s
	}
	seedProject(t, pool[0], "mer")
	owner := seedSession(t, pool[0], "mer")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := pool[0].AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute)); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	outcomes := make([]domain.SimHoldOutcome, racers)
	errs := make([]error, racers)
	runConcurrently(racers, func(i int) {
		token := "tok-" + string(rune('a'+i))
		outcomes[i], errs[i] = pool[i].AcquireSimHold(ctx, simHold(testUDID, owner, token, now, time.Minute), now)
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}

	winners := 0
	for _, out := range outcomes {
		if out.Granted {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent gestures on one device produced %d winners, want exactly 1", winners)
	}
	for i, out := range outcomes {
		if !out.Granted && !out.Busy {
			t.Fatalf("racer %d lost but was not told the device is mid-gesture: %+v", i, out)
		}
	}
}
