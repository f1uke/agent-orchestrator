package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
	"github.com/aoagents/agent-orchestrator/backend/internal/simstream"
)

const otherSimUDID = "C4764B41-8F74-49C6-8766-A20EA46125BF"

// fakeScreen stands in for the machine: what simulators it has, whether it can
// capture, and whether it can touch a screen.
type fakeScreen struct {
	listing   simctl.Listing
	listErr   error
	driver    simbridge.Driver
	driverErr error
	// listed counts device-list reads. Reading it per drag move is what put a
	// `xcrun simctl list` in the middle of a drag, so it is counted.
	listed     atomic.Int64
	subscribed []string
	subErr     error

	// The guest keyboard the device reports, and what was asked about it.
	mu            sync.Mutex
	keyboard      string
	keyboardErr   error
	keyboardUDID  string
	keyboardCalls int
}

func (f *fakeScreen) keyboardAsked() (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keyboardUDID, f.keyboardCalls
}

func (f *fakeScreen) Devices(context.Context) (simctl.Listing, error) {
	f.listed.Add(1)
	return f.listing, f.listErr
}

func (f *fakeScreen) listings() int64 { return f.listed.Load() }

func (f *fakeScreen) Subscribe(_ context.Context, udid string) (<-chan simstream.Event, error) {
	f.subscribed = append(f.subscribed, udid)
	if f.subErr != nil {
		return nil, f.subErr
	}
	ch := make(chan simstream.Event)
	close(ch)
	return ch, nil
}

func (f *fakeScreen) Driver(context.Context) (simbridge.Driver, error) {
	if f.driverErr != nil {
		return nil, f.driverErr
	}
	return f.driver, nil
}

// Keyboard is what the guest would turn key presses into. A US keyboard by
// default, which is the setup where typing has always worked; the tests about
// the other setups set keyboard/keyboardErr themselves.
func (f *fakeScreen) Keyboard(_ context.Context, udid string) (simkeyboard.Mode, error) {
	f.mu.Lock()
	f.keyboardUDID = udid
	f.keyboardCalls++
	f.mu.Unlock()
	if f.keyboardErr != nil {
		return simkeyboard.Mode{}, f.keyboardErr
	}
	if f.keyboard == "" {
		return simkeyboard.ParseMode("en_US@sw=QWERTY;hw=Automatic"), nil
	}
	return simkeyboard.ParseMode(f.keyboard), nil
}

// fakeDriver records the events that reached the device.
type fakeDriver struct {
	mu      sync.Mutex
	events  [][]simbridge.Event
	err     error
	liftErr error
	holdErr error
}

func (d *fakeDriver) AX(context.Context, string) (simbridge.Snapshot, error) {
	return simbridge.Snapshot{}, errors.New("not used")
}

func (d *fakeDriver) Hold(_ context.Context, _ string, events []simbridge.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, events)
	return d.holdErr
}

// sent is the flat list of touch types that reached the device, which is what
// every test here is really asserting about.
func (d *fakeDriver) sent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []string{}
	for _, batch := range d.events {
		for _, e := range batch {
			out = append(out, e.Kind+":"+e.Type+e.Name)
		}
	}
	return out
}

func (d *fakeDriver) Perform(_ context.Context, _ string, events []simbridge.Event) (simbridge.PerformResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, events)
	if len(events) == 1 && events[0].Type == "end" {
		return simbridge.PerformResult{}, d.liftErr
	}
	return simbridge.PerformResult{}, d.err
}

func twoBooted() simctl.Listing {
	return simctl.Summarize([]simctl.Device{
		{UDID: testSimUDID, Name: "iPhone 17 Pro Max", Runtime: "iOS 26.3", State: "Booted", Available: true},
		{UDID: otherSimUDID, Name: "iPhone 17 Pro", Runtime: "iOS 26.3", State: "Booted", Available: true},
	})
}

func oneBooted() simctl.Listing {
	return simctl.Summarize([]simctl.Device{
		{UDID: testSimUDID, Name: "iPhone 17 Pro Max", Runtime: "iOS 26.3", State: "Booted", Available: true},
		{UDID: otherSimUDID, Name: "iPhone 17 Pro", Runtime: "iOS 26.3", State: "Shutdown", Available: true},
	})
}

func newScreenTestServer(t *testing.T, svc simsvc.Manager, screen httpd.SimScreen) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{Sim: svc, SimScreen: screen, SimDrags: simgesture.NewDrags()}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := http.Get(url) //nolint:noctx // test helper against httptest
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if out != nil && len(body) > 0 {
		_ = json.Unmarshal(body, out)
	}
	return res.StatusCode
}

func postJSON(t *testing.T, url string, in any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(in)
	res, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// A machine that cannot list simulators must say so, not answer with an empty
// list that reads as "you have none".
func TestSimDevices_WithoutAScreenIs501(t *testing.T) {
	srv := newScreenTestServer(t, &fakeSimService{}, nil)
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", nil); code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", code)
	}
}

// The rule the CLI enforces has to hold here too: two booted simulators mean no
// default, and the UI must be told why rather than handed a pick.
func TestSimDevices_TwoBootedHaveNoDefaultAndSayWhy(t *testing.T) {
	srv := newScreenTestServer(t, &fakeSimService{}, &fakeScreen{listing: twoBooted()})
	var out struct {
		Devices []struct {
			UDID    string `json:"udid"`
			Default bool   `json:"default"`
		} `json:"devices"`
		DefaultUDID   *string `json:"defaultUdid"`
		DefaultReason string  `json:"defaultReason"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if out.DefaultUDID != nil {
		t.Fatalf("two booted simulators must not produce a default, got %q", *out.DefaultUDID)
	}
	if !strings.Contains(out.DefaultReason, "2 simulators are booted") {
		t.Fatalf("reason must say why there is none, got %q", out.DefaultReason)
	}
	for _, d := range out.Devices {
		if d.Default {
			t.Fatalf("device %s must not be flagged default", d.UDID)
		}
	}
}

func TestSimDevices_OneBootedIsTheDefault(t *testing.T) {
	srv := newScreenTestServer(t, &fakeSimService{}, &fakeScreen{listing: oneBooted()})
	var out struct {
		DefaultUDID *string `json:"defaultUdid"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if out.DefaultUDID == nil || *out.DefaultUDID != testSimUDID {
		t.Fatalf("default not reported: %v", out.DefaultUDID)
	}
}

// Lease state has to be as honest here as in the terminal: held names the
// holder, and everything else is unknown WITH THE REASON - never "free".
func TestSimDevices_LeaseStateIsHeldOrUnknownWithAReason(t *testing.T) {
	now := time.Now().UTC()
	svc := &fakeSimService{leases: []domain.SimLease{{
		UDID: testSimUDID, SessionID: "agent-orchestrator-9", AcquiredAt: now, ExpiresAt: now.Add(9 * time.Minute),
	}}}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: twoBooted()})
	var out struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Lease struct {
				State  string `json:"state"`
				Holder string `json:"holder"`
				Reason string `json:"reason"`
			} `json:"lease"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	byUDID := map[string]string{}
	for _, d := range out.Devices {
		byUDID[d.UDID] = d.Lease.State
		if d.UDID == testSimUDID {
			if d.Lease.State != "held" || d.Lease.Holder != "agent-orchestrator-9" {
				t.Fatalf("held device reported as %+v", d.Lease)
			}
		}
		if d.UDID == otherSimUDID {
			if d.Lease.State != "unknown" {
				t.Fatalf("an unleased device must read unknown, got %q", d.Lease.State)
			}
			if !strings.Contains(d.Lease.Reason, "cannot see whether a human is driving it") {
				t.Fatalf("unknown must carry the honest reason, got %q", d.Lease.Reason)
			}
		}
	}
	if len(byUDID) != 2 {
		t.Fatalf("want both devices, got %v", byUDID)
	}
}

func TestSimGesture_WithoutAScreenIs501(t *testing.T) {
	srv := newScreenTestServer(t, &fakeSimService{}, nil)
	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "tap", "x": 0.5, "y": 0.5})
	if code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", code)
	}
}

func TestSimGesture_TapTakesTheHoldAndReachesTheDevice(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})
	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "tap", "x": 0.5, "y": 0.8})
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	if len(driver.events) != 1 {
		t.Fatalf("want one gesture on the device, got %d", len(driver.events))
	}
	if svc.gotUDID != testSimUDID || svc.gotSession != "p-1" {
		t.Fatalf("the hold was taken for %s/%s", svc.gotSession, svc.gotUDID)
	}
	if svc.gotToken != "tok-fake" {
		t.Fatalf("the hold must be given back, released token %q", svc.gotToken)
	}
}

// Typing is arbitrated like every other gesture, but it has one extra
// question to settle first, and the daemon route has to settle it the same way
// the CLI does: the guest decides what a key press becomes.
func TestSimGesture_TypeRefusesAGuestThatWouldRemapTheKeys(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	screen := &fakeScreen{listing: oneBooted(), driver: driver, keyboard: "th_TH@sw=Thai;hw=Automatic"}
	srv := newScreenTestServer(t, svc, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "fa12345"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %v", code, out)
	}
	if len(driver.events) != 0 {
		t.Fatalf("%d gesture(s) reached the device, want none", len(driver.events))
	}
	if svc.gotUDID != "" {
		t.Fatalf("the gesture hold was taken to refuse, for %s", svc.gotUDID)
	}
	if udid, calls := screen.keyboardAsked(); udid != testSimUDID || calls != 1 {
		t.Fatalf("asked %q about its keyboard %d time(s), want %s once", udid, calls, testSimUDID)
	}
}

func TestSimGesture_TypeOnAUSGuestReachesTheDevice(t *testing.T) {
	driver := &fakeDriver{}
	screen := &fakeScreen{listing: oneBooted(), driver: driver, keyboard: "en_US@sw=QWERTY;hw=Automatic"}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "fa12345"})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", code, out)
	}
	var keys int
	for _, sent := range driver.sent() {
		if strings.HasPrefix(sent, "key:") {
			keys++
		}
	}
	if keys == 0 {
		t.Fatalf("no key events reached the device: %v", driver.sent())
	}
	if detail, _ := out["detail"].(string); !strings.Contains(detail, "7 characters") {
		t.Fatalf("detail = %q, want the characters it can now promise", detail)
	}
}

func TestSimGesture_TypeRefusesAKeyboardItCouldNotRead(t *testing.T) {
	// Unknown is not US: a device that cannot say what it will do with the keys
	// is refused, and told how to say so anyway.
	driver := &fakeDriver{}
	screen := &fakeScreen{listing: oneBooted(), driver: driver, keyboardErr: errors.New("device is not booted")}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "hunter2"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %v", code, out)
	}
	if len(driver.events) != 0 {
		t.Fatal("nothing may reach the device when the mapping is unknown")
	}
	if message, _ := out["message"].(string); !strings.Contains(message, "rawKeys") {
		t.Fatalf("message %q does not say how to send the keys anyway", message)
	}
}

func TestSimGesture_TypeWithRawKeysSendsWithoutAskingTheDevice(t *testing.T) {
	driver := &fakeDriver{}
	screen := &fakeScreen{listing: oneBooted(), driver: driver, keyboard: "th_TH@sw=Thai;hw=Automatic"}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "fa12345", "rawKeys": true})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", code, out)
	}
	if len(driver.events) != 1 {
		t.Fatalf("want one gesture on the device, got %d", len(driver.events))
	}
	if _, calls := screen.keyboardAsked(); calls != 0 {
		t.Fatalf("asked the device about a mapping it had agreed to ignore (%d time(s))", calls)
	}
	// The response must not claim characters it cannot promise.
	if detail, _ := out["detail"].(string); !strings.Contains(detail, "key presses") {
		t.Fatalf("detail = %q, want it to say key presses", detail)
	}
}

func TestSimGesture_OnlyTypingAsksAboutTheKeyboard(t *testing.T) {
	// The probe is a subprocess on a real machine. Paying it for every tap in a
	// drag is what this route already learned not to do with `simctl list`.
	screen := &fakeScreen{listing: oneBooted(), driver: &fakeDriver{}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	for _, body := range []map[string]any{
		{"kind": "tap", "x": 0.5, "y": 0.5},
		{"kind": "swipe", "x": 0.5, "y": 0.8, "toX": 0.5, "toY": 0.2},
		{"kind": "button", "name": "home"},
	} {
		if code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture", body); code != http.StatusOK {
			t.Fatalf("%v: status %d: %v", body, code, out)
		}
	}
	if _, calls := screen.keyboardAsked(); calls != 0 {
		t.Fatalf("a gesture that sends no keys asked about the keyboard %d time(s)", calls)
	}
}

// A click from the desktop app is refused for exactly the reasons a command is,
// and the reason travels so the UI can say what to do about it.
func TestSimGesture_HoldRefusalIs409WithItsReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason simsvc.HoldRefusedReason
	}{
		{"not claimed", simsvc.HoldRefusedNotLeased},
		{"someone else holds it", simsvc.HoldRefusedLeasedByOther},
		{"a gesture is in flight", simsvc.HoldRefusedBusy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeSimService{holdErr: &simsvc.HoldRefusedError{
				UDID:   testSimUDID,
				Reason: tc.reason,
				Lease:  domain.SimLease{SessionID: "other-1", ExpiresAt: time.Now().Add(time.Minute)},
				Now:    time.Now(),
			}}
			driver := &fakeDriver{}
			srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})
			code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
				map[string]any{"kind": "tap", "x": 0.5, "y": 0.8})
			if code != http.StatusConflict {
				t.Fatalf("status %d, want 409", code)
			}
			details, _ := out["details"].(map[string]any)
			if details["reason"] != string(tc.reason) {
				t.Fatalf("reason %v, want %s", details["reason"], tc.reason)
			}
			if len(driver.events) != 0 {
				t.Fatal("a refused hold must leave the device untouched")
			}
		})
	}
}

func TestSimGesture_UnknownKindAndBadCoordinatesAreRejectedBeforeTheDevice(t *testing.T) {
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, &fakeSimService{}, &fakeScreen{listing: oneBooted(), driver: driver})
	for _, body := range []map[string]any{
		{"kind": "explode"},
		{"kind": "tap", "x": 4.2, "y": 0.5},
		{"kind": "button", "name": "self-destruct"},
	} {
		code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture", body)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("%v: status %d, want 422", body, code)
		}
	}
	if len(driver.events) != 0 {
		t.Fatal("nothing invalid may reach the device")
	}
}

// A device that is not booted cannot be driven, and saying so beats a bridge
// error nobody can read.
func TestSimGesture_UnknownOrShutDownDeviceIsRefused(t *testing.T) {
	srv := newScreenTestServer(t, &fakeSimService{}, &fakeScreen{listing: oneBooted(), driver: &fakeDriver{}})
	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+otherSimUDID+"/gesture",
		map[string]any{"kind": "tap", "x": 0.5, "y": 0.5})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("shut-down device: status %d, want 422", code)
	}
	code, _ = postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/00000000-0000-0000-0000-000000000000/gesture",
		map[string]any{"kind": "tap", "x": 0.5, "y": 0.5})
	if code != http.StatusNotFound {
		t.Fatalf("unknown device: status %d, want 404", code)
	}
}

// The worst outcome must be legible in the response, not only in a log.
func TestSimGesture_FailedGestureWhoseLiftAlsoFailedSaysTheDeviceMayBeWedged(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{err: errors.New("bridge exploded"), liftErr: errors.New("still gone")}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})
	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "tap", "x": 0.5, "y": 0.8})
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", code)
	}
	message, _ := out["message"].(string)
	if !strings.Contains(message, "finger") {
		t.Fatalf("the wedged-device warning must survive into the response, got %q", message)
	}
	if svc.gotToken != "tok-fake" {
		t.Fatal("the hold must come back even when the gesture failed")
	}
}

// A drag is one touch spread over three requests. What has to hold is that it
// takes exactly one hold for the whole thing, follows the finger while it is
// down, and gives the hold back once.
func TestSimGesture_ADragIsOneTouchUnderOneHold(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})
	url := srv.URL + "/api/v1/sessions/p-1/sim-devices/" + testSimUDID + "/gesture"

	for _, body := range []map[string]any{
		{"kind": "drag-begin", "x": 0.5, "y": 0.8},
		{"kind": "drag-move", "x": 0.5, "y": 0.6},
		{"kind": "drag-move", "x": 0.5, "y": 0.4},
		{"kind": "drag-end", "x": 0.5, "y": 0.4},
	} {
		if code, out := postJSON(t, url, body); code != http.StatusOK {
			t.Fatalf("%s: status %d: %v", body["kind"], code, out)
		}
	}

	want := []string{"touch:begin", "touch:move", "touch:move", "touch:end"}
	got := driver.sent()
	if len(got) != len(want) {
		t.Fatalf("events on the device = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events on the device = %v, want %v", got, want)
		}
	}
	if svc.holds != 1 {
		t.Fatalf("the drag took %d holds, want exactly one for the whole touch", svc.holds)
	}
	if svc.gotToken != "tok-fake" {
		t.Fatalf("the hold must be given back once the drag ends, released token %q", svc.gotToken)
	}
}

// The move is the one call that reaches a device without taking a hold of its
// own, so a move with no begin before it must reach nothing.
func TestSimGesture_ADragMoveWithNoDragIsRefused(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "drag-move", "x": 0.5, "y": 0.5})
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	if got := driver.sent(); len(got) != 0 {
		t.Fatalf("a stray move reached the device: %v", got)
	}
}

// A drag another session started may not be moved out from under it - the same
// refusal a single gesture gets, for the same reason.
func TestSimGesture_ADragCannotBeMovedByAnotherSession(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})
	base := srv.URL + "/api/v1/sessions/"

	if code, out := postJSON(t, base+"p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "drag-begin", "x": 0.5, "y": 0.8}); code != http.StatusOK {
		t.Fatalf("begin: status %d: %v", code, out)
	}
	code, _ := postJSON(t, base+"other-7/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "drag-move", "x": 0.1, "y": 0.1})
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	if got := driver.sent(); len(got) != 1 {
		t.Fatalf("another session reached the device mid-drag: %v", got)
	}
}

// Off-screen coordinates are refused for a drag by the same rule as for a tap,
// before anything is held or sent.
func TestSimGesture_ADragOffTheScreenIsRefusedBeforeTheHold(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "drag-begin", "x": 1.5, "y": 0.5})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", code)
	}
	if svc.holds != 0 {
		t.Fatalf("a refused drag took %d holds, want none", svc.holds)
	}
	if got := driver.sent(); len(got) != 0 {
		t.Fatalf("a refused drag reached the device: %v", got)
	}
}

// A drag must not shell out to `xcrun simctl list` per move. It did, and the
// listing cache expiring mid-drag was felt as the picture stalling for most of
// a second every couple of seconds. A move belongs to a touch already down, so
// there is nothing left to resolve.
func TestSimGesture_ADragStepDoesNotReadTheDeviceList(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	screen := &fakeScreen{listing: oneBooted(), driver: driver}
	srv := newScreenTestServer(t, svc, screen)
	url := srv.URL + "/api/v1/sessions/p-1/sim-devices/" + testSimUDID + "/gesture"

	if code, out := postJSON(t, url, map[string]any{"kind": "drag-begin", "x": 0.5, "y": 0.8}); code != http.StatusOK {
		t.Fatalf("begin: status %d: %v", code, out)
	}
	listedAfterBegin := screen.listings()

	for range 20 {
		if code, out := postJSON(t, url, map[string]any{"kind": "drag-move", "x": 0.5, "y": 0.5}); code != http.StatusOK {
			t.Fatalf("move: status %d: %v", code, out)
		}
	}
	if code, out := postJSON(t, url, map[string]any{"kind": "drag-end", "x": 0.5, "y": 0.5}); code != http.StatusOK {
		t.Fatalf("end: status %d: %v", code, out)
	}

	if got := screen.listings(); got != listedAfterBegin {
		t.Fatalf("the device list was read %d times during the drag, want none after the begin",
			got-listedAfterBegin)
	}
}

// And a move for a device with no drag open is still refused, without the
// listing being what refuses it.
func TestSimGesture_ADragStepForAnUnknownDeviceIsStillRefused(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/NOT-A-DEVICE/gesture",
		map[string]any{"kind": "drag-move", "x": 0.5, "y": 0.5})
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409", code)
	}
	if got := driver.sent(); len(got) != 0 {
		t.Fatalf("a move for an unknown device reached one: %v", got)
	}
}
