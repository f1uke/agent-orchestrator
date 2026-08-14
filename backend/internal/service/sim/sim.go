// Package sim is the daemon's device-lease service for local iOS Simulators.
// A lease is bookkeeping about a device, never an operation on one: nothing
// here boots, shuts down, reboots or erases a simulator, and the daemon never
// shells out to simctl (device discovery stays in the `ao sim` CLI, which is
// the only part that must run on a macOS box with Xcode).
//
// The exclusion itself is not implemented here. It is the sim_lease schema:
// udid is the primary key and acquire is a single conditional upsert, so two
// simultaneous callers resolve to exactly one winner without this service
// holding a lock. Likewise a lease is released when its owning session ends by
// a trigger on sessions.is_terminated, so it holds for every path that ends a
// session - including ones that never call this package.
package sim

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrInvalid and ErrNotFound are the service sentinels the HTTP controller maps
// to 422 and 404 respectively. A HeldError maps to 409.
var (
	ErrInvalid  = errors.New("sim: invalid request")
	ErrNotFound = errors.New("sim: not found")
)

// TTL bounds.
//
// DefaultTTL is 10 minutes. The common way a holder dies - its session ends -
// releases the device immediately, so the TTL only has to cover a session that
// is alive but wedged, or an agent that forgot to release. Ten minutes is long
// enough that a normal drive-the-app loop (read the screen, decide, act, read
// again) can never expire mid-gesture, which is the failure that leaves a stuck
// finger on the device, and short enough that a forgotten lease hands the
// machine's one simulator back within a coffee break.
//
// MinTTL is one second, on purpose: holding a device for the length of a single
// gesture is a first-class case, because a gesture (begin..end) is the smallest
// unit that must not interleave.
const (
	DefaultTTL = 10 * time.Minute
	MinTTL     = time.Second
	MaxTTL     = time.Hour
)

// maxUDIDLen bounds the key we store. Real simulator udids are 36-character
// UUIDs; this only stops an unbounded string from becoming a row.
const maxUDIDLen = 64

// HeldError says the device is held by someone else. It carries the holder so
// callers never have to answer "held by whom?" with a second, racy read.
type HeldError struct {
	Lease domain.SimLease
	Now   time.Time
	// MidGesture: the refusal is a touch in flight rather than the lease
	// itself. It says what to do about it - wait a moment - where "leased by
	// somebody" says wait for them to finish with the device.
	MidGesture bool
}

func (e *HeldError) Error() string {
	if e.MidGesture {
		return fmt.Sprintf("simulator %s has a gesture in flight from @%s: retry in a moment",
			e.Lease.UDID, e.Lease.SessionID)
	}
	return fmt.Sprintf("simulator %s is leased by @%s for another %s",
		e.Lease.UDID, e.Lease.SessionID, humanizeDuration(e.Lease.ExpiresAt.Sub(e.Now)))
}

// humanizeDuration renders the time left the way a person reads it ("7m12s"),
// never as a negative or sub-second value.
func humanizeDuration(d time.Duration) string {
	if d < time.Second {
		return "less than a second"
	}
	return d.Round(time.Second).String()
}

// Manager is the lease surface the HTTP controller depends on.
type Manager interface {
	Acquire(ctx context.Context, sessionID domain.SessionID, udid string, ttl time.Duration) (domain.SimLease, error)
	// TakeOver claims a device another session holds. It leaves a gesture that
	// is in flight alone; see the method for why that is the one part of the
	// arbitration a human may not override.
	TakeOver(ctx context.Context, sessionID domain.SessionID, udid string, ttl time.Duration) (domain.SimLease, error)
	Release(ctx context.Context, sessionID domain.SessionID, udid string) error
	List(ctx context.Context) ([]domain.SimLease, error)
	// AcquireHold/ReleaseHold bracket one gesture. See hold.go: the lease says
	// which session may drive the device, the hold says whether a gesture is in
	// flight, and both are needed because one session can run two commands.
	AcquireHold(ctx context.Context, sessionID domain.SessionID, udid string, ttl time.Duration) (domain.SimHold, error)
	ReleaseHold(ctx context.Context, udid, token string) error
}

// Store is the persistence surface the service owns; *sqlite.Store satisfies it.
type Store interface {
	AcquireSimLease(ctx context.Context, lease domain.SimLease) (domain.SimLease, bool, error)
	TakeOverSimLease(ctx context.Context, lease domain.SimLease) (domain.SimLease, bool, error)
	ReleaseSimLease(ctx context.Context, udid string, sessionID domain.SessionID) (bool, error)
	GetSimLease(ctx context.Context, udid string, now time.Time) (domain.SimLease, bool, error)
	ListSimLeases(ctx context.Context, now time.Time) ([]domain.SimLease, error)
	AcquireSimHold(ctx context.Context, hold domain.SimHold, now time.Time) (domain.SimHoldOutcome, error)
	ReleaseSimHold(ctx context.Context, udid, token string, now time.Time) (bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Service is the concrete Manager.
type Service struct {
	store  Store
	clock  func() time.Time
	tokens func() string
}

// Option customizes a Service.
type Option func(*Service)

// WithClock overrides the service clock for tests.
func WithClock(clock func() time.Time) Option {
	return func(s *Service) { s.clock = clock }
}

// WithTokenSource overrides hold-token generation for tests.
func WithTokenSource(tokens func() string) Option {
	return func(s *Service) { s.tokens = tokens }
}

// New builds the lease service over a store.
func New(store Store, opts ...Option) *Service {
	s := &Service{store: store, clock: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Acquire claims a device for a session, or renews the session's own lease. A
// device already held by a different live lease is refused with a *HeldError
// naming the holder - never granted, and never silently shared.
func (s *Service) Acquire(ctx context.Context, sessionID domain.SessionID, udid string, ttl time.Duration) (domain.SimLease, error) {
	key, err := s.leaseKey(udid)
	if err != nil {
		return domain.SimLease{}, err
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return domain.SimLease{}, fmt.Errorf("%w: ttl must be between %s and %s, got %s", ErrInvalid, MinTTL, MaxTTL, ttl)
	}
	if err := s.requireLiveSession(ctx, sessionID); err != nil {
		return domain.SimLease{}, err
	}

	now := s.now()
	holder, granted, err := s.store.AcquireSimLease(ctx, domain.SimLease{
		UDID:       key,
		SessionID:  sessionID,
		AcquiredAt: now,
		ExpiresAt:  now.Add(ttl),
	})
	if err != nil {
		return domain.SimLease{}, err
	}
	if !granted {
		return domain.SimLease{}, &HeldError{Lease: holder, Now: now}
	}
	return holder, nil
}

// TakeOver claims a device another session holds.
//
// The lease says who is driving; a human deciding that is now them is a
// legitimate thing to want, and refusing it made the pane feel locked to
// whichever worker got there first. What is NOT negotiable is a touch that is
// happening: a gesture in flight - a tap, or a drag with the finger still down
// - is left alone, and this is refused until it finishes, which takes seconds.
// The previous holder finds out the ordinary way: its next gesture is refused
// because it no longer holds the lease, and the desktop pane switches its own
// driving off within a poll.
func (s *Service) TakeOver(ctx context.Context, sessionID domain.SessionID, udid string, ttl time.Duration) (domain.SimLease, error) {
	key, err := s.leaseKey(udid)
	if err != nil {
		return domain.SimLease{}, err
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return domain.SimLease{}, fmt.Errorf("%w: ttl must be between %s and %s, got %s", ErrInvalid, MinTTL, MaxTTL, ttl)
	}
	if err := s.requireLiveSession(ctx, sessionID); err != nil {
		return domain.SimLease{}, err
	}

	now := s.now()
	holder, granted, err := s.store.TakeOverSimLease(ctx, domain.SimLease{
		UDID:       key,
		SessionID:  sessionID,
		AcquiredAt: now,
		ExpiresAt:  now.Add(ttl),
	})
	if err != nil {
		return domain.SimLease{}, err
	}
	if !granted {
		return domain.SimLease{}, &HeldError{Lease: holder, Now: now, MidGesture: true}
	}
	return holder, nil
}

// Release drops the session's own lease. Releasing a device held by someone
// else is refused with a *HeldError rather than stealing it, and releasing one
// nobody holds is ErrNotFound rather than a misleading success.
func (s *Service) Release(ctx context.Context, sessionID domain.SessionID, udid string) error {
	key, err := s.leaseKey(udid)
	if err != nil {
		return err
	}
	released, err := s.store.ReleaseSimLease(ctx, key, sessionID)
	if err != nil {
		return err
	}
	if released {
		return nil
	}
	now := s.now()
	holder, ok, err := s.store.GetSimLease(ctx, key, now)
	if err != nil {
		return err
	}
	if ok {
		return &HeldError{Lease: holder, Now: now}
	}
	return fmt.Errorf("%w: no lease on simulator %s to release", ErrNotFound, key)
}

// List returns every lease still live now.
func (s *Service) List(ctx context.Context) ([]domain.SimLease, error) {
	return s.store.ListSimLeases(ctx, s.now())
}

// leaseKey validates and canonicalizes the device key. Normalization matters
// for correctness, not tidiness: the udid IS the exclusion, so "abc" and "ABC"
// resolving differently would hand out two leases on one device.
func (s *Service) leaseKey(udid string) (string, error) {
	key := domain.NormalizeSimUDID(udid)
	if key == "" {
		return "", fmt.Errorf("%w: a simulator udid is required", ErrInvalid)
	}
	if len(key) > maxUDIDLen {
		return "", fmt.Errorf("%w: simulator udid is longer than %d characters", ErrInvalid, maxUDIDLen)
	}
	return key, nil
}

// requireLiveSession refuses a lease for a session that cannot release it. The
// schema releases a lease when its owner ends, but that reacts to the moment a
// session ends - an already-ended session must not be able to take one after
// the fact.
func (s *Service) requireLiveSession(ctx context.Context, sessionID domain.SessionID) error {
	rec, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: session %s does not exist", ErrNotFound, sessionID)
	}
	if rec.IsTerminated {
		return fmt.Errorf("%w: session %s has ended and cannot hold a simulator", ErrInvalid, sessionID)
	}
	return nil
}

func (s *Service) now() time.Time { return s.clock().UTC() }
