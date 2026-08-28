package sim_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const udidAir = "9B8CDFCC-EE68-41A8-8C13-764D8B0619AC"

// fakeDevices is what the machine has. The assigner never shells out, so this
// is the whole boundary.
type fakeDevices struct {
	devices []simctl.Device
	err     error
	calls   int
}

func (f *fakeDevices) Devices(context.Context) (simctl.Listing, error) {
	f.calls++
	if f.err != nil {
		return simctl.Listing{}, f.err
	}
	return simctl.Listing{Devices: f.devices}, nil
}

func device(udid, name, state string) simctl.Device {
	return simctl.Device{UDID: udid, Name: name, State: state, Available: true}
}

func newAssigner(t *testing.T, now time.Time, devices *fakeDevices) (*sim.Assigner, *sqlite.Store) {
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
	return sim.NewAssigner(store, devices, fixedClock(now)), store
}

// The failure this whole change exists to end: two members of one task each
// reach for "the booted simulator" and land on the same device.
func TestAssignDevice_TwoMembersOfOneTaskGetDifferentDevices(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	assigner, store := newAssigner(t, now, &fakeDevices{devices: []simctl.Device{
		device(udidProMax, "iPhone 17 Pro Max", simctl.BootedState),
		device(udidPro, "iPhone 17 Pro", "Shutdown"),
	}})
	dev, qa := newSession(t, store, now), newSession(t, store, now)

	devAssignment, err := assigner.AssignDevice(t.Context(), dev)
	if err != nil {
		t.Fatalf("assign dev: %v", err)
	}
	qaAssignment, err := assigner.AssignDevice(t.Context(), qa)
	if err != nil {
		t.Fatalf("assign qa: %v", err)
	}
	if devAssignment.UDID == "" || qaAssignment.UDID == "" {
		t.Fatalf("both members must get a device: dev=%q qa=%q", devAssignment.UDID, qaAssignment.UDID)
	}
	if devAssignment.UDID == qaAssignment.UDID {
		t.Fatalf("both members were given %s, which is the collision this exists to prevent", devAssignment.UDID)
	}
	// A booted device is worth more than a shut-down one: whoever asks first
	// can start working, rather than paying for a multi-gigabyte boot.
	if devAssignment.UDID != udidProMax {
		t.Fatalf("first caller got %s, want the booted device", devAssignment.UDID)
	}
}

// A restore must come back to the same device, or the environment variable an
// agent was told to trust would point somewhere else after every restart.
func TestAssignDevice_IsStableAcrossRespawns(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	devices := &fakeDevices{devices: []simctl.Device{
		device(udidProMax, "iPhone 17 Pro Max", simctl.BootedState),
		device(udidPro, "iPhone 17 Pro", "Shutdown"),
	}}
	assigner, store := newAssigner(t, now, devices)
	session := newSession(t, store, now)

	first, err := assigner.AssignDevice(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	before := devices.calls
	second, err := assigner.AssignDevice(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	if first.UDID != second.UDID {
		t.Fatalf("a respawn moved the session from %s to %s", first.UDID, second.UDID)
	}
	// And it does not pay for a device listing to be told what it already knows.
	if devices.calls != before {
		t.Fatalf("re-assigning read the machine %d extra times", devices.calls-before)
	}
}

// A device somebody is mid-gesture on is not free, even if nothing was ever
// assigned on it: handing it out is the collision, just arrived by the other
// door.
func TestAssignDevice_SkipsADeviceAnotherSessionIsDriving(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	assigner, store := newAssigner(t, now, &fakeDevices{devices: []simctl.Device{
		device(udidProMax, "iPhone 17 Pro Max", simctl.BootedState),
		device(udidPro, "iPhone 17 Pro", simctl.BootedState),
	}})
	driver, newcomer := newSession(t, store, now), newSession(t, store, now)
	if _, _, err := store.AcquireSimLease(t.Context(), domain.SimLease{
		UDID: udidProMax, SessionID: driver, AcquiredAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	got, err := assigner.AssignDevice(t.Context(), newcomer)
	if err != nil {
		t.Fatal(err)
	}
	if got.UDID != udidPro {
		t.Fatalf("assigned %s, want the device nobody is driving", got.UDID)
	}
}

// A machine with nothing left to give is a normal state, not a spawn failure:
// the session simply exports no device and behaves exactly as it did before
// assignments existed.
func TestAssignDevice_RunningOutOfDevicesIsNotAnError(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	assigner, store := newAssigner(t, now, &fakeDevices{devices: []simctl.Device{
		device(udidProMax, "iPhone 17 Pro Max", simctl.BootedState),
	}})
	first, second := newSession(t, store, now), newSession(t, store, now)

	if _, err := assigner.AssignDevice(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	got, err := assigner.AssignDevice(t.Context(), second)
	if err != nil {
		t.Fatalf("an exhausted machine must not fail a spawn: %v", err)
	}
	if got.UDID != "" {
		t.Fatalf("assigned %s from an exhausted pool", got.UDID)
	}
}

// Unavailable devices cannot be booted or driven, so handing one out would give
// a session a variable that only ever produces failures.
func TestAssignDevice_SkipsUnavailableDevices(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	unavailable := device(udidProMax, "iPhone 17 Pro Max", "Shutdown")
	unavailable.Available = false
	assigner, store := newAssigner(t, now, &fakeDevices{devices: []simctl.Device{
		unavailable,
		device(udidAir, "iPhone Air", "Shutdown"),
	}})

	got, err := assigner.AssignDevice(t.Context(), newSession(t, store, now))
	if err != nil {
		t.Fatal(err)
	}
	if got.UDID != udidAir {
		t.Fatalf("assigned %s, want the device that can actually be used", got.UDID)
	}
}

// No Xcode, no simulators, no listing: none of those is a reason to refuse to
// launch an agent.
func TestAssignDevice_AMachineThatCannotAnswerIsNotAnError(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	assigner, store := newAssigner(t, now, &fakeDevices{err: simctl.ErrUnavailable})

	got, err := assigner.AssignDevice(t.Context(), newSession(t, store, now))
	if err != nil {
		t.Fatalf("an unreadable machine must not fail a spawn: %v", err)
	}
	if got.UDID != "" {
		t.Fatalf("assigned %s from a machine that could not be read", got.UDID)
	}
}

// A device is given back when its session ends, or the pool would drain one
// session at a time until nobody could be assigned anything.
func TestAssignDevice_DeviceReturnsToThePoolWhenTheSessionEnds(t *testing.T) {
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	assigner, store := newAssigner(t, now, &fakeDevices{devices: []simctl.Device{
		device(udidProMax, "iPhone 17 Pro Max", simctl.BootedState),
	}})
	first := newSession(t, store, now)
	if _, err := assigner.AssignDevice(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	record, _, err := store.GetSession(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	record.IsTerminated = true
	if err := store.UpdateSession(t.Context(), record); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	got, err := assigner.AssignDevice(t.Context(), newSession(t, store, now))
	if err != nil {
		t.Fatal(err)
	}
	if got.UDID != udidProMax {
		t.Fatalf("assigned %q; the ended session's device must return to the pool", got.UDID)
	}
}
