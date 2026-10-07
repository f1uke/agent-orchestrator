package domain

import (
	"fmt"
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
//
// A device is one machine-wide resource, so a lease is too: every AO daemon on
// the machine - the human's own and any sandbox daemon a worker runs from its
// branch with its own AO_DATA_DIR - sees and honours every other daemon's
// leases (internal/simowner). OtherDaemon is set on a lease that was taken
// through a daemon other than the one being asked.
type SimLease struct {
	UDID       string    `json:"udid"`
	SessionID  SessionID `json:"sessionId"`
	AcquiredAt time.Time `json:"acquiredAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// OtherDaemon names the AO daemon the lease was taken through when that is
	// not this one. Its SessionID belongs to THAT daemon's sessions, so it can
	// share an id with a session here and still be somebody else.
	OtherDaemon *SimDaemon `json:"otherDaemon,omitempty"`
}

// Live reports whether the lease still holds the device at now. Expiry is
// evaluated on read - there is no sweeper and no background watcher.
func (l SimLease) Live(now time.Time) bool { return l.ExpiresAt.After(now) }

// HeldElsewhere reports whether the lease belongs to another AO daemon.
func (l SimLease) HeldElsewhere() bool { return l.OtherDaemon != nil }

// SimDaemon is one AO daemon on this machine, as another daemon sees it: the
// data dir is its identity (one daemon per data dir), the pid is how its
// liveness is checked and the port is how a person reaches it.
type SimDaemon struct {
	DataDir string `json:"dataDir" description:"The other daemon's AO_DATA_DIR - its identity on this machine."`
	PID     int    `json:"pid" description:"The other daemon's process id. Its leases end when that process does."`
	Port    int    `json:"port,omitempty" description:"The port the other daemon serves on (its AO_PORT)."`
}

// Describe names the daemon the way a refusal or a listing says it.
func (d SimDaemon) Describe() string {
	if d.Port > 0 {
		return fmt.Sprintf("the AO daemon on port %d (data dir %s, pid %d)", d.Port, d.DataDir, d.PID)
	}
	return fmt.Sprintf("the AO daemon with data dir %s (pid %d)", d.DataDir, d.PID)
}

// SimBoot is a simulator boot another AO daemon on this machine has in flight.
// It is what lets the boot cap count across daemons: simctl already reports
// every Booted device machine-wide, but a boot that is still coming up - and a
// slimming boot spends tens of seconds rebooting, not Booted while its memory
// is very much allocated - is known only to the daemon running it.
type SimBoot struct {
	UDID      string
	Phase     string
	StartedAt time.Time
	Daemon    SimDaemon
}

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
	// Text is what was typed, for kind "type" - except into a secure field,
	// whose text is never kept (Secure).
	Text string `json:"text,omitempty"`
	// Secure: a "type" step that went into a secure field. Its Text is empty
	// on purpose and its Selector names the field, which the flow
	// long-presses to paste instead of typing.
	Secure bool `json:"secure,omitempty"`
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

// SimBase is one of the simulators AO clones a session's devices from. A base
// is a template and never a work device: it is never handed to a session,
// leased fresh or booted through AO, because anything that ran on it - an
// installed app, a keychain entry, a login - would leak into every clone made
// after it.
//
// A base is found by its simctl NAME, which is what a person creates it with
// and sees in Xcode. DeviceType is what `xcrun simctl create` needs to make a
// missing one.
type SimBase struct {
	// Key is the label a clone of this base gets when the session names none.
	Key        string
	Name       string
	DeviceType string
}

// SimBases is every base AO clones from. The first is the default: the device
// every iOS worker is given at spawn. The others are for checking a layout at
// another size, and a session asks for them by model.
var SimBases = []SimBase{
	{Key: "iphone-17-pro-max", Name: "iPhone 17 Pro Max", DeviceType: "com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro-Max"},
	{Key: "iphone-se", Name: "iPhone SE (3rd generation)", DeviceType: "com.apple.CoreSimulator.SimDeviceType.iPhone-SE-3rd-generation"},
	{Key: "ipad-pro-11", Name: "iPad Pro 11-inch (M5)", DeviceType: "com.apple.CoreSimulator.SimDeviceType.iPad-Pro-11-inch-M5-12GB"},
}

// DefaultSimBase is the base a session's own device ($AO_SIM_UDID) is cloned
// from.
func DefaultSimBase() SimBase { return SimBases[0] }

// SimBaseNamed is the base with this exact simctl name.
func SimBaseNamed(name string) (SimBase, bool) {
	for _, base := range SimBases {
		if base.Name == name {
			return base, true
		}
	}
	return SimBase{}, false
}

// MatchSimBase resolves what an agent typed after --model to a base: the exact
// name ignoring case, else the one base whose name starts with it, so "iPhone
// SE" and "iPad Pro 11-inch" are enough. Empty means the default. Anything that
// names no base, or more than one, is refused with the names it could have
// meant.
func MatchSimBase(model string) (SimBase, error) {
	want := strings.ToLower(strings.TrimSpace(model))
	if want == "" {
		return DefaultSimBase(), nil
	}
	var matches []SimBase
	for _, base := range SimBases {
		name := strings.ToLower(base.Name)
		if name == want {
			return base, nil
		}
		if strings.HasPrefix(name, want) {
			matches = append(matches, base)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return SimBase{}, fmt.Errorf("%q names no simulator model AO clones; the models are %s", model, simBaseList())
	default:
		return SimBase{}, fmt.Errorf("%q names more than one simulator model; the models are %s", model, simBaseList())
	}
}

func simBaseList() string {
	names := make([]string, 0, len(SimBases))
	for _, base := range SimBases {
		names = append(names, fmt.Sprintf("%q", base.Name))
	}
	return strings.Join(names, ", ")
}

// SimPrimaryLabel is the label of a session's primary device: the clone of
// DefaultSimBase made at spawn and exported as AO_SIM_UDID / AO_SIM_DESTINATION.
const SimPrimaryLabel = "primary"

// SimClone is a simulator AO made for one session by cloning a base, and the
// only kind of device AO ever deletes.
//
// It outlives nothing: when its session ends AO deletes the device and then
// this row. The row is what proves AO made the device, so a device with no row
// - a human's, another daemon's - is never touched.
//
// A session holds one primary clone and any number of extra ones, each under a
// label unique within the session: an SE for a layout check, a second iPhone
// for the other side of a chat. Commands address an extra device by its label.
type SimClone struct {
	UDID      string    `json:"udid"`
	SessionID SessionID `json:"sessionId"`
	Label     string    `json:"label"`
	// Base is the name of the base it was cloned from.
	Base string `json:"base"`
	// Name is the clone's own simctl name, which says whose it is.
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// Primary reports whether this is the session's AO_SIM_UDID device.
func (c SimClone) Primary() bool { return c.Label == SimPrimaryLabel }

// SimCloneName is what a clone is called on this machine: enough for a person
// looking at Xcode's device list to tell whose it is and what it is a copy of.
func SimCloneName(sessionID SessionID, label string, base SimBase) string {
	if label == SimPrimaryLabel {
		return fmt.Sprintf("AO %s (%s)", sessionID, base.Name)
	}
	return fmt.Sprintf("AO %s %s (%s)", sessionID, label, base.Name)
}

// ParseSimLabel validates a label an agent typed. Labels end up in device
// names and on command lines, so they are kept to what needs no quoting.
func ParseSimLabel(raw string) (string, error) {
	label := strings.ToLower(strings.TrimSpace(raw))
	if label == "" {
		return "", fmt.Errorf("a device label cannot be empty")
	}
	if len(label) > 32 {
		return "", fmt.Errorf("device label %q is longer than 32 characters", raw)
	}
	for _, r := range label {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return "", fmt.Errorf("device label %q may hold only letters, digits, '-' and '_'", raw)
		}
	}
	return label, nil
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
