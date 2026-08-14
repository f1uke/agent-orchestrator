package domain

import (
	"strings"
	"time"
)

// SimLeaseState is what AO knows about who is driving a simulator. There is
// deliberately no "free": AO can see its own leases and nothing else, so the
// absence of a lease is honestly reported as unknown rather than as a promise
// that the device is idle.
type SimLeaseState string

// Simulator lease states.
const (
	// SimLeaseHeld: an AO session holds a live lease. We know exactly who and
	// until when.
	SimLeaseHeld SimLeaseState = "held"
	// SimLeaseUnknown: no AO session holds this device, AND AO cannot tell
	// whether something outside AO is driving it - a human in Xcode (which takes
	// its own exclusive lock we cannot see), Simulator.app, or any other tool.
	SimLeaseUnknown SimLeaseState = "unknown"
)

// Why a device reads as unknown. Both surfaces that report lease state - the
// `ao sim` CLI and the desktop app's Simulator tab - say the same sentence,
// because the honesty is the point: "nobody holds it" and "nobody could be
// asked" are both unknown, and printing the wrong one states something AO never
// checked.
const (
	// SimLeaseUnknownReason: AO knows its own leases and nothing else.
	SimLeaseUnknownReason = "no AO session holds this device; AO cannot see whether a human is driving it from Xcode"
	// SimLeaseNoDaemonReason: AO could not even ask.
	SimLeaseNoDaemonReason = "the AO daemon is not reachable, so AO cannot tell who holds this device"
)

// SimLease is one AO session's exclusive claim on one local iOS Simulator, held
// for a bounded time. It is pure bookkeeping: taking a lease never touches the
// device (AO never boots, shuts down, reboots or erases a simulator).
//
// The claim is scoped to an AO session because that is the unit that both dies
// (ending a session releases the device) and drives a device across many
// commands - a gesture, or a whole interaction sequence, is expressible as one
// hold with a caller-chosen TTL.
type SimLease struct {
	UDID       string    `json:"udid"`
	SessionID  SessionID `json:"sessionId"`
	AcquiredAt time.Time `json:"acquiredAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// Live reports whether the lease still holds the device at now. Expiry is
// evaluated on read - there is no sweeper and no background watcher.
func (l SimLease) Live(now time.Time) bool { return l.ExpiresAt.After(now) }

// SimHold is the finger: one caller's exclusive right to inject HID events on
// one device for the length of a single gesture. It is strictly narrower than a
// SimLease and cannot exist without one.
//
// The lease answers "which session may drive this device"; the hold answers "is
// a gesture in flight". Both are needed because the lease's owner is a session,
// and one session can run two commands at once - which on a device with a
// single, caller-less finger merges into one teleporting touch whose first
// release lifts the other's finger.
//
// The TTL is short by design (seconds, not minutes). It is not a working
// window: it is the ceiling on how long a command that died mid-gesture can
// keep the device to itself.
type SimHold struct {
	UDID      string    `json:"udid"`
	SessionID SessionID `json:"sessionId"`
	// Token identifies this hold so a command can only ever release its own -
	// a stale caller must not be able to drop the live gesture's hold.
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Live reports whether the hold still owns the finger at now. Expiry is read at
// query time; nothing sweeps.
func (h SimHold) Live(now time.Time) bool { return h.ExpiresAt.After(now) }

// SimHoldOutcome is what the database decided about a hold request, and enough
// context to explain a refusal without a second, racy read.
type SimHoldOutcome struct {
	// Granted: the caller owns the finger until Hold.ExpiresAt.
	Granted bool
	Hold    SimHold
	// Lease/Leased describe the live lease on the device at the time of the
	// decision, so a refusal can name the holder.
	Lease  SimLease
	Leased bool
	// Busy: a live hold owns the finger, so this is "mid-gesture", not "not
	// yours". The two need different advice, so they are reported apart.
	Busy bool
}

// NormalizeSimUDID canonicalizes a simulator udid for storage and comparison.
// simctl reports udids upper-cased but accepts either case, and the udid is the
// primary key that enforces the lease's exclusion - an un-normalized "abc"
// would take a second lease on the device already held as "ABC".
func NormalizeSimUDID(udid string) string {
	return strings.ToUpper(strings.TrimSpace(udid))
}
