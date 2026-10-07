package controllers_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
)

// fakeFleet is which devices AO made, for the boot cap to read.
type fakeFleet struct {
	clones []domain.SimClone
	bases  []simsvc.BaseStatus
}

func (f *fakeFleet) Clones(context.Context) ([]domain.SimClone, error)  { return f.clones, nil }
func (f *fakeFleet) Bases(context.Context) ([]simsvc.BaseStatus, error) { return f.bases, nil }
func (f *fakeFleet) Claim(context.Context, domain.SessionID, string, string) (domain.SimClone, error) {
	return domain.SimClone{}, errors.New("not in this test")
}
func (f *fakeFleet) Remove(context.Context, domain.SessionID, string) (domain.SimClone, error) {
	return domain.SimClone{}, errors.New("not in this test")
}

type fixedCap struct{ max int }

func (f fixedCap) Get() simpower.Settings    { return simpower.Settings{MaxBooted: f.max} }
func (fixedCap) Set(simpower.Settings) error { return errors.New("read only") }

func newCapTestServer(t *testing.T, leases simsvc.Manager, screen httpd.SimScreen, fleet controllers.SimFleet, boot controllers.SimBootSettingsService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{Sim: leases, SimScreen: screen, SimDrags: simgesture.NewDrags(), SimFleet: fleet, SimBoot: boot},
		httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// oneIdleClone is a machine at a cap of one: p-3's clone is up and nobody holds
// it, and otherSimUDID is the device somebody wants booted.
func oneIdleClone() (*fakeScreen, *fakeFleet) {
	return &fakeScreen{listing: oneBooted(), settle: true},
		&fakeFleet{clones: []domain.SimClone{{UDID: testSimUDID, SessionID: "p-3", Label: domain.SimPrimaryLabel, Name: "AO p-3 primary"}}}
}

// The Device tab asks without makeRoom and gets the same cap an agent does -
// told plainly, with who holds it - and nothing is shut down on the human's
// behalf: they can see the devices and choose.
func TestSimPower_BootPastTheCapIsRefusedWithWhoHoldsIt(t *testing.T) {
	screen, fleet := oneIdleClone()
	srv := newCapTestServer(t, &fakeSimService{}, screen, fleet, fixedCap{max: 1})

	code, body := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	if got := errorCode(body); got != "SIM_BOOT_CAP_REACHED" {
		t.Fatalf("code %q, want SIM_BOOT_CAP_REACHED", got)
	}
	if ops := screen.powered(); len(ops) != 0 {
		t.Fatalf("powered %+v; a refused boot changes nothing, and the tab never shuts down for the human", ops)
	}
	details, _ := body["details"].(map[string]any)
	holders, _ := details["holders"].([]any)
	if details["maxBooted"] != float64(1) || len(holders) != 1 {
		t.Fatalf("details %v, want the cap and the one device holding it", details)
	}
	holder, _ := holders[0].(map[string]any)
	if holder["udid"] != testSimUDID || holder["cloneOf"] != "p-3" || holder["idle"] != true {
		t.Fatalf("holder %v, want p-3's idle clone", holder)
	}
}

// `ao sim boot` asks with makeRoom: the idle clone goes down first, under a
// lease held only while it does, and the boot follows.
func TestSimPower_MakeRoomShutsDownTheIdleCloneThenBoots(t *testing.T) {
	screen, fleet := oneIdleClone()
	leases := &fakeSimService{}
	srv := newCapTestServer(t, leases, screen, fleet, fixedCap{max: 1})

	code, body := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted", "makeRoom": true})
	if code != http.StatusAccepted {
		t.Fatalf("status %d (%v), want 202", code, body)
	}
	ops := screen.powered()
	if len(ops) != 2 || ops[0].Op != simpower.Shutdown || ops[0].UDID != testSimUDID ||
		ops[1].Op != simpower.Boot || ops[1].UDID != otherSimUDID {
		t.Fatalf("powered %+v, want the idle clone shut down and then the boot", ops)
	}
	if leases.gotTTL != simpower.ShutdownTimeout || len(leases.released) != 1 || leases.released[0] != testSimUDID {
		t.Fatalf("lease ttl %s, released %v; the clone must be held for exactly the shutdown", leases.gotTTL, leases.released)
	}
	made, _ := body["madeRoom"].([]any)
	if len(made) != 1 {
		t.Fatalf("madeRoom %v, want the clone that went down reported", body["madeRoom"])
	}
	if m, _ := made[0].(map[string]any); m["udid"] != testSimUDID || m["sessionId"] != "p-3" {
		t.Fatalf("madeRoom[0] = %v, want p-3's clone", m)
	}
}

// A clone somebody holds is not idle, however long ago it booted.
func TestSimPower_MakeRoomNeverShutsDownALeasedClone(t *testing.T) {
	screen, fleet := oneIdleClone()
	leases := &fakeSimService{leases: []domain.SimLease{{
		UDID: testSimUDID, SessionID: "p-3", ExpiresAt: time.Now().Add(time.Hour),
	}}}
	srv := newCapTestServer(t, leases, screen, fleet, fixedCap{max: 1})

	code, body := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted", "makeRoom": true})
	if code != http.StatusConflict || errorCode(body) != "SIM_BOOT_CAP_REACHED" {
		t.Fatalf("status %d code %q, want the cap's refusal", code, errorCode(body))
	}
	if ops := screen.powered(); len(ops) != 0 {
		t.Fatalf("powered %+v; p-3 is on that device", ops)
	}
}

// Without a setting the cap is the default four: four up refuses a fifth, and
// three up leaves room.
func TestSimPower_TheCapDefaultsToFour(t *testing.T) {
	devices := []simctl.Device{
		{UDID: "A0000000-0000-0000-0000-000000000001", Name: "one", State: "Booted", Available: true},
		{UDID: "A0000000-0000-0000-0000-000000000002", Name: "two", State: "Booted", Available: true},
		{UDID: "A0000000-0000-0000-0000-000000000003", Name: "three", State: "Booted", Available: true},
		{UDID: otherSimUDID, Name: "iPhone 17 Pro", State: "Shutdown", Available: true},
	}
	screen := &fakeScreen{listing: simctl.Summarize(devices)}
	srv := newCapTestServer(t, &fakeSimService{}, screen, nil, nil)
	if code, body := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"}); code != http.StatusAccepted {
		t.Fatalf("three up: status %d (%v), want 202 under the default cap", code, body)
	}

	devices = append(devices, simctl.Device{UDID: "A0000000-0000-0000-0000-000000000004", Name: "four", State: "Booted", Available: true})
	screen = &fakeScreen{listing: simctl.Summarize(devices)}
	srv = newCapTestServer(t, &fakeSimService{}, screen, nil, nil)
	code, body := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted", "makeRoom": true})
	if code != http.StatusConflict || errorCode(body) != "SIM_BOOT_CAP_REACHED" {
		t.Fatalf("four up: status %d code %q, want the cap's refusal", code, errorCode(body))
	}
	if details, _ := body["details"].(map[string]any); details["maxBooted"] != float64(simpower.DefaultMaxBooted) {
		t.Fatalf("details %v, want the default cap reported", details)
	}
}

// The cap is a setting a person changes, and one that would refuse every boot
// is refused itself.
func TestSettings_SimBootReadsAndWritesTheCap(t *testing.T) {
	store, err := simpower.NewSettingsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := newCapTestServer(t, &fakeSimService{}, nil, nil, store)

	var got controllers.SimBootSettingsResponse
	if code := getJSON(t, srv.URL+"/api/v1/settings/sim-boot", &got); code != http.StatusOK || got.MaxBooted != 4 || got.DefaultMaxBooted != 4 {
		t.Fatalf("GET status %d body %+v, want the default cap of 4", code, got)
	}
	if code := putJSON(t, srv.URL+"/api/v1/settings/sim-boot", `{"maxBooted":0}`); code != http.StatusBadRequest {
		t.Fatalf("PUT 0: status %d, want 400", code)
	}
	if code := putJSON(t, srv.URL+"/api/v1/settings/sim-boot", `{"maxBooted":6}`); code != http.StatusOK {
		t.Fatalf("PUT 6: status %d, want 200", code)
	}
	if store.Get().MaxBooted != 6 {
		t.Fatalf("stored cap %d, want 6", store.Get().MaxBooted)
	}
}

func putJSON(t *testing.T, url, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewBufferString(body)) //nolint:noctx // test helper
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}
