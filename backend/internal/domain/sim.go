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

// NormalizeSimUDID canonicalizes a simulator udid for storage and comparison.
// simctl reports udids upper-cased but accepts either case, and the udid is the
// primary key that enforces the lease's exclusion - an un-normalized "abc"
// would take a second lease on the device already held as "ABC".
func NormalizeSimUDID(udid string) string {
	return strings.ToUpper(strings.TrimSpace(udid))
}
