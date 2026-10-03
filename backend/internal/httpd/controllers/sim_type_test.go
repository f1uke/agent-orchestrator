package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	simsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
)

// typingRunner is a ready runner whose screen changes when it types.
type typingRunner struct {
	status   simrunner.Status
	awaitErr error
	before   simbridge.XCTestHierarchy
	after    simbridge.XCTestHierarchy
	typed    bool
	focus    simrunner.TypeAnswer
	answer   simrunner.TypeAnswer
	texts    []string
	waited   time.Duration
}

func (r *typingRunner) Read(context.Context, string, simrunner.ReadOptions) (simbridge.XCTestHierarchy, simrunner.Status, error) {
	if r.typed {
		return r.after, simrunner.Status{State: simrunner.StateReady}, nil
	}
	return r.before, simrunner.Status{State: simrunner.StateReady}, nil
}

func (r *typingRunner) Await(_ context.Context, _ string, wait time.Duration) (simrunner.Status, error) {
	r.waited = wait
	return r.status, r.awaitErr
}

func (r *typingRunner) Focus(context.Context, string) (simrunner.TypeAnswer, error) {
	return r.focus, nil
}

func (r *typingRunner) Type(_ context.Context, _, text string, _ simrunner.TypeOptions) (simrunner.TypeAnswer, error) {
	r.texts = append(r.texts, text)
	r.typed = true
	return r.answer, nil
}

func emailScreen(value, placeholder string) simbridge.XCTestHierarchy {
	return simbridge.XCTestHierarchy{
		Version: simrunner.WireVersion,
		Screen:  simbridge.Size{Width: 402, Height: 874},
		Apps: []simbridge.XCTestApp{{BundleID: "com.apple.SafariViewService", Tree: simbridge.XCTestNode{
			Type: "Application", Enabled: true, Frame: simbridge.Rect{Width: 402, Height: 874},
			Children: []simbridge.XCTestNode{{Type: "TextField", Label: "อีเมล", Value: value, Placeholder: placeholder,
				Enabled: true, Focused: true, Frame: simbridge.Rect{X: 74, Y: 436, Width: 254, Height: 53}}},
		}}},
	}
}

func readyTypingRunner() *typingRunner {
	field := &simrunner.TypeField{Type: "TextField", Label: "อีเมล"}
	return &typingRunner{
		status: simrunner.Status{State: simrunner.StateReady},
		before: emailScreen("example@email.com", ""),
		after:  emailScreen("qa@a.co", "example@email.com"),
		focus:  simrunner.TypeAnswer{Version: simrunner.WireVersion, App: "com.apple.SafariViewService", Keyboard: true, Field: field},
		answer: simrunner.TypeAnswer{Version: simrunner.WireVersion, App: "com.apple.SafariViewService", Keyboard: true,
			Field: field, Typed: true, TypingMs: 420},
	}
}

func postType(t *testing.T, runner controllers.SimRunner, leases simsvc.Manager, body string) (*http.Response, []byte) {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.SimTypeController{Runner: runner, Leases: leases}).Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/sessions/mer-9/sim-devices/"+testSimUDID+"/type", "application/json",
		bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func TestSimType_TypesInsideTheHoldAndReportsWhereItLanded(t *testing.T) {
	runner := readyTypingRunner()
	leases := &fakeSimService{}

	resp, body := postType(t, runner, leases, `{"text":"qa@a.co","waitMs":15000}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var out controllers.SimTypeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Landed.Evidence != "exact" || out.Landed.Shown != "qa@a.co" || out.Field.Label != "อีเมล" ||
		out.App != "com.apple.SafariViewService" || out.TypingMs != 420 {
		t.Fatalf("response = %+v", out)
	}
	if !strings.Contains(out.Detail, `now reads "qa@a.co"`) {
		t.Fatalf("detail = %q, want the field and what it reads", out.Detail)
	}
	// The hold is the session's, for a type, carrying the text a recording keeps.
	if leases.holds != 1 || leases.gotSession != "mer-9" || leases.gotIntent.Kind != "type" ||
		leases.gotIntent.Text != "qa@a.co" || !leases.gotPerformed {
		t.Fatalf("hold = %+v", leases)
	}
	if runner.waited != 15*time.Second {
		t.Fatalf("waited %s for the runner, want the 15s asked for", runner.waited)
	}
}

func TestSimType_ARunnerThatIsNotReadyIsA503AndTakesNoHold(t *testing.T) {
	runner := readyTypingRunner()
	runner.status = simrunner.Status{State: simrunner.StateStarting, Reason: "the XCTest runner is starting"}
	runner.awaitErr = simrunner.ErrNotReady
	leases := &fakeSimService{}

	resp, body := postType(t, runner, leases, `{"text":"hello"}`)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "SIM_RUNNER_NOT_READY") ||
		!strings.Contains(string(body), `"state":"starting"`) {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if leases.holds != 0 || len(runner.texts) != 0 {
		t.Fatal("a runner that is not ready must not cost a hold or a type")
	}
}

func TestSimType_NoRunnerAtAllIsUnavailable(t *testing.T) {
	resp, body := postType(t, nil, &fakeSimService{}, `{"text":"hello"}`)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), `"state":"unavailable"`) {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
}

func TestSimType_NoFocusIs422AndNotPerformed(t *testing.T) {
	runner := readyTypingRunner()
	runner.focus = simrunner.TypeAnswer{Version: simrunner.WireVersion, Checked: []string{"com.example.app"},
		Error: &simrunner.TypeError{Code: simrunner.TypeNoFocus, Message: "no element has keyboard focus"}}
	leases := &fakeSimService{}

	resp, body := postType(t, runner, leases, `{"text":"hello"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "SIM_TYPE_NO_FOCUS") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if len(runner.texts) != 0 || leases.gotPerformed {
		t.Fatal("nothing focused: nothing typed, and the hold must not record a type")
	}
}

func TestSimType_ASecureFieldTheKeyboardCannotServeIsUnsuitable(t *testing.T) {
	runner := readyTypingRunner()
	runner.focus.Field = &simrunner.TypeField{Type: "SecureTextField", Label: "password"}

	resp, body := postType(t, runner, &fakeSimService{}, `{"text":"รหัส"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "SIM_TYPE_UNSUITABLE") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
}

func TestSimType_ALeasedDeviceIsRefusedBeforeAnythingIsSent(t *testing.T) {
	runner := readyTypingRunner()
	leases := &fakeSimService{holdErr: &simsvc.HoldRefusedError{UDID: testSimUDID, Reason: simsvc.HoldRefusedNotLeased}}

	resp, body := postType(t, runner, leases, `{"text":"hello"}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "SIM_DEVICE_BUSY") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if len(runner.texts) != 0 {
		t.Fatal("typed on a device this session may not touch")
	}
}

func TestSimType_RefusesNothingAndTooMuch(t *testing.T) {
	for _, body := range []string{`{"text":""}`, `{"text":"` + strings.Repeat("x", 101) + `"}`} {
		resp, out := postType(t, readyTypingRunner(), &fakeSimService{}, body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d: %s", body[:20], resp.StatusCode, out)
		}
	}
}

// --- /paste: the edit menu, no key pressed ---------------------------------

func postPaste(t *testing.T, runner controllers.SimRunner, screen *fakeScreen, body string) (*http.Response, []byte) {
	t.Helper()
	r := chi.NewRouter()
	c := &controllers.SimTypeController{Runner: runner, Leases: &fakeSimService{}}
	if screen != nil {
		c.Screen = screen
	}
	c.Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/sessions/mer-9/sim-devices/"+testSimUDID+"/paste", "application/json",
		bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func TestSimPaste_ARunnerThatIsNotReadyIsA503TheCLIFallsBackOn(t *testing.T) {
	runner := readyTypingRunner()
	runner.status = simrunner.Status{State: simrunner.StateStarting}
	runner.awaitErr = simrunner.ErrNotReady
	resp, body := postPaste(t, runner, &fakeScreen{pasteboard: &fakePasteboard{}}, `{"text":"hello"}`)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "SIM_RUNNER_NOT_READY") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
}

func TestSimPaste_NoScreenOrNoTextIsRefused(t *testing.T) {
	if resp, body := postPaste(t, readyTypingRunner(), nil, `{"text":"x"}`); resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("no screen: status %d: %s", resp.StatusCode, body)
	}
	if resp, body := postPaste(t, readyTypingRunner(), &fakeScreen{pasteboard: &fakePasteboard{}}, `{"text":""}`); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("no text: status %d: %s", resp.StatusCode, body)
	}
}

// pastingDevice is a device the edit-menu paste can be driven against: the
// runner's screen moves on with each touch the bridge sends - the field, then
// the edit menu after the press, then the field holding the text after the
// tap on Paste.
type pastingDevice struct {
	*typingRunner
	screens []simbridge.XCTestHierarchy
	stage   int
	touches [][]simbridge.Event
}

func (d *pastingDevice) Read(context.Context, string, simrunner.ReadOptions) (simbridge.XCTestHierarchy, simrunner.Status, error) {
	return d.screens[min(d.stage, len(d.screens)-1)], simrunner.Status{State: simrunner.StateReady}, nil
}

func (d *pastingDevice) AX(context.Context, string) (simbridge.Snapshot, error) {
	return simbridge.Snapshot{}, errors.New("reads go through the runner")
}

func (d *pastingDevice) Perform(_ context.Context, _ string, events []simbridge.Event) (simbridge.PerformResult, error) {
	d.touches = append(d.touches, events)
	d.stage++
	return simbridge.PerformResult{}, nil
}

func (d *pastingDevice) Hold(context.Context, string, []simbridge.Event) error { return nil }

func editMenuScreen(items ...string) simbridge.XCTestHierarchy {
	screen := emailScreen("example@email.com", "")
	for i, label := range items {
		screen.Apps[0].Tree.Children = append(screen.Apps[0].Tree.Children, simbridge.XCTestNode{
			Type: "MenuItem", Label: label, Enabled: true,
			Frame: simbridge.Rect{X: 100 + 80*float64(i), Y: 380, Width: 70, Height: 40}})
	}
	return screen
}

func TestSimPaste_HoldsTheFieldTapsPasteAndProvesItWithNoKeyPressed(t *testing.T) {
	runner := readyTypingRunner()
	runner.focus.PasteLabels = []string{"วาง", "Paste"}
	device := &pastingDevice{typingRunner: runner, screens: []simbridge.XCTestHierarchy{
		emailScreen("example@email.com", ""),
		editMenuScreen("AutoFill", "วาง"),
		emailScreen("qa@a.co", "example@email.com"),
	}}
	pb := &fakePasteboard{content: "what the human copied"}
	leases := &fakeSimService{}
	r := chi.NewRouter()
	(&controllers.SimTypeController{Runner: device, Leases: leases,
		Screen: &fakeScreen{driver: device, pasteboard: pb}}).Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/sessions/mer-9/sim-devices/"+testSimUDID+"/paste", "application/json",
		bytes.NewBufferString(`{"text":"qa@a.co"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out controllers.SimPasteResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %v: %+v", resp.StatusCode, err, out)
	}
	if out.Via != "edit-menu" || out.MenuItem != "วาง" || out.Landed.Evidence != "exact" || !out.PasteboardRestored {
		t.Fatalf("response = %+v", out)
	}
	if len(device.touches) != 2 {
		t.Fatalf("touches = %+v, want the press and the tap on วาง", device.touches)
	}
	for _, events := range device.touches {
		for _, e := range events {
			if e.Kind == "key" {
				t.Fatalf("pressed a key %+v: a hardware key press minimizes the software keyboard", e)
			}
		}
	}
	if tap := device.touches[1][0]; tap.Y != 400.0/874 {
		t.Fatalf("tap = %+v, want the วาง item", tap)
	}
	if got := pb.written(); len(got) != 2 || got[0] != "qa@a.co" || got[1] != "what the human copied" {
		t.Fatalf("pasteboard writes %q", got)
	}
	if leases.holds != 1 || leases.gotIntent.Kind != "type" || !leases.gotPerformed {
		t.Fatalf("hold = %+v", leases)
	}
}

func TestSimPaste_AFieldWithNoPasteInItsMenuIsNoItem(t *testing.T) {
	runner := readyTypingRunner()
	device := &pastingDevice{typingRunner: runner, screens: []simbridge.XCTestHierarchy{
		emailScreen("example@email.com", ""), editMenuScreen("AutoFill"),
	}}
	pb := &fakePasteboard{content: "original"}
	r := chi.NewRouter()
	(&controllers.SimTypeController{Runner: device, Leases: &fakeSimService{},
		Screen: &fakeScreen{driver: device, pasteboard: pb}}).Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	resp, err := http.Post(srv.URL+"/sessions/mer-9/sim-devices/"+testSimUDID+"/paste", "application/json",
		bytes.NewBufferString(`{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(buf.String(), "SIM_PASTE_NO_ITEM") ||
		!strings.Contains(buf.String(), "AutoFill") {
		t.Fatalf("status %d: %s", resp.StatusCode, buf.String())
	}
	if pb.content != "original" {
		t.Fatalf("pasteboard left holding %q", pb.content)
	}
}
