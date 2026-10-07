package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const simUDIDClone = "C10E0000-0000-0000-0000-000000000001"

func cloneFixtures() []simCloneClient {
	return []simCloneClient{
		{UDID: simUDIDProMax, SessionID: "mer-9", Label: "primary", Primary: true, Base: "iPhone 17 Pro Max", Name: "AO mer-9 (iPhone 17 Pro Max)"},
		{UDID: simUDIDClone, SessionID: "mer-9", Label: "iphone-se", Base: "iPhone SE (3rd generation)", Name: "AO mer-9 iphone-se (iPhone SE (3rd generation))"},
	}
}

func seClonedAndBooted(t *testing.T) string {
	t.Helper()
	return simDevicesJSON(t,
		simDeviceFixture(simUDIDProMax, "AO mer-9 (iPhone 17 Pro Max)", "Booted"),
		simDeviceFixture(simUDIDClone, "AO mer-9 iphone-se (iPhone SE (3rd generation))", "Booted"),
	)
}

func TestSimClaim_ModelClaimsTheSessionsCloneOfThatModel(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	daemon := newSimDaemon(t, setConfigEnv(t))
	daemon.clones = cloneFixtures()

	out, errOut, err := executeCLI(t, simLeaseDeps(t, seClonedAndBooted(t), fakePNG), "sim", "claim", "--model", "iPhone SE")
	if err != nil {
		t.Fatalf("claim: %v\nstderr=%s", err, errOut)
	}
	if !simCalled(daemon, "POST /api/v1/sessions/mer-9/sim-clones") {
		t.Fatalf("calls = %v, want the clone route asked first", daemon.calls)
	}
	var req acquireSimLeaseRequest
	if err := json.Unmarshal([]byte(daemon.body), &req); err != nil || req.UDID != simUDIDClone {
		t.Fatalf("leased %q (%v), want the SE clone %s", req.UDID, err, simUDIDClone)
	}
	if !strings.Contains(out, "--device iphone-se") {
		t.Fatalf("output does not say how to address the new device:\n%s", out)
	}
}

func TestSimClaim_AMissingBaseIsReportedNotWorkedAround(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	daemon := newSimDaemon(t, setConfigEnv(t))
	daemon.clones = []simCloneClient{}

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "claim", "--model", "iPhone SE")
	if err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("err = %v, want the missing base named", err)
	}
	if simCalled(daemon, "POST /api/v1/sessions/mer-9/sim-leases") {
		t.Fatal("claimed some other device when the base was missing")
	}
}

func TestSimDevice_NamesTheDeviceForAnyCommand(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_UDID", simUDIDProMax)
	daemon := newSimDaemon(t, setConfigEnv(t))
	daemon.clones = cloneFixtures()

	out, errOut, err := executeCLI(t, simLeaseDeps(t, seClonedAndBooted(t), fakePNG), "sim", "shot", "--device", "iphone-se", "--json")
	if err != nil {
		t.Fatalf("shot: %v\nstderr=%s", err, errOut)
	}
	var shot simShotResult
	if err := json.Unmarshal([]byte(out), &shot); err != nil || shot.UDID != simUDIDClone {
		t.Fatalf("shot of %q (%v), want the SE clone", shot.UDID, err)
	}

	_, _, err = executeCLI(t, simLeaseDeps(t, seClonedAndBooted(t), fakePNG), "sim", "shot", "--device", "nope")
	if err == nil || !strings.Contains(err.Error(), "iphone-se") {
		t.Fatalf("unknown label: err = %v, want the session's labels listed", err)
	}
	_, _, err = executeCLI(t, simLeaseDeps(t, seClonedAndBooted(t), fakePNG), "sim", "shot", "--device", "iphone-se", "--udid", simUDIDProMax)
	if err == nil {
		t.Fatal("--device with --udid must be refused")
	}
}

func TestSimUDID_PrintsTheLabelledDevice(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_UDID", simUDIDProMax)
	daemon := newSimDaemon(t, setConfigEnv(t))
	daemon.clones = cloneFixtures()

	out, _, err := executeCLI(t, simLeaseDeps(t, seClonedAndBooted(t), fakePNG), "sim", "udid", "--device", "iphone-se")
	if err != nil || strings.TrimSpace(out) != simUDIDClone {
		t.Fatalf("udid --device iphone-se = %q, %v", out, err)
	}
	out, _, err = executeCLI(t, simLeaseDeps(t, seClonedAndBooted(t), fakePNG), "sim", "udid")
	if err != nil || strings.TrimSpace(out) != simUDIDProMax {
		t.Fatalf("udid = %q, %v; want $AO_SIM_UDID", out, err)
	}
}

func TestSimRelease_DeviceDeletesThatExtraDevice(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	daemon := newSimDaemon(t, setConfigEnv(t))
	daemon.clones = cloneFixtures()

	out, errOut, err := executeCLI(t, simLeaseDeps(t, seClonedAndBooted(t), fakePNG), "sim", "release", "--device", "iphone-se")
	if err != nil {
		t.Fatalf("release: %v\nstderr=%s", err, errOut)
	}
	if !simCalled(daemon, "DELETE /api/v1/sessions/mer-9/sim-clones/iphone-se") || !strings.Contains(out, "Deleted") {
		t.Fatalf("calls = %v, out = %s; want the device deleted", daemon.calls, out)
	}
}

func TestSimList_ShowsWhoseCloneEachDeviceIs(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	daemon := newSimDaemon(t, setConfigEnv(t))
	daemon.clones = cloneFixtures()

	out, _, err := executeCLI(t, simLeaseDeps(t, seClonedAndBooted(t), fakePNG), "sim", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"ROLE", "yours: primary", "yours: iphone-se"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list missing %q:\n%s", want, out)
		}
	}
}
