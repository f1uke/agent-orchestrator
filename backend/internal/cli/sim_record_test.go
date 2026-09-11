package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// `ao sim record` is the SCREEN recorder. These assert what an agent reading a
// terminal actually gets: the path on its own line, who holds the device, what
// stopped the recording, and refusals that say what to do next.

// --- ao sim record start ----------------------------------------------------

func TestSimRecordStart_PrintsTheDeviceThePathAndTheCap(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimDaemon(t, cfg)

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, want := range []string{
		"Recording the screen of",
		simUDIDProMax,
		// The path on a line of its own, the way `ao sim shot` prints it, so it
		// can be read straight off the terminal.
		"\n/data/sim/mer-9/videos/20260813-074102.417Z-" + simUDIDProMax + ".mov\n",
		// The cap, because a recording that stops itself and says nothing about
		// it beforehand is a surprise at exactly the wrong moment.
		"It stops itself after 10m0s",
		"ao sim record stop",
		"Lease:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("start output missing %q:\n%s", want, out)
		}
	}
}

// A screen recording is the moving sibling of `ao sim shot`, and takes a lease
// no more than a screenshot does: filming a device a human is driving is one of
// the things it is for.
func TestSimRecordStart_NeverTakesALease(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)

	if _, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start"); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, call := range daemon.calls {
		if strings.HasPrefix(call, http.MethodPost+" ") && strings.Contains(call, "/sim-leases") {
			t.Fatalf("`ao sim record start` must not claim the device: %v", daemon.calls)
		}
	}
}

func TestSimRecordStart_ForwardsMaxDurationInSeconds(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)

	if _, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG),
		"sim", "record", "start", "--max-duration", "90s"); err != nil {
		t.Fatalf("start: %v", err)
	}
	var in startSimVideoRequest
	if err := json.Unmarshal([]byte(daemon.videoStartRequest), &in); err != nil {
		t.Fatalf("body %q: %v", daemon.videoStartRequest, err)
	}
	if in.MaxDurationSeconds != 90 {
		t.Errorf("--max-duration must reach the daemon in seconds: %d", in.MaxDurationSeconds)
	}
}

// A session told to ask ITSELF to stop the recording is a sentence that makes a
// reader doubt everything else the command says, so the two cases are worded
// differently: the holder is you, or the holder is somebody else.
func TestSimRecordStart_ADeviceThisSessionAlreadyRecordsSaysSoInTheFirstPerson(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.videoStartStatus = http.StatusConflict
	daemon.videoStartBody = `{"error":"conflict","code":"SIM_VIDEO_HELD",` +
		`"message":"already being recorded",` +
		`"details":{"udid":"` + simUDIDProMax + `","holder":"mer-9","path":"/data/sim/mer-9/videos/x.mov"}}`

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start")
	if err == nil {
		t.Fatal("starting twice must be refused")
	}
	msg := err.Error()
	for _, want := range []string{"started by this session", "ao sim record stop", "ao sim record status"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "ask @mer-9") {
		t.Errorf("the refusal must not tell this session to ask itself:\n%s", msg)
	}
}

func TestSimRecordStart_RefusedOnADeviceSomebodyElseIsRecording(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.videoStartStatus = http.StatusConflict
	daemon.videoStartBody = `{"error":"conflict","code":"SIM_VIDEO_HELD",` +
		`"message":"already being recorded",` +
		`"details":{"udid":"` + simUDIDProMax + `","holder":"mer-3","path":"/data/sim/mer-3/videos/x.mov"}}`

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start")
	if err == nil {
		t.Fatal("a second recorder on one device must be refused")
	}
	msg := err.Error()
	for _, want := range []string{
		"already being recorded by @mer-3",
		// The refusal has to say nothing happened, or a reader assumes a
		// recording of theirs is now open somewhere.
		"nothing was started or stopped",
		"ao sim record stop",
		"/data/sim/mer-3/videos/x.mov",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q:\n%s", want, msg)
		}
	}
}

func TestSimRecordStart_JSONCarriesThePathAndTheCap(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimDaemon(t, cfg)

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "start", "--json")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var result simRecordStartResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if !strings.HasSuffix(result.Path, ".mov") {
		t.Errorf("path = %q", result.Path)
	}
	if result.MaxDurationSeconds != 600 {
		t.Errorf("maxDurationSeconds = %d", result.MaxDurationSeconds)
	}
	if result.DeviceName == "" || result.Runtime == "" {
		t.Errorf("the device the CLI resolved must be in the result: %+v", result)
	}
}

// --- ao sim record status ---------------------------------------------------

func TestSimRecordStatus_ReportsHowLongItHasBeenRecording(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimDaemon(t, cfg)

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{
		"Recording the screen of",
		"held by @mer-9",
		// Started 07:40:02, the fixed clock is 07:41:02: a minute in, of ten.
		"for 1m0s of at most 10m0s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

// Nothing being recorded is an ANSWER, not an error: an agent that has to
// pattern-match an exit code to learn "no" will get it wrong.
func TestSimRecordStatus_NothingRecordingIsNotAnError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.videoGetStatus = http.StatusNotFound
	daemon.videoGetBody = `{"error":"not_found","code":"SIM_VIDEO_NOT_FOUND","message":"no recording is open on this device"}`

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "status")
	if err != nil {
		t.Fatalf("a device with nothing recording must exit 0: %v", err)
	}
	if !strings.Contains(out, "Nothing is recording the screen of") {
		t.Errorf("status output:\n%s", out)
	}

	out, _, err = executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "status", "--json")
	if err != nil {
		t.Fatalf("--json: %v", err)
	}
	var result simRecordStatusResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if result.Recording {
		t.Error("recording must be false when nothing is open")
	}
	if result.UDID != simUDIDProMax {
		t.Errorf("the answer must still name the device it is about: %+v", result)
	}
}

// --- ao sim record stop -----------------------------------------------------

func TestSimRecordStop_PrintsTheSizeAndThePath(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimDaemon(t, cfg)

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	for _, want := range []string{
		"Stopped recording",
		"5m0s of screen",
		"2.6 MB",
		"\n/data/sim/mer-9/videos/20260813-074102.417Z-" + simUDIDProMax + ".mov\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stop output missing %q:\n%s", want, out)
		}
	}
	// The cap is only mentioned when it fired.
	if strings.Contains(out, "maximum duration") {
		t.Errorf("a recording stopped as asked must not mention the cap:\n%s", out)
	}
}

// A recording that hit its cap produces the same file as one you stopped, and
// the difference - whether the end of the scenario is in it - has to be said.
func TestSimRecordStop_SaysWhenTheCapEndedItInstead(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.videoStopBody = simVideoBody(`,"stoppedAt":"2026-08-13T07:50:02Z","stopReason":"max-duration","bytes":900`)

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	for _, want := range []string{
		"reached its maximum duration",
		"the end of what you drove may not be in it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stop output missing %q:\n%s", want, out)
		}
	}
}

// An empty file means the screen never changed - simctl records a frame only
// when the picture does. Saying so at the one moment it is actionable keeps an
// agent from reporting a broken recorder.
func TestSimRecordStop_ExplainsAnEmptyVideo(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.videoStopBody = simVideoBody(`,"stoppedAt":"2026-08-13T07:45:02Z","stopReason":"requested"`)

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(out, "records a frame only when the screen changes") {
		t.Errorf("an empty video must explain itself:\n%s", out)
	}
}

func TestSimRecordStop_NothingRecordingIsAPlainRefusal(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.videoStopStatus = http.StatusNotFound
	daemon.videoStopBody = `{"error":"not_found","code":"SIM_VIDEO_NOT_FOUND","message":"no recording is open on this device"}`

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop")
	if err == nil {
		t.Fatal("stopping nothing must fail")
	}
	for _, want := range []string{"nothing is recording the screen of", "ao sim record start"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q:\n%s", want, err.Error())
		}
	}
}

func TestSimRecordStop_JSONCarriesTheFileAndWhatEndedIt(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimDaemon(t, cfg)

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "record", "stop", "--json")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	var result simRecordStopResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if result.Bytes != 2737311 || result.StopReason != "requested" || result.ElapsedSeconds != 300 {
		t.Errorf("stop result = %+v", result)
	}
}

// --- the rename -------------------------------------------------------------

// The old spelling must FAIL, not quietly keep working: a name that still works
// is the confusion the rename exists to remove. `ao sim record` with no
// subcommand is not the gesture recorder any more, and `--name`/`--entry` -
// the flags that only ever meant a flow - are gone from it.
func TestSimRecord_TheOldGestureRecorderSpellingIsGone(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimDaemon(t, cfg)

	for _, args := range [][]string{
		{"sim", "record", "start", "--name", "sign up flow"},
		{"sim", "record", "stop", "--entry", "../flows/sign-in.yaml"},
		{"sim", "record", "stop", "--out", "/tmp/flow.yaml"},
	} {
		_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), args...)
		if err == nil {
			t.Errorf("%v must not be accepted by the screen recorder", args)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%v: want an unknown-flag error, got: %v", args, err)
		}
	}
}
