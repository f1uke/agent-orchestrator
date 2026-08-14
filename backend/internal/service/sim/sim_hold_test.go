package sim_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
)

func TestAcquireHold_GrantsToTheLeaseHolderWithADefaultTTL(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if hold.Token == "" {
		t.Fatal("a hold without a token could be released by any other command")
	}
	if got := hold.ExpiresAt.Sub(now); got != sim.DefaultHoldTTL {
		t.Fatalf("hold ttl = %s, want the default %s", got, sim.DefaultHoldTTL)
	}
}

func TestAcquireHold_TokensAreNotGuessable(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0)
		if err != nil {
			t.Fatalf("hold %d: %v", i, err)
		}
		if seen[hold.Token] {
			t.Fatalf("hold token %q was handed out twice", hold.Token)
		}
		if len(hold.Token) < 16 {
			t.Fatalf("hold token %q is too short to be unguessable", hold.Token)
		}
		seen[hold.Token] = true
		if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
	}
}

func TestAcquireHold_RefusedWhenAnotherSessionHoldsTheLease(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	holder := newSession(t, store, now)
	other := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), holder, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}

	_, err := svc.AcquireHold(context.Background(), other, udidProMax, 0)
	var refused *sim.HoldRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want a *HoldRefusedError", err)
	}
	if refused.Reason != sim.HoldRefusedLeasedByOther {
		t.Fatalf("reason = %q, want %q", refused.Reason, sim.HoldRefusedLeasedByOther)
	}
	if refused.Lease.SessionID != holder {
		t.Fatalf("the refusal must name the holder, got %+v", refused.Lease)
	}
	if !strings.Contains(err.Error(), string(holder)) {
		t.Fatalf("error %q does not name the holder %q", err, holder)
	}
}

func TestAcquireHold_RefusedWhenNobodyHoldsTheDevice(t *testing.T) {
	// Touching an unclaimed device is refused rather than quietly claiming it:
	// AO cannot see a human driving the same simulator from Xcode, so taking a
	// device nobody asked for would be exactly the silent grab a claim exists
	// to prevent.
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)

	_, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0)
	var refused *sim.HoldRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want a *HoldRefusedError", err)
	}
	if refused.Reason != sim.HoldRefusedNotLeased {
		t.Fatalf("reason = %q, want %q", refused.Reason, sim.HoldRefusedNotLeased)
	}
}

func TestAcquireHold_RefusedWhileTheSameSessionIsMidGesture(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("first hold: %v", err)
	}

	_, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0)
	var refused *sim.HoldRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want a *HoldRefusedError", err)
	}
	if refused.Reason != sim.HoldRefusedBusy {
		t.Fatalf("reason = %q, want %q", refused.Reason, sim.HoldRefusedBusy)
	}
}

func TestAcquireHold_TTLBounds(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}

	for _, ttl := range []time.Duration{sim.MaxHoldTTL + time.Second, -time.Second} {
		if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, ttl); !errors.Is(err, sim.ErrInvalid) {
			t.Fatalf("ttl %s: err = %v, want ErrInvalid", ttl, err)
		}
	}
	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, sim.MaxHoldTTL)
	if err != nil {
		t.Fatalf("max ttl: %v", err)
	}
	if got := hold.ExpiresAt.Sub(now); got != sim.MaxHoldTTL {
		t.Fatalf("ttl = %s, want %s", got, sim.MaxHoldTTL)
	}
}

func TestReleaseHold_UnknownTokenIsNotFound(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("hold: %v", err)
	}

	if err := svc.ReleaseHold(context.Background(), udidProMax, "not-the-live-token"); !errors.Is(err, sim.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestReleaseHold_FreesTheDeviceForTheNextGesture(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	first, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, first.Token); err != nil {
		t.Fatalf("release: %v", err)
	}

	if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("the next gesture must be able to take the finger: %v", err)
	}
}

func TestAcquireHold_UDIDCaseCannotSlipPastTheLease(t *testing.T) {
	// The udid IS the exclusion. A lower-case spelling that acquired its own
	// hold would hand two callers the same finger.
	now := time.Date(2026, 8, 13, 7, 41, 2, 0, time.UTC)
	svc, store := newService(t, fixedClock(now))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("hold: %v", err)
	}

	_, err := svc.AcquireHold(context.Background(), owner, strings.ToLower(udidProMax), 0)
	var refused *sim.HoldRefusedError
	if !errors.As(err, &refused) || refused.Reason != sim.HoldRefusedBusy {
		t.Fatalf("err = %v, want a busy refusal for the same device spelled differently", err)
	}
}
