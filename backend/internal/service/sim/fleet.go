package sim

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simclone"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// The device half of this package: which simulators belong to which session.
//
// Every device a session gets is a clone AO makes for it from a base (see
// domain.SimBases), and AO deletes it when the session ends. That replaced an
// assigner that handed out whatever device was free. It had no notion of model
// and no notion of who needed a device, so orchestrators held iPhones, a dead
// session kept one for days, and an iOS worker was given an iPad. A clone
// cannot run out, cannot be the wrong model, and cannot outlive its owner.
//
// The bases themselves are templates and nothing here hands one out: anything
// that ran on a base would be in every clone made after it.

// CloneStore is the persistence the fleet needs. *sqlite.Store satisfies it.
type CloneStore interface {
	AddSimClone(ctx context.Context, clone domain.SimClone) (domain.SimClone, bool, error)
	GetSimClone(ctx context.Context, sessionID domain.SessionID, label string) (domain.SimClone, bool, error)
	ListSimClones(ctx context.Context) ([]domain.SimClone, error)
	ListOrphanSimClones(ctx context.Context) ([]domain.SimClone, error)
	DeleteSimClone(ctx context.Context, udid string) (bool, error)
	ListSimLeases(ctx context.Context, now time.Time) ([]domain.SimLease, error)
	ReleaseSimLease(ctx context.Context, udid string, sessionID domain.SessionID) (bool, error)
}

// Machine is the simulators this machine has, and the one door that makes and
// removes them. The daemon satisfies it with internal/simstream's Screen, whose
// cached listing the Device tab already polls.
type Machine interface {
	Devices(ctx context.Context) (simctl.Listing, error)
	FreshDevices(ctx context.Context) (simctl.Listing, error)
	CloneDevice(ctx context.Context, sourceUDID, name string) (string, error)
	DeleteDevice(ctx context.Context, udid string) error
}

// BaseMissingError is a base this machine does not have. It is never answered
// with some other device: a fallback is how an iOS worker ended up on an iPad.
type BaseMissingError struct {
	Base    domain.SimBase
	Runtime string
}

func (e *BaseMissingError) Error() string {
	runtime := e.Runtime
	if runtime == "" {
		runtime = "<an iOS runtime from `xcrun simctl list runtimes`>"
	}
	return fmt.Sprintf("the base simulator %q is missing, so AO cannot clone a device from it. Create it once with:\n  xcrun simctl create %q %s %s",
		e.Base.Name, e.Base.Name, e.Base.DeviceType, runtime)
}

// BaseBootedError is a base somebody started. CoreSimulator cannot clone a
// running device, and a base that ran anything is no longer a clean template.
type BaseBootedError struct {
	Base domain.SimBase
	UDID string
}

func (e *BaseBootedError) Error() string {
	return fmt.Sprintf("the base simulator %q (%s) is booted, and a booted device cannot be cloned. A base is a template, not a work device: ask the human to shut it down",
		e.Base.Name, e.UDID)
}

// ErrPrimaryNotRemovable: a session's primary device goes when the session
// does, because $AO_SIM_UDID in its environment names it.
var ErrPrimaryNotRemovable = errors.New("the primary device is deleted when the session ends, not before")

// ErrBaseDevice: the device is one of the bases, which are never driven.
var ErrBaseDevice = errors.New("that simulator is a base AO clones devices from, and is never driven")

// Fleet makes, finds and deletes sessions' clones.
type Fleet struct {
	store   CloneStore
	machine Machine
	clock   func() time.Time

	// locks serialises the work on one session's devices, so two calls for one
	// label make one clone. Different sessions clone in parallel.
	mu    sync.Mutex
	locks map[domain.SessionID]*sessionLock
}

type sessionLock struct {
	sync.Mutex
	waiters int
}

// NewFleet builds a Fleet. machine may be nil, which is the ordinary
// configuration where there are no simulators (Linux, tests): every session
// then gets no device.
func NewFleet(store CloneStore, machine Machine, clock func() time.Time) *Fleet {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Fleet{store: store, machine: machine, clock: clock, locks: map[domain.SessionID]*sessionLock{}}
}

// Primary returns the session's primary device, cloning it on first use. A
// restored session gets the clone it already had.
func (f *Fleet) Primary(ctx context.Context, sessionID domain.SessionID) (domain.SimClone, error) {
	return f.Ensure(ctx, sessionID, domain.SimPrimaryLabel, "")
}

// Ensure returns the session's device under label, cloning model's base for it
// when there is none. model empty means "whatever it already is", or the
// default base for a new device. Naming a model that differs from the existing
// device's is refused rather than silently ignored.
func (f *Fleet) Ensure(ctx context.Context, sessionID domain.SessionID, label, model string) (domain.SimClone, error) {
	if f == nil || f.machine == nil {
		return domain.SimClone{}, simctl.ErrUnavailable
	}
	var base domain.SimBase
	if model != "" || label == domain.SimPrimaryLabel {
		matched, err := domain.MatchSimBase(model)
		if err != nil {
			return domain.SimClone{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		base = matched
	}
	unlock := f.lock(sessionID)
	defer unlock()

	listing, err := f.machine.Devices(ctx)
	if err != nil {
		return domain.SimClone{}, err
	}
	held, ok, err := f.store.GetSimClone(ctx, sessionID, label)
	if err != nil {
		return domain.SimClone{}, err
	}
	if ok {
		if base.Name != "" && base.Name != held.Base {
			return domain.SimClone{}, fmt.Errorf("%w: device %q is already a %s; give the %s another --label", ErrInvalid, label, held.Base, base.Name)
		}
		if usable(listing.Devices, held.UDID) {
			return held, nil
		}
		// The cache may predate the clone; only a fresh read may call it gone.
		if fresh, err := f.machine.FreshDevices(ctx); err == nil && usable(fresh.Devices, held.UDID) {
			return held, nil
		}
		// Gone, or left unusable (its runtime was removed): replace it.
		if err := f.delete(ctx, held); err != nil {
			return domain.SimClone{}, err
		}
		if base.Name == "" {
			base, _ = domain.SimBaseNamed(held.Base)
		}
	}
	if base.Name == "" {
		base = domain.DefaultSimBase()
	}
	source, err := findBase(listing.Devices, base)
	if err != nil {
		return domain.SimClone{}, err
	}
	udid, err := f.machine.CloneDevice(ctx, source.UDID, domain.SimCloneName(sessionID, label, base))
	if errors.Is(err, simclone.ErrBooted) {
		return domain.SimClone{}, &BaseBootedError{Base: base, UDID: source.UDID}
	}
	if err != nil {
		return domain.SimClone{}, err
	}
	clone, added, err := f.store.AddSimClone(ctx, domain.SimClone{
		UDID:      udid,
		SessionID: sessionID,
		Label:     label,
		Base:      base.Name,
		Name:      domain.SimCloneName(sessionID, label, base),
		CreatedAt: f.clock().UTC(),
	})
	if err != nil || !added {
		// Nobody will own the device just made, so it must not outlive this
		// call: an unrecorded clone is one AO can never prove it made.
		if delErr := f.machine.DeleteDevice(context.WithoutCancel(ctx), udid); delErr != nil {
			err = errors.Join(err, delErr)
		}
	}
	if err != nil {
		return domain.SimClone{}, err
	}
	return clone, nil
}

// Remove deletes one of a session's extra devices now, rather than when the
// session ends.
func (f *Fleet) Remove(ctx context.Context, sessionID domain.SessionID, label string) (domain.SimClone, error) {
	if f == nil || f.machine == nil {
		return domain.SimClone{}, simctl.ErrUnavailable
	}
	if label == domain.SimPrimaryLabel {
		return domain.SimClone{}, fmt.Errorf("%w: %w", ErrInvalid, ErrPrimaryNotRemovable)
	}
	unlock := f.lock(sessionID)
	defer unlock()
	held, ok, err := f.store.GetSimClone(ctx, sessionID, label)
	if err != nil {
		return domain.SimClone{}, err
	}
	if !ok {
		return domain.SimClone{}, fmt.Errorf("%w: this session has no device labelled %q", ErrNotFound, label)
	}
	leases, err := f.store.ListSimLeases(ctx, f.clock().UTC())
	if err != nil {
		return domain.SimClone{}, err
	}
	for _, lease := range leases {
		if domain.NormalizeSimUDID(lease.UDID) == held.UDID && lease.SessionID != sessionID {
			return domain.SimClone{}, &HeldError{Lease: lease, Now: f.clock().UTC()}
		}
	}
	if err := f.delete(ctx, held); err != nil {
		return domain.SimClone{}, err
	}
	return held, nil
}

// Clones is every clone AO holds.
func (f *Fleet) Clones(ctx context.Context) ([]domain.SimClone, error) {
	if f == nil || f.store == nil {
		return nil, nil
	}
	return f.store.ListSimClones(ctx)
}

// IsBase reports whether udid is one of the bases on this machine.
func (f *Fleet) IsBase(ctx context.Context, udid string) (bool, error) {
	if f == nil || f.machine == nil {
		return false, nil
	}
	listing, err := f.machine.Devices(ctx)
	if err != nil {
		return false, err
	}
	key := domain.NormalizeSimUDID(udid)
	for _, base := range domain.SimBases {
		if d, err := findBase(listing.Devices, base); err == nil && domain.NormalizeSimUDID(d.UDID) == key {
			return true, nil
		}
	}
	return false, nil
}

// SweepReport is what one sweep did.
type SweepReport struct {
	// Deleted are clones whose session had ended: device and record gone.
	Deleted []domain.SimClone
	// Forgotten are records whose device no longer exists.
	Forgotten []domain.SimClone
	// Kept are ended sessions' clones another live session is driving.
	Kept []domain.SimClone
}

// Sweep converges the machine on the records: every clone of an ended session
// is deleted, and every record of a device that no longer exists is dropped.
// It runs at daemon start and periodically, and is safe to run any number of
// times. It never touches a device without a record.
func (f *Fleet) Sweep(ctx context.Context) (SweepReport, error) {
	if f == nil || f.machine == nil {
		return SweepReport{}, nil
	}
	report, sweepErr := f.sweepOrphans(ctx, "")

	listedAt := f.clock().UTC()
	listing, err := f.machine.FreshDevices(ctx)
	if err != nil {
		return report, errors.Join(sweepErr, err)
	}
	present := map[string]bool{}
	for _, d := range listing.Devices {
		present[domain.NormalizeSimUDID(d.UDID)] = true
	}
	clones, err := f.store.ListSimClones(ctx)
	if err != nil {
		return report, errors.Join(sweepErr, err)
	}
	errs := []error{sweepErr}
	for _, clone := range clones {
		// A clone recorded after the listing was read is missing from it
		// because it is new, not because it is gone.
		if present[clone.UDID] || !clone.CreatedAt.Before(listedAt) {
			continue
		}
		if _, err := f.store.DeleteSimClone(ctx, clone.UDID); err != nil {
			errs = append(errs, err)
			continue
		}
		report.Forgotten = append(report.Forgotten, clone)
	}
	return report, errors.Join(errs...)
}

// SweepSession deletes one ended session's clones, the moment it ends rather
// than at the next periodic sweep. A session that is not ended - because it was
// restored in the meantime - keeps its devices.
func (f *Fleet) SweepSession(ctx context.Context, sessionID domain.SessionID) (SweepReport, error) {
	if f == nil || f.machine == nil {
		return SweepReport{}, nil
	}
	return f.sweepOrphans(ctx, sessionID)
}

// sweepOrphans deletes the clones of ended sessions; only of sessionID's when
// it is set. Each session is handled under its lock and its orphans re-read
// there, so a session restored between the read and the delete is never left
// holding a device that was deleted under it.
func (f *Fleet) sweepOrphans(ctx context.Context, only domain.SessionID) (SweepReport, error) {
	var report SweepReport
	orphans, err := f.store.ListOrphanSimClones(ctx)
	if err != nil {
		return report, err
	}
	sessions := []domain.SessionID{}
	seen := map[domain.SessionID]bool{}
	for _, clone := range orphans {
		if (only == "" || clone.SessionID == only) && !seen[clone.SessionID] {
			seen[clone.SessionID] = true
			sessions = append(sessions, clone.SessionID)
		}
	}
	var errs []error
	for _, id := range sessions {
		if err := f.sweepSessionLocked(ctx, id, &report); err != nil {
			errs = append(errs, err)
		}
	}
	return report, errors.Join(errs...)
}

func (f *Fleet) sweepSessionLocked(ctx context.Context, id domain.SessionID, report *SweepReport) error {
	unlock := f.lock(id)
	defer unlock()
	orphans, err := f.store.ListOrphanSimClones(ctx)
	if err != nil {
		return err
	}
	leases, err := f.store.ListSimLeases(ctx, f.clock().UTC())
	if err != nil {
		return err
	}
	driving := map[string]domain.SessionID{}
	for _, lease := range leases {
		driving[domain.NormalizeSimUDID(lease.UDID)] = lease.SessionID
	}
	var errs []error
	for _, clone := range orphans {
		if clone.SessionID != id {
			continue
		}
		// The owner's own lease was released when it ended; a lease by anybody
		// else is a live session using the device, which is not disturbed.
		if holder, ok := driving[clone.UDID]; ok && holder != clone.SessionID {
			report.Kept = append(report.Kept, clone)
			continue
		}
		if err := f.delete(ctx, clone); err != nil {
			errs = append(errs, err)
			continue
		}
		report.Deleted = append(report.Deleted, clone)
	}
	return errors.Join(errs...)
}

// delete removes a clone's device, then its record, then the owner's lease on
// it. The device goes first: a record without a device is dropped by the next
// sweep, a device without a record is one AO could never prove it made.
func (f *Fleet) delete(ctx context.Context, clone domain.SimClone) error {
	if err := f.machine.DeleteDevice(ctx, clone.UDID); err != nil {
		return err
	}
	return f.forget(ctx, clone)
}

func (f *Fleet) forget(ctx context.Context, clone domain.SimClone) error {
	if _, err := f.store.DeleteSimClone(ctx, clone.UDID); err != nil {
		return err
	}
	_, err := f.store.ReleaseSimLease(ctx, clone.UDID, clone.SessionID)
	return err
}

func (f *Fleet) lock(id domain.SessionID) func() {
	f.mu.Lock()
	l := f.locks[id]
	if l == nil {
		l = &sessionLock{}
		f.locks[id] = l
	}
	l.waiters++
	f.mu.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		f.mu.Lock()
		l.waiters--
		if l.waiters == 0 {
			delete(f.locks, id)
		}
		f.mu.Unlock()
	}
}

func usable(devices []simctl.Device, udid string) bool {
	for _, d := range devices {
		if domain.NormalizeSimUDID(d.UDID) == domain.NormalizeSimUDID(udid) {
			return d.Available
		}
	}
	return false
}

// findBase is the base device on this machine: an available device with the
// base's exact name, on the newest runtime when there are several.
func findBase(devices []simctl.Device, base domain.SimBase) (simctl.Device, error) {
	var found []simctl.Device
	newestIOS := ""
	for _, d := range devices {
		if strings.Contains(d.RuntimeIdentifier, ".iOS-") && runtimeNewer(d.RuntimeIdentifier, newestIOS) {
			newestIOS = d.RuntimeIdentifier
		}
		if d.Available && d.Name == base.Name {
			found = append(found, d)
		}
	}
	if len(found) == 0 {
		return simctl.Device{}, &BaseMissingError{Base: base, Runtime: newestIOS}
	}
	sort.SliceStable(found, func(i, j int) bool {
		return runtimeNewer(found[i].RuntimeIdentifier, found[j].RuntimeIdentifier)
	})
	return found[0], nil
}

// runtimeNewer compares runtime identifiers such as
// com.apple.CoreSimulator.SimRuntime.iOS-26-3 by their version numbers.
func runtimeNewer(a, b string) bool {
	va, vb := runtimeVersion(a), runtimeVersion(b)
	for i := 0; i < len(va) || i < len(vb); i++ {
		var x, y int
		if i < len(va) {
			x = va[i]
		}
		if i < len(vb) {
			y = vb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func runtimeVersion(identifier string) []int {
	tail := identifier[strings.LastIndex(identifier, ".")+1:]
	parts := strings.Split(tail, "-")
	var out []int
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}
