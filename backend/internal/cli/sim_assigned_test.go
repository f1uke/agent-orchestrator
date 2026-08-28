package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// twoBooted is the shape that used to be a dead end: with several devices
// booted nothing could resolve an unqualified command, and an agent that
// guessed reached for its crewmate's device.
func twoBooted(t *testing.T) string {
	t.Helper()
	return simDevicesJSON(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Booted"),
	)
}

// The point of the assignment: an unqualified command means YOUR device, even
// when the machine has two booted and used to refuse to choose.
func TestSimShot_UnqualifiedMeansThisSessionsOwnDevice(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_UDID", simUDIDPro)
	setConfigEnv(t)
	deps, _ := simDeps(t, twoBooted(t), fakePNG)

	out, errOut, err := executeCLI(t, deps, "sim", "shot", "--json")
	if err != nil {
		t.Fatalf("shot failed: %v\nstderr=%s", err, errOut)
	}
	var res simShotResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if res.UDID != simUDIDPro {
		t.Fatalf("captured %s, want this session's own device %s", res.UDID, simUDIDPro)
	}
}

// Naming a device is a deliberate act and still wins: reading another device
// takes no lease and corrupts nothing, so there is no reason to refuse it.
func TestSimShot_ExplicitUDIDBeatsTheAssignment(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_UDID", simUDIDPro)
	setConfigEnv(t)
	deps, _ := simDeps(t, twoBooted(t), fakePNG)

	out, _, err := executeCLI(t, deps, "sim", "shot", "--udid", simUDIDProMax, "--json")
	if err != nil {
		t.Fatalf("shot failed: %v", err)
	}
	var res simShotResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.UDID != simUDIDProMax {
		t.Fatalf("captured %s, want the device that was named", res.UDID)
	}
}

// Simulators get deleted. A session holding a udid this machine no longer has
// must fall back to the old rule rather than blame the caller for a device it
// never typed.
func TestSimShot_AStaleAssignmentIsIgnored(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_UDID", "00000000-0000-0000-0000-000000000000")
	setConfigEnv(t)
	deps, _ := simDeps(t, bootedProMaxOnly(t), fakePNG)

	out, _, err := executeCLI(t, deps, "sim", "shot", "--json")
	if err != nil {
		t.Fatalf("a stale assignment must not break capture: %v", err)
	}
	var res simShotResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.UDID != simUDIDProMax {
		t.Fatalf("captured %s, want the fallback to the one booted device", res.UDID)
	}
}

// `ao sim list` has to make the assignment visible, or an agent reading the
// machine cannot tell its own device from a crewmate's.
func TestSimList_MarksTheSessionsOwnDevice(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_UDID", simUDIDPro)
	setConfigEnv(t)
	deps, _ := simDeps(t, twoBooted(t), fakePNG)

	out, _, err := executeCLI(t, deps, "sim", "list")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(out, "<- yours ($AO_SIM_UDID)") {
		t.Fatalf("the listing must say which device is this session's:\n%s", out)
	}

	jsonOut, _, err := executeCLI(t, deps, "sim", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Devices []struct {
			UDID     string `json:"udid"`
			Assigned bool   `json:"assigned"`
		} `json:"devices"`
		DefaultUDID *string `json:"defaultUdid"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, jsonOut)
	}
	assigned := 0
	for _, d := range res.Devices {
		if d.Assigned {
			assigned++
			if d.UDID != simUDIDPro {
				t.Fatalf("marked %s as this session's", d.UDID)
			}
		}
	}
	if assigned != 1 {
		t.Fatalf("%d devices marked as this session's, want exactly 1", assigned)
	}
	if res.DefaultUDID == nil || *res.DefaultUDID != simUDIDPro {
		t.Fatalf("defaultUdid = %v, want this session's own device", res.DefaultUDID)
	}
}

// Booting is where guessing costs the most - it starts a multi-gigabyte VM - so
// the assignment is the one answer it will take without being asked twice.
func TestSimBoot_DefaultsToTheSessionsOwnDevice(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_UDID", simUDIDPro)
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg,
		bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		bootListing(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)
	deps := simBootDeps(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)

	out, errOut, err := executeCLI(t, deps, "sim", "boot")
	if err != nil {
		t.Fatalf("boot failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, simUDIDPro) {
		t.Fatalf("booted the wrong device:\n%s", out)
	}
	if reqs := daemon.powerRequests(); len(reqs) != 1 {
		t.Fatalf("power requests = %v, want exactly the one for this session's device\n%s", reqs, daemon.callLog())
	}
	if !strings.Contains(daemon.callLog(), simUDIDPro) {
		t.Fatalf("the daemon was not asked about this session's device:\n%s", daemon.callLog())
	}
}
