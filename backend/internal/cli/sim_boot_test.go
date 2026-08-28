package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
)

// The boot half of `ao sim`. It is the only command here that changes a
// device's power state, and every test below is about a rule that keeps it the
// ONLY one: it never shuts anything down, it never boots past the memory cap,
// and it never guesses which of a machine's simulators to spend 4 GB starting.

// simPowerDaemon fakes the two daemon routes `ao sim boot` uses: the device
// listing it waits on, and the power route it asks. The device table is the
// test's to mutate, so a test can make a boot "land" between two polls.
type simPowerDaemon struct {
	mu sync.Mutex

	devices []simDeviceListing
	// powerStatus/powerBody override the power route's answer so a test can
	// serve the daemon's real refusals (already booted, an op in flight).
	powerStatus int
	powerBody   string
	// bootsAfter is how many listing polls a started boot takes to land. The
	// device flips to Booted and its power entry clears on the poll after that;
	// negative means never.
	bootsAfter int
	// onStart is the power entry a started boot leaves on the device, so a test
	// can make the daemon's boot fail the way a real one does.
	onStart *simDevicePowerListing

	polls  int
	powers []string // the JSON body of every power request, in order
	calls  []string
}

func (d *simPowerDaemon) powerRequests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.powers...)
}

func (d *simPowerDaemon) callLog() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.calls, "\n")
}

// setPower marks a device as having an operation in flight or failed, exactly
// as SimDeviceView.power reports one.
func (d *simPowerDaemon) setPower(udid string, power *simDevicePowerListing) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.devices {
		if d.devices[i].UDID == udid {
			d.devices[i].Power = power
		}
	}
}

func newSimPowerDaemon(t *testing.T, cfg testConfig, devices ...simDeviceListing) *simPowerDaemon {
	t.Helper()
	d := &simPowerDaemon{devices: devices, bootsAfter: 1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.calls = append(d.calls, r.Method+" "+r.URL.Path)
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sim/devices":
			d.mu.Lock()
			d.polls++
			if d.bootsAfter >= 0 && d.polls > d.bootsAfter {
				for i := range d.devices {
					if p := d.devices[i].Power; p != nil && p.Op == "boot" && p.State == "running" {
						d.devices[i].State = "Booted"
						d.devices[i].Power = nil
					}
				}
			}
			resp := listSimDevicesResponse{Devices: append([]simDeviceListing(nil), d.devices...)}
			d.mu.Unlock()
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/power"):
			d.mu.Lock()
			d.powers = append(d.powers, string(body))
			status, refusal := d.powerStatus, d.powerBody
			if status == 0 || status == http.StatusAccepted {
				udid := r.URL.Path[strings.Index(r.URL.Path, "/sim-devices/")+len("/sim-devices/"):]
				udid = strings.TrimSuffix(udid, "/power")
				started := d.onStart
				if started == nil {
					started = &simDevicePowerListing{Op: "boot", State: "running"}
				}
				for i := range d.devices {
					if strings.EqualFold(d.devices[i].UDID, udid) {
						d.devices[i].Power = started
					}
				}
			}
			d.mu.Unlock()
			if status != 0 && status != http.StatusAccepted {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, refusal)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"udid":"x","state":"booted","detail":"boot"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return d
}

func bootListing(udid, name, state string) simDeviceListing {
	return simDeviceListing{
		UDID: udid, Name: name, Runtime: "iOS 26.3",
		RuntimeIdentifier: simRuntimeIOS263, State: state, Available: true,
	}
}

// simBootDeps is the CLI wired to a live daemon, with simctl faked for the
// device discovery `ao sim boot` does before it asks for anything.
func simBootDeps(t *testing.T, devices ...map[string]any) Deps {
	t.Helper()
	deps, _ := simDeps(t, simDevicesJSON(t, devices...), nil)
	deps.ProcessAlive = func(int) bool { return true }
	return deps
}

type simBootJSON struct {
	UDID          string `json:"udid"`
	Name          string `json:"name"`
	Runtime       string `json:"runtime"`
	State         string `json:"state"`
	AlreadyBooted bool   `json:"alreadyBooted"`
	Note          string `json:"note"`
}

func TestSimBoot_BootsTheNamedDeviceAndWaitsUntilItIsUp(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg,
		bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"),
	)
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax, "--json")
	if err != nil {
		t.Fatalf("sim boot failed: %v\nstderr=%s", err, errOut)
	}
	var got simBootJSON
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("decode sim boot JSON: %v\n%s", jsonErr, out)
	}
	if got.UDID != simUDIDProMax || got.State != "Booted" {
		t.Errorf("got %+v, want the named device reported Booted", got)
	}
	if got.AlreadyBooted {
		t.Error("alreadyBooted = true, but this device was Shutdown when the command started")
	}
	// The state, not a verb: asking for "booted" twice asks for the same world
	// twice, which is what makes a retry safe.
	reqs := daemon.powerRequests()
	if len(reqs) != 1 || !strings.Contains(reqs[0], `"state":"booted"`) {
		t.Fatalf("power requests = %v, want exactly one asking for state booted", reqs)
	}
	// Waiting is the whole contract: `ao sim claim` runs next and needs a
	// device that is actually up, so returning on the 202 would be a lie.
	if !strings.Contains(daemon.callLog(), "GET /api/v1/sim/devices") {
		t.Errorf("boot never waited for the device to come up:\n%s", daemon.callLog())
	}
	// A booted device is not a held one. The note has to say the thing that is
	// easiest to assume and wrong - that starting it gave you it.
	if !strings.Contains(got.Note, "claim") {
		t.Errorf("note = %q, want it to say booting takes nothing and a claim is next", got.Note)
	}
	if strings.Contains(got.Note, "captured") {
		t.Errorf("note = %q reads like a screenshot's; a boot has no frame", got.Note)
	}
}

func TestSimBoot_AlreadyBootedIsANoOpNotAnError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax, "--json")
	if err != nil {
		t.Fatalf("booting an already-booted device must succeed: %v\nstderr=%s", err, errOut)
	}
	var got simBootJSON
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("decode sim boot JSON: %v\n%s", jsonErr, out)
	}
	if !got.AlreadyBooted || got.State != "Booted" {
		t.Errorf("got %+v, want alreadyBooted on a device that was already up", got)
	}
	// An agent retrying must not restart a device somebody is mid-gesture on.
	if reqs := daemon.powerRequests(); len(reqs) != 0 {
		t.Errorf("power was asked for %v; a booted device needs nothing done to it", reqs)
	}
}

func TestSimBoot_DaemonSayingAlreadyBootedIsStillSuccess(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))
	// The picker's 409: right for a UI whose list went stale, wrong for an
	// agent, which asked for a state and got it.
	daemon.powerStatus = http.StatusConflict
	daemon.powerBody = `{"code":"SIM_POWER_ALREADY","message":"simulator iPhone 17 Pro Max is already booted"}`
	// simctl still says Shutdown, so the CLI does ask - and must not fail.
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax, "--json")
	if err != nil {
		t.Fatalf("SIM_POWER_ALREADY is the state we asked for, not a failure: %v\nstderr=%s", err, errOut)
	}
	var got simBootJSON
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("decode sim boot JSON: %v\n%s", jsonErr, out)
	}
	if !got.AlreadyBooted {
		t.Errorf("got %+v, want it reported as already booted", got)
	}
}

func TestSimBoot_SecondSessionJoinsAnInFlightBoot(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	daemon.setPower(simUDIDProMax, &simDevicePowerListing{Op: "boot", State: "running"})
	daemon.powerStatus = http.StatusConflict
	daemon.powerBody = `{"code":"SIM_POWER_BUSY","message":"simpower: an operation is already in flight on this device"}`
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax, "--json")
	if err != nil {
		t.Fatalf("a boot already in flight is the same outcome, not a conflict: %v\nstderr=%s", err, errOut)
	}
	var got simBootJSON
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("decode sim boot JSON: %v\n%s", jsonErr, out)
	}
	if got.State != "Booted" {
		t.Errorf("got %+v, want the joined boot waited through to Booted", got)
	}
}

func TestSimBoot_RefusesWhileTheDeviceIsBeingShutDown(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))
	daemon.bootsAfter = -1
	daemon.setPower(simUDIDProMax, &simDevicePowerListing{Op: "shutdown", State: "running"})
	daemon.powerStatus = http.StatusConflict
	daemon.powerBody = `{"code":"SIM_POWER_BUSY","message":"simpower: an operation is already in flight on this device"}`
	// simctl has not caught up yet, so the CLI does reach the daemon.
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	_, _, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax)
	if err == nil {
		t.Fatal("booting a device somebody is powering off must be refused, not raced")
	}
	if !strings.Contains(err.Error(), "shut") {
		t.Errorf("error = %q, want it to say a shutdown is in flight", err)
	}
}

func TestSimBoot_ReportsWhyTheDaemonsBootFailed(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	daemon.bootsAfter = -1
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	// The boot starts, and the daemon records the machine's own words.
	daemon.onStart = &simDevicePowerListing{
		Op: "boot", State: "failed", Reason: "Unable to boot device in current state: Creating",
	}

	_, _, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax)
	if err == nil {
		t.Fatal("a failed boot must fail the command")
	}
	if !strings.Contains(err.Error(), "Unable to boot device in current state") {
		t.Errorf("error = %q, want the machine's own reason", err)
	}
}

func TestSimBoot_NoUDIDWithSeveralShutdownDevicesRefusesAndNamesEach(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg,
		bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"),
		bootListing(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)
	deps := simBootDeps(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)

	_, _, err := executeCLI(t, deps, "sim", "boot")
	if err == nil {
		t.Fatal("with several simulators and none booted, boot must refuse rather than pick one")
	}
	for _, udid := range []string{simUDIDProMax, simUDIDPro} {
		if !strings.Contains(err.Error(), "ao sim boot --udid "+udid) {
			t.Errorf("error = %q, want a runnable line for %s", err, udid)
		}
	}
	if reqs := daemon.powerRequests(); len(reqs) != 0 {
		t.Errorf("a refusal must not have started anything: %v", reqs)
	}
}

func TestSimBoot_NoUDIDBootsTheOnlySimulatorOnTheMachine(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	if _, errOut, err := executeCLI(t, deps, "sim", "boot"); err != nil {
		t.Fatalf("one simulator is not ambiguous: %v\nstderr=%s", err, errOut)
	}
	if reqs := daemon.powerRequests(); len(reqs) != 1 {
		t.Fatalf("power requests = %v, want exactly one", reqs)
	}
}

func TestSimBoot_NoUDIDWithOneBootedIsANoOp(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg,
		bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		bootListing(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)
	deps := simBootDeps(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--json")
	if err != nil {
		t.Fatalf("the state boot asks for is already true: %v\nstderr=%s", err, errOut)
	}
	var got simBootJSON
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("decode sim boot JSON: %v\n%s", jsonErr, out)
	}
	if got.UDID != simUDIDProMax || !got.AlreadyBooted {
		t.Errorf("got %+v, want the one booted device reported as already up", got)
	}
	if reqs := daemon.powerRequests(); len(reqs) != 0 {
		t.Errorf("nothing should have been started: %v", reqs)
	}
}

func TestSimBoot_NoUDIDWithSeveralBootedRefusesToGuess(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimPowerDaemon(t, cfg,
		bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		bootListing(simUDIDPro, "iPhone 17 Pro", "Booted"),
	)
	deps := simBootDeps(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Booted"),
	)

	_, _, err := executeCLI(t, deps, "sim", "boot")
	if err == nil {
		t.Fatal("two booted simulators leave no unambiguous device to speak for")
	}
	if !strings.Contains(err.Error(), simUDIDProMax) || !strings.Contains(err.Error(), simUDIDPro) {
		t.Errorf("error = %q, want both booted devices named", err)
	}
}

func TestSimBoot_RefusesToMakeTheThirdBootedSimulator(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg,
		bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		bootListing(simUDIDPro, "iPhone 17 Pro", "Booted"),
		bootListing(simUDIDAir, "iPhone Air", "Shutdown"),
	)
	deps := simBootDeps(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Booted"),
		simDeviceFixture(simUDIDAir, "iPhone Air", "Shutdown"),
	)

	_, _, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDAir)
	if err == nil {
		t.Fatal("three booted simulators have already OOM'd this machine once; an agent must not be the cause")
	}
	// Naming what is already up is what makes the refusal actionable: the
	// agent can see whether one of them is the device it actually wanted.
	if !strings.Contains(err.Error(), "iPhone 17 Pro Max") || !strings.Contains(err.Error(), "iPhone 17 Pro") {
		t.Errorf("error = %q, want the already-booted devices named", err)
	}
	if !strings.Contains(err.Error(), "Device tab") {
		t.Errorf("error = %q, want the human's way past the cap", err)
	}
	if reqs := daemon.powerRequests(); len(reqs) != 0 {
		t.Errorf("the cap must refuse before anything is started: %v", reqs)
	}
}

func TestSimBoot_CapCountsTheDeviceItWouldMakeNotTheOnesAlreadyUp(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg,
		bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		bootListing(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)
	deps := simBootDeps(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)

	// One booted is the ordinary case - a human's working device up, and the
	// agent wanting a scratch one. The cap must not stand in the way of that.
	if _, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDPro); err != nil {
		t.Fatalf("a second booted device is allowed: %v\nstderr=%s", err, errOut)
	}
	if reqs := daemon.powerRequests(); len(reqs) != 1 {
		t.Fatalf("power requests = %v, want the boot to have gone through", reqs)
	}
}

func TestSimBoot_UnknownUDIDIsAClearError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	_, _, err := executeCLI(t, deps, "sim", "boot", "--udid", "not-a-real-udid")
	if err == nil || !strings.Contains(err.Error(), "ao sim list") {
		t.Fatalf("err = %v, want an unknown udid to point at `ao sim list`", err)
	}
}

func TestSimBoot_UnavailableDeviceIsRefused(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	unavailable := simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown")
	unavailable["isAvailable"] = false
	deps := simBootDeps(t, unavailable)

	_, _, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("err = %v, want a refusal that says the device is unavailable", err)
	}
}

func TestSimBoot_OutsideASessionIsAUsageError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	_, _, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax)
	if err == nil || !strings.Contains(err.Error(), "AO_SESSION_ID") {
		t.Fatalf("err = %v, want a usage error naming AO_SESSION_ID", err)
	}
}

// Boot is additive and that is the whole reason it was allowed: nothing here
// may grow into a way for an agent to take a device away from somebody.
func TestSimBoot_NeverAsksForAnythingButBooted(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	if _, _, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax); err != nil {
		t.Fatalf("sim boot failed: %v", err)
	}
	for _, req := range daemon.powerRequests() {
		for _, forbidden := range []string{"shutdown", "reboot", "erase"} {
			if strings.Contains(req, forbidden) {
				t.Fatalf("power request %q asked for %q; `ao sim boot` only ever boots", req, forbidden)
			}
		}
	}
}

func TestSimBoot_HasNoShutdownRebootOrEraseSibling(t *testing.T) {
	root := NewRootCommand(Deps{})
	sim, _, err := root.Find([]string{"sim"})
	if err != nil {
		t.Fatalf("find sim: %v", err)
	}
	for _, sub := range sim.Commands() {
		switch sub.Name() {
		case "shutdown", "reboot", "erase":
			t.Fatalf("`ao sim %s` exists: taking a device down or wiping it is the human's, "+
				"and boot was allowed precisely because it is the additive half", sub.Name())
		}
	}
}

func TestSimBoot_TimesOutWithSomethingToDoNext(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimPowerDaemon(t, cfg, bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))
	daemon.bootsAfter = -1 // never lands
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	_, _, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax, "--timeout", "3s")
	if err == nil {
		t.Fatal("a boot that never lands must fail rather than hang for ever")
	}
	if !strings.Contains(err.Error(), "ao sim list") {
		t.Errorf("error = %q, want it to say how to check where the device got to", err)
	}
}

// A Warned boot means the boot itself worked and the device only ended up
// stock - it is simpower's entire mechanism for reporting that, and the wait
// loop must recognise it as done rather than polling it out to a timeout.
func TestWaitForSimBoot_WarnedBootIsASuccessNotATimeout(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	device := bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted")
	device.Power = &simDevicePowerListing{
		Op:            "boot",
		State:         "warned",
		Profile:       "skipped",
		ProfileReason: "simslim is not on PATH",
	}
	newSimPowerDaemon(t, cfg, device)
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))
	// No real waiting: if the fix is missing this still returns (with an
	// error) well inside the test timeout, it just returns the WRONG thing.
	deps.Sleep = func(time.Duration) {}
	c := &commandContext{deps: deps.withDefaults()}

	target := simDevice{Device: simctl.Device{
		UDID: simUDIDProMax, Name: "iPhone 17 Pro Max", Runtime: "iOS 26.3",
	}}
	listing, err := c.waitForSimBoot(context.Background(), target, 3*time.Second)
	if err != nil {
		t.Fatalf("waitForSimBoot returned %v, want nil - the boot itself succeeded", err)
	}
	if listing.Power == nil || listing.Power.Profile != "skipped" {
		t.Fatalf("listing = %+v, want the warned boot's profile fields carried back", listing)
	}
}

func TestWriteSimBoot_SaysPlainlyWhenTheDeviceIsStock(t *testing.T) {
	var out bytes.Buffer
	result := simBootResult{
		UDID: "4754DB41-86C8-4326-81A7-172DDD41D5DA", Name: "AO scratch",
		Runtime: "iOS 26.3", State: "Booted", Note: simBootedDeviceNote,
		Profile:       "skipped",
		ProfileReason: "simslim is not on PATH",
	}

	if err := writeSimBoot(&out, result); err != nil {
		t.Fatalf("writeSimBoot: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "STOCK") {
		t.Fatalf("output never says the device is stock:\n%s", got)
	}
	if !strings.Contains(got, "simslim is not on PATH") {
		t.Fatalf("output dropped the reason:\n%s", got)
	}
}

func TestWriteSimBoot_SaysNothingExtraWhenTheProfileLanded(t *testing.T) {
	var out bytes.Buffer
	result := simBootResult{
		UDID: "4754DB41-86C8-4326-81A7-172DDD41D5DA", Name: "AO scratch",
		Runtime: "iOS 26.3", State: "Booted", Note: simBootedDeviceNote,
		Profile: "applied",
	}

	if err := writeSimBoot(&out, result); err != nil {
		t.Fatalf("writeSimBoot: %v", err)
	}

	if strings.Contains(out.String(), "STOCK") {
		t.Fatalf("a slimmed device was reported as stock:\n%s", out.String())
	}
}

func TestWriteSimBoot_SaysNothingExtraForAProjectThatDoesNotSlim(t *testing.T) {
	var out bytes.Buffer
	result := simBootResult{
		UDID: "4754DB41-86C8-4326-81A7-172DDD41D5DA", Name: "AO scratch",
		Runtime: "iOS 26.3", State: "Booted", Note: simBootedDeviceNote,
	}

	if err := writeSimBoot(&out, result); err != nil {
		t.Fatalf("writeSimBoot: %v", err)
	}

	if strings.Contains(out.String(), "STOCK") {
		t.Fatalf("a project with no profile was warned at:\n%s", out.String())
	}
}

// The CLI's default wait has to outlast the DAEMON's whole operation, not just
// the boot half of it. The daemon boots the device and then, inside the same
// `POST .../power`, brings it to the project's profile - and `simslim on`
// reboots the device, so that second half is minutes, not seconds.
//
// Two things break when the sum is wrong, and the second is the worse one: a
// boot that actually succeeded is reported as "did not finish booting", and
// because writeSimBoot is never reached, a device that came up STOCK says
// nothing at all - which is precisely the silence this feature exists to end.
//
// ⚠ This test fails if simpower.ProfileTimeout is raised and the CLI's wait is
// not. That is what it is for.
func TestParseSimBootTimeout_DefaultOutlastsTheDaemonsWholeOperation(t *testing.T) {
	got, err := parseSimBootTimeout("")
	if err != nil {
		t.Fatalf("parseSimBootTimeout(\"\"): %v", err)
	}
	if want := simpower.BootTimeout + simpower.ProfileTimeout + simBootGrace; got != want {
		t.Fatalf("default wait = %s, want %s - every phase the daemon spends inside one power "+
			"request, plus the grace that makes the daemon's reason win", got, want)
	}
	// Stated separately from the sum above so the invariant survives a
	// refactor of how the sum is spelled: whatever the formula becomes, the
	// CLI must still be waiting after the daemon has given up.
	if got <= simpower.BootTimeout+simpower.ProfileTimeout {
		t.Fatalf("default wait %s does not outlast the daemon's own worst case %s; a boot that "+
			"succeeded slowly would fail here with our timeout and its STOCK warning would never print",
			got, simpower.BootTimeout+simpower.ProfileTimeout)
	}
}

// The cap counts a device the daemon is still booting, and this is the case it
// was blind to: `simslim on` REBOOTS the device, so through the slimming phase
// an AO-booted simulator is not Booted while its several GB are allocated. A
// crewmate booting in that window would be waved through to a third device -
// the OOM the cap exists to prevent, in the dev-and-qa case slimming is for.
func TestSimBoot_CapCountsADeviceTheDaemonIsStillBooting(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	inFlight := bootListing(simUDIDPro, "iPhone 17 Pro", "Shutdown")
	inFlight.Power = &simDevicePowerListing{Op: "boot", State: "running", Phase: "slimming"}
	daemon := newSimPowerDaemon(t, cfg,
		bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		inFlight,
		bootListing(simUDIDAir, "iPhone Air", "Shutdown"),
	)
	daemon.bootsAfter = -1 // the in-flight boot is still slimming, not landing
	deps := simBootDeps(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		// simctl says Shutdown for the device that is mid-reboot, which is
		// exactly why counting simctl alone undercounts.
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
		simDeviceFixture(simUDIDAir, "iPhone Air", "Shutdown"),
	)

	_, _, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDAir)
	if err == nil {
		t.Fatal("a device mid-boot is a device holding several GB; booting a third must be refused")
	}
	if !strings.Contains(err.Error(), "iPhone 17 Pro Max") || !strings.Contains(err.Error(), "iPhone 17 Pro") {
		t.Errorf("error = %q, want both the booted and the still-coming-up device named", err)
	}
	if !strings.Contains(err.Error(), "coming up") {
		t.Errorf("error = %q, want it to say one of them is not up yet", err)
	}
	if reqs := daemon.powerRequests(); len(reqs) != 0 {
		t.Errorf("the cap must refuse before anything is started: %v", reqs)
	}
}

// The warning a device carries is not tied to the boot that raised it. AO's own
// earlier boot leaves a Warned entry that is never cleared, so the SECOND
// crewmate to run `ao sim boot` - the one who takes the already-booted no-op,
// and typically the one who records the FAIL - has to be told too.
func TestSimBoot_AlreadyBootedDeviceStillReportsItIsStock(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	device := bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted")
	device.Power = &simDevicePowerListing{
		Op: "boot", State: "warned", Profile: "skipped", ProfileReason: "simslim is not on PATH",
	}
	daemon := newSimPowerDaemon(t, cfg, device)
	daemon.bootsAfter = -1
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax)
	if err != nil {
		t.Fatalf("an already-booted device is a no-op, not a failure: %v\nstderr=%s", err, errOut)
	}
	if reqs := daemon.powerRequests(); len(reqs) != 0 {
		t.Errorf("nothing should have been started: %v", reqs)
	}
	if !strings.Contains(out, "STOCK") {
		t.Fatalf("the no-op path said nothing about a device AO knows is stock:\n%s", out)
	}
	if !strings.Contains(out, "simslim is not on PATH") {
		t.Fatalf("the warning dropped the daemon's reason:\n%s", out)
	}
}

// The same news on the other no-op path: the device came up between our listing
// and our request, the daemon answers 409 SIM_POWER_ALREADY, and whatever it
// knows about that device's profile still has to reach the caller.
func TestSimBoot_DaemonSayingAlreadyBootedStillReportsItIsStock(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	device := bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted")
	device.Power = &simDevicePowerListing{
		Op: "boot", State: "warned", Profile: "failed", ProfileReason: "simslim: unknown daemon com.apple.nope",
	}
	daemon := newSimPowerDaemon(t, cfg, device)
	daemon.bootsAfter = -1
	daemon.powerStatus = http.StatusConflict
	daemon.powerBody = `{"code":"SIM_POWER_ALREADY","message":"simulator iPhone 17 Pro Max is already booted"}`
	// simctl has not caught up, so the CLI does ask and does get the 409.
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax)
	if err != nil {
		t.Fatalf("SIM_POWER_ALREADY is the state we asked for, not a failure: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "STOCK") || !strings.Contains(out, "com.apple.nope") {
		t.Fatalf("the 409 path dropped what the daemon knows about this device:\n%s", out)
	}
}

// The rendered sentence, not just its parts: "stock" twice in one line, or a
// reason that runs straight into the sentence after it, is how a warning stops
// being read.
func TestWriteSimBoot_StockWarningReadsAsOneSentence(t *testing.T) {
	var out bytes.Buffer
	result := simBootResult{
		UDID: "4754DB41-86C8-4326-81A7-172DDD41D5DA", Name: "AO scratch",
		Runtime: "iOS 26.3", State: "Booted", Note: simBootedDeviceNote,
		Profile:       "skipped",
		ProfileReason: "simslim is not on PATH",
	}

	if err := writeSimBoot(&out, result); err != nil {
		t.Fatalf("writeSimBoot: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "simslim is not on PATH.\nFeatures") {
		t.Fatalf("the reason runs into the next sentence:\n%s", got)
	}
	if n := strings.Count(strings.ToLower(got), "stock"); n != 1 {
		t.Fatalf("the word stock appears %d times in one warning:\n%s", n, got)
	}
}

func TestWriteSimBoot_DoesNotDoubleAFullStopTheMachineWrote(t *testing.T) {
	var out bytes.Buffer
	result := simBootResult{
		UDID: "4754DB41-86C8-4326-81A7-172DDD41D5DA", Name: "AO scratch",
		Runtime: "iOS 26.3", State: "Booted", Note: simBootedDeviceNote,
		Profile:       "failed",
		ProfileReason: "simslim: unknown daemon com.apple.nope.",
	}

	if err := writeSimBoot(&out, result); err != nil {
		t.Fatalf("writeSimBoot: %v", err)
	}

	if strings.Contains(out.String(), "nope..") {
		t.Fatalf("appended a full stop the machine had already written:\n%s", out.String())
	}
}
