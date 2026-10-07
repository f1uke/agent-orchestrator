package sim

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
)

// The boot cap: how many simulators may be up or coming up on this machine at
// once (simpower.Settings), and which of AO's own clones may be shut down to
// stay under it. It is decided here, next to the Fleet, because only the fleet
// knows which devices AO made; the daemon's power route applies it and
// internal/simpower does the shutting down.

// BootSlot is one simulator that counts against the cap.
type BootSlot struct {
	Device simctl.Device
	// ComingUp is a boot still in flight. It counts even though simctl may not
	// say Booted: `simslim on` REBOOTS the device, so for the tens of seconds of
	// a slimming boot the device is not Booted while its several GB are very
	// much allocated. Counting only Booted would show headroom that does not
	// exist, in exactly the dev-and-qa-hold-a-device-each case slimming is for.
	ComingUp bool
	// Elsewhere is the AO daemon running that boot, when it is not this one.
	Elsewhere *domain.SimDaemon
	Base      bool
	// Clone is set when AO made this device for a session.
	Clone *domain.SimClone
	// Lease is the live lease on it, through any daemon on this machine.
	Lease *domain.SimLease
	// BootedAt is when this run of the device started, from CoreSimulator's
	// own stamp (simctl.Device.Boot); zero when it did not say.
	BootedAt time.Time
}

// Idle is a device AO may shut down to make room: its own clone, up, and
// nobody's. A base, a device AO did not make, a leased device, and one still
// coming up are never idle.
func (s BootSlot) Idle() bool {
	return s.Clone != nil && !s.Base && !s.ComingUp && s.Lease == nil && s.Device.Booted()
}

// describe says whose the device is and what it is doing, for a refusal.
func (s BootSlot) describe() string {
	owner := "not AO's"
	switch {
	case s.Clone != nil:
		owner = "AO clone of @" + string(s.Clone.SessionID)
	case s.Base:
		owner = "an AO base"
	}
	var state string
	switch {
	case s.ComingUp && s.Elsewhere != nil:
		state = "still coming up in " + s.Elsewhere.Describe()
	case s.ComingUp:
		state = "still coming up"
	case s.Lease != nil && s.Lease.OtherDaemon != nil:
		state = "leased by @" + string(s.Lease.SessionID) + " through " + s.Lease.OtherDaemon.Describe()
	case s.Lease != nil:
		state = "leased by @" + string(s.Lease.SessionID)
	case s.Idle():
		state = "idle"
	default:
		state = "booted"
	}
	return fmt.Sprintf("%s (%s, %s) - %s, %s", s.Device.Name, s.Device.Runtime, s.Device.UDID, owner, state)
}

// BootCapInput is everything the cap is read from. The daemon gathers it; the
// decision is pure so it can be tested without a machine.
type BootCapInput struct {
	// Max is the cap (simpower.Settings.MaxBooted).
	Max int
	// Target is the device about to be booted. It never counts against itself.
	Target simctl.Device
	// Devices is this machine's simulators.
	Devices []simctl.Device
	// Power is what simpower has in flight, other daemons' boots included.
	Power  map[string]simpower.Status
	Clones []domain.SimClone
	Bases  []BaseStatus
	// Leases must include other daemons' (Service.List does), or a device a
	// sandbox daemon is driving would read as idle.
	Leases []domain.SimLease
}

// BootCap is the cap applied to one boot.
type BootCap struct {
	Max    int
	Target simctl.Device
	// Slots is every device holding the cap, the target excluded.
	Slots []BootSlot
}

// NewBootCap reads which devices hold the cap and whose each one is.
func NewBootCap(in BootCapInput) BootCap {
	clones := map[string]domain.SimClone{}
	for _, c := range in.Clones {
		clones[domain.NormalizeSimUDID(c.UDID)] = c
	}
	bases := map[string]bool{}
	for _, b := range in.Bases {
		if b.UDID != "" {
			bases[domain.NormalizeSimUDID(b.UDID)] = true
		}
	}
	leases := map[string]domain.SimLease{}
	for _, l := range in.Leases {
		leases[domain.NormalizeSimUDID(l.UDID)] = l
	}
	target := domain.NormalizeSimUDID(in.Target.UDID)

	out := BootCap{Max: in.Max, Target: in.Target}
	for _, d := range in.Devices {
		key := domain.NormalizeSimUDID(d.UDID)
		if key == target {
			continue
		}
		status := in.Power[key]
		comingUp := status.Op == simpower.Boot && status.State == simpower.Running
		if !d.Booted() && !comingUp {
			continue
		}
		slot := BootSlot{Device: d, ComingUp: comingUp, Base: bases[key], BootedAt: bootedAt(d)}
		if comingUp {
			slot.Elsewhere = status.OtherDaemon
		}
		if c, ok := clones[key]; ok {
			slot.Clone = &c
		}
		if l, ok := leases[key]; ok {
			slot.Lease = &l
		}
		out.Slots = append(out.Slots, slot)
	}
	return out
}

// bootedAt is the least-recently-used signal, and it is the boot time because
// that is the only activity time AO can actually observe for every device. The
// lease table does not keep history - a release deletes the row and the next
// claim overwrites it - so "when was this last driven" is not recorded
// anywhere. CoreSimulator's stamp (lastBootedAt, lastUsedAt on Xcode 26.3) is
// written once per boot, so least recently used here means least recently
// booted.
func bootedAt(d simctl.Device) time.Time {
	stamp, err := d.Boot()
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Excess is how many slots must be freed before one more boot fits.
func (c BootCap) Excess() int {
	if n := len(c.Slots) - c.Max + 1; n > 0 {
		return n
	}
	return 0
}

// Idle is the slots that may be shut down to make room, least recently booted
// first. A device with no boot stamp sorts last: AO evicts on evidence first.
func (c BootCap) Idle() []BootSlot {
	var idle []BootSlot
	for _, s := range c.Slots {
		if s.Idle() {
			idle = append(idle, s)
		}
	}
	sort.SliceStable(idle, func(i, j int) bool {
		a, b := idle[i].BootedAt, idle[j].BootedAt
		switch {
		case a.IsZero() != b.IsZero():
			return b.IsZero()
		case !a.Equal(b):
			return a.Before(b)
		default:
			return idle[i].Device.UDID < idle[j].Device.UDID
		}
	})
	return idle
}

// Refusal is the error for a boot that does not fit.
func (c BootCap) Refusal() *BootCapError {
	return &BootCapError{Max: c.Max, Target: c.Target, Slots: c.Slots}
}

// MakeRoom frees the slots one more boot needs by shutting down idle clones,
// least recently booted first, and returns the ones it shut down.
//
// shutdown returning a *HeldError (claimed after the cap was read) or
// simpower.ErrBusy (somebody else is already powering it) means the clone is no
// longer idle, and the next one is tried. Any other error stops here. With too few idle clones nothing is shut down at all - a boot
// that would still be refused must not cost anybody a device.
func (c BootCap) MakeRoom(shutdown func(BootSlot) error) ([]BootSlot, error) {
	need := c.Excess()
	if need == 0 {
		return nil, nil
	}
	idle := c.Idle()
	if len(idle) < need {
		return nil, c.Refusal()
	}
	var freed []BootSlot
	for _, slot := range idle {
		if len(freed) == need {
			break
		}
		var held *HeldError
		switch err := shutdown(slot); {
		case errors.As(err, &held), errors.Is(err, simpower.ErrBusy):
			continue
		case err != nil:
			return freed, err
		}
		freed = append(freed, slot)
	}
	if len(freed) < need {
		return freed, c.Refusal()
	}
	return freed, nil
}

// BootCapError is a boot refused because the cap is reached and not enough of
// what holds it is AO's to shut down.
type BootCapError struct {
	Max    int
	Target simctl.Device
	Slots  []BootSlot
}

func (e *BootCapError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s was not booted: %d simulators are already up or coming up and this machine's boot cap is %d. "+
		"Each is a virtual machine of several GB, and too many at once runs the machine out of memory. Holding the cap:",
		e.Target.Name, len(e.Slots), e.Max)
	idle := 0
	for _, s := range e.Slots {
		fmt.Fprintf(&b, "\n  %s", s.describe())
		if s.Idle() {
			idle++
		}
	}
	if idle == 0 {
		b.WriteString("\nNone of them is an idle AO clone, so none can be shut down to make room.")
	}
	b.WriteString("\nShut one down from the desktop app's Device tab, or raise the cap (maxBooted in PUT /api/v1/settings/sim-boot).")
	return b.String()
}
