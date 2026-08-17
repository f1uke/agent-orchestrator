package sim

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// Gesture-hold TTL bounds.
//
// A hold is not a working window - the lease is. A hold covers one gesture,
// which takes tens of milliseconds, so its TTL exists for exactly one purpose:
// bounding how long a command that died between begin and end can keep the
// device to itself. Thirty seconds is far longer than any gesture this CLI
// composes (the longest, a swipe, is capped in the seconds) and short enough
// that a killed command's leftovers never outlast the human noticing.
const (
	DefaultHoldTTL = 30 * time.Second
	MinHoldTTL     = time.Second
	MaxHoldTTL     = time.Minute
)

// holdTokenBytes is 128 bits: a token is what proves a release belongs to the
// gesture that took the hold, so it must not be guessable by another command.
const holdTokenBytes = 16

// HoldRefusedReason says which of the hold's preconditions failed. They are
// reported apart because they need different advice: claim the device, wait for
// another session, or wait for the gesture in flight.
type HoldRefusedReason string

// Hold refusal reasons.
const (
	// HoldRefusedNotLeased: no live lease on the device. AO refuses rather than
	// claiming one implicitly - it cannot see whether a human is driving the
	// same simulator from Xcode, so a silent grab is exactly what a claim is
	// there to prevent.
	HoldRefusedNotLeased HoldRefusedReason = "not_leased"
	// HoldRefusedLeasedByOther: another AO session holds the device.
	HoldRefusedLeasedByOther HoldRefusedReason = "leased_by_other"
	// HoldRefusedBusy: a gesture is in flight on this device, possibly one of
	// the caller's own commands. Overlapping it is the failure that merges two
	// gestures into one teleporting finger.
	HoldRefusedBusy HoldRefusedReason = "busy"
)

// HoldRefusedError is a refusal to touch the screen. It carries the lease when
// there is one so a caller never has to answer "held by whom?" with a second,
// racy read.
type HoldRefusedError struct {
	UDID   string
	Reason HoldRefusedReason
	Lease  domain.SimLease
	Now    time.Time
}

func (e *HoldRefusedError) Error() string {
	switch e.Reason {
	case HoldRefusedLeasedByOther:
		return fmt.Sprintf("simulator %s is leased by @%s for another %s, so this session may not touch it",
			e.UDID, e.Lease.SessionID, humanizeDuration(e.Lease.ExpiresAt.Sub(e.Now)))
	case HoldRefusedBusy:
		return fmt.Sprintf("simulator %s is mid-gesture: another command holds the finger right now", e.UDID)
	default:
		return fmt.Sprintf("simulator %s is not claimed by this session, so it may not be touched", e.UDID)
	}
}

// AcquireHold takes the finger on a device for one gesture. It is refused - it
// never waits and never shares - because two overlapping gestures on a device
// with one caller-less finger do not queue, they merge.
//
// intent is what the caller is about to do. It travels here, not with
// ReleaseHold, because this is the only moment the screen still shows the
// gesture's "before" state and the only moment the caller knows what it is
// about to attempt.
func (s *Service) AcquireHold(ctx context.Context, sessionID domain.SessionID, udid string, ttl time.Duration, intent GestureIntent) (domain.SimHold, error) {
	key, err := s.leaseKey(udid)
	if err != nil {
		return domain.SimHold{}, err
	}
	if ttl == 0 {
		ttl = DefaultHoldTTL
	}
	if ttl < MinHoldTTL || ttl > MaxHoldTTL {
		return domain.SimHold{}, fmt.Errorf("%w: hold ttl must be between %s and %s, got %s", ErrInvalid, MinHoldTTL, MaxHoldTTL, ttl)
	}
	token, err := s.newToken()
	if err != nil {
		return domain.SimHold{}, err
	}

	now := s.now()
	outcome, err := s.store.AcquireSimHold(ctx, domain.SimHold{
		UDID:      key,
		SessionID: sessionID,
		Token:     token,
		ExpiresAt: now.Add(ttl),
	}, now)
	if err != nil {
		return domain.SimHold{}, err
	}
	if !outcome.Granted {
		return domain.SimHold{}, &HoldRefusedError{
			UDID:   key,
			Reason: holdRefusedReason(outcome, sessionID),
			Lease:  outcome.Lease,
			Now:    now,
		}
	}
	if s.recorder != nil {
		s.recordIntent(ctx, key, outcome.Hold.Token, intent, outcome.Hold.ExpiresAt)
	}
	return outcome.Hold, nil
}

// GestureOutcome is what a release says about the gesture its hold covered.
//
// Performed says whether that gesture actually happened. It is what turns the
// step AcquireHold stashed into a recorded one - see recording.go: a gesture
// that was attempted and failed must not be written down as if it had
// happened.
//
// End is where the gesture finished, and is set only by a gesture whose end
// was not knowable when the hold was taken - a drag that follows a human's
// finger. Every other gesture is composed in full before it starts, so its end
// travelled with the intent at AcquireHold and there is nothing to correct
// here; those callers leave this nil and the stashed step keeps the end it was
// resolved with. A drag's hold is taken on `drag-begin`, whose request body
// has no end point at all, so without this the recorded step would carry
// ToX/ToY of zero and the emitted flow would read `end: "0%,0%"` - a
// coordinate nobody ever touched.
type GestureOutcome struct {
	Performed bool
	End       *simbridge.Point
}

// ReleaseHold gives the finger back, keeping the lease. A token that no longer
// owns the hold is ErrNotFound rather than a misleading success: the caller has
// to know its gesture was already taken over, because that is the case where a
// finger can have been left down.
func (s *Service) ReleaseHold(ctx context.Context, udid, token string, outcome GestureOutcome) error {
	key, err := s.leaseKey(udid)
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("%w: a hold token is required", ErrInvalid)
	}
	released, err := s.store.ReleaseSimHold(ctx, key, token, s.now())
	if err != nil {
		return err
	}
	if s.recorder != nil {
		s.finishRecording(ctx, token, released && outcome.Performed, outcome.End)
	}
	if !released {
		return fmt.Errorf("%w: no live hold with that token on simulator %s; it may have lapsed and been taken over", ErrNotFound, key)
	}
	return nil
}

// holdRefusedReason picks the refusal a caller can act on. Busy wins over the
// lease state: if a gesture is in flight, waiting for it is the advice whether
// or not the caller owns the device.
func holdRefusedReason(outcome domain.SimHoldOutcome, caller domain.SessionID) HoldRefusedReason {
	switch {
	case outcome.Busy:
		return HoldRefusedBusy
	case outcome.Leased && outcome.Lease.SessionID != caller:
		return HoldRefusedLeasedByOther
	default:
		return HoldRefusedNotLeased
	}
}

func (s *Service) newToken() (string, error) {
	if s.tokens != nil {
		return s.tokens(), nil
	}
	buf := make([]byte, holdTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate hold token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
