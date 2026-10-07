package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCrash writes a small fictional .ips into the fake home's
// DiagnosticReports: a header line, then a body for an app on device udid.
func writeCrash(t *testing.T, home, file, app, bundleID, stamp, udid, exception string) {
	t.Helper()
	dir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	header := fmt.Sprintf(`{"app_name":%q,"timestamp":%q,"app_version":"2.4.0","build_version":"318","bundleID":%q,"bug_type":"309"}`,
		app, stamp, bundleID)
	body := fmt.Sprintf(`{
  "procPath" : "\/Users\/USER\/Library\/Developer\/CoreSimulator\/Devices\/%s\/data\/Containers\/Bundle\/Application\/X\/%s.app\/%s",
  "exception" : {"type" : %q, "signal" : "SIGTRAP"},
  "termination" : {"indicator" : "Trace\/BPT trap: 5", "byProc" : "exc handler"},
  "asi" : {"libswiftCore.dylib" : ["%s\/Store.swift:42: Fatal error: boom"]},
  "faultingThread" : 0,
  "threads" : [{"triggered" : true, "queue" : "com.apple.main-thread", "frames" : [
    {"imageOffset" : 18432, "sourceLine" : 42, "sourceFile" : "Store.swift", "symbol" : "Store.load()", "symbolLocation" : 88, "imageIndex" : 0},
    {"imageOffset" : 9216, "imageIndex" : 0}
  ]}],
  "usedImages" : [{"name" : %q, "path" : "\/x"}]
}`, udid, app, app, exception, app, app)
	if err := os.WriteFile(filepath.Join(dir, file), []byte(header+"\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func crashMachine(t *testing.T) *debugMachine {
	t.Helper()
	m := newDebugMachine(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCrash(t, home, "Nimbus-1.ips", "Nimbus", "com.example.Nimbus", "2026-10-08 09:10:00.00 +0700", simUDIDProMax, "EXC_CRASH")
	writeCrash(t, home, "Nimbus-2.ips", "Nimbus", "com.example.Nimbus", "2026-10-08 10:15:00.00 +0700", simUDIDProMax, "EXC_BREAKPOINT")
	writeCrash(t, home, "Lantern-1.ips", "Lantern", "com.example.Lantern", "2026-10-08 09:30:00.00 +0700", simUDIDProMax, "EXC_BAD_ACCESS")
	writeCrash(t, home, "Nimbus-crew.ips", "Nimbus", "com.example.Nimbus", "2026-10-08 12:00:00.00 +0700", simUDIDPro, "EXC_GUARD")
	return m
}

func TestSimCrashes_ListsThisDevicesCrashesNewestFirst(t *testing.T) {
	m := crashMachine(t)
	out, _, err := executeCLI(t, m.deps, "sim", "crashes")
	if err != nil {
		t.Fatal(err)
	}
	breakpoint, badAccess, crash := strings.Index(out, "EXC_BREAKPOINT"), strings.Index(out, "EXC_BAD_ACCESS"), strings.Index(out, "EXC_CRASH")
	if breakpoint < 0 || badAccess < breakpoint || crash < badAccess {
		t.Fatalf("want every app on the device, newest first:\n%s", out)
	}
	if strings.Contains(out, "EXC_GUARD") {
		t.Fatalf("a crash on a crewmate's device was listed:\n%s", out)
	}
	for _, want := range []string{"2.4.0 (318)", "Trace/BPT trap: 5", "Nimbus-2.ips", "--show"} {
		if !strings.Contains(out, want) {
			t.Errorf("list does not carry %q:\n%s", want, out)
		}
	}
}

func TestSimCrashes_ABundleIDOrAOSimAppNarrowsIt(t *testing.T) {
	m := crashMachine(t)
	t.Setenv("AO_SIM_APP", "com.example.Lantern")
	out, _, err := executeCLI(t, m.deps, "sim", "crashes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got simCrashList
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(got.Reports) != 1 || got.Reports[0].BundleID != "com.example.Lantern" || got.Reports[0].Index != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestSimCrashes_LimitKeepsTheNewest(t *testing.T) {
	m := crashMachine(t)
	out, _, err := executeCLI(t, m.deps, "sim", "crashes", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "EXC_BREAKPOINT") || strings.Contains(out, "EXC_BAD_ACCESS") || !strings.Contains(out, "1 of 3") {
		t.Fatalf("--limit 1:\n%s", out)
	}
}

func TestSimCrashes_ShowPrintsTheCrashedThreadReadably(t *testing.T) {
	m := crashMachine(t)
	out, _, err := executeCLI(t, m.deps, "sim", "crashes", "com.example.Nimbus", "--show", "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"EXC_BREAKPOINT (SIGTRAP)",
		"Trace/BPT trap: 5",
		"libswiftCore.dylib: Nimbus/Store.swift:42: Fatal error: boom",
		"#0  Nimbus  Store.load() + 88  (Store.swift:42)",
		"#1  Nimbus + 9216",
		"Nimbus-2.ips",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--show 1 does not carry %q:\n%s", want, out)
		}
	}
}

func TestSimCrashes_ShowPastTheEndIsAUsageError(t *testing.T) {
	m := crashMachine(t)
	_, _, err := executeCLI(t, m.deps, "sim", "crashes", "--show", "9")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "3") {
		t.Fatalf("got %v", err)
	}
}

func TestSimCrashes_NoneIsAnAnswerNotAFailure(t *testing.T) {
	m := newDebugMachine(t)
	t.Setenv("HOME", t.TempDir())
	out, _, err := executeCLI(t, m.deps, "sim", "crashes")
	if err != nil || !strings.Contains(out, "No crash reports") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestSimCrashes_RefusesABaseDevice(t *testing.T) {
	m := crashMachine(t)
	t.Setenv("AO_SIM_UDID", simUDIDBase)
	if _, _, err := executeCLI(t, m.deps, "sim", "crashes"); err == nil || !strings.Contains(err.Error(), "base") {
		t.Fatalf("got %v", err)
	}
}

// Measured on a real simulator crash (fatalError in the app): the .ips carried
// no app-specific information at all, while the message was in the unified log.
// A report without it says where the message is instead of leaving a gap.
func TestSimCrashes_ShowWithoutAppInfoPointsAtTheLogForTheMessage(t *testing.T) {
	m := newDebugMachine(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	header := `{"app_name":"Nimbus","timestamp":"2026-10-08 10:15:00.00 +0700","app_version":"2.4.0","build_version":"318","bundleID":"com.example.Nimbus","bug_type":"309"}`
	body := `{"procPath" : "\/Users\/USER\/Library\/Developer\/CoreSimulator\/Devices\/` + simUDIDProMax + `\/data\/Containers\/Bundle\/Application\/X\/Nimbus.app\/Nimbus",
  "exception" : {"type" : "EXC_BREAKPOINT", "signal" : "SIGTRAP"}, "faultingThread" : 0,
  "threads" : [{"triggered" : true, "frames" : [{"imageOffset" : 1, "imageIndex" : 0}]}], "usedImages" : [{"name" : "Nimbus"}]}`
	if err := os.WriteFile(filepath.Join(dir, "Nimbus-1.ips"), []byte(header+"\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeCLI(t, m.deps, "sim", "crashes", "--show", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ao sim log") || !strings.Contains(out, "Fatal error") {
		t.Fatalf("a report with no app info must say where a fatalError's message is:\n%s", out)
	}
}

// Measured: the report of a crash appeared about 30 s after the app died. An
// empty list right after a crash says so, or it reads as "it did not crash".
func TestSimCrashes_NoneSaysAReportArrivesLate(t *testing.T) {
	m := newDebugMachine(t)
	t.Setenv("HOME", t.TempDir())
	out, _, err := executeCLI(t, m.deps, "sim", "crashes")
	if err != nil || !strings.Contains(out, "30 s") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
