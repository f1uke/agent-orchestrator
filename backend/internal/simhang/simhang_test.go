package simhang

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The three shapes `sample` reports for an app's main thread. They are written
// out rather than captured from a device so the tests run on a Linux CI box
// with no macOS, no Xcode and no simulator - and so the app they describe is a
// fictional one.
const (
	// runLoopWait is a HEALTHY iOS app: the main thread is parked in the run
	// loop waiting for the next event. It looks exactly like a blocked thread
	// from a distance - every sample in one identical stack - which is why the
	// run loop's own wait has to be recognised rather than counted.
	runLoopWait = `Analysis of sampling Nimbus (pid 4242) every 100 milliseconds
Process:         Nimbus [4242]
Path:            /Users/fluke/Library/Developer/CoreSimulator/Devices/Nimbus.app/Nimbus
----

Call graph:
    10 Thread_9000001   DispatchQueue_1: com.apple.main-thread  (serial)
    + 10 start  (in dyld) + 6992  [0x105387da4]
    +   10 start_sim  (in dyld_sim) + 20  [0x1052913d0]
    +     10 main  (in Nimbus) + 120  [0x105024dc4]
    +       10 UIApplicationMain  (in UIKitCore) + 124  [0x18640e42c]
    +         10 -[UIApplication _run]  (in UIKitCore) + 772  [0x18640a204]
    +           10 GSEventRunModal  (in GraphicsServices) + 116  [0x192b809bc]
    +             10 _CFRunLoopRunSpecificWithOptions  (in CoreFoundation) + 496  [0x18044fdbc]
    +               10 __CFRunLoopRun  (in CoreFoundation) + 1128  [0x180454e8c]
    +                 10 __CFRunLoopServiceMachPort  (in CoreFoundation) + 156  [0x180455cd4]
    +                   10 mach_msg  (in libsystem_kernel.dylib) + 20  [0x1055f4ef0]
    +                     10 mach_msg2_internal  (in libsystem_kernel.dylib) + 72  [0x105605e5c]
    +                       10 mach_msg2_trap  (in libsystem_kernel.dylib) + 8  [0x1055f4b70]
    10 Thread_9000002: com.apple.uikit.eventfetch-thread
      10 thread_start  (in libsystem_pthread.dylib) + 8  [0x105245a34]
        10 __workq_kernreturn  (in libsystem_kernel.dylib) + 8  [0x1055f6698]

Binary Images:
       0x105000000 -        0x105100000 +Nimbus (1.0) <0000> /Nimbus.app/Nimbus
`

	// blockedOnStdout is the hazard: something stopped draining the app's
	// stdout, the 64 KB pipe buffer filled, and a `print` on the main thread is
	// parked in write() and will never come back.
	blockedOnStdout = `Analysis of sampling Nimbus (pid 4242) every 100 milliseconds
Process:         Nimbus [4242]
----

Call graph:
    10 Thread_9000001   DispatchQueue_1: com.apple.main-thread  (serial)
    + 10 start  (in dyld) + 6992  [0x105387da4]
    +   10 start_sim  (in dyld_sim) + 20  [0x1052913d0]
    +     10 main  (in Nimbus) + 120  [0x105024dc4]
    +       10 Nimbus.PortfolioView.body.getter  (in Nimbus) + 480  [0x105030000]
    +         10 debugPrint  (in libswiftCore.dylib) + 92  [0x1a0000000]
    +           10 _debugPrint_unlocked  (in libswiftCore.dylib) + 236  [0x1a0000100]
    +             10 _Stdout.write  (in libswiftCore.dylib) + 44  [0x1a0000200]
    +               10 fwrite  (in libsystem_c.dylib) + 148  [0x18a690000]
    +                 10 _swrite  (in libsystem_c.dylib) + 128  [0x18a691000]
    +                   10 write  (in libsystem_kernel.dylib) + 8  [0x1055f5000]
    10 Thread_9000002: com.apple.uikit.eventfetch-thread
      10 thread_start  (in libsystem_pthread.dylib) + 8  [0x105245a34]
        10 __workq_kernreturn  (in libsystem_kernel.dylib) + 8  [0x1055f6698]
`

	// busyMainThread is an app doing work: the main thread moves between
	// samples, so no single stack accounts for all of them. It is not blocked,
	// it is busy, and calling it blocked would be a different wrong answer.
	busyMainThread = `Analysis of sampling Nimbus (pid 4242) every 100 milliseconds

Call graph:
    10 Thread_9000001   DispatchQueue_1: com.apple.main-thread  (serial)
    + 10 start  (in dyld) + 6992  [0x105387da4]
    +   10 main  (in Nimbus) + 120  [0x105024dc4]
    +     6 Nimbus.Ledger.recompute  (in Nimbus) + 44  [0x105030000]
    +     ! 6 Nimbus.Ledger.sum  (in Nimbus) + 12  [0x105031000]
    +     4 Nimbus.Ledger.render  (in Nimbus) + 88  [0x105032000]
`
)

// A deadlocked main thread: no stdout in sight, but just as unable to answer.
const blockedOnMutex = `Call graph:
    10 Thread_9000001   DispatchQueue_1: com.apple.main-thread  (serial)
    + 10 start  (in dyld) + 6992  [0x105387da4]
    +   10 main  (in Nimbus) + 120  [0x105024dc4]
    +     10 Nimbus.Session.refresh  (in Nimbus) + 44  [0x105030000]
    +       10 __psynch_mutexwait  (in libsystem_kernel.dylib) + 8  [0x1055f6000]
`

// runner answers each `sample` invocation from a script, in order, and records
// what it was asked to run.
type runner struct {
	answers []answer
	calls   [][]string
}

type answer struct {
	out string
	err error
}

func (r *runner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if len(r.answers) == 0 {
		return nil, errors.New("unexpected sample call")
	}
	next := r.answers[0]
	r.answers = r.answers[1:]
	return []byte(next.out), next.err
}

func lookFound(string) (string, error) { return "/usr/bin/sample", nil }
func lookMissing(string) (string, error) {
	return "", errors.New("not found")
}

func TestDiagnose_BlockedOnStdoutIsReportedWithTheBlockingFrame(t *testing.T) {
	// Two calls: the cheap single sample rules the run loop out, the second
	// confirms the thread never moved.
	r := &runner{answers: []answer{{out: blockedOnStdout}, {out: blockedOnStdout}}}

	got, ok := Diagnose(context.Background(), lookFound, r.run, 4242)
	if !ok {
		t.Fatal("a stack this clear must produce a verdict")
	}
	if !got.Blocked {
		t.Fatalf("main thread parked in write() must read as blocked: %+v", got)
	}
	if got.TopFrame != "write" {
		t.Fatalf("topFrame = %q, want the leaf of the stack", got.TopFrame)
	}
	if !got.StdoutWrite {
		t.Fatal("a stack through fwrite/_Stdout.write must be recognised as the stdout hazard")
	}
	if got.Samples != 10 {
		t.Fatalf("samples = %d, want the confirming run's own count", got.Samples)
	}
	// The frames are the evidence, and they have to reach past the plumbing:
	// naming only the leaf ("write") says nothing about which subsystem is
	// stuck, and six frames of libc say no more. One frame per binary reaches
	// the app's OWN code, which is the frame that identifies the screen.
	joined := strings.Join(got.Frames, " <- ")
	for _, want := range []string{"write", "_Stdout.write", "Nimbus.PortfolioView.body.getter"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("frames = %q, want %q in the chain", joined, want)
		}
	}
	// libsystem_c contributes fwrite AND _swrite; only one of them is worth a
	// line, or the app's own frame never fits.
	if strings.Contains(joined, "fwrite") && strings.Contains(joined, "_swrite") {
		t.Fatalf("frames = %q, want one frame per binary rather than a run of plumbing", joined)
	}
}

func TestDiagnose_RunLoopWaitIsNotBlocked(t *testing.T) {
	// The direction that matters most: an idle, perfectly healthy app also
	// spends 100% of its samples in one identical stack. Calling that "hung"
	// would send an agent down a different wrong path.
	r := &runner{answers: []answer{{out: runLoopWait}}}

	got, ok := Diagnose(context.Background(), lookFound, r.run, 4242)
	if !ok {
		t.Fatal("a healthy run loop is a verdict, not an unknown")
	}
	if got.Blocked {
		t.Fatalf("an app waiting in its own run loop is not blocked: %+v", got)
	}
	if len(r.calls) != 1 {
		t.Fatalf("%d sample calls, want the cheap one only - the run loop rules it out", len(r.calls))
	}
}

func TestDiagnose_BusyMainThreadIsNotBlocked(t *testing.T) {
	// The first sample catches app code, so the cheap check cannot rule it out;
	// the confirming run shows the thread moving, which it does because it is
	// working rather than stuck.
	r := &runner{answers: []answer{{out: busyMainThread}, {out: busyMainThread}}}

	got, ok := Diagnose(context.Background(), lookFound, r.run, 4242)
	if !ok {
		t.Fatal("want a verdict")
	}
	if got.Blocked {
		t.Fatalf("a moving stack is a busy thread, not a blocked one: %+v", got)
	}
	if len(r.calls) != 2 {
		t.Fatalf("%d sample calls, want the confirming second one", len(r.calls))
	}
}

func TestDiagnose_DeadlockIsBlockedWithoutTheStdoutAdvice(t *testing.T) {
	r := &runner{answers: []answer{{out: blockedOnMutex}, {out: blockedOnMutex}}}

	got, ok := Diagnose(context.Background(), lookFound, r.run, 4242)
	if !ok || !got.Blocked {
		t.Fatalf("a main thread in __psynch_mutexwait is blocked: %+v ok=%v", got, ok)
	}
	if got.StdoutWrite {
		t.Fatal("a mutex wait must not be reported as the stdout hazard")
	}
}

func TestDiagnose_DegradesWhenItCannotTell(t *testing.T) {
	// Every one of these must come back "no verdict" rather than a guess: the
	// caller falls back to what it said before, which is never worse than a
	// confident wrong answer.
	tests := []struct {
		name string
		pid  int
		look func(string) (string, error)
		runs []answer
	}{
		{name: "no pid to sample", pid: 0, look: lookFound},
		{name: "no sample binary", pid: 4242, look: lookMissing},
		{name: "sample failed", pid: 4242, look: lookFound, runs: []answer{{err: errors.New("exit status 1")}}},
		{name: "sample said nothing", pid: 4242, look: lookFound, runs: []answer{{out: ""}}},
		{name: "no main thread in the graph", pid: 4242, look: lookFound, runs: []answer{{out: "Call graph:\n    3 Thread_1: com.apple.other\n"}}},
		{name: "confirming run failed", pid: 4242, look: lookFound,
			runs: []answer{{out: blockedOnStdout}, {err: errors.New("exit status 1")}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{answers: tc.runs}
			got, ok := Diagnose(context.Background(), tc.look, r.run, tc.pid)
			if ok {
				t.Fatalf("want no verdict, got %+v", got)
			}
			if got.Blocked {
				t.Fatalf("a failed probe must never report a hang: %+v", got)
			}
		})
	}
}

func TestDiagnose_AsksSampleForTheProcessItWasGiven(t *testing.T) {
	r := &runner{answers: []answer{{out: blockedOnStdout}, {out: blockedOnStdout}}}

	if _, ok := Diagnose(context.Background(), lookFound, r.run, 4242); !ok {
		t.Fatal("want a verdict")
	}
	if len(r.calls) != 2 {
		t.Fatalf("calls = %v", r.calls)
	}
	for _, call := range r.calls {
		if call[0] != Binary {
			t.Fatalf("ran %q, want %q", call[0], Binary)
		}
		if call[1] != "4242" {
			t.Fatalf("sampled %q, want the pid it was given: %v", call[1], call)
		}
		joined := strings.Join(call, " ")
		// -mayDie keeps the report usable when the app exits mid-probe, and
		// writing to stdout keeps a stray report file out of /tmp.
		if !strings.Contains(joined, "-mayDie") || !strings.Contains(joined, "/dev/stdout") {
			t.Fatalf("call = %v, want -mayDie and an explicit stdout destination", call)
		}
	}
	// The cheap call takes one sample; only the confirming call pays for a
	// second of wall clock.
	if r.calls[0][2] != "0" {
		t.Fatalf("first call = %v, want a single sample", r.calls[0])
	}
	if r.calls[1][2] == "0" {
		t.Fatalf("second call = %v, want a sampling window", r.calls[1])
	}
}

func TestDiagnose_TruncatedGraphIsNoVerdict(t *testing.T) {
	// A main thread header with nothing under it says nothing about the thread.
	r := &runner{answers: []answer{{out: "Call graph:\n    10 Thread_1   DispatchQueue_1: com.apple.main-thread  (serial)\n"}}}

	if got, ok := Diagnose(context.Background(), lookFound, r.run, 4242); ok {
		t.Fatalf("want no verdict, got %+v", got)
	}
}
