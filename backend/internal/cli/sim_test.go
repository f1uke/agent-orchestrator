package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// simctl fixtures. The shape mirrors `xcrun simctl list devices --json`: a
// "devices" object keyed by runtime identifier, each holding an array of
// devices. Tests fake the simctl boundary rather than requiring a real
// simulator, so they run unchanged on a Linux CI box with no Xcode.
const (
	simRuntimeIOS263 = "com.apple.CoreSimulator.SimRuntime.iOS-26-3"
	simUDIDProMax    = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"
	simUDIDPro       = "C4764B41-8F74-49C6-8766-A20EA46125BF"
	simUDIDAir       = "9B8CDFCC-EE68-41A8-8C13-764D8B0619AC"
)

func simDevicesJSON(t *testing.T, devices ...map[string]any) string {
	t.Helper()
	byRuntime := map[string][]map[string]any{}
	for _, d := range devices {
		rt, _ := d["_runtime"].(string)
		if rt == "" {
			rt = simRuntimeIOS263
		}
		delete(d, "_runtime")
		byRuntime[rt] = append(byRuntime[rt], d)
	}
	if len(byRuntime) == 0 {
		byRuntime = map[string][]map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"devices": byRuntime})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

func simDeviceFixture(udid, name, state string) map[string]any {
	return map[string]any{"udid": udid, "name": name, "state": state, "isAvailable": true}
}

// simDeps builds CLI deps whose simctl boundary is faked: `list devices --json`
// returns listJSON, and `io <udid> screenshot <path>` writes screenshotBytes to
// the path simctl was told to write (exactly what the real simctl does — it
// writes the file itself and prints nothing).
func simDeps(t *testing.T, listJSON string, screenshotBytes []byte) (Deps, *[][]string) {
	t.Helper()
	var calls [][]string
	deps := Deps{
		LookPath: func(name string) (string, error) {
			if name == "xcrun" {
				return "/usr/bin/xcrun", nil
			}
			return "", errors.New("missing")
		},
		CommandOutput: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string{name}, args...))
			switch {
			case len(args) >= 3 && args[1] == "list" && args[2] == "devices":
				return []byte(listJSON), nil
			case len(args) >= 4 && args[1] == "io" && args[3] == "screenshot":
				path := args[len(args)-1]
				if screenshotBytes == nil {
					return []byte(""), nil
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					return nil, err
				}
				if err := os.WriteFile(path, screenshotBytes, 0o600); err != nil {
					return nil, err
				}
				return []byte(""), nil
			}
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		},
		ProcessAlive: func(int) bool { return false },
		Now:          func() time.Time { return time.Date(2026, 8, 13, 7, 41, 2, 417_000_000, time.UTC) },
	}
	return deps, &calls
}

// fakePNG is a minimal non-empty payload standing in for a captured frame.
var fakePNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

type simListJSON struct {
	Devices []struct {
		UDID              string `json:"udid"`
		Name              string `json:"name"`
		Runtime           string `json:"runtime"`
		RuntimeIdentifier string `json:"runtimeIdentifier"`
		State             string `json:"state"`
		Available         bool   `json:"available"`
		Default           bool   `json:"default"`
	} `json:"devices"`
	DefaultUDID   *string `json:"defaultUdid"`
	DefaultReason string  `json:"defaultReason"`
}

// --- ao sim list -----------------------------------------------------------

func TestSimList_ReportsDevicesAndDerivedRuntime(t *testing.T) {
	setConfigEnv(t)
	deps, _ := simDeps(t, simDevicesJSON(t,
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
	), fakePNG)

	out, errOut, err := executeCLI(t, deps, "sim", "list", "--json")
	if err != nil {
		t.Fatalf("sim list --json failed: %v\nstderr=%s", err, errOut)
	}
	var got simListJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode sim list JSON: %v\n%s", err, out)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("devices = %d, want 2: %s", len(got.Devices), out)
	}
	var proMax *struct {
		UDID              string `json:"udid"`
		Name              string `json:"name"`
		Runtime           string `json:"runtime"`
		RuntimeIdentifier string `json:"runtimeIdentifier"`
		State             string `json:"state"`
		Available         bool   `json:"available"`
		Default           bool   `json:"default"`
	}
	for i := range got.Devices {
		if got.Devices[i].UDID == simUDIDProMax {
			proMax = &got.Devices[i]
		}
	}
	if proMax == nil {
		t.Fatalf("booted device missing from list: %s", out)
	}
	// The runtime label must match what `xcrun simctl list devices` prints as
	// its own section header, derived from the runtime identifier.
	if proMax.Runtime != "iOS 26.3" {
		t.Errorf("runtime = %q, want %q", proMax.Runtime, "iOS 26.3")
	}
	if proMax.RuntimeIdentifier != simRuntimeIOS263 {
		t.Errorf("runtimeIdentifier = %q, want %q", proMax.RuntimeIdentifier, simRuntimeIOS263)
	}
	if proMax.State != "Booted" || !proMax.Available || !proMax.Default {
		t.Errorf("booted device = %+v, want Booted/available/default", *proMax)
	}
	if got.DefaultUDID == nil || *got.DefaultUDID != simUDIDProMax {
		t.Errorf("defaultUdid = %v, want %q", got.DefaultUDID, simUDIDProMax)
	}
}

func TestSimList_NoDefaultWhenSeveralBooted(t *testing.T) {
	setConfigEnv(t)
	deps, _ := simDeps(t, simDevicesJSON(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDAir, "iPhone Air", "Booted"),
	), fakePNG)

	out, errOut, err := executeCLI(t, deps, "sim", "list", "--json")
	if err != nil {
		t.Fatalf("sim list --json failed: %v\nstderr=%s", err, errOut)
	}
	var got simListJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.DefaultUDID != nil {
		t.Errorf("defaultUdid = %q, want null when several are booted", *got.DefaultUDID)
	}
	if got.DefaultReason == "" {
		t.Error("defaultReason is empty; the ambiguity must be explained")
	}
	for _, d := range got.Devices {
		if d.Default {
			t.Errorf("device %s marked default despite ambiguity", d.UDID)
		}
	}
}

func TestSimList_NoSimulatorsAtAll(t *testing.T) {
	setConfigEnv(t)
	deps, _ := simDeps(t, simDevicesJSON(t), fakePNG)

	out, errOut, err := executeCLI(t, deps, "sim", "list")
	if err != nil {
		t.Fatalf("sim list failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "No simulators") {
		t.Errorf("output %q does not say there are no simulators", out)
	}
}

func TestSimList_SimctlFailureIsAnError(t *testing.T) {
	setConfigEnv(t)
	deps, _ := simDeps(t, "", fakePNG)
	deps.CommandOutput = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("xcrun: error: unable to find utility \"simctl\""), errors.New("exit status 72")
	}

	out, _, err := executeCLI(t, deps, "sim", "list", "--json")
	if err == nil {
		t.Fatalf("sim list succeeded despite a simctl failure; output=%s", out)
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "simctl") {
		t.Errorf("error %q does not mention simctl", err)
	}
	if !strings.Contains(err.Error(), "unable to find utility") {
		t.Errorf("error %q drops simctl's own output", err)
	}
}

// TestSimList_SimctlExitCodeIsNotSwallowed is the case that a malformed-output
// test cannot reach: simctl exits non-zero while still printing parseable JSON.
// Only the exit status distinguishes "this machine has no simulators" from
// "simctl broke", and reporting the former is a silent empty result.
func TestSimList_SimctlExitCodeIsNotSwallowed(t *testing.T) {
	setConfigEnv(t)
	deps, _ := simDeps(t, "", fakePNG)
	deps.CommandOutput = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(simDevicesJSON(t)), errors.New("exit status 1")
	}

	out, _, err := executeCLI(t, deps, "sim", "list")
	if err == nil {
		t.Fatalf("sim list reported an empty device list instead of simctl's failure; output=%s", out)
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
}

func TestSimList_MalformedSimctlJSONIsAnError(t *testing.T) {
	setConfigEnv(t)
	deps, _ := simDeps(t, "not json at all", fakePNG)

	if _, _, err := executeCLI(t, deps, "sim", "list"); err == nil {
		t.Fatal("sim list succeeded on malformed simctl JSON")
	} else if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
}

func TestSimList_MissingXcrunIsAClearError(t *testing.T) {
	setConfigEnv(t)
	deps, _ := simDeps(t, simDevicesJSON(t), fakePNG)
	deps.LookPath = func(string) (string, error) { return "", errors.New("not found") }

	_, _, err := executeCLI(t, deps, "sim", "list")
	if err == nil {
		t.Fatal("sim list succeeded without xcrun on PATH")
	}
	if !strings.Contains(err.Error(), "xcrun") {
		t.Errorf("error %q does not name xcrun", err)
	}
}

// --- ao sim shot -----------------------------------------------------------

type simShotJSON struct {
	UDID       string `json:"udid"`
	Name       string `json:"name"`
	Runtime    string `json:"runtime"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	CapturedAt string `json:"capturedAt"`
	Note       string `json:"note"`
}

func TestSimShot_ExactlyOneBootedWritesSessionArtifact(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	deps, calls := simDeps(t, simDevicesJSON(t,
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
	), fakePNG)

	out, errOut, err := executeCLI(t, deps, "sim", "shot", "--json")
	if err != nil {
		t.Fatalf("sim shot failed: %v\nstderr=%s", err, errOut)
	}
	var got simShotJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode sim shot JSON: %v\n%s", err, out)
	}
	if got.UDID != simUDIDProMax {
		t.Errorf("udid = %q, want the only booted device %q", got.UDID, simUDIDProMax)
	}
	wantDir := filepath.Join(cfg.dataDir, "sim", "agent-orchestrator-123")
	if filepath.Dir(got.Path) != wantDir {
		t.Errorf("artifact dir = %q, want session dir %q", filepath.Dir(got.Path), wantDir)
	}
	// Millisecond-precision UTC stamp plus udid keeps concurrent captures from
	// colliding inside one session.
	wantName := "20260813-074102.417Z-" + simUDIDProMax + ".png"
	if filepath.Base(got.Path) != wantName {
		t.Errorf("artifact name = %q, want %q", filepath.Base(got.Path), wantName)
	}
	info, statErr := os.Stat(got.Path)
	if statErr != nil {
		t.Fatalf("artifact not written: %v", statErr)
	}
	if info.Size() == 0 {
		t.Fatal("artifact is zero bytes")
	}
	if got.Bytes != info.Size() {
		t.Errorf("reported bytes = %d, on-disk = %d", got.Bytes, info.Size())
	}
	if got.Note == "" || !strings.Contains(strings.ToLower(got.Note), "shared") {
		t.Errorf("note = %q, want a warning that the device is shared", got.Note)
	}

	var shotCall []string
	for _, c := range *calls {
		if len(c) > 2 && c[2] == "io" {
			shotCall = c
		}
	}
	if shotCall == nil {
		t.Fatalf("simctl io screenshot was never invoked: %v", *calls)
	}
	if shotCall[3] != simUDIDProMax {
		t.Errorf("screenshot targeted %q, want %q", shotCall[3], simUDIDProMax)
	}
	// Read-only contract: nothing that mutates a device may ever be invoked.
	for _, c := range *calls {
		for _, arg := range c {
			switch arg {
			case "boot", "shutdown", "reboot", "erase":
				t.Fatalf("sim shot invoked a mutating simctl verb: %v", c)
			}
		}
	}
}

func TestSimShot_TextOutputPrintsPathOnItsOwnLine(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	deps, _ := simDeps(t, simDevicesJSON(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted")), fakePNG)

	out, errOut, err := executeCLI(t, deps, "sim", "shot")
	if err != nil {
		t.Fatalf("sim shot failed: %v\nstderr=%s", err, errOut)
	}
	// The point is that some line is nothing but the absolute artifact path, so
	// it can be copied straight out of the terminal. filepath.IsAbs, not a
	// leading separator: a Windows path starts with its drive letter.
	var pathLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, ".png") && filepath.IsAbs(line) {
			pathLine = line
		}
	}
	if pathLine == "" {
		t.Fatalf("no bare artifact path line in output:\n%s", out)
	}
	if _, err := os.Stat(pathLine); err != nil {
		t.Errorf("printed path is not readable: %v", err)
	}
	if !strings.Contains(out, "iPhone 17 Pro Max") {
		t.Errorf("output does not name the captured device:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "shared") {
		t.Errorf("output does not warn the device is shared:\n%s", out)
	}
}

func TestSimShot_NoBootedSimulatorRefusesAndDoesNotBoot(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	deps, calls := simDeps(t, simDevicesJSON(t,
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
		simDeviceFixture(simUDIDAir, "iPhone Air", "Shutdown"),
	), fakePNG)

	_, _, err := executeCLI(t, deps, "sim", "shot")
	if err == nil {
		t.Fatal("sim shot succeeded with nothing booted")
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "booted") {
		t.Errorf("error %q does not say nothing is booted", err)
	}
	for _, c := range *calls {
		for _, arg := range c {
			if arg == "boot" {
				t.Fatalf("sim shot tried to boot a simulator: %v", c)
			}
		}
	}
}

func TestSimShot_SeveralBootedRefusesToGuessAndNamesEach(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	deps, calls := simDeps(t, simDevicesJSON(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDAir, "iPhone Air", "Booted"),
	), fakePNG)

	_, _, err := executeCLI(t, deps, "sim", "shot")
	if err == nil {
		t.Fatal("sim shot silently picked one of several booted simulators")
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
	for _, want := range []string{simUDIDProMax, simUDIDAir, "iPhone 17 Pro Max", "iPhone Air", "--udid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error %q is missing %q", err, want)
		}
	}
	for _, c := range *calls {
		if len(c) > 2 && c[2] == "io" {
			t.Fatalf("a screenshot was captured despite ambiguity: %v", c)
		}
	}
}

func TestSimShot_ExplicitUDIDWins(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	deps, _ := simDeps(t, simDevicesJSON(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDAir, "iPhone Air", "Booted"),
	), fakePNG)

	out, errOut, err := executeCLI(t, deps, "sim", "shot", "--udid", strings.ToLower(simUDIDAir), "--json")
	if err != nil {
		t.Fatalf("sim shot --udid failed: %v\nstderr=%s", err, errOut)
	}
	var got simShotJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	// A lowercased udid must still match: simctl prints them uppercase but
	// users paste them from anywhere.
	if got.UDID != simUDIDAir {
		t.Errorf("udid = %q, want %q", got.UDID, simUDIDAir)
	}
	if got.Name != "iPhone Air" {
		t.Errorf("name = %q, want %q", got.Name, "iPhone Air")
	}
}

func TestSimShot_UnknownUDIDIsAClearError(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	deps, calls := simDeps(t, simDevicesJSON(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted")), fakePNG)

	_, _, err := executeCLI(t, deps, "sim", "shot", "--udid", "DEAD-BEEF")
	if err == nil {
		t.Fatal("sim shot succeeded for a nonexistent udid")
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "DEAD-BEEF") {
		t.Errorf("error %q does not echo the udid asked for", err)
	}
	for _, c := range *calls {
		if len(c) > 2 && c[2] == "io" {
			t.Fatalf("a screenshot was attempted for an unknown udid: %v", c)
		}
	}
}

func TestSimShot_ExplicitUDIDNotBootedIsRefused(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	deps, calls := simDeps(t, simDevicesJSON(t,
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
	), fakePNG)

	_, _, err := executeCLI(t, deps, "sim", "shot", "--udid", simUDIDPro)
	if err == nil {
		t.Fatal("sim shot succeeded against a shut-down simulator")
	}
	if !strings.Contains(err.Error(), "Shutdown") || !strings.Contains(err.Error(), "iPhone 17 Pro") {
		t.Errorf("error %q does not report the device and its actual state", err)
	}
	for _, c := range *calls {
		for _, arg := range c {
			if arg == "boot" {
				t.Fatalf("sim shot tried to boot the shut-down device: %v", c)
			}
		}
	}
}

func TestSimShot_ScreenshotFailureSurfaces(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	listJSON := simDevicesJSON(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))
	deps, _ := simDeps(t, listJSON, fakePNG)
	deps.CommandOutput = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[1] == "list" {
			return []byte(listJSON), nil
		}
		return []byte("An error was encountered processing the command"), errors.New("exit status 1")
	}

	_, _, err := executeCLI(t, deps, "sim", "shot")
	if err == nil {
		t.Fatal("sim shot succeeded despite a failing screenshot")
	}
	if !strings.Contains(err.Error(), "An error was encountered") {
		t.Errorf("error %q drops simctl's own output", err)
	}
}

// TestSimShot_EmptyArtifactIsAnError guards the silent-success case: simctl can
// exit 0 without leaving a usable frame, and an empty PNG must never be handed
// to an agent as if it were a screen.
func TestSimShot_EmptyArtifactIsAnError(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-123")
	deps, _ := simDeps(t, simDevicesJSON(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted")), []byte{})

	_, _, err := executeCLI(t, deps, "sim", "shot")
	if err == nil {
		t.Fatal("sim shot reported success for a zero-byte capture")
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
}

func TestSimShot_OutputFlagOverridesArtifactPath(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	dest := filepath.Join(t.TempDir(), "nested", "screen.png")
	deps, _ := simDeps(t, simDevicesJSON(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted")), fakePNG)

	// No AO_SESSION_ID on purpose: --output is the escape hatch for running
	// outside a session.
	out, errOut, err := executeCLI(t, deps, "sim", "shot", "--output", dest, "--json")
	if err != nil {
		t.Fatalf("sim shot --output failed: %v\nstderr=%s", err, errOut)
	}
	var got simShotJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.Path != dest {
		t.Errorf("path = %q, want %q", got.Path, dest)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("artifact not written to --output: %v", err)
	}
}

func TestSimShot_WithoutSessionIDRequiresOutput(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	deps, _ := simDeps(t, simDevicesJSON(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted")), fakePNG)

	_, _, err := executeCLI(t, deps, "sim", "shot")
	if err == nil {
		t.Fatal("sim shot succeeded with no session and no --output")
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Errorf("error %q does not point at --output", err)
	}
}

// TestSimShot_ConcurrentSessionsDoNotCollide covers the multi-worker case: two
// sessions capturing the same device at the same instant must land in separate
// files, with no shared temp path in between.
func TestSimShot_ConcurrentSessionsDoNotCollide(t *testing.T) {
	cfg := setConfigEnv(t)
	listJSON := simDevicesJSON(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))

	paths := map[string]string{}
	for _, session := range []string{"agent-orchestrator-1", "agent-orchestrator-2"} {
		t.Setenv("AO_SESSION_ID", session)
		deps, _ := simDeps(t, listJSON, fakePNG)
		out, errOut, err := executeCLI(t, deps, "sim", "shot", "--json")
		if err != nil {
			t.Fatalf("sim shot for %s failed: %v\nstderr=%s", session, err, errOut)
		}
		var got simShotJSON
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("decode: %v\n%s", err, out)
		}
		paths[session] = got.Path
		if want := filepath.Join(cfg.dataDir, "sim", session); filepath.Dir(got.Path) != want {
			t.Errorf("%s wrote to %q, want %q", session, filepath.Dir(got.Path), want)
		}
	}
	if paths["agent-orchestrator-1"] == paths["agent-orchestrator-2"] {
		t.Fatalf("both sessions wrote the same artifact path %q", paths["agent-orchestrator-1"])
	}
	for session, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s artifact missing: %v", session, err)
		}
	}
}
