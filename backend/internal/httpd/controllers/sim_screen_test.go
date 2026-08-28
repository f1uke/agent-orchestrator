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
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simkeyboard"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
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
	pasteboard    *fakePasteboard

	// The power surface: what was asked of the machine, and what the machine
	// is reporting back about operations already in flight.
	powerErr    error
	powerOps    []powerCall
	powerStatus map[string]simpower.Status
	cleared     []string
}

// powerCall is one boot or shutdown the route asked for, with the callback it
// passed - the shutdown path uses that callback to give a lease back, so a
// test has to be able to fire it.
type powerCall struct {
	UDID string
	Op   simpower.Op
	Req  *simslim.Request
	Done func()
}

func (f *fakeScreen) StartPower(_ context.Context, udid string, op simpower.Op, req *simslim.Request, done func()) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.powerErr != nil {
		return f.powerErr
	}
	f.powerOps = append(f.powerOps, powerCall{UDID: udid, Op: op, Req: req, Done: done})
	return nil
}

func (f *fakeScreen) PowerStatus() map[string]simpower.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.powerStatus
}

func (f *fakeScreen) ClearPower(udid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, udid)
}

func (f *fakeScreen) powered() []powerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]powerCall, len(f.powerOps))
	copy(out, f.powerOps)
	return out
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

// Pasteboard is the guest clipboard. nil when a test has not set one up, which
// is how "this machine cannot paste" is exercised.
func (f *fakeScreen) Pasteboard() simpaste.Pasteboard {
	if f.pasteboard == nil {
		return nil
	}
	return f.pasteboard
}

// fakePasteboard remembers what the guest clipboard was given, so a test can
// prove the payload was put there and then taken back off.
type fakePasteboard struct {
	mu      sync.Mutex
	content string
	writes  []string
}

func (p *fakePasteboard) Read(context.Context, string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.content, nil
}

func (p *fakePasteboard) Write(_ context.Context, _, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.content = text
	p.writes = append(p.writes, text)
	return nil
}

func (p *fakePasteboard) written() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.writes...)
}

// fakeDriver records the events that reached the device.
type fakeDriver struct {
	mu        sync.Mutex
	events    [][]simbridge.Event
	err       error
	liftErr   error
	holdErr   error
	snapshots []simbridge.Snapshot
}

// AX hands out the queued snapshots in order, which is how a paste is proven:
// the field is empty before and holds the payload after.
func (d *fakeDriver) AX(context.Context, string) (simbridge.Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.snapshots) == 0 {
		return simbridge.Snapshot{}, errors.New("not used")
	}
	next := d.snapshots[0]
	if len(d.snapshots) > 1 {
		d.snapshots = d.snapshots[1:]
	}
	return next, nil
}

// pasteLands is a driver whose screen gains text where a paste would put it.
func pasteLands(text string) *fakeDriver {
	return &fakeDriver{snapshots: []simbridge.Snapshot{
		{Elements: []simbridge.Element{{Path: "0.1", Value: ""}}},
		{Elements: []simbridge.Element{{Path: "0.1", Value: text}}},
	}}
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
	return newScreenTestServerWithProfiles(t, svc, screen, nil)
}

func newScreenTestServerWithProfiles(t *testing.T, svc simsvc.Manager, screen httpd.SimScreen, profiles controllers.SimProfileResolver) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{Sim: svc, SimScreen: screen, SimDrags: simgesture.NewDrags(), SimProfiles: profiles},
		httpd.ControlDeps{}))
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
func TestSimGesture_TypePastesWhenTheGuestWouldRemapTheKeys(t *testing.T) {
	// The daemon route reaches the device the same way the CLI does. Two answers
	// to "is this keyboard safe" would mean one of these surfaces silently types
	// the wrong characters again, which is the bug being fixed.
	svc := &fakeSimService{}
	driver := pasteLands("fa12345")
	pb := &fakePasteboard{content: "what the human had copied"}
	screen := &fakeScreen{listing: oneBooted(), driver: driver,
		keyboard: "th_TH@sw=Thai;hw=Automatic", pasteboard: pb}
	srv := newScreenTestServer(t, svc, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "fa12345"})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", code, out)
	}
	if writes := pb.written(); len(writes) != 2 || writes[0] != "fa12345" {
		t.Fatalf("pasteboard writes = %q, want the payload then the restore", writes)
	}
	if pb.content != "what the human had copied" {
		t.Fatalf("guest pasteboard left holding %q", pb.content)
	}
	for _, sent := range driver.sent() {
		if strings.HasPrefix(sent, "key:") && !strings.Contains(sent, "down") && !strings.Contains(sent, "up") {
			t.Fatalf("unexpected event %q", sent)
		}
	}
	if detail, _ := out["detail"].(string); !strings.Contains(detail, "pasted") {
		t.Fatalf("detail = %q, must say the route taken", detail)
	}
	if udid, calls := screen.keyboardAsked(); udid != testSimUDID || calls != 1 {
		t.Fatalf("asked %q about its keyboard %d time(s)", udid, calls)
	}
}

func TestSimGesture_TypePastesWhenTheKeyboardCouldNotBeRead(t *testing.T) {
	// Unknown is not US, but it is not a dead end either: the pasteboard does
	// not care what the input mode is.
	pb := &fakePasteboard{content: "original"}
	screen := &fakeScreen{listing: oneBooted(), driver: pasteLands("hunter2"),
		keyboardErr: errors.New("device has not shown a keyboard"), pasteboard: pb}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "hunter2"})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", code, out)
	}
	if writes := pb.written(); len(writes) == 0 || writes[0] != "hunter2" {
		t.Fatalf("pasteboard writes = %q", writes)
	}
}

func TestSimGesture_TypeIs501WhenTheMachineCannotPaste(t *testing.T) {
	// A daemon with no pasteboard cannot deliver the characters and must say so,
	// rather than falling back to keys that would arrive as something else.
	screen := &fakeScreen{listing: oneBooted(), driver: &fakeDriver{}, keyboard: "th_TH@sw=Thai;hw=Automatic"}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "fa12345"})
	if code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501: %v", code, out)
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

func TestSimGesture_ForwardedKeysReachTheDeviceWithoutAskingAboutTheKeyboard(t *testing.T) {
	// 🗝 The fix, at the route. "ด" has no US key, so planning from the TEXT
	// would take the pasteboard - measured at 2.7-3.7 s, with two screen reads
	// to prove it landed. The human pressed the position a US keyboard prints
	// `f` on, and the guest's Thai mode turns that back into "ด" by itself.
	driver := &fakeDriver{}
	pb := &fakePasteboard{content: "what the human had copied"}
	screen := &fakeScreen{listing: oneBooted(), driver: driver,
		keyboard: "th_TH@sw=Thai;hw=Automatic", pasteboard: pb}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "ด", "keys": []map[string]any{{"code": "KeyF"}}})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", code, out)
	}
	if len(driver.events) != 1 {
		t.Fatalf("want one gesture on the device, got %d", len(driver.events))
	}
	// The probe is a subprocess inside the guest and it is the only reason
	// typing was ever slow. Forwarding must not pay for it.
	if _, calls := screen.keyboardAsked(); calls != 0 {
		t.Fatalf("forwarding a key asked the guest about its input mode %d time(s)", calls)
	}
	if writes := pb.written(); len(writes) != 0 {
		t.Fatalf("forwarding a key cycled the guest pasteboard: %q", writes)
	}
	// It promises a key press. Saying "1 character" would be the claim the
	// whole layout fix exists to stop anything making.
	if detail, _ := out["detail"].(string); !strings.Contains(detail, "key presses forwarded") {
		t.Fatalf("detail = %q, want it to say what was actually sent", detail)
	}
}

func TestSimGesture_ForwardedKeysCarryShiftAsPartOfThePress(t *testing.T) {
	// On a Thai keyboard shift produces a DIFFERENT Thai letter, so dropping it
	// would type the wrong character rather than the same one in lower case.
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, &fakeSimService{},
		&fakeScreen{listing: oneBooted(), driver: driver, keyboard: "th_TH@sw=Thai;hw=Automatic"})

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "ศ", "keys": []map[string]any{{"code": "KeyL", "shift": true}}})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", code, out)
	}
	events := driver.events[0]
	if len(events) == 0 || events[0].Usage != 225 || events[0].Type != "down" {
		t.Fatalf("first event = %+v, want left shift held first", events[0])
	}
	var released bool
	for _, e := range events {
		if e.Usage == 225 && e.Type == "up" {
			released = true
		}
	}
	if !released {
		t.Fatal("shift left held is the keyboard's stuck finger: every later keystroke would arrive shifted")
	}
}

func TestSimGesture_AKeyThatCannotBeForwardedStillDeliversTheCharacter(t *testing.T) {
	// The position is unknown; the character is not. The guest is asked after
	// all, and the ordinary planned route carries it.
	driver := pasteLands("ด")
	screen := &fakeScreen{listing: oneBooted(), driver: driver,
		keyboard: "th_TH@sw=Thai;hw=Automatic", pasteboard: &fakePasteboard{}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "ด", "keys": []map[string]any{{"code": "IntlRo"}}})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", code, out)
	}
	if _, calls := screen.keyboardAsked(); calls != 1 {
		t.Fatalf("the guest was asked %d time(s); a route that cannot be forwarded has to be planned", calls)
	}
	if detail, _ := out["detail"].(string); !strings.Contains(detail, "pasted") {
		t.Fatalf("detail = %q, want the character delivered by a route that can carry it", detail)
	}
}

func TestSimGesture_KeysThatDoNotAccountForTheTextAreRefusedBeforeTheDevice(t *testing.T) {
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, &fakeSimService{}, &fakeScreen{listing: oneBooted(), driver: driver})
	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "type", "text": "ดฟ", "keys": []map[string]any{{"code": "KeyF"}}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", code)
	}
	if len(driver.events) != 0 {
		t.Fatal("a request whose keys and text disagree must not reach the device")
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

// --- keyboard keys ----------------------------------------------------------

// 🗝 A named key is sent as a key press and is NOT planned like text.
//
// The distinction is the layout bug's own lesson turned around. A CHARACTER has
// to be planned, because the guest remaps character keys according to its input
// mode - which is how `ao sim type "fa12345"` once arrived as Thai gibberish.
// A key that produces no character has nothing to remap, so it goes straight
// through, and asking the device about its keyboard for one would be paying a
// subprocess for an answer that cannot change the outcome.
func TestSimGesture_NamedKeysAreSentDirectlyAndNeverAskAboutTheKeyboard(t *testing.T) {
	for _, name := range []string{"enter", "backspace", "tab", "arrow-up", "arrow-down", "arrow-left", "arrow-right"} {
		t.Run(name, func(t *testing.T) {
			driver := &fakeDriver{}
			screen := &fakeScreen{listing: oneBooted(), driver: driver}
			srv := newScreenTestServer(t, &fakeSimService{}, screen)

			code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
				map[string]any{"kind": "key", "name": name})
			if code != http.StatusOK {
				t.Fatalf("status %d: %v", code, out)
			}
			if _, calls := screen.keyboardAsked(); calls != 0 {
				t.Errorf("a key that produces no character asked about the keyboard %d time(s)", calls)
			}
			if detail, _ := out["detail"].(string); detail != name {
				t.Errorf("detail = %q, want %q", detail, name)
			}
			// Down then up: a key left held is the keyboard's stuck finger, and
			// every later keystroke would arrive with it applied.
			if len(driver.events) != 1 {
				t.Fatalf("driver was called %d time(s), want once", len(driver.events))
			}
			events := driver.events[0]
			if len(events) != 2 {
				t.Fatalf("events = %d, want a down and an up: %+v", len(events), events)
			}
			if events[0].Kind != "key" || events[0].Type != "down" {
				t.Errorf("first event = %+v, want a key down", events[0])
			}
			if events[1].Type != "up" || events[1].Usage != events[0].Usage {
				t.Errorf("second event = %+v, want the same key released", events[1])
			}
		})
	}
}

func TestSimGesture_UnknownKeyIsRefusedBeforeTheDevice(t *testing.T) {
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, &fakeSimService{}, &fakeScreen{listing: oneBooted(), driver: driver})
	code, _ := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "key", "name": "page-down"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", code)
	}
	if len(driver.events) != 0 {
		t.Fatal("an unknown key must not reach the device")
	}
}

// --- the keyboard the pane asks about before typing --------------------------
//
// The pane asks this when the human focuses the device surface, which is the
// moment BEFORE they type - so the ~935 ms the guest takes to answer is spent
// while their hands are still moving, instead of in front of the first
// character. What it gets back is a pacing decision, never a routing one: the
// daemon still plans every `type` request itself.

func TestSimKeyboard_WithoutAScreenIs501(t *testing.T) {
	srv := newScreenTestServer(t, &fakeSimService{}, nil)
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices/"+testSimUDID+"/keyboard", nil); code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", code)
	}
}

func TestSimKeyboard_AUSGuestTakesKeyPressesOneAtATime(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), keyboard: "en_US@sw=QWERTY;hw=Automatic"}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		UDID         string `json:"udid"`
		Mode         string `json:"mode"`
		SendsUSASCII bool   `json:"sendsUSASCII"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices/"+testSimUDID+"/keyboard", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if !out.SendsUSASCII {
		t.Fatal("a US guest takes key presses faithfully, so characters may go one at a time")
	}
	if udid, calls := screen.keyboardAsked(); udid != testSimUDID || calls != 1 {
		t.Fatalf("asked %q %d times, want the named device asked once", udid, calls)
	}
}

// A guest that would remap the keys must never be paced one character at a
// time: every one of those is a pasteboard round trip measured at 3.1-3.4 s.
func TestSimKeyboard_ARemappingGuestIsPacedInBursts(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), keyboard: "th_TH@sw=Thai;hw=Automatic"}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		SendsUSASCII bool `json:"sendsUSASCII"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices/"+testSimUDID+"/keyboard", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if out.SendsUSASCII {
		t.Fatal("a Thai guest remaps key presses; pacing it per character would paste per character")
	}
}

// ⚠ A device that will not say is an ANSWER here, not a failure: the pane's
// question is "may I send these one at a time", and "I could not find out" is
// safely no. Returning an error instead would leave the pane with nothing to
// pace by at exactly the moment it is about to be typed into.
func TestSimKeyboard_AGuestThatWillNotSayIsAnAnswerNotAnError(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), keyboardErr: errors.New("device not booted")}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		SendsUSASCII bool   `json:"sendsUSASCII"`
		Reason       string `json:"reason"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices/"+testSimUDID+"/keyboard", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200 - not knowing is an answer", code)
	}
	if out.SendsUSASCII {
		t.Fatal("an unknown keyboard must never be paced as a faithful one")
	}
	if out.Reason == "" {
		t.Fatal("a no needs its reason, or nobody can tell a Thai guest from an unreadable one")
	}
}

// A pinch is the same held touch as a drag with a second contact in it: one
// hold for the whole gesture, both fingers in every frame, and both released
// together. It is the Device tab's Option-drag, following a human's hand across
// as many requests as their hand takes.
func TestSimGesture_APinchIsOneHeldTouchWithTwoContacts(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})
	url := srv.URL + "/api/v1/sessions/p-1/sim-devices/" + testSimUDID + "/gesture"

	for _, body := range []map[string]any{
		{"kind": "pinch-begin", "x": 0.5, "y": 0.4, "x2": 0.5, "y2": 0.6},
		{"kind": "pinch-move", "x": 0.5, "y": 0.3, "x2": 0.5, "y2": 0.7},
		{"kind": "pinch-end", "x": 0.5, "y": 0.2, "x2": 0.5, "y2": 0.8},
	} {
		if code, out := postJSON(t, url, body); code != http.StatusOK {
			t.Fatalf("%s: status %d: %v", body["kind"], code, out)
		}
	}

	want := []string{"multitouch:begin", "multitouch:move", "multitouch:end"}
	got := driver.sent()
	if len(got) != len(want) {
		t.Fatalf("events on the device = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events on the device = %v, want %v", got, want)
		}
	}
	// ⚠ Both contacts, in every frame. A pinch whose second point never left
	// this process would send a perfectly ordinary one-finger drag and report
	// success - which is indistinguishable from a pinch that worked until
	// somebody looks at the screen.
	for i, batch := range driver.events {
		if len(batch) != 1 {
			t.Fatalf("frame %d carried %d events, want one frame per step", i, len(batch))
		}
		e := batch[0]
		if e.X != 0.5 || e.X2 != 0.5 || e.Y2 <= e.Y {
			t.Fatalf("frame %d = %+v, want two contacts with the second below the first", i, e)
		}
	}
	if svc.holds != 1 {
		t.Fatalf("the pinch took %d holds, want exactly one for the whole touch", svc.holds)
	}
	if svc.gotToken != "tok-fake" {
		t.Fatalf("the hold must be given back once the pinch ends, released token %q", svc.gotToken)
	}
}

// A held touch may not change how many fingers are on the screen, and the route
// says so in its own words rather than adapting: the contact that vanished was
// never lifted and the one that appeared never landed. What it must NOT do is
// leave the touch it interrupted holding the device.
func TestSimGesture_AHeldTouchThatChangesItsFingerCountIsRefusedAndReleased(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})
	url := srv.URL + "/api/v1/sessions/p-1/sim-devices/" + testSimUDID + "/gesture"

	if code, out := postJSON(t, url, map[string]any{"kind": "drag-begin", "x": 0.5, "y": 0.8}); code != http.StatusOK {
		t.Fatalf("drag-begin: status %d: %v", code, out)
	}
	code, out := postJSON(t, url, map[string]any{"kind": "pinch-move", "x": 0.4, "y": 0.5, "x2": 0.6, "y2": 0.5})
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %v", code, out)
	}
	// The one finger that WAS down comes back up, at the place it was last
	// seen rather than at either of the two the caller described.
	if got := driver.sent(); len(got) != 2 || got[0] != "touch:begin" || got[1] != "touch:end" {
		t.Fatalf("events on the device = %v, want the one finger that was down to be lifted", got)
	}
	if svc.gotToken != "tok-fake" {
		t.Fatal("the hold must be given back when a held touch is cut off")
	}
}

// Two contacts on the same spot arrive as one, which reads exactly like a pinch
// that worked while nothing zoomed. It is refused before anything is held.
func TestSimGesture_APinchWhoseFingersWouldLandAsOneIsRefused(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "pinch-begin", "x": 0.5, "y": 0.5, "x2": 0.5, "y2": 0.501})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %v", code, out)
	}
	if svc.holds != 0 {
		t.Fatalf("a refused pinch took %d holds, want none", svc.holds)
	}
	if got := driver.sent(); len(got) != 0 {
		t.Fatalf("a refused pinch reached the device: %v", got)
	}
}

// Off-screen is refused per contact, and the message says WHICH finger - a
// pinch has two and "coordinates must be normalized" would not say which one to
// move.
func TestSimGesture_APinchNamesTheFingerThatIsOffScreen(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "pinch-begin", "x": 0.5, "y": 0.5, "x2": 0.5, "y2": 1.4})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422: %v", code, out)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "finger 2") {
		t.Fatalf("message = %q, want it to name the finger that is off screen", msg)
	}
	if svc.holds != 0 {
		t.Fatalf("a refused pinch took %d holds, want none", svc.holds)
	}
}

// What a recording is told about a pinch is the point BETWEEN the fingers - the
// same point `ao sim pinch` records - because that is what the gesture is about
// and neither finger alone describes it.
func TestSimGesture_APinchIsRecordedAtThePointBetweenItsFingers(t *testing.T) {
	svc := &fakeSimService{}
	driver := &fakeDriver{}
	srv := newScreenTestServer(t, svc, &fakeScreen{listing: oneBooted(), driver: driver})

	if code, out := postJSON(t, srv.URL+"/api/v1/sessions/p-1/sim-devices/"+testSimUDID+"/gesture",
		map[string]any{"kind": "pinch-begin", "x": 0.2, "y": 0.3, "x2": 0.8, "y2": 0.7}); code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	if got := svc.gotIntent; got.X != 0.5 || got.Y != 0.5 {
		t.Fatalf("recorded intent at (%g, %g), want the midpoint (0.5, 0.5)", got.X, got.Y)
	}
	if svc.gotIntent.Kind != "pinch-begin" {
		t.Fatalf("recorded intent kind = %q, want pinch-begin", svc.gotIntent.Kind)
	}
}
