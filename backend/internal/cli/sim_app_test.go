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

// A device fixture that also says where it keeps its data, which is where its
// installed apps live. Everything the build fingerprint reads is under there,
// so a test lays out a directory rather than mocking a reader.
func simDeviceWithData(udid, name, state, dataPath string) map[string]any {
	fixture := simDeviceFixture(udid, name, state)
	fixture["dataPath"] = dataPath
	return fixture
}

// installFixture writes an app bundle into a device's data directory exactly
// where CoreSimulator puts one.
func installFixture(t *testing.T, dataPath, name, bundleID, version, number, payload string) string {
	t.Helper()
	app := filepath.Join(dataPath, "Containers", "Bundle", "Application", bundleID, name+".app")
	if err := os.MkdirAll(app, 0o750); err != nil {
		t.Fatal(err)
	}
	info, err := json.Marshal(map[string]string{
		"CFBundleIdentifier":         bundleID,
		"CFBundleName":               name,
		"CFBundleShortVersionString": version,
		"CFBundleVersion":            number,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), info, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, name), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return app
}

// appDeps is the `ao sim install` / `ao sim launch` boundary: a device with a
// data directory, plus the three tools those paths shell out to. Every simctl
// call is recorded so a test can assert what ran - and, more importantly, what
// did NOT run when the lease was refused.
func appDeps(t *testing.T) (Deps, *simDaemon, string, *[][]string) {
	t.Helper()
	cfg := setConfigEnv(t)
	dataPath := t.TempDir()
	listJSON := simDevicesJSON(t,
		simDeviceWithData(simUDIDProMax, "iPhone 17 Pro Max", "Booted", dataPath),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)
	deps, calls := simDeps(t, listJSON, fakePNG)
	deps.ProcessAlive = func(int) bool { return true }
	inner := deps.CommandOutput
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "plutil":
			*calls = append(*calls, append([]string{name}, args...))
			return os.ReadFile(args[len(args)-1]) //nolint:gosec // test fixture
		case name == "codesign":
			*calls = append(*calls, append([]string{name}, args...))
			// Unsigned, which is what a simulator build usually is; the
			// fingerprint falls back to hashing the bundle.
			return []byte("code object is not signed at all"), errors.New("exit 1")
		case len(args) >= 2 && (args[1] == "install" || args[1] == "launch" || args[1] == "terminate"):
			*calls = append(*calls, append([]string{name}, args...))
			if args[1] == "launch" {
				return []byte(args[len(args)-1] + ": 51234\n"), nil
			}
			return nil, nil
		}
		return inner(ctx, name, args...)
	}
	daemon := newSimDaemon(t, cfg)
	t.Setenv("AO_SESSION_ID", "mer-9")
	return deps, daemon, dataPath, calls
}

// touchLater makes one install unambiguously the newest, without a test having
// to sleep for a filesystem timestamp to move.
func touchLater(t *testing.T, app string) {
	t.Helper()
	later := time.Now().Add(time.Hour)
	for _, path := range []string{app, filepath.Dir(app)} {
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatalf("touch %s: %v", path, err)
		}
	}
}

func ranSimctl(calls [][]string, verb string) []string {
	for _, call := range calls {
		if len(call) >= 3 && call[0] == "xcrun" && call[2] == verb {
			return call
		}
	}
	return nil
}

func TestSimInstall_TakesTheLeaseBeforeItWritesToTheDevice(t *testing.T) {
	deps, daemon, dataPath, calls := appDeps(t)
	bundle := installFixture(t, t.TempDir(), "MyApp", "com.example.MyApp", "1.0", "1", "build A")
	// The device already carries the app, so the install has something to read
	// back afterwards.
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")

	out, errOut, err := executeCLI(t, deps, "sim", "install", bundle)
	if err != nil {
		t.Fatalf("install failed: %v\nstderr=%s", err, errOut)
	}
	if !simCalled(daemon, "POST /api/v1/sessions/mer-9/sim-leases") {
		t.Fatalf("no lease was taken:\n%s", daemon.callLog())
	}
	install := ranSimctl(*calls, "install")
	if install == nil {
		t.Fatalf("simctl install never ran: %+v", *calls)
	}
	if install[3] != simUDIDProMax || install[4] != bundle {
		t.Fatalf("installed %v, want %s onto %s", install, bundle, simUDIDProMax)
	}
	if !strings.Contains(out, "Build: com.example.MyApp 1.0 (1) sha256:") {
		t.Fatalf("an install must report what is on the device now:\n%s", out)
	}
	if !strings.Contains(out, "ao sim release") {
		t.Fatalf("an install that takes the device must say how to give it back:\n%s", out)
	}
}

// The incident, as a test. A claim was refused and the install ran anyway
// because nothing chained them. Here the lease IS the install's first act, so a
// refusal means nothing reached the device.
func TestSimInstall_RefusedLeaseWritesNothing(t *testing.T) {
	deps, daemon, _, calls := appDeps(t)
	daemon.acquireStatus = 409
	daemon.acquireBody = `{"error":"conflict","code":"SIM_DEVICE_LEASED",` +
		`"message":"simulator is leased by @nter-ios-app-69 for another 7m9s",` +
		`"details":{"udid":"` + simUDIDProMax + `","holder":"nter-ios-app-69","expiresAt":"2026-08-28T09:10:00Z"}}`
	bundle := installFixture(t, t.TempDir(), "MyApp", "com.example.MyApp", "1.0", "1", "build B")

	_, _, err := executeCLI(t, deps, "sim", "install", bundle)
	if err == nil {
		t.Fatal("installing onto a device another session holds must fail")
	}
	if ranSimctl(*calls, "install") != nil {
		t.Fatalf("the device was written to despite a refused lease: %+v", *calls)
	}
	if !strings.Contains(err.Error(), "nter-ios-app-69") {
		t.Fatalf("the refusal must name the holder: %v", err)
	}
	if !strings.Contains(err.Error(), "Nothing was written to the device") {
		t.Fatalf("the refusal must say the device is untouched: %v", err)
	}
}

// A path that is not an app bundle is caught BEFORE a device is taken, so a
// typo does not hold somebody's simulator for ten minutes for nothing.
func TestSimInstall_ABadPathTakesNoDevice(t *testing.T) {
	deps, daemon, _, calls := appDeps(t)

	_, _, err := executeCLI(t, deps, "sim", "install", filepath.Join(t.TempDir(), "MyApp.app"))
	if err == nil {
		t.Fatal("installing a bundle that is not there must fail")
	}
	if simCalled(daemon, "POST /api/v1/sessions/mer-9/sim-leases") {
		t.Fatalf("a device was claimed for a path that does not exist:\n%s", daemon.callLog())
	}
	if ranSimctl(*calls, "install") != nil {
		t.Fatalf("simctl ran for a path that does not exist: %+v", *calls)
	}
}

func TestSimLaunch_StartsTheSingleInstalledApp(t *testing.T) {
	deps, daemon, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")

	out, errOut, err := executeCLI(t, deps, "sim", "launch")
	if err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	if !simCalled(daemon, "POST /api/v1/sessions/mer-9/sim-leases") {
		t.Fatalf("no lease was taken:\n%s", daemon.callLog())
	}
	launch := ranSimctl(*calls, "launch")
	if launch == nil || launch[4] != "com.example.MyApp" {
		t.Fatalf("launched %v, want the single installed app", launch)
	}
	if ranSimctl(*calls, "terminate") != nil {
		t.Fatal("launch terminated the app without being asked to")
	}
	if !strings.Contains(out, "pid 51234") {
		t.Fatalf("a launch should report the pid simctl gave it:\n%s", out)
	}
}

// Straight after an install, a running instance is still the OLD code. This is
// the flag that makes the screen show what was just installed.
func TestSimLaunch_TerminateFirstRelaunchesTheInstalledCode(t *testing.T) {
	deps, _, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")

	out, errOut, err := executeCLI(t, deps, "sim", "launch", "--terminate-first")
	if err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	terminate := ranSimctl(*calls, "terminate")
	if terminate == nil || terminate[4] != "com.example.MyApp" {
		t.Fatalf("terminate = %v, want the app terminated first", terminate)
	}
	if !strings.Contains(out, "Terminated and relaunched") {
		t.Fatalf("output must say the app was restarted:\n%s", out)
	}
}

// A developer's simulator accumulates apps - the one this was measured on had
// nine - so several candidates resolve to the newest install and the launch
// says out loud that it chose. Silence would be the problem; refusing outright
// would make the command useless on the machines it is for.
func TestSimLaunch_PicksTheNewestInstallAndSaysSo(t *testing.T) {
	deps, _, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "One", "com.example.One", "1.0", "1", "a")
	newest := installFixture(t, dataPath, "Two", "com.example.Two", "1.0", "1", "b")
	touchLater(t, newest)

	out, errOut, err := executeCLI(t, deps, "sim", "launch")
	if err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	launch := ranSimctl(*calls, "launch")
	if launch == nil || launch[4] != "com.example.Two" {
		t.Fatalf("launched %v, want the most recently installed app", launch)
	}
	if !strings.Contains(out, "newest of 2 apps") {
		t.Fatalf("a launch that chose must say so:\n%s", out)
	}
	if !strings.Contains(out, "AO_SIM_APP") {
		t.Fatalf("a launch that chose must name the way to pin it:\n%s", out)
	}
}

// $AO_SIM_APP is the standing answer for a project whose sessions always test
// the same app, so nothing has to be passed per command.
func TestSimLaunch_EnvironmentPinsTheApp(t *testing.T) {
	deps, _, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "One", "com.example.One", "1.0", "1", "a")
	newest := installFixture(t, dataPath, "Two", "com.example.Two", "1.0", "1", "b")
	touchLater(t, newest)
	t.Setenv("AO_SIM_APP", "com.example.One")

	out, errOut, err := executeCLI(t, deps, "sim", "launch")
	if err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	launch := ranSimctl(*calls, "launch")
	if launch == nil || launch[4] != "com.example.One" {
		t.Fatalf("launched %v, want the app $AO_SIM_APP names", launch)
	}
	if strings.Contains(out, "newest of") {
		t.Fatalf("a pinned launch reported itself as a pick:\n%s", out)
	}
}

// The XCUITest host `xcodebuild test` installs beside the app is never the
// thing under test, so it does not make a device ambiguous.
func TestSimLaunch_IgnoresTheXCTestRunner(t *testing.T) {
	deps, _, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "a")
	installFixture(t, dataPath, "MyAppUITests-Runner", "com.example.MyAppUITests.xctrunner", "1.0", "1", "b")

	if _, errOut, err := executeCLI(t, deps, "sim", "launch"); err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	launch := ranSimctl(*calls, "launch")
	if launch == nil || launch[4] != "com.example.MyApp" {
		t.Fatalf("launched %v, want the app rather than its test runner", launch)
	}
}

func TestSimInstallLaunch_JSONCarriesTheBuild(t *testing.T) {
	deps, _, dataPath, _ := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "2.1", "99", "build A")

	out, _, err := executeCLI(t, deps, "sim", "launch", "--json")
	if err != nil {
		t.Fatalf("launch failed: %v", err)
	}
	var res struct {
		BundleID string `json:"bundleId"`
		Build    *struct {
			ID       string `json:"id"`
			BundleID string `json:"bundleId"`
			Version  string `json:"version"`
			Digest   string `json:"digest"`
		} `json:"build"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if res.BundleID != "com.example.MyApp" || res.Build == nil {
		t.Fatalf("json = %s", out)
	}
	if res.Build.Version != "2.1" || !strings.HasPrefix(res.Build.Digest, "sha256:") {
		t.Fatalf("build = %+v", res.Build)
	}
	if want := fmt.Sprintf("com.example.MyApp 2.1 (99) %s", res.Build.Digest); res.Build.ID != want {
		t.Fatalf("id = %q, want %q", res.Build.ID, want)
	}
}
