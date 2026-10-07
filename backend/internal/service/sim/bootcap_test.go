package sim_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// capRig is a machine with AO's clones on it, over a real store: the clone
// records and the leases the cap reads are the same rows production reads.
type capRig struct {
	t       *testing.T
	now     time.Time
	machine *fakeSims
	fleet   *sim.Fleet
	store   *sqlite.Store
	leases  *sim.Service
}

func newCapRig(t *testing.T) *capRig {
	t.Helper()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	return &capRig{t: t, now: now, machine: machine, fleet: fleet, store: store,
		leases: sim.New(store, sim.WithClock(fixedClock(now)))}
}

// clone makes a session and its primary clone, and returns both.
func (r *capRig) clone() (domain.SessionID, string) {
	r.t.Helper()
	id := newSession(r.t, r.store, r.now)
	c, err := r.fleet.Primary(r.t.Context(), id)
	if err != nil {
		r.t.Fatalf("primary: %v", err)
	}
	return id, c.UDID
}

// boot marks a device up, with the boot stamp Xcode 26.3 reports.
func (r *capRig) boot(udid string, at time.Time) {
	for i := range r.machine.devices {
		if r.machine.devices[i].UDID == udid {
			r.machine.devices[i].State = "Booted"
			r.machine.devices[i].LastUsedAt = at.Format(time.RFC3339)
			return
		}
	}
	r.t.Fatalf("no device %s", udid)
}

func (r *capRig) lease(id domain.SessionID, udid string) {
	r.t.Helper()
	if _, err := r.leases.Acquire(r.t.Context(), id, udid, 0); err != nil {
		r.t.Fatalf("lease: %v", err)
	}
}

func (r *capRig) cap(maxBooted int, target string, power map[string]simpower.Status, extra ...domain.SimLease) sim.BootCap {
	r.t.Helper()
	ctx := r.t.Context()
	listing, _ := r.machine.Devices(ctx)
	bases, err := r.fleet.Bases(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	clones, err := r.fleet.Clones(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	leases, err := r.leases.List(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	device, ok := r.machine.device(target)
	if !ok {
		r.t.Fatalf("no target %s", target)
	}
	return sim.NewBootCap(sim.BootCapInput{
		Max: maxBooted, Target: device, Devices: listing.Devices, Power: power,
		Clones: clones, Bases: bases, Leases: append(leases, extra...),
	})
}

// recorder is a shutdown that records what it was asked to take down.
type recorder struct {
	asked []string
	fail  map[string]error
}

func (r *recorder) shutdown(slot sim.BootSlot) error {
	r.asked = append(r.asked, slot.Device.UDID)
	return r.fail[slot.Device.UDID]
}

func TestBootCap_UnderTheCapShutsNothingDown(t *testing.T) {
	rig := newCapRig(t)
	_, up := rig.clone()
	_, target := rig.clone()
	rig.boot(up, rig.now.Add(-time.Hour))

	capacity := rig.cap(2, target, nil)
	var rec recorder
	freed, err := capacity.MakeRoom(rec.shutdown)
	if err != nil || len(freed) != 0 || len(rec.asked) != 0 {
		t.Fatalf("freed %v, asked %v, err %v; one up under a cap of two needs no room", freed, rec.asked, err)
	}
}

// Least recently used is least recently booted - the only activity time AO can
// observe for every device - and a leased clone is never in the running.
func TestBootCap_MakesRoomFromTheLeastRecentlyBootedIdleClone(t *testing.T) {
	rig := newCapRig(t)
	_, recent := rig.clone()
	_, older := rig.clone()
	holder, oldest := rig.clone()
	_, target := rig.clone()
	rig.boot(recent, rig.now.Add(-1*time.Hour))
	rig.boot(older, rig.now.Add(-2*time.Hour))
	rig.boot(oldest, rig.now.Add(-3*time.Hour))
	rig.lease(holder, oldest)

	var rec recorder
	freed, err := rig.cap(3, target, nil).MakeRoom(rec.shutdown)
	if err != nil {
		t.Fatalf("make room: %v", err)
	}
	if len(rec.asked) != 1 || rec.asked[0] != older {
		t.Fatalf("shut down %v, want only %s: the oldest idle clone, not the leased one booted before it", rec.asked, older)
	}
	if len(freed) != 1 || freed[0].Clone == nil || freed[0].Device.UDID != older {
		t.Fatalf("freed %+v, want the clone that went down reported with its owner", freed)
	}
}

// A lowered cap can leave the machine more than one over. Every slot the boot
// needs is freed, oldest first.
func TestBootCap_FreesAsManyIdleClonesAsTheBootNeeds(t *testing.T) {
	rig := newCapRig(t)
	_, a := rig.clone()
	_, b := rig.clone()
	_, c := rig.clone()
	_, target := rig.clone()
	rig.boot(a, rig.now.Add(-1*time.Hour))
	rig.boot(b, rig.now.Add(-3*time.Hour))
	rig.boot(c, rig.now.Add(-2*time.Hour))

	var rec recorder
	if _, err := rig.cap(2, target, nil).MakeRoom(rec.shutdown); err != nil {
		t.Fatalf("make room: %v", err)
	}
	if strings.Join(rec.asked, ",") != b+","+c {
		t.Fatalf("shut down %v, want %s then %s", rec.asked, b, c)
	}
}

// 🗝 What the cap may never take: a base (a template every clone is made
// from), a device AO did not make, and any device a session is on - including
// a session of another AO daemon on this machine.
func TestBootCap_NeverShutsDownABaseAForeignDeviceOrALeasedClone(t *testing.T) {
	rig := newCapRig(t)
	holder, leased := rig.clone()
	_, sandboxed := rig.clone()
	_, target := rig.clone()
	rig.boot(udidProMax, rig.now.Add(-5*time.Hour)) // the default base
	rig.boot(udidHuman, rig.now.Add(-4*time.Hour))  // somebody's own simulator
	rig.boot(leased, rig.now.Add(-3*time.Hour))
	rig.boot(sandboxed, rig.now.Add(-2*time.Hour))
	rig.lease(holder, leased)
	sandbox := domain.SimLease{
		UDID: sandboxed, SessionID: "sbx-1", AcquiredAt: rig.now, ExpiresAt: rig.now.Add(time.Hour),
		OtherDaemon: &domain.SimDaemon{DataDir: "/tmp/ao-sandbox", PID: 4242, Port: 3399},
	}

	var rec recorder
	freed, err := rig.cap(4, target, nil, sandbox).MakeRoom(rec.shutdown)
	if len(rec.asked) != 0 || len(freed) != 0 {
		t.Fatalf("shut down %v: none of these is AO's idle clone", rec.asked)
	}
	var refused *sim.BootCapError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want the cap's refusal", err)
	}
	msg := refused.Error()
	for _, want := range []string{
		"boot cap is 4",
		"iPhone 17 Pro Max (", "an AO base",
		"UI Test 17 Pro Max (", "not AO's",
		"AO clone of @" + string(holder) + ", leased by @" + string(holder),
		"leased by @sbx-1 through the AO daemon on port 3399",
		"None of them is an idle AO clone",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	for _, udid := range []string{udidProMax, udidHuman, leased, sandboxed} {
		if !strings.Contains(msg, udid) {
			t.Errorf("refusal does not name %s:\n%s", udid, msg)
		}
	}
}

// A device still coming up holds its several GB already, whichever daemon is
// booting it, and is nobody's to shut down mid-boot. The device about to be
// booted never counts against itself.
func TestBootCap_CountsBootsInFlightAndNeverTheTarget(t *testing.T) {
	rig := newCapRig(t)
	_, slimming := rig.clone()
	_, elsewhere := rig.clone()
	_, target := rig.clone()
	power := map[string]simpower.Status{
		slimming: {Op: simpower.Boot, State: simpower.Running, Phase: simpower.PhaseSlimming},
		elsewhere: {Op: simpower.Boot, State: simpower.Running,
			OtherDaemon: &domain.SimDaemon{DataDir: "/tmp/ao-sandbox", PID: 4242, Port: 3399}},
		target: {Op: simpower.Boot, State: simpower.Failed},
	}

	capacity := rig.cap(2, target, power)
	if len(capacity.Slots) != 2 || capacity.Excess() != 1 {
		t.Fatalf("slots %d, excess %d; want both boots in flight counted and the target not", len(capacity.Slots), capacity.Excess())
	}
	var rec recorder
	_, err := capacity.MakeRoom(rec.shutdown)
	if len(rec.asked) != 0 {
		t.Fatalf("shut down %v while it was still coming up", rec.asked)
	}
	if err == nil || !strings.Contains(err.Error(), "still coming up in the AO daemon on port 3399") {
		t.Fatalf("err = %v, want the other daemon's boot named with where it runs", err)
	}
}

// Between reading the cap and shutting a clone down, somebody may claim it, or
// start powering it. Either way it is not idle any more: the next one goes.
func TestBootCap_SkipsACloneThatStoppedBeingIdle(t *testing.T) {
	rig := newCapRig(t)
	_, claimed := rig.clone()
	_, busy := rig.clone()
	_, next := rig.clone()
	_, target := rig.clone()
	rig.boot(claimed, rig.now.Add(-3*time.Hour))
	rig.boot(busy, rig.now.Add(-2*time.Hour))
	rig.boot(next, rig.now.Add(-1*time.Hour))

	rec := recorder{fail: map[string]error{
		claimed: &sim.HeldError{Lease: domain.SimLease{UDID: claimed, SessionID: "mer-7"}, Now: rig.now},
		busy:    simpower.ErrBusy,
	}}
	freed, err := rig.cap(3, target, nil).MakeRoom(rec.shutdown)
	if err != nil {
		t.Fatalf("make room: %v", err)
	}
	if len(freed) != 1 || freed[0].Device.UDID != next {
		t.Fatalf("freed %+v, want %s once the two older ones turned out not to be idle", freed, next)
	}
}

// A boot that would be refused anyway must not cost anybody a device first.
func TestBootCap_ShutsNothingDownWhenTooFewAreIdle(t *testing.T) {
	rig := newCapRig(t)
	_, idle := rig.clone()
	holder, leased := rig.clone()
	_, target := rig.clone()
	rig.boot(idle, rig.now.Add(-2*time.Hour))
	rig.boot(leased, rig.now.Add(-1*time.Hour))
	rig.lease(holder, leased)

	var rec recorder
	_, err := rig.cap(1, target, nil).MakeRoom(rec.shutdown)
	var refused *sim.BootCapError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %v, want the cap's refusal", err)
	}
	if len(rec.asked) != 0 {
		t.Fatalf("shut down %v for a boot that is refused anyway", rec.asked)
	}
	if !strings.Contains(refused.Error(), "idle") || strings.Contains(refused.Error(), "None of them") {
		t.Fatalf("refusal should mark the idle clone as idle:\n%s", refused.Error())
	}
}
