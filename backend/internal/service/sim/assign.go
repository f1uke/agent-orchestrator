package sim

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// The device-assignment half of this package. An assignment answers a question
// the lease never did: not "may I drive this device right now" but "which
// device is MINE".
//
// That distinction is the whole reason this exists. The lease already refused
// to share a device and the refusal worked - but nothing told an agent which
// device it was supposed to be using, so with a single simulator booted every
// session reached for that one, including the one its crewmate was verifying
// on. Telling an agent to remember whose device is whose is not a fix; putting
// the udid in its environment is, because it survives the agent forgetting.
//
// Like the rest of this package, nothing here shells out to simctl: the device
// listing arrives through DeviceLister, which the daemon satisfies with the
// same cached listing the Device tab already polls.

// assignAttempts bounds the pick-then-insert retry. Each attempt loses only to
// a *simultaneous* spawn taking the exact device this one chose, so a third
// attempt is already a machine that has run out of devices rather than one that
// is contended.
const assignAttempts = 3

// AssignmentStore is the persistence surface assignment needs. *sqlite.Store
// satisfies it.
type AssignmentStore interface {
	AssignSimDevice(ctx context.Context, assignment domain.SimDeviceAssignment) (domain.SimDeviceAssignment, bool, error)
	GetSimDeviceAssignment(ctx context.Context, sessionID domain.SessionID) (domain.SimDeviceAssignment, bool, error)
	ListSimDeviceAssignments(ctx context.Context) ([]domain.SimDeviceAssignment, error)
	ReleaseSimDeviceAssignment(ctx context.Context, sessionID domain.SessionID) (bool, error)
	ListSimLeases(ctx context.Context, now time.Time) ([]domain.SimLease, error)
}

// DeviceLister is what this machine has. The daemon satisfies it with
// internal/simstream's resident screen, whose listing is cached and refreshed
// on demand - so assigning a device at spawn costs a map lookup rather than a
// subprocess.
type DeviceLister interface {
	Devices(ctx context.Context) (simctl.Listing, error)
}

// Assigner hands out one simulator per session.
type Assigner struct {
	store   AssignmentStore
	devices DeviceLister
	clock   func() time.Time
}

// NewAssigner builds an Assigner. devices may be nil, which is the ordinary
// configuration everywhere there are no simulators to hand out (Linux, tests):
// every session then gets no device and behaves exactly as it did before
// assignments existed.
func NewAssigner(store AssignmentStore, devices DeviceLister, clock func() time.Time) *Assigner {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Assigner{store: store, devices: devices, clock: clock}
}

// AssignDevice returns the device this session owns, giving it one if it has
// none.
//
// A session that gets nothing is a normal outcome, not an error: this machine
// has no simulators, or every one of them already belongs to somebody. It is
// reported as an empty udid so the caller exports no variable and every `ao
// sim` command falls back to the rule it has always used. Failing a spawn
// because a simulator could not be reserved would be a far worse trade than
// the ambiguity this feature removes.
func (a *Assigner) AssignDevice(ctx context.Context, sessionID domain.SessionID) (domain.SimDeviceAssignment, error) {
	if a == nil || a.store == nil {
		return domain.SimDeviceAssignment{}, nil
	}
	// The session's own device first, and without touching the machine: a
	// restored session must come back to the device its earlier runtime used,
	// and a spawn on a machine whose simulators are all spoken for should not
	// pay for a device listing to be told so twice.
	if held, ok, err := a.store.GetSimDeviceAssignment(ctx, sessionID); err != nil {
		return domain.SimDeviceAssignment{}, err
	} else if ok {
		return held, nil
	}
	if a.devices == nil {
		return domain.SimDeviceAssignment{}, nil
	}
	listing, err := a.devices.Devices(ctx)
	if err != nil {
		// No simulators readable is not a spawn failure. It is the ordinary
		// state of a machine without Xcode.
		//nolint:nilerr // intentional: an unreadable machine has no device to give, and that is an answer
		return domain.SimDeviceAssignment{}, nil
	}
	for attempt := 0; attempt < assignAttempts; attempt++ {
		spoken, err := a.spokenFor(ctx, sessionID)
		if err != nil {
			return domain.SimDeviceAssignment{}, err
		}
		udid := chooseSimDevice(listing.Devices, spoken)
		if udid == "" {
			return domain.SimDeviceAssignment{}, nil
		}
		held, taken, err := a.store.AssignSimDevice(ctx, domain.SimDeviceAssignment{
			SessionID:  sessionID,
			UDID:       udid,
			AssignedAt: a.clock().UTC(),
		})
		if err != nil {
			return domain.SimDeviceAssignment{}, err
		}
		if taken || held.UDID != "" {
			return held, nil
		}
		// Somebody took this device between the read and the insert. Look
		// again rather than reporting a machine as full when it is not.
	}
	return domain.SimDeviceAssignment{}, nil
}

// ReleaseDevice hands a session's device back. Ordinary session ends do not
// need it - the sim_device_assignment trigger releases on termination, exactly
// as it does for a lease - so this is for the paths that reach for it directly.
func (a *Assigner) ReleaseDevice(ctx context.Context, sessionID domain.SessionID) error {
	if a == nil || a.store == nil {
		return nil
	}
	_, err := a.store.ReleaseSimDeviceAssignment(ctx, sessionID)
	return err
}

// spokenFor is every udid this session may not take: one another session has
// been assigned, or one another session is driving right now. The second is not
// redundant. An assignment is durable and a lease is not, so a device somebody
// is mid-gesture on but has never been assigned is exactly the device a naive
// pick would hand to a new session - which is the collision this whole change
// exists to end.
func (a *Assigner) spokenFor(ctx context.Context, sessionID domain.SessionID) (map[string]bool, error) {
	spoken := map[string]bool{}
	assignments, err := a.store.ListSimDeviceAssignments(ctx)
	if err != nil {
		return nil, err
	}
	for _, assignment := range assignments {
		if assignment.SessionID != sessionID {
			spoken[domain.NormalizeSimUDID(assignment.UDID)] = true
		}
	}
	leases, err := a.store.ListSimLeases(ctx, a.clock().UTC())
	if err != nil {
		return nil, err
	}
	for _, lease := range leases {
		if lease.SessionID != sessionID {
			spoken[domain.NormalizeSimUDID(lease.UDID)] = true
		}
	}
	return spoken, nil
}

// chooseSimDevice picks this session's device out of what the machine has.
//
// Booted devices come first because a member that gets one can work at once,
// where a member that gets a shut-down device pays a multi-gigabyte boot before
// it can do anything - and `ao sim boot` stops at two booted simulators, which
// is a budget worth spending on the sessions that actually reach for a device.
// Within each group simctl's own order is kept, so the choice is stable across
// spawns rather than shuffling with every listing.
func chooseSimDevice(devices []simctl.Device, spokenFor map[string]bool) string {
	free := func(d simctl.Device) bool {
		return d.Available && !spokenFor[domain.NormalizeSimUDID(d.UDID)]
	}
	for _, d := range devices {
		if free(d) && d.Booted() {
			return domain.NormalizeSimUDID(d.UDID)
		}
	}
	for _, d := range devices {
		if free(d) {
			return domain.NormalizeSimUDID(d.UDID)
		}
	}
	return ""
}
