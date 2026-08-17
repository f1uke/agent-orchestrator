package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		`"startedAt":"2026-08-13T07:41:02Z","updatedAt":"2026-08-13T07:41:10Z"},` +
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
		`"steps":[{"seq":1,"at":"2026-08-13T07:41:05Z","kind":"tap"}]}`

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

// stopBodyWithOneTap is a stopped recording with a single resolved tap step,
// enough to exercise the domain-step -> simflow.Step -> Emit path end to end.
const stopBodyWithOneTap = `{"recording":{"udid":"` + simUDIDProMax + `","sessionId":"mer-9","name":"flow",` +
	`"startedAt":"2026-08-13T07:41:02Z","stoppedAt":"2026-08-13T07:45:02Z","updatedAt":"2026-08-13T07:45:02Z"},` +
	`"steps":[{"seq":1,"at":"2026-08-13T07:41:05Z","kind":"tap","selector":"Continue","selectorRung":1,` +
	`"ambiguity":1,"x":0.5,"y":0.934,"toX":0.5,"toY":0.934}]}`

func TestSimRecordStop_WritesTheFlowAndPrintsItsPath(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBodyWithOneTap

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if want := "DELETE /api/v1/sessions/mer-9/sim-recordings/" + simUDIDProMax; !simCalled(daemon, want) {
		t.Fatalf("calls = %v, want %s", daemon.calls, want)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	path := lines[len(lines)-1]
	if !strings.HasPrefix(path, filepath.Join(cfg.dataDir, "sim", "mer-9")) {
		t.Fatalf("path %q not under the session artifact directory %q", path, filepath.Join(cfg.dataDir, "sim", "mer-9"))
	}
	if !strings.HasSuffix(path, ".yaml") {
		t.Fatalf("path %q should end in .yaml", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the printed path must exist: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "appId: ${APP_ID}") {
		t.Fatalf("flow missing the literal appId placeholder:\n%s", content)
	}
	if !strings.Contains(content, `tapOn: "Continue"`) {
		t.Fatalf("flow missing the recorded tap:\n%s", content)
	}
	if strings.Contains(content, "launchApp") {
		t.Fatalf("flow must never fabricate launchApp:\n%s", content)
	}
}

func TestSimRecordStop_WithEntryPutsRunFlowFirst(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBodyWithOneTap

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG),
		"sim", "record", "stop", "--entry", "../flows/sign-in.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	path := lines[len(lines)-1]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read flow: %v", err)
	}
	content := string(data)
	runFlowIdx := strings.Index(content, `- runFlow: "../flows/sign-in.yaml"`)
	tapIdx := strings.Index(content, "tapOn:")
	if runFlowIdx < 0 {
		t.Fatalf("flow missing the entry point:\n%s", content)
	}
	if tapIdx < 0 || runFlowIdx > tapIdx {
		t.Fatalf("runFlow must come before the recorded steps:\n%s", content)
	}
}

func TestSimRecordStop_OutOverridesTheDefaultPath(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBodyWithOneTap
	dest := filepath.Join(t.TempDir(), "custom.yaml")

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG),
		"sim", "record", "stop", "--out", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, dest) {
		t.Fatalf("output must print the overridden path:\n%s", out)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("--out path must exist: %v", err)
	}
}

// A step whose selector matched more than one element on screen
// (selectorRung: RungTextIndex) must re-emit at the SAME index the human
// actually tapped - here, the second of three "Continue" buttons - not index
// 0 (the first). Before 0039_sim_recording_step_index.sql, SelectorIndex did
// not exist anywhere in the wire shape and this always came back as 0: a flow
// whose selector text read correctly while silently taking the FIRST matching
// element rather than the one that was recorded, which is precisely the
// failure this whole selector-ladder design exists to prevent.
const stopBodyWithDuplicateLabelTap = `{"recording":{"udid":"` + simUDIDProMax + `","sessionId":"mer-9","name":"flow",` +
	`"startedAt":"2026-08-13T07:41:02Z","stoppedAt":"2026-08-13T07:45:02Z","updatedAt":"2026-08-13T07:45:02Z"},` +
	`"steps":[{"seq":1,"at":"2026-08-13T07:41:05Z","kind":"tap","selector":"Continue","selectorRung":2,` +
	`"selectorIndex":2,"ambiguity":3,"x":0.5,"y":0.7,"toX":0.5,"toY":0.7}]}`

func TestSimRecordStop_DuplicateLabelTapKeepsTheRecordedIndex(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBodyWithDuplicateLabelTap

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	path := lines[len(lines)-1]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read flow: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "index: 2") {
		t.Fatalf("flow must tap the SECOND %q (the one actually recorded), not silently default to index 0:\n%s",
			"Continue", content)
	}
	if strings.Contains(content, "index: 0") {
		t.Fatalf("flow fell back to index 0 - the wrong element - instead of the recorded index:\n%s", content)
	}
}

// A recorded selector is STORED escaped (Maestro matches text as a regex), so
// re-emitting one has to keep it escaped where it is matched on and unescape
// it where a human reads it. scrollUntilVisible is the second kind: render.go
// says the scroll target is the plain label, and feeding it the stored text
// emitted `element: "See all \\(12\\)"` - a search for backslashes that are
// not in the label. The tapOn-shaped assertions elsewhere never saw this
// because no test used a label with metacharacters in it.
const stopBodyWithOffScreenEscapedTap = `{"recording":{"udid":"` + simUDIDProMax + `","sessionId":"mer-9","name":"flow",` +
	`"startedAt":"2026-08-13T07:41:02Z","stoppedAt":"2026-08-13T07:45:02Z","updatedAt":"2026-08-13T07:45:02Z"},` +
	`"steps":[{"seq":1,"at":"2026-08-13T07:41:05Z","kind":"tap","selector":"See all \\(12\\)","selectorRung":1,` +
	`"ambiguity":1,"offScreen":true,"x":0.5,"y":0.7}]}`

func TestSimRecordStop_OffScreenEscapedSelectorScrollsToThePlainLabel(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBodyWithOffScreenEscapedTap

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	data, err := os.ReadFile(lines[len(lines)-1])
	if err != nil {
		t.Fatalf("read flow: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `    element: "See all (12)"`) {
		t.Fatalf("scrollUntilVisible must search for the label a human reads:\n%s", content)
	}
	if strings.Contains(content, `element: "See all \\(12\\)"`) {
		t.Fatalf("the escaped pattern reached the scroll target:\n%s", content)
	}
}

// The on-screen half: the selector itself stays escaped, because that is what
// Maestro matches on - and the "# escaped" comment comes back too. Escaped has
// no column of its own; it is recovered by unescaping the stored text, which
// is what makes it safe not to persist.
const stopBodyWithEscapedTap = `{"recording":{"udid":"` + simUDIDProMax + `","sessionId":"mer-9","name":"flow",` +
	`"startedAt":"2026-08-13T07:41:02Z","stoppedAt":"2026-08-13T07:45:02Z","updatedAt":"2026-08-13T07:45:02Z"},` +
	`"steps":[{"seq":1,"at":"2026-08-13T07:41:05Z","kind":"tap","selector":"See all \\(12\\)","selectorRung":1,` +
	`"ambiguity":1,"x":0.5,"y":0.7}]}`

func TestSimRecordStop_EscapedSelectorKeepsItsEscapingAndExplainsIt(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.recordStopBody = stopBodyWithEscapedTap

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	data, err := os.ReadFile(lines[len(lines)-1])
	if err != nil {
		t.Fatalf("read flow: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `- tapOn: "See all \\(12\\)"`) {
		t.Fatalf("the matcher must keep the escaping it was recorded with:\n%s", content)
	}
	if !strings.Contains(content, "# escaped: the label contains regex characters") {
		t.Fatalf("an escaped selector must carry its explanation:\n%s", content)
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
// entire recording: `simRecordingStepToFlow` -> `simflow.Emit` refuses any
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

	// And the step that shape produces is one `simRecordingStepToFlow` and
	// `Emit` can actually translate - the whole point of sending a real intent.
	step := simRecordingStepClient{Kind: hold.Kind, Selector: "Continue", SelectorRung: 1}
	flowStep := simRecordingStepToFlow(step)
	if flowStep.Kind == "" {
		t.Fatal("a by-name tap must not produce a step Emit refuses to translate")
	}
}
