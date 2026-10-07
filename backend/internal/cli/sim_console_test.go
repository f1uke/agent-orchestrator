package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// consoleLaunches records every simctl run with extra environment, which is
// how a console launch reaches simctl, and answers it the way simctl does.
type consoleLaunches struct {
	mu    sync.Mutex
	calls [][]string
	envs  [][]string
}

func withConsoleLaunch(deps Deps) (Deps, *consoleLaunches) {
	rec := &consoleLaunches{}
	deps.CommandOutputWithEnv = func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.calls = append(rec.calls, append([]string{name}, args...))
		rec.envs = append(rec.envs, env)
		return []byte(args[len(args)-1] + ": 51234\n"), nil
	}
	return deps, rec
}

func consolePaths(t *testing.T, udid, bundleID string) (string, string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("AO_DATA_DIR"), "sim", "mer-9", "console")
	return filepath.Join(dir, udid+"-"+bundleID+".log"), filepath.Join(dir, udid+"-"+bundleID+".json")
}

func TestSimLaunchConsole_RemovesTheOldFileAndRedirectsBothStreamsUnbuffered(t *testing.T) {
	deps, _, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "Nimbus", "com.example.Nimbus", "1.0", "1", "nimbus")
	deps, launches := withConsoleLaunch(deps)
	logPath, recordPath := consolePaths(t, simUDIDProMax, "com.example.Nimbus")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		t.Fatal(err)
	}
	// simctl does not truncate: a stale file would keep the last run's tail.
	if err := os.WriteFile(logPath, []byte("stale output from the last launch\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := executeCLI(t, deps, "sim", "launch", "com.example.Nimbus", "--console")
	if err != nil {
		t.Fatalf("launch --console failed: %v\n%s", err, errOut)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("the old console file was not removed before the launch (stat err %v)", err)
	}
	if len(launches.calls) != 1 {
		t.Fatalf("launches = %v", launches.calls)
	}
	want := []string{"xcrun", "simctl", "launch", "--terminate-running-process",
		"--stdout=" + logPath, "--stderr=" + logPath, simUDIDProMax, "com.example.Nimbus"}
	if strings.Join(launches.calls[0], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("launched\n%q\nwant\n%q", launches.calls[0], want)
	}
	if strings.Join(launches.envs[0], " ") != "SIMCTL_CHILD_NSUnbufferedIO=YES" {
		t.Fatalf("env = %q; without it a file-backed print is fully buffered and nothing appears", launches.envs[0])
	}
	if ranSimctl(*calls, "launch") != nil {
		t.Fatal("the app was launched a second time without the redirect")
	}

	raw, err := os.ReadFile(recordPath) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("no launch record: %v", err)
	}
	var record simConsoleRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.PID != 51234 || record.BundleID != "com.example.Nimbus" || record.UDID != simUDIDProMax || record.LaunchedAt.IsZero() {
		t.Fatalf("record = %+v", record)
	}
	for _, want := range []string{"Console: " + logPath, "ao sim console", "Maestro", "/dev/null"} {
		if !strings.Contains(out, want) {
			t.Errorf("launch output does not say %q:\n%s", want, out)
		}
	}
}

func TestSimLaunch_WithoutConsoleKeepsTheOldLaunch(t *testing.T) {
	deps, _, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "Nimbus", "com.example.Nimbus", "1.0", "1", "nimbus")
	deps, launches := withConsoleLaunch(deps)
	if _, _, err := executeCLI(t, deps, "sim", "launch", "com.example.Nimbus"); err != nil {
		t.Fatal(err)
	}
	if len(launches.calls) != 0 || ranSimctl(*calls, "launch") == nil {
		t.Fatalf("a plain launch changed: console=%v", launches.calls)
	}
}

func TestSimRunConsole_LaunchesWithTheRedirect(t *testing.T) {
	deps, _, calls, _ := runDeps(t, `"Nter"`)
	deps, launches := withConsoleLaunch(deps)
	logPath, _ := consolePaths(t, simUDIDProMax, "com.example.Nter")

	out, errOut, err := executeCLI(t, deps, "sim", "run", "--console")
	if err != nil {
		t.Fatalf("run --console failed: %v\n%s", err, errOut)
	}
	if len(launches.calls) != 1 || !strings.Contains(strings.Join(launches.calls[0], " "), "--stdout="+logPath) {
		t.Fatalf("console launches = %v", launches.calls)
	}
	if ranSimctl(*calls, "launch") != nil {
		t.Fatal("run --console also launched without the redirect")
	}
	if !strings.Contains(out, "Console: "+logPath) {
		t.Fatalf("run output does not name the file:\n%s", out)
	}
}

// consoleMachine is a debugMachine whose app was launched with --console: a
// file of output and the record of which pid wrote it.
func consoleMachine(t *testing.T, recordedPID int, lines ...string) (*debugMachine, string) {
	t.Helper()
	m := newDebugMachine(t)
	logPath, recordPath := consolePaths(t, simUDIDProMax, "com.example.Nimbus")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	if err := os.WriteFile(logPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	record, _ := json.Marshal(simConsoleRecord{PID: recordedPID, BundleID: "com.example.Nimbus", UDID: simUDIDProMax,
		LaunchedAt: time.Date(2026, 10, 8, 5, 0, 0, 0, time.UTC)})
	if err := os.WriteFile(recordPath, record, 0o600); err != nil {
		t.Fatal(err)
	}
	return m, logPath
}

func TestSimConsole_ReadsTheCaptureOfTheRunningApp(t *testing.T) {
	m, logPath := consoleMachine(t, 601, "tick 1", "resp: {\"ok\":true}", "tick 2")
	m.ps([]string{m.row(601, 500, "Ss")})

	out, _, err := executeCLI(t, m.deps, "sim", "console")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{logPath, "pid 601", "tick 1", "resp: {\"ok\":true}", "tick 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "relaunched") || strings.Contains(out, "not running") {
		t.Fatalf("a current capture is reported as stale:\n%s", out)
	}
}

func TestSimConsole_SaysSoAboveTheLinesWhenTheAppWasRelaunched(t *testing.T) {
	m, _ := consoleMachine(t, 601, "tick 1")
	m.ps([]string{m.row(777, 500, "Ss")})

	out, _, err := executeCLI(t, m.deps, "sim", "console")
	if err != nil {
		t.Fatal(err)
	}
	warning := strings.Index(out, "relaunched")
	line := strings.Index(out, "tick 1")
	if warning < 0 || line < 0 || warning > line {
		t.Fatalf("the relaunch must be said above the lines:\n%s", out)
	}
	for _, want := range []string{"pid 777", "not in the file", "ao sim launch --console"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not say %q:\n%s", want, out)
		}
	}
}

func TestSimConsole_SaysSoWhenTheAppIsNotRunning(t *testing.T) {
	m, _ := consoleMachine(t, 601, "last words")
	m.ps([]string{"500 1 Ss /sbin/launchd_sim"})

	out, _, err := executeCLI(t, m.deps, "sim", "console")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not running") || !strings.Contains(out, "last words") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestSimConsole_GrepAndMaxLinesKeepTheNewestMatches(t *testing.T) {
	m, _ := consoleMachine(t, 601, "resp 1", "noise", "resp 2", "resp 3")
	m.ps([]string{m.row(601, 500, "Ss")})

	out, _, err := executeCLI(t, m.deps, "sim", "console", "--grep", "^resp", "--max-lines", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got simConsoleResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if strings.Join(got.Lines, ",") != "resp 2,resp 3" || got.Matched != 3 || got.Dropped != 1 || got.Capture != simConsoleCurrent {
		t.Fatalf("result = %+v", got)
	}
}

func TestSimConsole_NoFileSaysHowToMakeOne(t *testing.T) {
	m := newDebugMachine(t)
	m.ps([]string{m.row(601, 500, "Ss")})
	_, _, err := executeCLI(t, m.deps, "sim", "console")
	if err == nil || !strings.Contains(err.Error(), "ao sim launch --console") {
		t.Fatalf("want a refusal naming `ao sim launch --console`, got %v", err)
	}
}

func TestSimConsole_RefusesAnotherSessionsDevice(t *testing.T) {
	m, _ := consoleMachine(t, 601, "x")
	t.Setenv("AO_SIM_UDID", simUDIDPro)
	_, _, err := executeCLI(t, m.deps, "sim", "console")
	if err == nil || !strings.Contains(err.Error(), "@crew-2") {
		t.Fatalf("got %v", err)
	}
}

func TestSimConsole_FollowPrintsWhatIsAppendedUntilStopped(t *testing.T) {
	m, logPath := consoleMachine(t, 601, "before")
	m.ps([]string{m.row(601, 500, "Ss")})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, done := executeCLIStreamingContext(ctx, t, m.deps, "sim", "console", "--follow", "--grep", "keep")
	waitFor(t, func() bool { return strings.Contains(out.String(), logPath) }, "the follow never started")

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("keep: first\ndrop this\nkeep: sec")
	_, _ = f.WriteString("ond\n")
	_ = f.Close()
	waitFor(t, func() bool { return strings.Contains(out.String(), "keep: second") }, "an appended line never arrived")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a stopped follow is not a failure: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the follow did not stop")
	}
	got := out.String()
	if !strings.Contains(got, "keep: first") || strings.Contains(got, "drop this") {
		t.Fatalf("--grep was not applied to the follow:\n%s", got)
	}
}
