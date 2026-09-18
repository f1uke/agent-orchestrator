package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runDeps is `ao sim run`'s whole boundary: a worktree with an Xcode project in
// it, the two xcodebuild queries, the build child, and the simctl calls that
// follow. Every one of them is recorded, because the assertions that matter are
// about what did NOT run.
func runDeps(t *testing.T, schemes string) (Deps, *simDaemon, *[][]string, *[][]string) {
	t.Helper()
	deps, daemon, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "Nter", "com.example.Nter", "1.0", "1", "build A")

	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktree, "Nter.xcworkspace"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(worktree)

	// The bundle the build "produced". It needs a real Info.plist: the run reads
	// the identifier out of what it is about to install, so the app it launches
	// is the one it just built rather than whichever app is newest on the device.
	products := t.TempDir()
	app := filepath.Join(products, "Nter.app")
	if err := os.MkdirAll(app, 0o750); err != nil {
		t.Fatal(err)
	}
	info, err := json.Marshal(map[string]string{
		"CFBundleIdentifier":         "com.example.Nter",
		"CFBundleName":               "Nter",
		"CFBundleShortVersionString": "1.0",
		"CFBundleVersion":            "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), info, 0o600); err != nil {
		t.Fatal(err)
	}
	deps.CommandOutputInDir = func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, append([]string{name}, args...))
		line := strings.Join(args, " ")
		switch {
		case strings.Contains(line, "-list"):
			return []byte(`{"workspace":{"name":"Nter","schemes":[` + schemes + `]}}`), nil
		case strings.Contains(line, "-showBuildSettings"):
			return []byte(`[{"buildSettings":{"TARGET_BUILD_DIR":"` + products + `","FULL_PRODUCT_NAME":"Nter.app"}}]`), nil
		}
		return nil, errors.New("unexpected: " + line)
	}

	var builds [][]string
	deps.StartStream = func(_ context.Context, name string, args ...string) (ProcessStream, error) {
		builds = append(builds, append([]string{name}, args...))
		stream := newFakeStream()
		stream.feed("** BUILD SUCCEEDED **\n")
		return stream, nil
	}
	return deps, daemon, calls, &builds
}

func TestSimRun_BuildsInstallsAndLaunches(t *testing.T) {
	deps, daemon, calls, builds := runDeps(t, `"Nter"`)

	out, errOut, err := executeCLI(t, deps, "sim", "run")
	if err != nil {
		t.Fatalf("sim run failed: %v\nstderr=%s", err, errOut)
	}
	if len(*builds) != 1 {
		t.Fatalf("ran %d builds, want 1: %v", len(*builds), *builds)
	}
	build := strings.Join((*builds)[0], " ")
	for _, want := range []string{"-scheme Nter", "-configuration Debug", "-workspace"} {
		if !strings.Contains(build, want) {
			t.Fatalf("the build is missing %q: %s", want, build)
		}
	}
	if ranSimctl(*calls, "install") == nil || ranSimctl(*calls, "launch") == nil {
		t.Fatalf("a run must install AND launch: %+v", *calls)
	}
	if !simCalled(daemon, "POST /api/v1/sessions/mer-9/sim-leases") {
		t.Fatalf("no lease was taken:\n%s", daemon.callLog())
	}
	// The build output is the feedback, so it goes to the terminal as it
	// arrives rather than being held back until the end.
	if !strings.Contains(errOut, "BUILD SUCCEEDED") {
		t.Fatalf("the build output never reached the terminal:\n%s", errOut)
	}
	if !strings.Contains(out, "com.example.Nter") {
		t.Fatalf("a run must say what it launched:\n%s", out)
	}
}

// The whole point of Run is seeing the code that was just built. `simctl launch`
// against an already-running app brings the OLD process to the front, which is
// a build that succeeded and a screen that did not change.
func TestSimRun_TerminatesBeforeItLaunches(t *testing.T) {
	deps, _, calls, _ := runDeps(t, `"Nter"`)

	if _, errOut, err := executeCLI(t, deps, "sim", "run"); err != nil {
		t.Fatalf("sim run failed: %v\nstderr=%s", err, errOut)
	}
	terminate, launch := -1, -1
	for i, call := range *calls {
		if len(call) >= 3 && call[0] == "xcrun" {
			switch call[2] {
			case "terminate":
				terminate = i
			case "launch":
				launch = i
			}
		}
	}
	if terminate < 0 || launch < 0 || terminate > launch {
		t.Fatalf("the app must be terminated before it is launched: %+v", *calls)
	}
}

// The recorded hole, as a test: `xcodebuild -destination id=<udid>` consults no
// lease, so the build must never name a device at all.
func TestSimRun_TheBuildNeverNamesADevice(t *testing.T) {
	deps, _, _, builds := runDeps(t, `"Nter"`)

	if _, errOut, err := executeCLI(t, deps, "sim", "run", "--udid", simUDIDProMax); err != nil {
		t.Fatalf("sim run failed: %v\nstderr=%s", err, errOut)
	}
	build := strings.Join((*builds)[0], " ")
	if strings.Contains(build, simUDIDProMax) || strings.Contains(build, "id=") {
		t.Fatalf("the build was aimed at a device, which no lease can guard: %s", build)
	}
	if !strings.Contains(build, "generic/platform=iOS Simulator") {
		t.Fatalf("the build must target the simulator generically: %s", build)
	}
}

// Two sessions press Run. The second is refused at the lease - and crucially
// BEFORE the build, so the refusal costs a second rather than three minutes.
func TestSimRun_RefusedLeaseNeitherBuildsNorWrites(t *testing.T) {
	deps, daemon, calls, builds := runDeps(t, `"Nter"`)
	daemon.acquireStatus = 409
	daemon.acquireBody = `{"error":"conflict","code":"SIM_DEVICE_LEASED",` +
		`"message":"simulator is leased by @agent-orchestrator-105 for another 7m9s",` +
		`"details":{"udid":"` + simUDIDProMax + `","holder":"agent-orchestrator-105"}}`

	_, _, err := executeCLI(t, deps, "sim", "run")
	if err == nil {
		t.Fatal("running on a device another session holds must fail")
	}
	if len(*builds) != 0 {
		t.Fatalf("the build ran before the refusal, which is the wasted build this ordering exists to avoid: %v", *builds)
	}
	if ranSimctl(*calls, "install") != nil {
		t.Fatalf("the device was written to despite a refused lease: %+v", *calls)
	}
	if !strings.Contains(err.Error(), "agent-orchestrator-105") {
		t.Fatalf("the refusal must name the holder: %v", err)
	}
	if !strings.Contains(err.Error(), "Nothing was written to the device") {
		t.Fatalf("a refused write must say nothing happened: %v", err)
	}
}

// A failed build must reach the human, and must say the device is untouched -
// which is what somebody about to retry needs to know.
func TestSimRun_AFailedBuildInstallsNothingAndSaysSo(t *testing.T) {
	deps, _, calls, _ := runDeps(t, `"Nter"`)
	deps.StartStream = func(_ context.Context, name string, args ...string) (ProcessStream, error) {
		stream := newFakeStream()
		// A compiler error on stdout, then the non-zero exit xcodebuild reports
		// through Err() - the shape a real failed build arrives in.
		stream.feed("Nter/AppDelegate.swift:12:5: error: cannot find 'foo' in scope\n** BUILD FAILED **\n")
		stream.err = errors.New("exit status 65")
		return stream, nil
	}

	_, errOut, err := executeCLI(t, deps, "sim", "run")
	if err == nil {
		t.Fatal("a failed build must fail the command")
	}
	if !strings.Contains(errOut, "cannot find 'foo' in scope") {
		t.Fatalf("the compiler's own error never reached the terminal:\n%s", errOut)
	}
	if ranSimctl(*calls, "install") != nil {
		t.Fatalf("a failed build must install nothing: %+v", *calls)
	}
	if !strings.Contains(err.Error(), "Nothing was installed") {
		t.Fatalf("a failed build must say the device is as it was: %v", err)
	}
}

// Several schemes and none named: refuse and print the command for each, rather
// than guess. Building the wrong scheme installs the wrong app, and the two
// look identical on the device.
func TestSimRun_SeveralSchemesRefusesAndLists(t *testing.T) {
	deps, _, _, builds := runDeps(t, `"Nter","NterDev","NterStaging"`)

	_, _, err := executeCLI(t, deps, "sim", "run")
	if err == nil {
		t.Fatal("a project with three schemes has no default")
	}
	for _, want := range []string{"ao sim run --scheme Nter", "ao sim run --scheme NterDev", "ao sim run --scheme NterStaging"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must print %q: %v", want, err)
		}
	}
	if len(*builds) != 0 {
		t.Fatalf("nothing should have been built: %v", *builds)
	}
}

func TestSimRun_AnUnknownSchemeListsTheRealOnes(t *testing.T) {
	deps, _, _, _ := runDeps(t, `"Nter","NterDev"`)

	_, _, err := executeCLI(t, deps, "sim", "run", "--scheme", "Ntr")
	if err == nil {
		t.Fatal("a misspelled scheme must not build something else")
	}
	if !strings.Contains(err.Error(), "ao sim run --scheme NterDev") {
		t.Fatalf("the refusal must list what the project really has: %v", err)
	}
}

// The visibility test the run bar uses, from the CLI side: no project, no run.
func TestSimRun_NoXcodeProjectSaysWhereItLooked(t *testing.T) {
	deps, _, _, _ := runDeps(t, `"Nter"`)
	empty := t.TempDir()
	t.Chdir(empty)

	_, _, err := executeCLI(t, deps, "sim", "run")
	if err == nil {
		t.Fatal("a directory with no Xcode project has nothing to build")
	}
	if !strings.Contains(err.Error(), empty) {
		t.Fatalf("the error must say WHERE it looked, because the usual cause is the wrong directory: %v", err)
	}
}

// bootableRunDeps is runDeps with the chosen device SHUT DOWN, and a machine
// that reports it booted once the daemon has been asked to boot it. Both
// listings have to move: the daemon's, which the wait polls, and simctl's,
// which is what decides a device may be written to.
func bootableRunDeps(t *testing.T, alsoBooted []simDeviceListing) (Deps, *simDaemon, *[][]string, *[][]string) {
	t.Helper()
	deps, daemon, calls, builds := runDeps(t, `"Nter"`)

	booted := false
	inner := deps.CommandOutput
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[1] == "list" && args[2] == "devices" {
			state := "Shutdown"
			if booted {
				state = "Booted"
			}
			return []byte(simDevicesJSON(t,
				simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"),
				simDeviceWithData(simUDIDPro, "iPhone 17 Pro", state, t.TempDir()),
			)), nil
		}
		return inner(ctx, name, args...)
	}
	devices := append([]simDeviceListing{{UDID: simUDIDPro, Name: "iPhone 17 Pro", State: "Shutdown"}}, alsoBooted...)
	daemon.bootsOnPower(devices...)
	// simctl agrees with the daemon the moment the boot is asked for, which is
	// what production's device does a few seconds later.
	daemon.onPower = func() { booted = true }
	return deps, daemon, calls, builds
}

// The brief's case: a simulator is picked that is currently shut down. The run
// boots it on the way through rather than refusing, because "boot it yourself
// and press Run again" is a dead end the Run button should not have.
func TestSimRun_BootsAShutDownDeviceOnTheWayThrough(t *testing.T) {
	deps, daemon, calls, builds := bootableRunDeps(t, nil)

	out, errOut, err := executeCLI(t, deps, "sim", "run", "--udid", simUDIDPro)
	if err != nil {
		t.Fatalf("sim run failed: %v\nstderr=%s", err, errOut)
	}
	if len(daemon.powerRequests()) != 1 {
		t.Fatalf("the shut-down device was not booted: %v\n%s", daemon.powerRequests(), daemon.callLog())
	}
	if !strings.Contains(errOut, "Booting it") {
		t.Fatalf("a boot that costs a human tens of seconds must say it is happening:\n%s", errOut)
	}
	if len(*builds) != 1 || ranSimctl(*calls, "install") == nil {
		t.Fatalf("the run must go on to build and install: builds=%v calls=%+v", *builds, *calls)
	}
	if !strings.Contains(out, "Booted iPhone 17 Pro") {
		t.Fatalf("the result must say a device was powered on:\n%s", out)
	}
}

// The memory cap, reached. Two simulators are already up, so booting a third is
// refused - the same guard `ao sim boot` applies, because a second cap that
// could drift from the first is worse than none.
func TestSimRun_RefusesToBootPastTheMemoryCap(t *testing.T) {
	deps, daemon, calls, builds := bootableRunDeps(t, []simDeviceListing{
		{UDID: "11111111-1111-1111-1111-111111111111", Name: "iPhone 16", State: "Booted"},
		{UDID: "22222222-2222-2222-2222-222222222222", Name: "iPad Pro", State: "Booted"},
	})
	// simctl has to agree that two others are up: the budget counts real
	// devices, not the daemon's view of them.
	inner := deps.CommandOutput
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[1] == "list" && args[2] == "devices" {
			return []byte(simDevicesJSON(t,
				simDeviceFixture("11111111-1111-1111-1111-111111111111", "iPhone 16", "Booted"),
				simDeviceFixture("22222222-2222-2222-2222-222222222222", "iPad Pro", "Booted"),
				simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
			)), nil
		}
		return inner(ctx, name, args...)
	}

	_, _, err := executeCLI(t, deps, "sim", "run", "--udid", simUDIDPro)
	if err == nil {
		t.Fatal("booting a third simulator must be refused")
	}
	if len(daemon.powerRequests()) != 0 {
		t.Fatalf("a refused boot must not ask the daemon anyway: %v", daemon.powerRequests())
	}
	if len(*builds) != 0 || ranSimctl(*calls, "install") != nil {
		t.Fatalf("nothing should have been built or installed: builds=%v calls=%+v", *builds, *calls)
	}
	if !strings.Contains(err.Error(), "iPhone 16") || !strings.Contains(err.Error(), "iPad Pro") {
		t.Fatalf("the refusal must name what is already up: %v", err)
	}
}
