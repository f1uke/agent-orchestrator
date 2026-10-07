package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// withProcesses answers `ps` with these rows, as simproc reads it.
func withProcesses(deps Deps, rows ...string) (Deps, *int) {
	reads := 0
	inner := deps.CommandOutput
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ps" {
			reads++
			return []byte(strings.Join(rows, "\n") + "\n"), nil
		}
		return inner(ctx, name, args...)
	}
	return deps, &reads
}

const heldAppPath = "/Users/someone/Library/Developer/CoreSimulator/Devices/D/data/Containers/Bundle/Application/U/Nimbus.app/Nimbus"

// Measured on a real device: an lldb expression that timed out left the app
// `Ts` after lldb detached, and `ao sim ax` then spent a minute sampling it
// before saying nothing useful. A stopped app is named, with the command that
// resumes it, and is never sampled.
func TestSimAX_EmptyTreeFromAStoppedAppSaysSIGSTOPAndSamplesNothing(t *testing.T) {
	deps, probes := withSampler(hungAppDeps(t), blockedSampleReport, nil)
	deps, reads := withProcesses(deps, "4242 1 Ts "+heldAppPath)

	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil {
		t.Fatal("an empty tree must still fail")
	}
	got := err.Error()
	for _, want := range []string{"com.example.nimbus", "stopped by SIGSTOP", "an lldb expression that was interrupted", "`kill -CONT 4242`"} {
		if !strings.Contains(got, want) {
			t.Errorf("error must mention %q:\n%s", want, got)
		}
	}
	if *reads == 0 {
		t.Fatal("the process table was never read")
	}
	if *probes != 0 {
		t.Fatalf("a stopped app was sampled %d times; sampling it is what cost the minute", *probes)
	}
}

func TestSimAX_EmptyTreeFromADebuggedAppNamesTheDebugger(t *testing.T) {
	deps, probes := withSampler(hungAppDeps(t), blockedSampleReport, nil)
	deps, _ = withProcesses(deps,
		"900 880 S+ /usr/bin/lldb",
		"950 900 S /Applications/Xcode.app/Contents/SharedFrameworks/LLDB.framework/Versions/A/Resources/debugserver",
		"4242 950 SXs "+heldAppPath,
	)
	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil || !strings.Contains(err.Error(), "debugserver 950 (lldb 900)") || !strings.Contains(err.Error(), "`process detach`") {
		t.Fatalf("want the debugger named:\n%v", err)
	}
	if *probes != 0 {
		t.Fatalf("a debugged app was sampled %d times", *probes)
	}
}

func TestSimAX_ARunningAppThatAnswersNothingIsStillSampled(t *testing.T) {
	deps, probes := withSampler(hungAppDeps(t), blockedSampleReport, nil)
	deps, _ = withProcesses(deps, "4242 1 Ss "+heldAppPath)
	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil || !strings.Contains(err.Error(), "BLOCKED MAIN THREAD") || *probes == 0 {
		t.Fatalf("a running app that answers nothing is still sampled (probes=%d):\n%v", *probes, err)
	}
}

func TestSimType_PasteThatChangedNothingOnAStoppedAppSaysSIGSTOP(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, _, _ := pasteDeps(t, driver, simKeyboardUS, "hunter2")
	driver.snapshotQueue = nil
	driver.snapshot = simbridge.Snapshot{Frontmost: simbridge.Frontmost{BundleID: "com.example.nimbus", PID: 4242}}
	deps, probes := withSampler(deps, blockedSampleReport, nil)
	deps, _ = withProcesses(deps, "4242 1 T "+heldAppPath)

	_, _, err := executeCLI(t, deps, "sim", "type", "hunter2", "--paste")
	if err == nil || !strings.Contains(err.Error(), "`kill -CONT 4242`") {
		t.Fatalf("want the SIGSTOP named:\n%v", err)
	}
	if *probes != 0 {
		t.Fatalf("a stopped app was sampled %d times", *probes)
	}
}

// heldDeviceDeps is touchDeps on a booted device whose data directory is the
// one heldAppPath lives under, so a process table can place the app on it.
func heldDeviceDeps(t *testing.T, driver *fakeSimDriver) Deps {
	t.Helper()
	setConfigEnv(t)
	device := simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted")
	device["dataPath"] = "/Users/someone/Library/Developer/CoreSimulator/Devices/D/data"
	deps := simLeaseDeps(t, simDevicesJSON(t, device), fakePNG)
	deps.SimDriver = func(string) (simbridge.Driver, error) { return driver, nil }
	t.Setenv("AO_SESSION_ID", "mer-9")
	return deps
}

// Measured on a real device: with the app SIGSTOPped, the accessibility read
// itself waited a minute before coming back empty. A stopped app cannot answer,
// so the screen is not read at all.
func TestSimAX_AStoppedAppOnTheDeviceIsNamedBeforeTheScreenIsRead(t *testing.T) {
	driver := &fakeSimDriver{snapshot: simbridge.Snapshot{Frontmost: simbridge.Frontmost{BundleID: "com.example.nimbus", PID: 4242}}}
	deps, _ := withProcesses(heldDeviceDeps(t, driver), "4242 1 Ts "+heldAppPath)

	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil || !strings.Contains(err.Error(), "`kill -CONT 4242`") {
		t.Fatalf("want the SIGSTOP named with its fix:\n%v", err)
	}
	if driver.axReads != 0 {
		t.Fatalf("the screen was read %d times; a stopped app cannot answer, and the read is what costs the minute", driver.axReads)
	}
}

// An lldb that is attached and running (a logpoint session) leaves the app
// answering normally, and reading the screen while it runs is how a logpoint is
// driven. Only a SIGSTOP stops the read.
func TestSimAX_ADebuggedRunningAppIsStillRead(t *testing.T) {
	driver := &fakeSimDriver{snapshot: simbridge.Snapshot{Frontmost: simbridge.Frontmost{BundleID: "com.example.nimbus", PID: 4242}}}
	deps, _ := withProcesses(heldDeviceDeps(t, driver), "4242 950 SXs "+heldAppPath)

	_, _, _ = executeCLI(t, deps, "sim", "ax")
	if driver.axReads == 0 {
		t.Fatal("an app with a debugger attached and running must still be read")
	}
}

func TestSimTapByLabel_AStoppedAppIsNamedBeforeTheScreenIsRead(t *testing.T) {
	driver := &fakeSimDriver{snapshot: simbridge.Snapshot{Frontmost: simbridge.Frontmost{BundleID: "com.example.nimbus", PID: 4242}}}
	deps, _ := withProcesses(heldDeviceDeps(t, driver), "4242 1 T "+heldAppPath)

	_, _, err := executeCLI(t, deps, "sim", "tap", "--label", "Continue")
	if err == nil || !strings.Contains(err.Error(), "`kill -CONT 4242`") {
		t.Fatalf("want the SIGSTOP named with its fix:\n%v", err)
	}
	if driver.axReads != 0 || len(driver.gestures) != 0 {
		t.Fatalf("a stopped app was read %d times and touched %d times", driver.axReads, len(driver.gestures))
	}
}
