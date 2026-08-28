package controllers_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
)

func powerURL(base, session, udid string) string {
	return base + "/api/v1/sessions/" + session + "/sim-devices/" + udid + "/power"
}

// A machine that cannot power a simulator says so rather than accepting the
// request and losing it.
func TestSimPower_WithoutAScreenIs501(t *testing.T) {
	srv := newScreenTestServer(t, &fakeSimService{}, nil)
	code, _ := postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{"state": "booted"})
	if code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", code)
	}
}

func TestSimPower_BootsAShutDownDevice(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	code, _ := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})
	// 202, not 200: a boot takes tens of seconds and the answer is "this has
	// started", never "this has happened".
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
	ops := screen.powered()
	if len(ops) != 1 || ops[0].Op != simpower.Boot || ops[0].UDID != otherSimUDID {
		t.Fatalf("powered %+v, want one boot of %s", ops, otherSimUDID)
	}
}

// Booting cannot disturb anybody - nothing can be driving a device that is not
// running - so it does not consult the lease at all.
func TestSimPower_BootIgnoresAForeignLease(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	leases := &fakeSimService{leases: []domain.SimLease{{
		UDID: otherSimUDID, SessionID: "p-9", ExpiresAt: time.Now().Add(time.Hour),
	}}}
	srv := newScreenTestServer(t, leases, screen)

	code, _ := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
	if leases.tookOver {
		t.Error("booting took a lease; it has nothing to arbitrate against")
	}
}

func TestSimPower_ShutsABootedDeviceDown(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	leases := &fakeSimService{}
	srv := newScreenTestServer(t, leases, screen)

	code, _ := postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{"state": "shutdown"})
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
	ops := screen.powered()
	if len(ops) != 1 || ops[0].Op != simpower.Shutdown {
		t.Fatalf("powered %+v, want one shutdown", ops)
	}
	// The device is arbitrated for exactly as long as it is being powered off:
	// taken before the shutdown starts, given back when it settles.
	if !leases.tookOver {
		t.Error("the shutdown did not take the device, so a gesture could have started underneath it")
	}
	if ops[0].Done == nil {
		t.Fatal("no callback to give the lease back with; the tab would keep claiming a dead device")
	}
	ops[0].Done()
	if leases.gotUDID != testSimUDID {
		t.Errorf("released %q, want %q", leases.gotUDID, testSimUDID)
	}
}

// 🗝 The one refusal that no confirmation overrides. A touch cut off
// mid-gesture leaves the driver believing a finger is down, and a stuck touch
// wedges the device's input until somebody reboots it.
func TestSimPower_ShutdownRefusedWhileAGestureIsInFlight(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	leases := &fakeSimService{takeOverErr: &simsvc.HeldError{
		Lease:      domain.SimLease{UDID: testSimUDID, SessionID: "p-9", ExpiresAt: time.Now().Add(time.Minute)},
		Now:        time.Now(),
		MidGesture: true,
	}}
	srv := newScreenTestServer(t, leases, screen)

	code, body := postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{
		"state": "shutdown", "confirmHolder": "p-9",
	})
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	if got := errorCode(body); got != "SIM_POWER_GESTURE_IN_FLIGHT" {
		t.Errorf("code %q, want SIM_POWER_GESTURE_IN_FLIGHT", got)
	}
	if ops := screen.powered(); len(ops) != 0 {
		t.Fatalf("a shutdown reached the machine mid-gesture: %+v", ops)
	}
}

// A device somebody else holds may be powered off, but only by a request that
// names them - so a picker acting on a list that went stale cannot shut down a
// device on the strength of a lease it read before somebody else took it.
func TestSimPower_ShutdownOfAForeignDeviceMustNameTheHolder(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	leases := &fakeSimService{leases: []domain.SimLease{{
		UDID: testSimUDID, SessionID: "p-9", ExpiresAt: time.Now().Add(time.Hour),
	}}}
	srv := newScreenTestServer(t, leases, screen)

	code, body := postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{"state": "shutdown"})
	if code != http.StatusConflict {
		t.Fatalf("unnamed status %d, want 409", code)
	}
	if got := errorCode(body); got != "SIM_POWER_HOLDER_UNCONFIRMED" {
		t.Errorf("code %q, want SIM_POWER_HOLDER_UNCONFIRMED", got)
	}
	if ops := screen.powered(); len(ops) != 0 {
		t.Fatalf("an unconfirmed shutdown reached the machine: %+v", ops)
	}
	// And the refusal names them, so the UI never has to ask a second time.
	if details, ok := body["details"].(map[string]any); !ok || details["holder"] != "p-9" {
		t.Errorf("details %v, want the holder named", body["details"])
	}

	code, _ = postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{
		"state": "shutdown", "confirmHolder": "p-9",
	})
	if code != http.StatusAccepted {
		t.Fatalf("named status %d, want 202", code)
	}
}

// Naming the wrong session is the stale-list case, and is refused exactly like
// naming nobody.
func TestSimPower_ShutdownRefusesAMismatchedHolder(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	leases := &fakeSimService{leases: []domain.SimLease{{
		UDID: testSimUDID, SessionID: "p-9", ExpiresAt: time.Now().Add(time.Hour),
	}}}
	srv := newScreenTestServer(t, leases, screen)

	code, body := postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{
		"state": "shutdown", "confirmHolder": "p-4",
	})
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	if got := errorCode(body); got != "SIM_POWER_HOLDER_UNCONFIRMED" {
		t.Errorf("code %q, want SIM_POWER_HOLDER_UNCONFIRMED", got)
	}
}

// This session's own device needs no name: the only party being asked is the
// person pressing the button, and the UI asks them.
func TestSimPower_ShutdownOfOwnDeviceNeedsNoHolderName(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	leases := &fakeSimService{leases: []domain.SimLease{{
		UDID: testSimUDID, SessionID: "p-1", ExpiresAt: time.Now().Add(time.Hour),
	}}}
	srv := newScreenTestServer(t, leases, screen)

	code, _ := postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{"state": "shutdown"})
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
}

func TestSimPower_RefusesAStateTheDeviceIsAlreadyIn(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	for _, tc := range []struct{ udid, state string }{
		{testSimUDID, "booted"},
		{otherSimUDID, "shutdown"},
	} {
		code, body := postJSON(t, powerURL(srv.URL, "p-1", tc.udid), map[string]any{"state": tc.state})
		if code != http.StatusConflict {
			t.Errorf("%s -> %s: status %d, want 409", tc.udid, tc.state, code)
		}
		if got := errorCode(body); got != "SIM_POWER_ALREADY" {
			t.Errorf("%s -> %s: code %q, want SIM_POWER_ALREADY", tc.udid, tc.state, got)
		}
	}
	if ops := screen.powered(); len(ops) != 0 {
		t.Fatalf("a no-op reached the machine: %+v", ops)
	}
}

func TestSimPower_UnknownDeviceIs404(t *testing.T) {
	srv := newScreenTestServer(t, &fakeSimService{}, &fakeScreen{listing: oneBooted()})
	code, _ := postJSON(t, powerURL(srv.URL, "p-1", "00000000-0000-0000-0000-000000000000"),
		map[string]any{"state": "booted"})
	if code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", code)
	}
}

func TestSimPower_UnknownStateIs400(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)
	for _, state := range []string{"", "reboot", "erase"} {
		code, _ := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": state})
		if code != http.StatusBadRequest {
			t.Errorf("state %q: status %d, want 400", state, code)
		}
	}
	if ops := screen.powered(); len(ops) != 0 {
		t.Fatalf("an unknown state reached the machine: %+v", ops)
	}
}

// A boot in flight rides the listing the pane already polls, so the control
// can say it is working without a second thing to ask.
func TestSimDevices_CarryAPowerOperationInFlight(t *testing.T) {
	started := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		otherSimUDID: {Op: simpower.Boot, State: simpower.Running, StartedAt: started},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Power *struct {
				Op     string `json:"op"`
				State  string `json:"state"`
				Reason string `json:"reason"`
			} `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, d := range out.Devices {
		switch d.UDID {
		case otherSimUDID:
			if d.Power == nil || d.Power.State != "running" || d.Power.Op != "boot" {
				t.Errorf("%s power = %+v, want a running boot", d.UDID, d.Power)
			}
		default:
			if d.Power != nil {
				t.Errorf("%s power = %+v, want none", d.UDID, d.Power)
			}
		}
	}
}

// A failure whose device has since reached the state anyway is dropped rather
// than left to contradict the device sitting up beside it.
func TestSimDevices_DropAFailureTheMachineHasMadeMoot(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		testSimUDID: {Op: simpower.Boot, State: simpower.Failed, Reason: "the simulator did not finish booting"},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Power *struct {
				State string `json:"state"`
			} `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, d := range out.Devices {
		if d.UDID == testSimUDID && d.Power != nil {
			t.Fatalf("a failed boot still reported on a device that is Booted: %+v", d.Power)
		}
	}
	screen.mu.Lock()
	cleared := len(screen.cleared)
	screen.mu.Unlock()
	if cleared == 0 {
		t.Error("the stale failure was hidden but not forgotten, so it comes back next poll")
	}
}

// A failure that still stands is reported with the machine's own reason, which
// is the whole point: the control must never sit on a spinner for ever.
func TestSimDevices_ReportAFailureThatStillStands(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		otherSimUDID: {Op: simpower.Boot, State: simpower.Failed, Reason: "Unable to boot device"},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Power *struct {
				State  string `json:"state"`
				Reason string `json:"reason"`
			} `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, d := range out.Devices {
		if d.UDID != otherSimUDID {
			continue
		}
		if d.Power == nil || d.Power.State != "failed" || d.Power.Reason != "Unable to boot device" {
			t.Fatalf("power = %+v, want the failure and its reason", d.Power)
		}
	}
}

func TestSimDevices_ReportABootedDeviceThatCameUpStock(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		testSimUDID: {
			Op:      simpower.Boot,
			State:   simpower.Warned,
			Profile: &simslim.Result{Outcome: simslim.Skipped, Reason: "simslim is not on PATH, so this device is stock"},
		},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Power *struct {
				State         string `json:"state"`
				Phase         string `json:"phase"`
				Profile       string `json:"profile"`
				ProfileReason string `json:"profileReason"`
			} `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}

	var found bool
	for _, d := range out.Devices {
		if d.UDID != testSimUDID {
			continue
		}
		found = true
		if d.Power == nil {
			t.Fatal("a stock device produced no power view, so the pane says nothing")
		}
		if d.Power.State != "warned" {
			t.Fatalf("state = %q, want warned - the boot itself worked", d.Power.State)
		}
		if d.Power.Profile != "skipped" {
			t.Fatalf("profile = %q, want skipped", d.Power.Profile)
		}
		if d.Power.ProfileReason == "" {
			t.Fatal("no reason reached the pane")
		}
	}
	if !found {
		t.Fatalf("%s missing from the listing", testSimUDID)
	}
}

func TestSimDevices_CarryTheSlimmingPhase(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		testSimUDID: {Op: simpower.Boot, State: simpower.Running, Phase: simpower.PhaseSlimming},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Power *struct {
				Phase string `json:"phase"`
			} `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, d := range out.Devices {
		if d.UDID == testSimUDID {
			if d.Power == nil || d.Power.Phase != "slimming" {
				t.Fatalf("power = %+v, want phase slimming", d.Power)
			}
		}
	}
}

// A Warned entry is ABOUT a device that is booted. The rule that drops a stale
// failure once the device reached the goal anyway must not touch it, or the
// warning is deleted the instant it becomes true.
func TestSimDevices_KeepAStockWarningOnABootedDevice(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		testSimUDID: {Op: simpower.Boot, State: simpower.Warned,
			Profile: &simslim.Result{Outcome: simslim.Failed, Reason: "it refused"}},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string          `json:"udid"`
			Power *map[string]any `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, d := range out.Devices {
		if d.UDID == testSimUDID && d.Power == nil {
			t.Fatal("the warning was cleared because the device reached Booted")
		}
	}

	screen.mu.Lock()
	cleared := len(screen.cleared)
	screen.mu.Unlock()
	if cleared != 0 {
		t.Fatalf("ClearPower was called %d times on a Warned entry", cleared)
	}
}

// errorCode digs the machine-readable code out of the locked error envelope.
func errorCode(body map[string]any) string {
	code, _ := body["code"].(string)
	return code
}

type fakeProfiles struct {
	profile *simslim.Profile
	err     error
	asked   domain.SessionID
}

func (f *fakeProfiles) SimProfileFor(_ context.Context, id domain.SessionID) (*simslim.Profile, error) {
	f.asked = id
	return f.profile, f.err
}

func TestSimPower_BootCarriesTheProjectsProfile(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	profiles := &fakeProfiles{profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}
	srv := newScreenTestServerWithProfiles(t, &fakeSimService{}, screen, profiles)

	code, _ := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}

	ops := screen.powered()
	if len(ops) != 1 || ops[0].Req == nil || ops[0].Req.Profile == nil {
		t.Fatalf("powered %+v, want a boot carrying the project's profile", ops)
	}
	if ops[0].Req.Profile.Keep[0] != "com.apple.apsd" {
		t.Fatalf("keep = %v", ops[0].Req.Profile.Keep)
	}
	if profiles.asked != domain.SessionID("p-1") {
		t.Fatalf("resolved for %q, want the session named in the route", profiles.asked)
	}
}

func TestSimPower_BootWithoutAConfiguredProfileSlimsNothing(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServerWithProfiles(t, &fakeSimService{}, screen, &fakeProfiles{})

	postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})

	ops := screen.powered()
	if len(ops) != 1 || ops[0].Req != nil {
		t.Fatalf("powered %+v, want a nil request for a project that does not slim", ops)
	}
}

// A resolver that failed must not end up looking like a project that does not
// slim, and must not fail the boot either.
func TestSimPower_BootCarriesAResolverFailure(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServerWithProfiles(t, &fakeSimService{}, screen,
		&fakeProfiles{err: errors.New("project 7 is degraded")})

	code, _ := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: a boot must not fail over a profile", code)
	}

	ops := screen.powered()
	if len(ops) != 1 || ops[0].Req == nil || ops[0].Req.Err == nil {
		t.Fatalf("powered %+v, want the resolver error carried through", ops)
	}
}

// A daemon with no resolver behaves exactly as it did before this feature.
func TestSimPower_BootWithoutAResolverSlimsNothing(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})

	ops := screen.powered()
	if len(ops) != 1 || ops[0].Req != nil {
		t.Fatalf("powered %+v, want a nil request with no resolver", ops)
	}
}

// Shutdown never resolves a profile; there is nothing to slim on the way down.
func TestSimPower_ShutdownNeverResolvesAProfile(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	profiles := &fakeProfiles{profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}
	srv := newScreenTestServerWithProfiles(t, &fakeSimService{}, screen, profiles)

	postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{"state": "shutdown"})

	if profiles.asked != "" {
		t.Fatalf("shutdown resolved a profile for %q", profiles.asked)
	}
}
