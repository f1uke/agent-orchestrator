package sim_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simclone"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const (
	runtime263 = "com.apple.CoreSimulator.SimRuntime.iOS-26-3"
	udidBaseSE = "2C584C54-1339-436D-8404-31E287C88382"
	udidBaseiP = "567D23AD-A75C-4CD7-AD08-4F9BD932A907"
	udidHuman  = "11111111-2222-3333-4444-555555555555"
)

// fakeSims behaves like simctl: clone copies a shut-down device, delete
// removes one, and a device that is not there deletes as a no-op.
type fakeSims struct {
	devices []simctl.Device
	cloned  int
	deleted []string
}

func newSims() *fakeSims {
	return &fakeSims{devices: []simctl.Device{
		baseDevice(udidProMax, "iPhone 17 Pro Max"),
		baseDevice(udidBaseSE, "iPhone SE (3rd generation)"),
		baseDevice(udidBaseiP, "iPad Pro 11-inch (M5)"),
		baseDevice(udidHuman, "UI Test 17 Pro Max"),
	}}
}

func baseDevice(udid, name string) simctl.Device {
	return simctl.Device{UDID: udid, Name: name, State: "Shutdown", Available: true, RuntimeIdentifier: runtime263}
}

func (m *fakeSims) Devices(context.Context) (simctl.Listing, error) {
	return simctl.Listing{Devices: append([]simctl.Device{}, m.devices...)}, nil
}

func (m *fakeSims) FreshDevices(ctx context.Context) (simctl.Listing, error) { return m.Devices(ctx) }

func (m *fakeSims) CloneDevice(_ context.Context, source, name string) (string, error) {
	for _, d := range m.devices {
		if d.UDID != source {
			continue
		}
		if d.Booted() {
			return "", simclone.ErrBooted
		}
		m.cloned++
		clone := d
		clone.UDID = fmt.Sprintf("C10E0000-0000-0000-0000-%012d", m.cloned)
		clone.Name = name
		m.devices = append(m.devices, clone)
		return clone.UDID, nil
	}
	return "", fmt.Errorf("Invalid device: %s", source)
}

func (m *fakeSims) DeleteDevice(_ context.Context, udid string) error {
	m.deleted = append(m.deleted, udid)
	for i, d := range m.devices {
		if d.UDID == udid {
			m.devices = append(m.devices[:i], m.devices[i+1:]...)
			break
		}
	}
	return nil
}

func (m *fakeSims) device(udid string) (simctl.Device, bool) {
	for _, d := range m.devices {
		if d.UDID == udid {
			return d, true
		}
	}
	return simctl.Device{}, false
}

func newFleet(t *testing.T, now time.Time, machine *fakeSims) (*sim.Fleet, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", RegisteredAt: now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return sim.NewFleet(store, machine, fixedClock(now)), store
}

func terminate(t *testing.T, store *sqlite.Store, id domain.SessionID) {
	t.Helper()
	rec, _, _ := store.GetSession(t.Context(), id)
	rec.IsTerminated = true
	if err := store.UpdateSession(t.Context(), rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}
}

func TestPrimary_EachSessionGetsItsOwnCloneOfTheDefaultBase(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	dev, qa := newSession(t, store, now), newSession(t, store, now)

	devClone, err := fleet.Primary(t.Context(), dev)
	if err != nil {
		t.Fatalf("primary dev: %v", err)
	}
	qaClone, err := fleet.Primary(t.Context(), qa)
	if err != nil {
		t.Fatalf("primary qa: %v", err)
	}
	if devClone.UDID == qaClone.UDID {
		t.Fatalf("both sessions got %s", devClone.UDID)
	}
	for _, c := range []domain.SimClone{devClone, qaClone} {
		if c.UDID == udidProMax || c.UDID == udidHuman {
			t.Fatalf("handed out %s, which AO did not make", c.UDID)
		}
		if c.Base != "iPhone 17 Pro Max" || !c.Primary() {
			t.Fatalf("clone = %+v, want the primary clone of iPhone 17 Pro Max", c)
		}
		d, ok := machine.device(c.UDID)
		if !ok || d.Name != c.Name || !strings.Contains(d.Name, string(c.SessionID)) {
			t.Fatalf("device %s named %q, want it on the machine and naming its session", c.UDID, d.Name)
		}
	}
}

func TestPrimary_RestoredSessionGetsTheCloneItHad(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	id := newSession(t, store, now)

	first, _ := fleet.Primary(t.Context(), id)
	again, err := fleet.Primary(t.Context(), id)
	if err != nil || again.UDID != first.UDID {
		t.Fatalf("second primary = %s (%v), want %s", again.UDID, err, first.UDID)
	}
	if machine.cloned != 1 {
		t.Fatalf("cloned %d devices, want 1", machine.cloned)
	}
}

func TestPrimary_DeviceDeletedBehindAOsBackIsReplaced(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	id := newSession(t, store, now)

	first, _ := fleet.Primary(t.Context(), id)
	_ = machine.DeleteDevice(t.Context(), first.UDID)
	again, err := fleet.Primary(t.Context(), id)
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	if again.UDID == first.UDID {
		t.Fatalf("kept handing out %s, which no longer exists", first.UDID)
	}
	if _, ok := machine.device(again.UDID); !ok {
		t.Fatalf("new primary %s is not on the machine", again.UDID)
	}
}

func TestEnsure_ModelAndLabelMakeExtraDevices(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	id := newSession(t, store, now)
	primary, _ := fleet.Primary(t.Context(), id)

	se, err := fleet.Ensure(t.Context(), id, "small", "iPhone SE")
	if err != nil {
		t.Fatalf("ensure SE: %v", err)
	}
	if se.Base != "iPhone SE (3rd generation)" || se.UDID == primary.UDID || se.Primary() {
		t.Fatalf("SE clone = %+v", se)
	}
	// Two sides of a chat: a second device of the SAME model, under its own label.
	other, err := fleet.Ensure(t.Context(), id, "advisor", "")
	if err != nil {
		t.Fatalf("ensure advisor: %v", err)
	}
	if other.Base != "iPhone 17 Pro Max" || other.UDID == primary.UDID {
		t.Fatalf("advisor clone = %+v, want a second iPhone 17 Pro Max", other)
	}
	if again, _ := fleet.Ensure(t.Context(), id, "small", ""); again.UDID != se.UDID {
		t.Fatalf("label lookup = %s, want %s", again.UDID, se.UDID)
	}
	if _, err := fleet.Ensure(t.Context(), id, "small", "iPad"); !errors.Is(err, sim.ErrInvalid) {
		t.Fatalf("re-labelling an SE as an iPad: err = %v, want ErrInvalid", err)
	}
	if _, err := fleet.Ensure(t.Context(), id, "x", "iPhone"); !errors.Is(err, sim.ErrInvalid) {
		t.Fatalf("an ambiguous model: err = %v, want ErrInvalid", err)
	}
}

func TestEnsure_MissingBaseNamesItAndHowToCreateIt(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	_ = machine.DeleteDevice(t.Context(), udidBaseSE)
	fleet, store := newFleet(t, now, machine)
	id := newSession(t, store, now)

	_, err := fleet.Ensure(t.Context(), id, "small", "iPhone SE")
	var missing *sim.BaseMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want BaseMissingError", err)
	}
	want := `xcrun simctl create "iPhone SE (3rd generation)" com.apple.CoreSimulator.SimDeviceType.iPhone-SE-3rd-generation ` + runtime263
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not say how to create the base (%s)", err, want)
	}
	if machine.cloned != 0 {
		t.Fatalf("cloned %d devices from something else, want no fallback", machine.cloned)
	}
}

func TestEnsure_BootedBaseIsRefusedNotUsed(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	machine.devices[0].State = simctl.BootedState
	fleet, store := newFleet(t, now, machine)

	_, err := fleet.Primary(t.Context(), newSession(t, store, now))
	var booted *sim.BaseBootedError
	if !errors.As(err, &booted) || booted.UDID != udidProMax {
		t.Fatalf("err = %v, want BaseBootedError for the base", err)
	}
}

func TestRemove_DeletesAnExtraDeviceNowButNeverThePrimary(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	id := newSession(t, store, now)
	primary, _ := fleet.Primary(t.Context(), id)
	se, _ := fleet.Ensure(t.Context(), id, "small", "iPhone SE")

	if _, err := fleet.Remove(t.Context(), id, domain.SimPrimaryLabel); !errors.Is(err, sim.ErrInvalid) {
		t.Fatalf("remove primary: err = %v, want ErrInvalid", err)
	}
	if _, err := fleet.Remove(t.Context(), id, "small"); err != nil {
		t.Fatalf("remove small: %v", err)
	}
	if _, ok := machine.device(se.UDID); ok {
		t.Fatalf("SE clone %s still on the machine", se.UDID)
	}
	if _, ok := machine.device(primary.UDID); !ok {
		t.Fatalf("primary %s was deleted with the extra device", primary.UDID)
	}
	if _, ok, _ := store.GetSimClone(t.Context(), id, "small"); ok {
		t.Fatal("record of the removed device is still there")
	}
}

func TestRemove_RefusedWhileAnotherSessionDrivesIt(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	owner, other := newSession(t, store, now), newSession(t, store, now)
	se, _ := fleet.Ensure(t.Context(), owner, "small", "iPhone SE")
	svc := sim.New(store, sim.WithClock(fixedClock(now)))
	if _, err := svc.Acquire(t.Context(), other, se.UDID, 0); err != nil {
		t.Fatalf("other session claims it: %v", err)
	}

	var held *sim.HeldError
	if _, err := fleet.Remove(t.Context(), owner, "small"); !errors.As(err, &held) {
		t.Fatalf("err = %v, want HeldError", err)
	}
	if _, ok := machine.device(se.UDID); !ok {
		t.Fatal("deleted a device another live session was driving")
	}
}

func TestSweep_DeletesEndedSessionsClonesAndNothingElse(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	ended, live := newSession(t, store, now), newSession(t, store, now)
	endedPrimary, _ := fleet.Primary(t.Context(), ended)
	endedSE, _ := fleet.Ensure(t.Context(), ended, "small", "iPhone SE")
	livePrimary, _ := fleet.Primary(t.Context(), live)
	terminate(t, store, ended)

	report, err := fleet.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(report.Deleted) != 2 {
		t.Fatalf("deleted %+v, want the ended session's two clones", report.Deleted)
	}
	for _, udid := range []string{endedPrimary.UDID, endedSE.UDID} {
		if _, ok := machine.device(udid); ok {
			t.Fatalf("ended session's clone %s survived the sweep", udid)
		}
	}
	for _, udid := range []string{livePrimary.UDID, udidProMax, udidBaseSE, udidBaseiP, udidHuman} {
		if _, ok := machine.device(udid); !ok {
			t.Fatalf("sweep deleted %s, which it must never touch", udid)
		}
	}
	// A second sweep finds nothing to do.
	again, err := fleet.Sweep(t.Context())
	if err != nil || len(again.Deleted)+len(again.Forgotten) != 0 {
		t.Fatalf("second sweep = %+v, %v; want a no-op", again, err)
	}
}

func TestSweep_KeepsAnEndedSessionsCloneALiveSessionIsDriving(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	ended, live := newSession(t, store, now), newSession(t, store, now)
	clone, _ := fleet.Primary(t.Context(), ended)
	svc := sim.New(store, sim.WithClock(fixedClock(now)))
	if _, err := svc.Acquire(t.Context(), live, clone.UDID, 0); err != nil {
		t.Fatalf("live session claims it: %v", err)
	}
	terminate(t, store, ended)

	report, _ := fleet.Sweep(t.Context())
	if len(report.Kept) != 1 {
		t.Fatalf("report = %+v, want the driven clone kept", report)
	}
	if _, ok := machine.device(clone.UDID); !ok {
		t.Fatal("deleted a device a live session was driving")
	}
}

func TestSweep_ForgetsRecordsOfDevicesThatAreGone(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	clock := now
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	_ = store.UpsertProject(t.Context(), domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now})
	fleet := sim.NewFleet(store, machine, func() time.Time { return clock })
	id := newSession(t, store, now)
	clone, _ := fleet.Primary(t.Context(), id)
	_ = machine.DeleteDevice(t.Context(), clone.UDID)
	clock = now.Add(time.Minute)

	report, err := fleet.Sweep(t.Context())
	if err != nil || len(report.Forgotten) != 1 {
		t.Fatalf("report = %+v, %v; want the gone device's record forgotten", report, err)
	}
	if _, ok, _ := store.GetSimClone(t.Context(), id, domain.SimPrimaryLabel); ok {
		t.Fatal("record of a device that no longer exists survived the sweep")
	}
}

func TestIsBase(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	fleet, _ := newFleet(t, now, newSims())
	for udid, want := range map[string]bool{udidProMax: true, udidBaseSE: true, udidBaseiP: true, udidHuman: false} {
		if got, err := fleet.IsBase(t.Context(), udid); err != nil || got != want {
			t.Fatalf("IsBase(%s) = %v, %v; want %v", udid, got, err, want)
		}
	}
}

func TestSweepSession_OnlyThatSessionAndOnlyOnceItHasEnded(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	a, b := newSession(t, store, now), newSession(t, store, now)
	aClone, _ := fleet.Primary(t.Context(), a)
	bClone, _ := fleet.Primary(t.Context(), b)

	// Not ended yet (or restored again): nothing is deleted.
	if report, _ := fleet.SweepSession(t.Context(), a); len(report.Deleted) != 0 {
		t.Fatalf("swept a live session's devices: %+v", report)
	}
	terminate(t, store, a)
	terminate(t, store, b)
	report, err := fleet.SweepSession(t.Context(), a)
	if err != nil || len(report.Deleted) != 1 || report.Deleted[0].UDID != aClone.UDID {
		t.Fatalf("report = %+v, %v; want only a's clone", report, err)
	}
	if _, ok := machine.device(bClone.UDID); !ok {
		t.Fatal("sweeping one session deleted another's device")
	}
}

func TestClaim_ModelAloneLabelsTheDeviceAfterItsModel(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	fleet, store := newFleet(t, now, newSims())
	id := newSession(t, store, now)

	se, err := fleet.Claim(t.Context(), id, "", "iPhone SE")
	if err != nil || se.Label != "iphone-se" {
		t.Fatalf("claim SE = %+v, %v; want label iphone-se", se, err)
	}
	primary, err := fleet.Claim(t.Context(), id, "", "")
	if err != nil || !primary.Primary() {
		t.Fatalf("plain claim = %+v, %v; want the primary device", primary, err)
	}
	if _, err := fleet.Claim(t.Context(), id, "has space", ""); !errors.Is(err, sim.ErrInvalid) {
		t.Fatalf("bad label: err = %v, want ErrInvalid", err)
	}
}

func TestClaim_RefusesOrchestratorsAndEndedSessions(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	orch, err := store.CreateSession(t.Context(), domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ended := newSession(t, store, now)
	terminate(t, store, ended)

	for _, id := range []domain.SessionID{orch.ID, ended} {
		if _, err := fleet.Claim(t.Context(), id, "", ""); !errors.Is(err, sim.ErrInvalid) {
			t.Fatalf("claim for %s: err = %v, want ErrInvalid", id, err)
		}
	}
	if machine.cloned != 0 {
		t.Fatalf("cloned %d devices for sessions that must get none", machine.cloned)
	}
}

func TestAcquire_RefusesANewLeaseOnABaseButRenewsAHeldOne(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	machine := newSims()
	fleet, store := newFleet(t, now, machine)
	svc := sim.New(store, sim.WithClock(fixedClock(now)))
	holder, fresh := newSession(t, store, now), newSession(t, store, now)
	// Taken before AO stopped handing out bases.
	if _, err := svc.Acquire(t.Context(), holder, udidBaseSE, 0); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	guarded := sim.New(store, sim.WithClock(fixedClock(now)), sim.WithBaseGuard(fleet.IsBase))

	if _, err := guarded.Acquire(t.Context(), holder, udidBaseSE, 0); err != nil {
		t.Fatalf("renewing a lease a live session already holds: %v", err)
	}
	_, err := guarded.Acquire(t.Context(), fresh, udidProMax, 0)
	if !errors.Is(err, sim.ErrInvalid) || !errors.Is(err, sim.ErrBaseDevice) {
		t.Fatalf("new lease on a base: err = %v, want ErrBaseDevice", err)
	}
	if _, err := guarded.Acquire(t.Context(), fresh, udidHuman, 0); err != nil {
		t.Fatalf("a device that is not a base: %v", err)
	}
}
