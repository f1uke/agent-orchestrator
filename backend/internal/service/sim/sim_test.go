package sim_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const (
	udidProMax = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"
	udidPro    = "C4764B41-8F74-49C6-8766-A20EA46125BF"
)

// newService builds the service over a real SQLite store: the exclusion this
// service exposes lives in the schema, so a fake store would test nothing.
func newService(t *testing.T, now func() time.Time) (*sim.Service, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", RegisteredAt: now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return sim.New(store, sim.WithClock(now)), store
}

func newSession(t *testing.T, store *sqlite.Store, now time.Time) domain.SessionID {
	t.Helper()
	rec, err := store.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return rec.ID
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestAcquire_DefaultTTLAndHolder(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)

	lease, err := svc.Acquire(context.Background(), owner, udidProMax, 0)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if lease.SessionID != owner {
		t.Fatalf("holder = %q, want %q", lease.SessionID, owner)
	}
	if got := lease.ExpiresAt.Sub(lease.AcquiredAt); got != sim.DefaultTTL {
		t.Fatalf("ttl = %s, want the default %s", got, sim.DefaultTTL)
	}
}

// The udid is the primary key that enforces the exclusion, so a differently
// cased udid must resolve to the same device rather than to a second lease.
func TestAcquire_UDIDCaseDoesNotBypassTheLease(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	first := newSession(t, store, now)
	second := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), first, udidProMax, 0); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	_, err := svc.Acquire(context.Background(), second, strings.ToLower(udidProMax), 0)
	var held *sim.HeldError
	if !errors.As(err, &held) {
		t.Fatalf("lower-cased udid must hit the same lease, got err=%v", err)
	}
	if held.Lease.SessionID != first {
		t.Fatalf("holder = %q, want %q", held.Lease.SessionID, first)
	}
}

func TestAcquire_HeldByAnotherSessionNamesTheHolderAndTimeLeft(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	clock := now
	svc, store := newService(t, func() time.Time { return clock })
	first := newSession(t, store, now)
	second := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), first, udidProMax, 10*time.Minute); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	clock = now.Add(3 * time.Minute)
	_, err := svc.Acquire(context.Background(), second, udidProMax, 0)
	var held *sim.HeldError
	if !errors.As(err, &held) {
		t.Fatalf("second acquire err = %v, want a HeldError", err)
	}
	if held.Lease.SessionID != first {
		t.Fatalf("holder = %q, want %q", held.Lease.SessionID, first)
	}
	// The message has to be actionable on its own: who, and for how much longer.
	if !strings.Contains(held.Error(), string(first)) || !strings.Contains(held.Error(), "7m") {
		t.Fatalf("contention message must name the holder and the time left, got %q", held.Error())
	}
}

func TestAcquire_RenewsForTheHolder(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	clock := now
	svc, store := newService(t, func() time.Time { return clock })
	owner := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 10*time.Minute); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	clock = now.Add(9 * time.Minute)
	renewed, err := svc.Acquire(context.Background(), owner, udidProMax, 10*time.Minute)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !renewed.ExpiresAt.Equal(clock.Add(10 * time.Minute)) {
		t.Fatalf("renewed expiry = %s, want %s", renewed.ExpiresAt, clock.Add(10*time.Minute))
	}
	leases, err := svc.List(context.Background())
	if err != nil || len(leases) != 1 {
		t.Fatalf("renew must not create a second lease: %+v err=%v", leases, err)
	}
}

func TestAcquire_ExpiredLeaseIsTakenOverAndDroppedFromList(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	clock := now
	svc, store := newService(t, func() time.Time { return clock })
	first := newSession(t, store, now)
	second := newSession(t, store, now)

	if _, err := svc.Acquire(context.Background(), first, udidProMax, time.Minute); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	clock = now.Add(90 * time.Second)
	// Expiry is computed on read: no sweeper ever ran, yet the lease is gone.
	if leases, err := svc.List(context.Background()); err != nil || len(leases) != 0 {
		t.Fatalf("expired lease must not be listed: %+v err=%v", leases, err)
	}
	lease, err := svc.Acquire(context.Background(), second, udidProMax, 0)
	if err != nil {
		t.Fatalf("takeover of an expired lease: %v", err)
	}
	if lease.SessionID != second {
		t.Fatalf("owner = %q, want %q", lease.SessionID, second)
	}
}

func TestAcquire_TTLBounds(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)

	// A gesture-length hold is a first-class case: the tap slice needs to be
	// able to hold a device for seconds, not minutes.
	lease, err := svc.Acquire(context.Background(), owner, udidProMax, 2*time.Second)
	if err != nil {
		t.Fatalf("short ttl: %v", err)
	}
	if got := lease.ExpiresAt.Sub(lease.AcquiredAt); got != 2*time.Second {
		t.Fatalf("ttl = %s, want 2s", got)
	}
	if _, err := svc.Acquire(context.Background(), owner, udidPro, sim.MaxTTL+time.Second); !errors.Is(err, sim.ErrInvalid) {
		t.Fatalf("ttl above the cap must be rejected, got %v", err)
	}
	if _, err := svc.Acquire(context.Background(), owner, udidPro, -time.Second); !errors.Is(err, sim.ErrInvalid) {
		t.Fatalf("negative ttl must be rejected, got %v", err)
	}
}

func TestAcquire_UnknownOrEndedSessionCannotHoldADevice(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	ctx := context.Background()

	if _, err := svc.Acquire(ctx, "no-such-session", udidProMax, 0); !errors.Is(err, sim.ErrNotFound) {
		t.Fatalf("unknown session must not take a lease, got %v", err)
	}

	// A lease is only as alive as its owner: an ended session taking one would
	// poison the device with a holder that can never release it.
	owner := newSession(t, store, now)
	rec, _, _ := store.GetSession(ctx, owner)
	rec.IsTerminated = true
	if err := store.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if _, err := svc.Acquire(ctx, owner, udidProMax, 0); !errors.Is(err, sim.ErrInvalid) {
		t.Fatalf("ended session must not take a lease, got %v", err)
	}
}

func TestAcquire_EmptyUDIDIsRejected(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, "  ", 0); !errors.Is(err, sim.ErrInvalid) {
		t.Fatalf("empty udid must be rejected, got %v", err)
	}
}

func TestRelease_HolderReleasesAndNonHolderIsRefused(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	ctx := context.Background()
	owner := newSession(t, store, now)
	other := newSession(t, store, now)

	if _, err := svc.Acquire(ctx, owner, udidProMax, 0); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	err := svc.Release(ctx, other, udidProMax)
	var held *sim.HeldError
	if !errors.As(err, &held) || held.Lease.SessionID != owner {
		t.Fatalf("a non-holder release must be refused and name the holder, got %v", err)
	}
	if leases, _ := svc.List(ctx); len(leases) != 1 {
		t.Fatalf("the lease must survive a non-holder release: %+v", leases)
	}

	if err := svc.Release(ctx, owner, strings.ToLower(udidProMax)); err != nil {
		t.Fatalf("holder release: %v", err)
	}
	if leases, _ := svc.List(ctx); len(leases) != 0 {
		t.Fatalf("lease must be gone: %+v", leases)
	}
	// Releasing what nobody holds is a plain not-found, not a silent success.
	if err := svc.Release(ctx, owner, udidProMax); !errors.Is(err, sim.ErrNotFound) {
		t.Fatalf("release with no lease = %v, want ErrNotFound", err)
	}
}

// Ending a session releases its devices. The service does not do this itself:
// it is a property of the schema, which is why it holds no matter which code
// path ended the session.
func TestLease_ReleasedWhenTheOwningSessionEnds(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	ctx := context.Background()
	owner := newSession(t, store, now)
	next := newSession(t, store, now)

	if _, err := svc.Acquire(ctx, owner, udidProMax, time.Hour); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	rec, _, _ := store.GetSession(ctx, owner)
	rec.IsTerminated = true
	if err := store.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	if leases, _ := svc.List(ctx); len(leases) != 0 {
		t.Fatalf("ending the owner must release its device: %+v", leases)
	}
	if _, err := svc.Acquire(ctx, next, udidProMax, 0); err != nil {
		t.Fatalf("device must be claimable after its holder ended: %v", err)
	}
}
