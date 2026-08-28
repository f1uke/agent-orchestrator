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
// device. Booting one is a separate act with its own command (`ao sim boot`),
// and a device somebody booted is no more theirs than any other.
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

// SimRecording is one open-or-closed capture of the gestures an AO session
// performs on one device, kept so a later task can emit them as a Maestro UI
// test flow. Like a lease, it is scoped by udid - one device carries at most
// one recording - and starting one requires the caller to already hold a live
// lease on that device: a recording without a lease behind it could not have
// produced any gestures to capture.
//
// StoppedAt is nil while the recording is open. Stopping never deletes the
// row or its steps: a flow is emitted from them after the fact, so both must
// outlive the recording being stopped.
type SimRecording struct {
	UDID      string     `json:"udid"`
	SessionID SessionID  `json:"sessionId"`
	Name      string     `json:"name"`
	StartedAt time.Time  `json:"startedAt"`
	StoppedAt *time.Time `json:"stoppedAt,omitempty"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// SimRecordingStep is one captured gesture or observation within a
// SimRecording, numbered from 1 in the order it was appended. The fields
// carry everything a later emitter needs to translate the step into a
// Maestro command: what kind of action it was, how it targeted the screen (a
// selector, which "rung" of the selector strategy matched, and whether that
// match was ambiguous), where on screen it happened, and free-form detail for
// steps a selector cannot describe.
type SimRecordingStep struct {
	Seq int64     `json:"seq"`
	At  time.Time `json:"at"`
	// Kind names the action: e.g. "tap", "swipe", "type", "wait".
	Kind string `json:"kind"`
	// Selector identifies the element the step targeted, when one could be
	// resolved. SelectorRung records which selector strategy produced it (a
	// coarser rung means a weaker match), and Ambiguity>0 means more than one
	// element on screen matched it. SelectorIndex is which of those Ambiguity
	// matches this step resolved to, in tree order (0 when there is no
	// ambiguity) - without it, re-emitting a flow from this step would always
	// address the FIRST element sharing the selector, even when a later one is
	// the one that was actually tapped.
	Selector      string `json:"selector,omitempty"`
	SelectorRung  int64  `json:"selectorRung,omitempty"`
	SelectorIndex int64  `json:"selectorIndex,omitempty"`
	// SelectorAnchor and SelectorAnchorRel pin an ambiguous selector without
	// an index: "the one element matching Selector that lies <rel> the element
	// labelled <anchor>". They are preferred over SelectorIndex when set,
	// because an index is counted in the tree this step was recorded from
	// while the runner counts its own - measured on a real app, that lands on
	// a different element 14% of the time and the flow still passes.
	SelectorAnchor    string `json:"selectorAnchor,omitempty"`
	SelectorAnchorRel string `json:"selectorAnchorRel,omitempty"`
	Ambiguity         int64  `json:"ambiguity,omitempty"`
	// OffScreen: the step's target was outside the visible viewport.
	OffScreen bool `json:"offScreen,omitempty"`
	// ScreenChange: this step caused a screen transition, so an emitted flow
	// may need to wait for the new screen before continuing.
	ScreenChange bool `json:"screenChange,omitempty"`
	// X/Y is where the step began; ToX/ToY is where it ended (equal to X/Y for
	// a tap, the far end of the gesture for a swipe).
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	ToX        float64 `json:"toX"`
	ToY        float64 `json:"toY"`
	DurationMS int64   `json:"durationMs,omitempty"`
	// Text is what was typed, for kind "type".
	Text string `json:"text,omitempty"`
	// Detail is free-form context for steps a selector cannot describe.
	Detail string `json:"detail,omitempty"`
}

// SimRecordingOutcome is what the database decided about a StartSimRecording
// request, and enough context to explain a refusal without a second, racy
// read - the same shape as SimHoldOutcome and for the same reason.
type SimRecordingOutcome struct {
	// Granted: the caller now owns the open recording on this device.
	Granted   bool
	Recording SimRecording
	// Lease/Leased describe the live lease on the device at the time of the
	// decision, so a refusal can name the holder.
	Lease  SimLease
	Leased bool
	// Busy: a recording is already open on this device, so this is "already
	// recording", not "not yours" or "no lease". The three need different
	// advice, so they are reported apart.
	Busy bool
}

// NormalizeSimUDID canonicalizes a simulator udid for storage and comparison.
// simctl reports udids upper-cased but accepts either case, and the udid is the
// primary key that enforces the lease's exclusion - an un-normalized "abc"
// would take a second lease on the device already held as "ABC".
func NormalizeSimUDID(udid string) string {
	return strings.ToUpper(strings.TrimSpace(udid))
}

// SimDeviceAssignment is the device that belongs to one session. It is NOT a
// lease and does not do a lease's job: a lease says who may drive a device for
// the next few minutes and expires; an assignment says which device is yours
// and lasts as long as your session does.
//
// It exists because the lease already refused to share and that was not enough.
// Nothing told an agent which device was supposed to be its own, so with one
// device booted every session reached for that one - including a crewmate's,
// mid-verification. The assignment is exported into the agent's environment at
// spawn (AO_SIM_UDID, AO_SIM_DESTINATION) precisely so that `ao sim` and a raw
// `xcodebuild -destination` land on the same device without the agent having to
// remember anything.
type SimDeviceAssignment struct {
	SessionID  SessionID `json:"sessionId"`
	UDID       string    `json:"udid"`
	AssignedAt time.Time `json:"assignedAt"`
}

// SimDestination renders a udid the way `xcodebuild -destination` wants it, so
// an agent can paste the environment variable straight into a build command.
// Empty in, empty out: a session with no assigned device exports neither var.
func SimDestination(udid string) string {
	udid = NormalizeSimUDID(udid)
	if udid == "" {
		return ""
	}
	return "id=" + udid
}
