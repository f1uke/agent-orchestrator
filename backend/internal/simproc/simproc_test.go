package simproc

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

const (
	dataA = "/Users/someone/Library/Developer/CoreSimulator/Devices/AAAAAAAA-0000-0000-0000-000000000001/data"
	dataB = "/Users/someone/Library/Developer/CoreSimulator/Devices/BBBBBBBB-0000-0000-0000-000000000002/data"
	appA  = dataA + "/Containers/Bundle/Application/1111AAAA-0000-0000-0000-000000000000/Nimbus.app"
	appB  = dataB + "/Containers/Bundle/Application/3333CCCC-0000-0000-0000-000000000000/Nimbus.app"
	spacy = dataA + "/Containers/Bundle/Application/2222BBBB-0000-0000-0000-000000000000/Cloud Atlas.app"
)

func fixture(t *testing.T) Table {
	t.Helper()
	raw, err := os.ReadFile("testdata/ps.txt")
	if err != nil {
		t.Fatal(err)
	}
	return Parse(raw)
}

func pids(procs []Process) []int {
	out := make([]int, 0, len(procs))
	for _, p := range procs {
		out = append(out, p.PID)
	}
	return out
}

func TestParse_KeepsAPathWithSpacesWhole(t *testing.T) {
	table := fixture(t)
	p, ok := table.Get(603)
	if !ok {
		t.Fatal("pid 603 is missing")
	}
	if p.PPID != 500 || p.Stat != "Ss" || p.Path != spacy+"/Cloud Atlas" {
		t.Fatalf("got %+v", p)
	}
	if len(table) != 10 {
		t.Fatalf("parsed %d processes, want every line of the fixture (10)", len(table))
	}
}

func TestParse_SkipsLinesThatAreNotProcesses(t *testing.T) {
	table := Parse([]byte("\n  garbage line\n 12 x Ss /bin/x\n  42   1 S    /bin/ok\n"))
	if got := pids(table); len(got) != 1 || got[0] != 42 {
		t.Fatalf("parsed %v, want only pid 42", got)
	}
}

func TestStat(t *testing.T) {
	cases := []struct {
		stat              string
		debugged, stopped bool
	}{
		{"Ss", false, false},
		{"SXs", true, false}, // measured: stopped at a breakpoint still reads S, with X
		{"T", false, true},
		{"Ts", false, true},
		{"TXs", true, true},
		{"S+", false, false},
	}
	for _, tc := range cases {
		p := Process{Stat: tc.stat}
		if p.Debugged() != tc.debugged || p.Stopped() != tc.stopped {
			t.Errorf("%s: debugged=%v stopped=%v, want %v %v", tc.stat, p.Debugged(), p.Stopped(), tc.debugged, tc.stopped)
		}
	}
}

func TestAppsOn_OnlyThatDevicesInstalledApps(t *testing.T) {
	table := fixture(t)
	got := pids(table.AppsOn(dataA))
	// 801 runs from the device's DATA container, not an installed bundle, and
	// 700 is the same app on another device.
	if !slices.Equal(got, []int{602, 601, 603}) {
		t.Fatalf("AppsOn(A) = %v, want [602 601 603]", got)
	}
	if got := pids(table.AppsOn(dataB)); len(got) != 1 || got[0] != 700 {
		t.Fatalf("AppsOn(B) = %v, want [700]", got)
	}
	if got := table.AppsOn(dataA + "/"); len(got) != 3 {
		t.Fatalf("a trailing slash on the data path changed the answer: %v", pids(got))
	}
}

func TestMain_IsTheExecutableDirectlyInsideTheApp(t *testing.T) {
	table := fixture(t)
	p, ok := table.Main(appA)
	if !ok || p.PID != 601 {
		t.Fatalf("Main(appA) = %+v %v, want pid 601 (not the widget under PlugIns/)", p, ok)
	}
	if p, ok := table.Main(spacy); !ok || p.PID != 603 {
		t.Fatalf("Main(Cloud Atlas) = %+v %v, want pid 603", p, ok)
	}
	if _, ok := table.Main(dataA + "/Containers/Bundle/Application/9999/Gone.app"); ok {
		t.Fatal("an app that is not running has a main process")
	}
	// The same bundle id on another device is another process.
	if p, ok := table.Main(appB); !ok || p.PID != 700 {
		t.Fatalf("Main(appB) = %+v %v, want pid 700", p, ok)
	}
}

func TestHold_DebuggerNamesTheChainAndHowToFreeIt(t *testing.T) {
	table := Parse([]byte(strings.Join([]string{
		"  880   870 Ss   -zsh",
		"  900   880 S+   /Applications/Xcode.app/Contents/Developer/usr/bin/lldb",
		"  950   900 S    /Applications/Xcode.app/Contents/SharedFrameworks/LLDB.framework/Versions/A/Resources/debugserver",
		"  601   950 SXs  " + appA + "/Nimbus",
	}, "\n")))
	app, _ := table.Main(appA)
	hold, held := table.HoldOf(app)
	if !held || hold.Kind != HeldByDebugger {
		t.Fatalf("an SXs app is not held by a debugger: %+v %v", hold, held)
	}
	if hold.Debugserver == nil || hold.Debugserver.PID != 950 || hold.Debugger == nil || hold.Debugger.PID != 900 {
		t.Fatalf("chain = %+v", hold)
	}
	got := hold.Describe()
	for _, want := range []string{
		"Nimbus.app/Nimbus (pid 601)",
		"attached by debugserver 950 (lldb 900)",
		"`process detach`",
		"end that lldb (pid 900) if it is yours",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not say %q", got, want)
		}
	}
	for _, never := range []string{"pkill", "killall", "kill -9 950"} {
		if strings.Contains(got, never) {
			t.Errorf("%q suggests %q: kill by pattern, or the debugserver, is never the fix", got, never)
		}
	}
}

func TestHold_SIGSTOPSaysContinue(t *testing.T) {
	table := Parse([]byte("  601   500 T    " + appA + "/Nimbus\n"))
	app, _ := table.Main(appA)
	hold, held := table.HoldOf(app)
	if !held || hold.Kind != HeldBySIGSTOP {
		t.Fatalf("a T app is not held by SIGSTOP: %+v %v", hold, held)
	}
	if got := hold.Describe(); !strings.Contains(got, "stopped by SIGSTOP") || !strings.Contains(got, "`kill -CONT 601`") {
		t.Fatalf("Describe() = %q", got)
	}
}

func TestHold_ADebuggerWhoseParentsAreGoneStillSaysWhatIsKnown(t *testing.T) {
	table := Parse([]byte("  601   950 SXs  " + appA + "/Nimbus\n"))
	app, _ := table.Main(appA)
	hold, held := table.HoldOf(app)
	if !held || hold.Debugserver != nil || hold.Debugger != nil {
		t.Fatalf("hold = %+v %v", hold, held)
	}
	if got := hold.Describe(); !strings.Contains(got, "attached by a debugger (pid 950)") {
		t.Fatalf("Describe() = %q", got)
	}
}

func TestHold_XcodeIsNamedAsXcode(t *testing.T) {
	table := Parse([]byte(strings.Join([]string{
		"  300     1 S    /Applications/Xcode.app/Contents/MacOS/Xcode",
		"  310   300 S    /Applications/Xcode.app/Contents/SharedFrameworks/LLDBRPC.framework/Versions/A/Resources/lldb-rpc-server",
		"  320   310 S    /Applications/Xcode.app/Contents/SharedFrameworks/LLDB.framework/Versions/A/Resources/debugserver",
		"  601   320 SXs  " + appA + "/Nimbus",
	}, "\n")))
	app, _ := table.Main(appA)
	hold, _ := table.HoldOf(app)
	if got := hold.Describe(); !strings.Contains(got, "Xcode") || !strings.Contains(got, "lldb-rpc-server 310") {
		t.Fatalf("Describe() = %q, want it to say the debugger is Xcode's", got)
	}
}

func TestHolds_EveryHeldAppOnTheDeviceAndNothingElse(t *testing.T) {
	table := Parse([]byte(strings.Join([]string{
		"  601   500 Ss   " + appA + "/Nimbus",
		"  602   500 T    " + appA + "/PlugIns/NimbusWidget.appex/NimbusWidget",
		"  700   950 SXs  " + appB + "/Nimbus",
		"  640   500 T    /usr/bin/something-else",
	}, "\n")))
	holds := table.Holds(dataA)
	if len(holds) != 1 || holds[0].App.PID != 602 {
		t.Fatalf("Holds(A) = %+v, want only the stopped widget", holds)
	}
	if holds := table.Holds(dataB); len(holds) != 1 || holds[0].App.PID != 700 {
		t.Fatalf("Holds(B) = %+v, want the debugged app", holds)
	}
}

func TestRead_RunsPsOnceAndParsesIt(t *testing.T) {
	var ran []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return []byte("  601   500 Ss   " + appA + "/Nimbus\n"), nil
	}
	table, err := Read(t.Context(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "ps -axo pid=,ppid=,stat=,comm=" {
		t.Fatalf("ran %q", ran)
	}
	if _, ok := table.Main(appA); !ok {
		t.Fatal("the table read has no Nimbus")
	}

	failing := func(context.Context, string, ...string) ([]byte, error) { return []byte("ps: boom"), errors.New("exit 1") }
	if _, err := Read(t.Context(), failing); err == nil || !strings.Contains(err.Error(), "ps: boom") {
		t.Fatalf("a failed ps is not an error naming what it said: %v", err)
	}
}
