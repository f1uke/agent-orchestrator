package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	simUDIDBase  = "11111111-0000-0000-0000-00000000BA5E"
	debugSession = "mer-9"
)

// debugMachine is this session's own primary device with Nimbus installed on
// it, a base and a crewmate's device the daemon knows about, and a process
// table a test scripts one read at a time.
type debugMachine struct {
	deps    Deps
	daemon  *simDaemon
	data    string // the device's data directory
	app     string // the installed Nimbus.app
	exe     string // its main executable, as ps reports it
	calls   *[][]string
	mu      sync.Mutex
	tables  [][]string
	psReads int
}

func newDebugMachine(t *testing.T) *debugMachine {
	t.Helper()
	deps, daemon, dataPath, calls := appDeps(t)
	m := &debugMachine{deps: deps, daemon: daemon, calls: calls, data: dataPath}
	m.app = installFixture(t, dataPath, "Nimbus", "com.example.Nimbus", "2.4.0", "318", "nimbus build")
	m.exe = filepath.Join(m.app, "Nimbus")
	daemon.clones = []simCloneClient{
		{UDID: simUDIDProMax, SessionID: debugSession, Label: "primary", Primary: true},
		{UDID: simUDIDPro, SessionID: "crew-2", Label: "primary", Primary: true},
	}
	daemon.bases = []simBaseClient{{Name: "iPhone 17", Key: "iphone", UDID: simUDIDBase}}
	t.Setenv("AO_SIM_UDID", simUDIDProMax)
	t.Setenv("AO_SIM_APP", "")
	inner := m.deps.CommandOutput
	m.deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ps" {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.psReads++
			rows := m.tables[0]
			if len(m.tables) > 1 {
				m.tables = m.tables[1:]
			}
			return []byte(strings.Join(rows, "\n") + "\n"), nil
		}
		if name == "kill" {
			*m.calls = append(*m.calls, append([]string{name}, args...))
			return nil, nil
		}
		return inner(ctx, name, args...)
	}
	return m
}

// ps scripts the process table: each call to `ps` takes the next table, and
// the last one answers every read after it.
func (m *debugMachine) ps(tables ...[]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tables = tables
}

func (m *debugMachine) reads() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.psReads
}

func (m *debugMachine) row(pid, ppid int, stat string) string {
	return fmt.Sprintf("%d %d %s %s", pid, ppid, stat, m.exe)
}

var debuggerRows = []string{
	"900 880 S+ /usr/bin/lldb",
	"950 900 S /Applications/Xcode.app/Contents/SharedFrameworks/LLDB.framework/Versions/A/Resources/debugserver",
}

func TestSimPID_PrintsOnlyThePidOnStdoutAndTheStateOnStderr(t *testing.T) {
	m := newDebugMachine(t)
	m.ps([]string{m.row(601, 500, "Ss")})

	out, errOut, err := executeCLI(t, m.deps, "sim", "pid")
	if err != nil {
		t.Fatalf("pid failed: %v\n%s", err, errOut)
	}
	if out != "601\n" {
		t.Fatalf("stdout = %q, want exactly the pid so `lldb -p $(ao sim pid)` works", out)
	}
	if !strings.Contains(errOut, "com.example.Nimbus") || !strings.Contains(errOut, "running") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestSimPID_ADebuggedAppStillPrintsThePidAndNamesTheHolder(t *testing.T) {
	m := newDebugMachine(t)
	m.ps(append(append([]string{}, debuggerRows...), m.row(601, 950, "SXs")))

	out, errOut, err := executeCLI(t, m.deps, "sim", "pid", "com.example.Nimbus")
	if err != nil {
		t.Fatalf("pid failed: %v", err)
	}
	if out != "601\n" {
		t.Fatalf("stdout = %q", out)
	}
	for _, want := range []string{"attached by debugserver 950 (lldb 900)", "`process detach`"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q does not say %q", errOut, want)
		}
	}
}

func TestSimPID_NotRunningFailsNamingLaunch(t *testing.T) {
	m := newDebugMachine(t)
	m.ps([]string{"500 1 Ss /sbin/launchd_sim"})

	out, _, err := executeCLI(t, m.deps, "sim", "pid")
	if err == nil || !strings.Contains(err.Error(), "ao sim launch") {
		t.Fatalf("want a failure naming `ao sim launch`, got %v", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q; a pid that does not exist must not be printed", out)
	}
}

func TestSimPID_JSONIsTheWholeResult(t *testing.T) {
	m := newDebugMachine(t)
	m.ps([]string{m.row(601, 500, "T")})

	out, _, err := executeCLI(t, m.deps, "sim", "pid", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got simPIDResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if got.PID != 601 || got.State != simAppSIGSTOP || got.BundleID != "com.example.Nimbus" || got.UDID != simUDIDProMax ||
		got.Hold == nil || !strings.Contains(got.Message, "kill -CONT 601") {
		t.Fatalf("result = %+v", got)
	}
}

func TestSimPID_SaysWhenItChoseBetweenApps(t *testing.T) {
	m := newDebugMachine(t)
	installFixture(t, m.data, "Lantern", "com.example.Lantern", "1.0", "1", "lantern")
	touchLater(t, m.app)
	m.ps([]string{m.row(601, 500, "Ss")})

	_, errOut, err := executeCLI(t, m.deps, "sim", "pid")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "newest of 2 apps") || !strings.Contains(errOut, "$AO_SIM_APP") {
		t.Fatalf("stderr = %q, want the choice said out loud", errOut)
	}
}

func TestSimPID_RefusesABaseDeviceAndReadsNothing(t *testing.T) {
	m := newDebugMachine(t)
	t.Setenv("AO_SIM_UDID", simUDIDBase)
	m.ps([]string{m.row(601, 500, "Ss")})

	_, _, err := executeCLI(t, m.deps, "sim", "pid")
	if err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("want a base device refused, got %v", err)
	}
	if m.reads() != 0 {
		t.Fatal("the process table was read for a device this command refused")
	}
}

func TestSimPID_RefusesAnotherSessionsDevice(t *testing.T) {
	m := newDebugMachine(t)
	t.Setenv("AO_SIM_UDID", simUDIDPro)

	_, _, err := executeCLI(t, m.deps, "sim", "pid")
	if err == nil || !strings.Contains(err.Error(), "@crew-2") {
		t.Fatalf("want a crewmate's device refused naming its holder, got %v", err)
	}
}

func TestSimPID_DeviceLabelNamesOneOfThisSessionsDevices(t *testing.T) {
	m := newDebugMachine(t)
	t.Setenv("AO_SIM_UDID", simUDIDPro) // a crewmate's: --device must win over it
	m.daemon.clones = append(m.daemon.clones, simCloneClient{UDID: simUDIDProMax, SessionID: debugSession, Label: "dbg"})
	m.ps([]string{m.row(601, 500, "Ss")})

	out, _, err := executeCLI(t, m.deps, "sim", "pid", "--device", "dbg")
	if err != nil || out != "601\n" {
		t.Fatalf("--device dbg: out=%q err=%v", out, err)
	}
}

func TestSimPID_HasNoUDIDFlag(t *testing.T) {
	m := newDebugMachine(t)
	_, _, err := executeCLI(t, m.deps, "sim", "pid", "--udid", simUDIDPro)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("`ao sim pid --udid` must not exist: %v", err)
	}
}

func TestSimPID_NeedsASession(t *testing.T) {
	m := newDebugMachine(t)
	t.Setenv("AO_SESSION_ID", "")
	_, _, err := executeCLI(t, m.deps, "sim", "pid")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "AO_SESSION_ID") {
		t.Fatalf("want a usage error naming AO_SESSION_ID, got %v", err)
	}
}

// lldbRun records the lldb this command started and lets a test play it.
type lldbRun struct {
	mu     sync.Mutex
	args   []string
	stream *fakeStream
}

func (m *debugMachine) lldb() *lldbRun {
	run := &lldbRun{stream: newFakeStream()}
	m.deps.StartStream = func(_ context.Context, name string, args ...string) (ProcessStream, error) {
		run.mu.Lock()
		defer run.mu.Unlock()
		run.args = append([]string{name}, args...)
		return run.stream, nil
	}
	return run
}

func (r *lldbRun) started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.args
}

func TestSimLLDB_AttachesToTheAppWithTheCallersArgs(t *testing.T) {
	m := newDebugMachine(t)
	run := m.lldb()
	run.stream.feed("(lldb) bt\n* frame #0: Nimbus`ForecastStore.load()\n")
	m.ps([]string{m.row(601, 500, "Ss")})

	out, errOut, err := executeCLI(t, m.deps, "sim", "lldb", "--", "-o", "bt")
	if err != nil {
		t.Fatalf("lldb failed: %v\n%s", err, errOut)
	}
	if got := strings.Join(run.started(), " "); got != "lldb --batch -p 601 -o bt" {
		t.Fatalf("started %q", got)
	}
	if !strings.Contains(out, "ForecastStore.load()") {
		t.Fatalf("lldb's output was not streamed: %q", out)
	}
	if !strings.Contains(errOut, "running and detached") {
		t.Fatalf("the end state was not reported: %q", errOut)
	}
}

func TestSimLLDB_ContinueForAppendsTheLogpointRecipe(t *testing.T) {
	m := newDebugMachine(t)
	run := m.lldb()
	run.stream.feed("")
	m.ps([]string{m.row(601, 500, "Ss")})

	_, _, err := executeCLI(t, m.deps, "sim", "lldb", "com.example.Nimbus", "--continue-for", "4s", "--",
		"-o", "breakpoint set -n VC.tick -C 'frame variable self.count' -G true")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lldb", "--batch", "-p", "601",
		"-o", "breakpoint set -n VC.tick -C 'frame variable self.count' -G true",
		"-o", "script lldb.debugger.SetAsync(True)",
		"-o", "process continue",
		"-o", "script import time; time.sleep(4)",
		"-o", "process interrupt",
		"-o", "process detach",
	}
	if got := run.started(); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("started\n%q\nwant\n%q", got, want)
	}
}

func TestSimLLDB_RefusesMisuseBeforeStartingAnything(t *testing.T) {
	cases := [][]string{
		{"sim", "lldb"}, // nothing for lldb to do
		{"sim", "lldb", "--continue-for", "2m", "--", "-o", "bt"},                    // not shorter than the default timeout
		{"sim", "lldb", "--timeout", "0s", "--", "-o", "bt"},                         // no time at all
		{"sim", "lldb", "com.example.Nimbus", "com.example.Other", "--", "-o", "bt"}, // two apps
	}
	for _, args := range cases {
		m := newDebugMachine(t)
		run := m.lldb()
		m.ps([]string{m.row(601, 500, "Ss")})
		_, _, err := executeCLI(t, m.deps, args...)
		if err == nil || ExitCode(err) != 2 {
			t.Errorf("%q: want a usage error, got %v", args, err)
		}
		if run.started() != nil {
			t.Errorf("%q: lldb was started", args)
		}
	}
}

func TestSimLLDB_RefusesASecondAttachNamingTheHolder(t *testing.T) {
	m := newDebugMachine(t)
	run := m.lldb()
	m.ps(append(append([]string{}, debuggerRows...), m.row(601, 950, "SXs")))

	_, _, err := executeCLI(t, m.deps, "sim", "lldb", "--", "-o", "bt")
	if err == nil || !strings.Contains(err.Error(), "debugserver 950 (lldb 900)") {
		t.Fatalf("want the holder named, got %v", err)
	}
	if run.started() != nil {
		t.Fatal("lldb was started against an app that already has a debugger")
	}
}

// The measured hazard: `process continue` with no breakpoint that stops waits
// for ever, keeping the app attached. The deadline ends the lldb this command
// started - and only it - and the app is then read again.
func TestSimLLDB_TheDeadlineEndsLLDBAndTheAppIsReadAgain(t *testing.T) {
	m := newDebugMachine(t)
	run := m.lldb() // never fed: an lldb that never returns
	m.ps(
		[]string{m.row(601, 500, "Ss")},
		append(append([]string{}, debuggerRows...), m.row(601, 950, "SXs")), // still attached as it dies
		[]string{m.row(601, 500, "Ss")},
	)

	done := make(chan error, 1)
	var errOut string
	go func() {
		var err error
		_, errOut, err = executeCLI(t, m.deps, "sim", "lldb", "--timeout", "50ms", "--", "-o", "process continue")
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ao sim lldb did not end at its deadline")
	}
	if run.stream.stops() == 0 {
		t.Fatal("the lldb child was never stopped")
	}
	if err == nil || !strings.Contains(err.Error(), "--timeout") {
		t.Fatalf("a timed-out lldb must fail naming --timeout, got %v", err)
	}
	if !strings.Contains(errOut, "running and detached") {
		t.Fatalf("the app's state after the kill was not reported: %q", errOut)
	}
	if m.reads() < 3 {
		t.Fatalf("the table was read %d times; it must be re-read until the debugger has let go", m.reads())
	}
}

func TestSimLLDB_AnAppLeftStoppedIsALoudFailure(t *testing.T) {
	m := newDebugMachine(t)
	run := m.lldb()
	run.stream.feed("")
	m.ps([]string{m.row(601, 500, "Ss")}, []string{m.row(601, 500, "Ts")})

	_, _, err := executeCLI(t, m.deps, "sim", "lldb", "--", "-o", "expression -- (void)objc_refs()")
	if err == nil || !strings.Contains(err.Error(), "`kill -CONT 601`") || !strings.Contains(err.Error(), "--resume") {
		t.Fatalf("want a failure naming kill -CONT and --resume, got %v", err)
	}
	if ranSimctlName(*m.calls, "kill") != nil {
		t.Fatal("the app was resumed without --resume")
	}
}

func TestSimLLDB_ResumeContinuesAnAppLeftStopped(t *testing.T) {
	m := newDebugMachine(t)
	run := m.lldb()
	run.stream.feed("")
	m.ps([]string{m.row(601, 500, "Ss")}, []string{m.row(601, 500, "Ts")}, []string{m.row(601, 500, "Ss")})

	_, errOut, err := executeCLI(t, m.deps, "sim", "lldb", "--resume", "--", "-o", "bt")
	if err != nil {
		t.Fatalf("a resumed app is a success: %v\n%s", err, errOut)
	}
	kill := ranSimctlName(*m.calls, "kill")
	if strings.Join(kill, " ") != "kill -CONT 601" {
		t.Fatalf("ran %q, want `kill -CONT 601`", kill)
	}
	if !strings.Contains(errOut, "running and detached") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestSimLLDB_ExitsWithLLDBsOwnStatus(t *testing.T) {
	m := newDebugMachine(t)
	run := m.lldb()
	var exit *exec.ExitError
	if !errors.As(exec.Command("sh", "-c", "exit 3").Run(), &exit) {
		t.Skip("no sh to produce a real exit status")
	}
	run.stream.err = exit
	run.stream.feed("error: attach failed\n")
	m.ps([]string{m.row(601, 500, "Ss")})

	_, _, err := executeCLI(t, m.deps, "sim", "lldb", "--", "-o", "bt")
	if ExitCode(err) != 3 {
		t.Fatalf("exit code = %d (%v), want lldb's 3", ExitCode(err), err)
	}
}

func ranSimctlName(calls [][]string, name string) []string {
	for _, call := range calls {
		if len(call) > 0 && call[0] == name {
			return call
		}
	}
	return nil
}
