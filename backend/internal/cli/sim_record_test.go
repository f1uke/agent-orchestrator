package cli

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

// --- ao sim record start ----------------------------------------------------

// `record start` must never acquire a lease on the caller's behalf - it only
// explains the daemon's refusal. Simulating "not_leased" here (no distinct
// holder to name) is the plainest version of that refusal.
func TestSimRecordStart_RequiresALeaseAndNeverAcquiresOne(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStartStatus = http.StatusConflict
	daemon.recordStartBody = `{"error":"conflict","code":"SIM_RECORDING_REFUSED",` +
		`"message":"simulator ` + simUDIDProMax + ` is not claimed by this session, so it may not be recorded",` +
		`"details":{"udid":"` + simUDIDProMax + `","reason":"not_leased"}}`

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start")
	if err == nil {
		t.Fatalf("recording without a lease must fail, got success:\n%s", out)
	}
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1", ExitCode(err))
	}
	msg := err.Error()
	for _, want := range []string{"not claimed", "ao sim claim"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal missing %q:\n%s", want, msg)
		}
	}
	for _, call := range daemon.calls {
		if strings.Contains(call, "/sim-leases") && !strings.Contains(call, "/sim-recordings/") {
			t.Fatalf("`ao sim record start` must never acquire a lease itself: %v", daemon.calls)
		}
	}
}

func TestSimRecordStart_RefusedWhenLeasedByOtherNamesTheHolder(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStartStatus = http.StatusConflict
	daemon.recordStartBody = `{"error":"conflict","code":"SIM_RECORDING_REFUSED",` +
		`"message":"simulator ` + simUDIDProMax + ` is leased by @mer-3, so this session may not record it",` +
		`"details":{"udid":"` + simUDIDProMax + `","reason":"leased_by_other","holder":"mer-3",` +
		`"expiresAt":"2026-08-13T07:48:14Z"}}`

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start")
	if err == nil {
		t.Fatal("recording a device leased by another session must fail")
	}
	msg := err.Error()
	for _, want := range []string{"mer-3", "iPhone 17 Pro Max"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal missing %q:\n%s", want, msg)
		}
	}
}

func TestSimRecordStart_RefusedWhenAlreadyOpen(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStartStatus = http.StatusConflict
	daemon.recordStartBody = `{"error":"conflict","code":"SIM_RECORDING_REFUSED",` +
		`"message":"simulator ` + simUDIDProMax + ` already has a recording open",` +
		`"details":{"udid":"` + simUDIDProMax + `","reason":"already_open"}}`

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start")
	if err == nil {
		t.Fatalf("starting over an open recording must fail, got success:\n%s", out)
	}
	msg := err.Error()
	for _, want := range []string{"already has a recording open", "ao sim record stop"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal missing %q:\n%s", want, msg)
		}
	}
}

func TestSimRecordStart_Success(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start", "--name", "sign up")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if want := "POST /api/v1/sessions/mer-9/sim-recordings/" + simUDIDProMax; !simCalled(daemon, want) {
		t.Fatalf("calls = %v, want %s", daemon.calls, want)
	}
	var req startSimRecordingRequest
	if err := json.Unmarshal([]byte(daemon.body), &req); err != nil {
		t.Fatalf("decode body: %v (%s)", err, daemon.body)
	}
	if req.Name != "sign up" {
		t.Fatalf("name = %q, want %q", req.Name, "sign up")
	}
	for _, want := range []string{"Recording started", "iPhone 17 Pro Max", simUDIDProMax} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// --- ao sim record status ---------------------------------------------------

// "nothing is being recorded" is an answer, not an error: the command must
// exit 0 and say so plainly.
func TestSimRecordStatus_NoRecordingSaysSoAndExitsZero(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordGetStatus = http.StatusNotFound
	daemon.recordGetBody = `{"error":"not_found","code":"SIM_NOT_FOUND",` +
		`"message":"no recording has ever been started on this simulator","details":null}`

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "status")
	if err != nil {
		t.Fatalf("no recording must not be an error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "Nothing is being recorded") {
		t.Fatalf("output must say plainly that nothing is recording:\n%s", out)
	}
}

func TestSimRecordStatus_OpenRecordingShowsStepCount(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordGetBody = `{"recording":{"udid":"` + simUDIDProMax + `","sessionId":"mer-9","name":"flow",` +
		`"startedAt":"2026-08-13T07:41:02Z","updatedAt":"2026-08-13T07:41:10Z"},"stepCount":2,` +
		`"steps":[{"seq":1,"at":"2026-08-13T07:41:05Z","kind":"tap","selector":"Continue","selectorRung":1,` +
		`"ambiguity":1,"x":0.5,"y":0.9,"toX":0.5,"toY":0.9},` +
		`{"seq":2,"at":"2026-08-13T07:41:06Z","kind":"tap","selector":"Next","selectorRung":1,"ambiguity":1,` +
		`"x":0.5,"y":0.8,"toX":0.5,"toY":0.8}]}`

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Recording open", "@mer-9", "2 step"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSimRecordStatus_JSONCarriesStepCount(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordGetBody = `{"recording":{"udid":"` + simUDIDProMax + `","sessionId":"mer-9","name":"",` +
		`"startedAt":"2026-08-13T07:41:02Z","stoppedAt":"2026-08-13T07:45:02Z","updatedAt":"2026-08-13T07:45:02Z"},` +
		`"stepCount":1,"steps":[]}`

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "status", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res simRecordStatusResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if !res.Found || res.Open {
		t.Fatalf("found/open = %v/%v, want true/false for a stopped recording", res.Found, res.Open)
	}
	if res.StepCount != 1 {
		t.Fatalf("stepCount = %d, want 1", res.StepCount)
	}
}

// --- ao sim record stop ------------------------------------------------------

// stopBody is what the daemon answers a stop with now that IT builds the
// flow: the recording, the step count, and the file that was written. The CLI
// does not emit anything - see stopSimRecording for why that moved - so what
// these tests pin is that it asks correctly and reports faithfully.
const stopBody = `{"recording":{"udid":"` + simUDIDProMax + `","sessionId":"mer-9","name":"flow",` +
	`"startedAt":"2026-08-13T07:41:02Z","stoppedAt":"2026-08-13T07:45:02Z","updatedAt":"2026-08-13T07:45:02Z"},` +
	`"stepCount":7,"steps":[],` +
	`"flow":{"name":"login-to-portfolio","fileName":"login-to-portfolio-20260813-074502.000Z.yaml",` +
	`"path":"/data/sim/mer-9/flows/login-to-portfolio-20260813-074502.000Z.yaml",` +
	`"steps":7,"review":2,"countsKnown":true,"bytes":812}}`

func TestSimRecordStop_PrintsThePathTheDaemonWrote(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBody

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if want := "DELETE /api/v1/sessions/mer-9/sim-recordings/" + simUDIDProMax; !simCalled(daemon, want) {
		t.Fatalf("calls = %v, want %s", daemon.calls, want)
	}
	// The path gets a line of its own, the way `ao sim shot` prints one, so it
	// can be read straight off the terminal and handed to a file read.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if got := lines[len(lines)-1]; got != "/data/sim/mer-9/flows/login-to-portfolio-20260813-074502.000Z.yaml" {
		t.Fatalf("last line = %q, want the path the daemon reported", got)
	}
	if !strings.Contains(out, "7 step(s) captured") {
		t.Fatalf("output must state the step count:\n%s", out)
	}
	// The review count is the number a human has to act on, and it comes from
	// the flow the daemon wrote - never counted a second time here.
	if !strings.Contains(out, "2 needing review") {
		t.Fatalf("output must state how many steps need review:\n%s", out)
	}
}

// A clean flow says nothing about review, for the same reason its own banner
// does not: a line that always reads "0 needing review" is one nobody reads on
// the day it says 3.
func TestSimRecordStop_SaysNothingAboutReviewWhenThereIsNone(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = strings.Replace(stopBody, `"review":2`, `"review":0`, 1)

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "review") {
		t.Fatalf("a clean flow must not mention review:\n%s", out)
	}
}

func TestSimRecordStop_JSONCarriesThePathAndBothCounts(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBody

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res simRecordStopResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if res.StepCount != 7 || res.ReviewCount != 2 {
		t.Fatalf("counts = %d steps / %d review, want 7 / 2", res.StepCount, res.ReviewCount)
	}
	// The exact path the daemon reported, not one this side reconstructed.
	// (Asserting filepath.IsAbs here instead was wrong on Windows, where a
	// POSIX fixture path is not absolute - and it tested less: what matters is
	// that the CLI passes the daemon's answer through untouched.)
	if want := "/data/sim/mer-9/flows/login-to-portfolio-20260813-074502.000Z.yaml"; res.Path != want {
		t.Fatalf("path = %q, want the daemon's own answer %q", res.Path, want)
	}
}

// --entry is passed through for the daemon to emit; --out is resolved HERE,
// because this is the side that knows which working directory a relative path
// meant. Sending it unresolved would land the file wherever the daemon happens
// to be running.
func TestSimRecordStop_ForwardsEntryAndAnAbsoluteOut(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBody

	dir := t.TempDir()
	t.Chdir(dir)
	if _, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG),
		"sim", "record", "stop", "--entry", "../flows/sign-in.yaml", "--out", "custom.yaml"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := "DELETE /api/v1/sessions/mer-9/sim-recordings/" + simUDIDProMax; !simCalled(daemon, want) {
		t.Fatalf("calls = %v, want %s", daemon.calls, want)
	}
	query, err := url.ParseQuery(daemon.query)
	if err != nil {
		t.Fatalf("parse query %q: %v", daemon.query, err)
	}
	if got := query.Get("entry"); got != "../flows/sign-in.yaml" {
		t.Errorf("entry = %q, want it passed through untouched", got)
	}
	out := query.Get("out")
	if !filepath.IsAbs(out) {
		t.Errorf("out = %q, want an absolute path resolved on this side", out)
	}
	if filepath.Base(out) != "custom.yaml" {
		t.Errorf("out = %q, want it to still name custom.yaml", out)
	}
}

// A daemon that stopped the recording but wrote no file must not be reported
// as a success with an empty path. The gestures ARE gone from the daemon's
// side at that point, and the message has to say so rather than read as
// "nothing happened".
func TestSimRecordStop_NoFlowInTheResponseIsAnError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = `{"recording":{"udid":"` + simUDIDProMax + `","sessionId":"mer-9",` +
		`"startedAt":"2026-08-13T07:41:02Z","updatedAt":"2026-08-13T07:45:02Z"},"stepCount":3,"steps":[]}`

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err == nil {
		t.Fatal("a stop that wrote no flow must not report success")
	}
	if !strings.Contains(err.Error(), "3 step(s) captured") {
		t.Fatalf("the error must say the recording stopped and how much it held: %v", err)
	}
}

func TestSimRecordStop_NoOpenRecordingIsAPlainRefusal(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopStatus = http.StatusNotFound
	daemon.recordStopBody = `{"error":"not_found","code":"SIM_NOT_FOUND",` +
		`"message":"no open recording for session mer-9 on simulator ` + simUDIDProMax + `","details":null}`

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err == nil {
		t.Fatal("stopping with nothing open must fail")
	}
	if !strings.Contains(err.Error(), "ao sim record start") {
		t.Fatalf("refusal should point at starting one: %v", err)
	}
}

// --- the gesture hold now carries an intent ---------------------------------

// A recording open on the device must see what `ao sim tap` actually did, not
// just that a hold existed - otherwise a flow recorded while a worker drives
// with `ao sim tap` comes out empty of everything but coordinates.
func TestSimTap_SendsAGestureIntentOnAcquireAndPerformedTrueOnRelease(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	_, errOut, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.934")
	if err != nil {
		t.Fatalf("sim tap failed: %v\nstderr=%s", err, errOut)
	}
	var hold acquireSimHoldRequest
	if err := json.Unmarshal([]byte(daemon.holdRequest), &hold); err != nil {
		t.Fatalf("decode hold body: %v (%s)", err, daemon.holdRequest)
	}
	if hold.Kind != "tap" {
		t.Fatalf("hold intent kind = %q, want %q", hold.Kind, "tap")
	}
	if hold.X != 0.5 || hold.Y != 0.934 || hold.ToX != 0.5 || hold.ToY != 0.934 {
		t.Fatalf("hold intent coordinates = %+v, want the tapped point on both ends", hold)
	}

	log := daemon.callLog()
	releaseCall := ""
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "DELETE") && strings.Contains(line, "/hold/") {
			releaseCall = line
		}
	}
	if releaseCall == "" {
		t.Fatalf("no hold release call found: %s", log)
	}
	if strings.Contains(releaseCall, "performed=false") {
		t.Fatalf("a successful tap must release with the real outcome (performed=true, i.e. no override), got: %s", releaseCall)
	}
}

func TestSimSwipe_SendsFromAndToInTheHoldIntent(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	_, errOut, err := executeCLI(t, deps, "sim", "swipe", "0.5", "0.8", "0.5", "0.2")
	if err != nil {
		t.Fatalf("sim swipe failed: %v\nstderr=%s", err, errOut)
	}
	var hold acquireSimHoldRequest
	if err := json.Unmarshal([]byte(daemon.holdRequest), &hold); err != nil {
		t.Fatalf("decode hold body: %v (%s)", err, daemon.holdRequest)
	}
	if hold.Kind != "swipe" {
		t.Fatalf("hold intent kind = %q, want %q", hold.Kind, "swipe")
	}
	if hold.X != 0.5 || hold.Y != 0.8 || hold.ToX != 0.5 || hold.ToY != 0.2 {
		t.Fatalf("hold intent = %+v, want the swipe's from/to points", hold)
	}
	if hold.DurationMS <= 0 {
		t.Fatalf("hold intent durationMs = %d, want a positive duration", hold.DurationMS)
	}
}

func TestSimButton_SendsTheButtonNameInTheHoldIntent(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	_, errOut, err := executeCLI(t, deps, "sim", "button", "home")
	if err != nil {
		t.Fatalf("sim button failed: %v\nstderr=%s", err, errOut)
	}
	var hold acquireSimHoldRequest
	if err := json.Unmarshal([]byte(daemon.holdRequest), &hold); err != nil {
		t.Fatalf("decode hold body: %v (%s)", err, daemon.holdRequest)
	}
	if hold.Kind != "button" || hold.Name != "home" {
		t.Fatalf("hold intent = %+v, want kind=button name=home", hold)
	}
}

func TestSimType_SendsTheTypedTextInTheHoldIntent(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	_, errOut, err := executeCLI(t, deps, "sim", "type", "hello")
	if err != nil {
		t.Fatalf("sim type failed: %v\nstderr=%s", err, errOut)
	}
	var hold acquireSimHoldRequest
	if err := json.Unmarshal([]byte(daemon.holdRequest), &hold); err != nil {
		t.Fatalf("decode hold body: %v (%s)", err, daemon.holdRequest)
	}
	if hold.Kind != "type" || hold.Text != "hello" {
		t.Fatalf("hold intent = %+v, want kind=type text=hello", hold)
	}
}

// A by-name tap used to send an empty intent (Kind ""), which poisoned an
// entire recording: the step mapping -> `simflow.Emit` refuses any
// step whose Kind it does not recognize, so `record stop` failed outright the
// first time a session tapped by --label/--id while recording - tapping by
// name being the idiomatic way to drive, not an edge case. This pins that the
// hold now carries a real, resolved intent, exactly like the coordinate form,
// so the recorded step comes back out with a Kind Emit can translate.
func TestSimTapByName_SendsAGestureIntentOnAcquire(t *testing.T) {
	driver := &fakeSimDriver{snapshot: namedScreen()}
	deps, daemon := touchDeps(t, driver)

	_, errOut, err := executeCLI(t, deps, "sim", "tap", "--label", "Continue")
	if err != nil {
		t.Fatalf("sim tap --label failed: %v\nstderr=%s", err, errOut)
	}
	var hold acquireSimHoldRequest
	if err := json.Unmarshal([]byte(daemon.holdRequest), &hold); err != nil {
		t.Fatalf("decode hold body: %v (%s)", err, daemon.holdRequest)
	}
	if hold.Kind != "tap" {
		t.Fatalf("hold intent kind = %q, want %q - an empty kind is exactly what broke `record stop`", hold.Kind, "tap")
	}
	// The name itself, not a coordinate rediscovered by reading the screen a
	// second time: see TestSimTapByName_DoesNotReadTheScreenBeforeAcquiringTheHold.
	if hold.Label != "Continue" {
		t.Fatalf("hold intent label = %q, want %q", hold.Label, "Continue")
	}
	if hold.ID != "" {
		t.Fatalf("hold intent id = %q, want empty for a --label tap", hold.ID)
	}
	if hold.X != 0 || hold.Y != 0 {
		t.Fatalf("hold intent = %+v, want no coordinates - the label is what was sent", hold)
	}

	// And the step that shape produces is one the daemon's own mapping and
	// `Emit` can actually translate - the whole point of sending a real intent.
	// The mapping lives in internal/simrecord now that the daemon builds the
	// flow; the assertion follows it rather than being dropped.
	flowSteps := simrecord.Steps([]domain.SimRecordingStep{{Kind: hold.Kind, Selector: "Continue", SelectorRung: 1}})
	if len(flowSteps) != 1 || flowSteps[0].Kind == "" {
		t.Fatal("a by-name tap must not produce a step Emit refuses to translate")
	}
}
