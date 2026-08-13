package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const testUDID = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"

// seedSession creates one live session and returns its id.
func seedSession(t *testing.T, s *sqlite.Store, project string) domain.SessionID {
	t.Helper()
	rec, err := s.CreateSession(context.Background(), sampleRecord(project))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return rec.ID
}

func simLease(udid string, owner domain.SessionID, at time.Time, ttl time.Duration) domain.SimLease {
	return domain.SimLease{
		UDID:       udid,
		SessionID:  owner,
		AcquiredAt: at,
		ExpiresAt:  at.Add(ttl),
	}
}

func TestAcquireSimLease_GrantsAFreeDevice(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	got, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !granted {
		t.Fatalf("acquire on a free device must be granted, got holder %+v", got)
	}
	if got.SessionID != owner || got.UDID != testUDID {
		t.Fatalf("granted lease = %+v, want udid %s owned by %s", got, testUDID, owner)
	}
	if !got.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("expiresAt = %s, want %s", got.ExpiresAt, now.Add(10*time.Minute))
	}
}

func TestAcquireSimLease_SecondSessionIsRefusedAndSeesTheHolder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	first := seedSession(t, s, "mer")
	second := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, first, now, 10*time.Minute)); err != nil || !granted {
		t.Fatalf("first acquire: granted=%v err=%v", granted, err)
	}
	holder, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, second, now.Add(time.Minute), 10*time.Minute))
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if granted {
		t.Fatal("a held device must not be granted to a second session")
	}
	// The refusal must name who holds it - a bare "no" cannot be reported usefully.
	if holder.SessionID != first {
		t.Fatalf("refused acquire returned holder %q, want %q", holder.SessionID, first)
	}
	if !holder.ExpiresAt.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("holder expiresAt = %s, want the FIRST lease's expiry %s", holder.ExpiresAt, now.Add(10*time.Minute))
	}
}

func TestAcquireSimLease_HolderRenewsInPlace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute)); err != nil || !granted {
		t.Fatalf("first acquire: granted=%v err=%v", granted, err)
	}
	later := now.Add(5 * time.Minute)
	got, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, later, 10*time.Minute))
	if err != nil || !granted {
		t.Fatalf("renew by the holder must be granted: granted=%v err=%v", granted, err)
	}
	if !got.ExpiresAt.Equal(later.Add(10 * time.Minute)) {
		t.Fatalf("renewed expiresAt = %s, want %s", got.ExpiresAt, later.Add(10*time.Minute))
	}
	leases, err := s.ListSimLeases(ctx, later)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("renew must not add a row: %d leases", len(leases))
	}
}

func TestAcquireSimLease_ExpiredLeaseIsTakenOver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	first := seedSession(t, s, "mer")
	second := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, first, now, time.Minute)); err != nil || !granted {
		t.Fatalf("first acquire: granted=%v err=%v", granted, err)
	}
	// Exactly at the expiry instant the lease is over: a wedged holder cannot
	// keep a device forever.
	after := now.Add(time.Minute)
	got, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, second, after, 10*time.Minute))
	if err != nil || !granted {
		t.Fatalf("expired lease must be takeable: granted=%v err=%v holder=%+v", granted, err, got)
	}
	if got.SessionID != second {
		t.Fatalf("owner after takeover = %q, want %q", got.SessionID, second)
	}
}

func TestListSimLeases_OmitsExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, time.Minute)); err != nil || !granted {
		t.Fatalf("acquire: granted=%v err=%v", granted, err)
	}
	if leases, err := s.ListSimLeases(ctx, now.Add(30*time.Second)); err != nil || len(leases) != 1 {
		t.Fatalf("live lease must be listed: %d leases, err=%v", len(leases), err)
	}
	// Expiry is computed on read - there is no sweeper, so a stale row must not
	// be reported as a live lease.
	if leases, err := s.ListSimLeases(ctx, now.Add(2*time.Minute)); err != nil || len(leases) != 0 {
		t.Fatalf("expired lease must not be listed: %+v err=%v", leases, err)
	}
}

func TestReleaseSimLease_OnlyTheHolderReleases(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	other := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, 10*time.Minute)); err != nil || !granted {
		t.Fatalf("acquire: granted=%v err=%v", granted, err)
	}
	released, err := s.ReleaseSimLease(ctx, testUDID, other)
	if err != nil {
		t.Fatalf("release by non-holder: %v", err)
	}
	if released {
		t.Fatal("a non-holder must not be able to release someone else's lease")
	}
	if leases, _ := s.ListSimLeases(ctx, now); len(leases) != 1 {
		t.Fatalf("lease must survive a non-holder release: %d leases", len(leases))
	}

	released, err = s.ReleaseSimLease(ctx, testUDID, owner)
	if err != nil || !released {
		t.Fatalf("holder release: released=%v err=%v", released, err)
	}
	if leases, _ := s.ListSimLeases(ctx, now); len(leases) != 0 {
		t.Fatalf("lease must be gone after release: %+v", leases)
	}
}

// A lease that outlives its owner permanently poisons a device, so ending the
// owning session must release it - enforced by the schema (a trigger on
// sessions.is_terminated), not by any caller remembering to do it.
func TestSimLease_ReleasedWhenOwningSessionTerminates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	other := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, time.Hour)); err != nil || !granted {
		t.Fatalf("acquire: granted=%v err=%v", granted, err)
	}

	rec, ok, err := s.GetSession(ctx, owner)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	rec.IsTerminated = true
	rec.UpdatedAt = now
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate session: %v", err)
	}

	if leases, _ := s.ListSimLeases(ctx, now); len(leases) != 0 {
		t.Fatalf("terminating the owner must release its leases, still held: %+v", leases)
	}
	// And the device is genuinely usable again, not merely hidden from the list.
	if _, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, other, now, time.Hour)); err != nil || !granted {
		t.Fatalf("device must be free after its holder ended: granted=%v err=%v", granted, err)
	}
}

// A session that is restored (is_terminated flips back to 0) must not
// resurrect the lease it held before it ended.
func TestSimLease_RestoringTheOwnerDoesNotResurrectTheLease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	owner := seedSession(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	if _, granted, err := s.AcquireSimLease(ctx, simLease(testUDID, owner, now, time.Hour)); err != nil || !granted {
		t.Fatalf("acquire: granted=%v err=%v", granted, err)
	}
	rec, _, _ := s.GetSession(ctx, owner)
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	rec.IsTerminated = false
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if leases, _ := s.ListSimLeases(ctx, now); len(leases) != 0 {
		t.Fatalf("a restored session must not get its old lease back: %+v", leases)
	}
}

// Exclusion is a property of the schema (udid is the primary key, and acquire
// is a single conditional upsert), not of a check-then-act guard in Go. Under
// genuinely simultaneous acquires exactly one caller may win: two winners here
// is the interleaved-gesture bug this whole slice exists to prevent.
func TestAcquireSimLease_ConcurrentAcquiresHaveExactlyOneWinner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	owners, race := simLeaseRacers(t, s, 8)
	holders, results := race(func(i int) (domain.SimLease, bool, error) {
		return s.AcquireSimLease(ctx, simLease(testUDID, owners[i], now, 10*time.Minute))
	})
	assertExactlyOneWinner(t, s, now, holders, results)
}

// The same race, but each racer goes through its OWN *Store over the same
// database file, so no shared Go mutex can serialize them: the only thing left
// standing between two callers is the schema itself. This is the test that
// actually pins the constraint down - the single-Store race above would stay
// green even if acquire were a check-then-act guarded by Store.writeMu.
func TestAcquireSimLease_ConcurrentAcquiresAcrossStoresHaveExactlyOneWinner(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	const stores = 4
	pool := make([]*sqlite.Store, stores)
	for i := range pool {
		s, err := sqlite.Open(dir)
		if err != nil {
			t.Fatalf("open store %d: %v", i, err)
		}
		t.Cleanup(func() { _ = s.Close() })
		pool[i] = s
	}
	seedProject(t, pool[0], "mer")
	now := time.Now().UTC().Truncate(time.Second)

	owners, race := simLeaseRacers(t, pool[0], stores)
	holders, results := race(func(i int) (domain.SimLease, bool, error) {
		return pool[i].AcquireSimLease(ctx, simLease(testUDID, owners[i], now, 10*time.Minute))
	})
	assertExactlyOneWinner(t, pool[0], now, holders, results)
}

// simLeaseRacers seeds n owner sessions and returns a runner that fires acquire
// from n goroutines released together by one barrier, so the calls genuinely
// overlap instead of queueing up behind each other.
func simLeaseRacers(t *testing.T, s *sqlite.Store, n int) ([]domain.SessionID, func(func(int) (domain.SimLease, bool, error)) ([]domain.SimLease, []bool)) {
	t.Helper()
	owners := make([]domain.SessionID, n)
	for i := range owners {
		owners[i] = seedSession(t, s, "mer")
	}
	run := func(acquire func(int) (domain.SimLease, bool, error)) ([]domain.SimLease, []bool) {
		var start, done sync.WaitGroup
		start.Add(1)
		holders := make([]domain.SimLease, n)
		results := make([]bool, n)
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			done.Add(1)
			go func(i int) {
				defer done.Done()
				start.Wait() // every goroutine blocks here until the barrier drops
				holders[i], results[i], errs[i] = acquire(i)
			}(i)
		}
		start.Done()
		done.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("racer %d: %v", i, err)
			}
		}
		return holders, results
	}
	return owners, run
}

func assertExactlyOneWinner(t *testing.T, s *sqlite.Store, now time.Time, holders []domain.SimLease, results []bool) {
	t.Helper()
	winners := 0
	for _, granted := range results {
		if granted {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent acquires produced %d winners, want exactly 1", winners)
	}
	leases, err := s.ListSimLeases(context.Background(), now)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("one device must hold exactly one lease row, got %d: %+v", len(leases), leases)
	}
	// Every loser must have been told the same true holder.
	for i, granted := range results {
		if granted {
			continue
		}
		if holders[i].SessionID != leases[0].SessionID {
			t.Fatalf("racer %d was told holder %q, but the real holder is %q", i, holders[i].SessionID, leases[0].SessionID)
		}
	}
}
