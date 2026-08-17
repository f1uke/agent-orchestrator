package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simhang"
)

// What `sample` prints for an app whose main thread is parked in a write to a
// stdout pipe nobody is draining, and for a healthy app idling in its run loop.
// Both are written out rather than captured, so these tests need no macOS, no
// Xcode and no simulator - and describe a fictional app.
const (
	blockedSampleReport = `Analysis of sampling Nimbus (pid 4242) every 100 milliseconds

Call graph:
    10 Thread_9000001   DispatchQueue_1: com.apple.main-thread  (serial)
    + 10 main  (in Nimbus) + 120  [0x105024dc4]
    +   10 debugPrint  (in libswiftCore.dylib) + 92  [0x1a0000000]
    +     10 _Stdout.write  (in libswiftCore.dylib) + 44  [0x1a0000200]
    +       10 fwrite  (in libsystem_c.dylib) + 148  [0x18a690000]
    +         10 write  (in libsystem_kernel.dylib) + 8  [0x1055f5000]
`
	healthySampleReport = `Analysis of sampling Nimbus (pid 4242) once

Call graph:
    1 Thread_9000001   DispatchQueue_1: com.apple.main-thread  (serial)
    + 1 main  (in Nimbus) + 120  [0x105024dc4]
    +   1 UIApplicationMain  (in UIKitCore) + 124  [0x18640e42c]
    +     1 __CFRunLoopServiceMachPort  (in CoreFoundation) + 156  [0x180455cd4]
    +       1 mach_msg2_trap  (in libsystem_kernel.dylib) + 8  [0x1055f4b70]
`
)

// withSampler makes `sample` resolvable and answers every probe with report.
// Everything else keeps answering as before, so a stray probe on a path that
// should not run one still fails loudly.
func withSampler(deps Deps, report string, err error) (Deps, *int) {
	calls := 0
	inner, innerLook := deps.CommandOutput, deps.LookPath
	deps.LookPath = func(name string) (string, error) {
		if name == simhang.Binary {
			return "/usr/bin/sample", nil
		}
		return innerLook(name)
	}
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == simhang.Binary {
			calls++
			return []byte(report), err
		}
		return inner(ctx, name, args...)
	}
	return deps, &calls
}

// hungAppDeps is `ao sim ax` against an app that answers nothing.
func hungAppDeps(t *testing.T) Deps {
	t.Helper()
	driver := &fakeSimDriver{snapshot: simbridge.Snapshot{
		Frontmost: simbridge.Frontmost{BundleID: "com.example.nimbus", PID: 4242},
	}}
	deps, _ := touchDeps(t, driver)
	return deps
}

func TestSimAX_EmptyTreeNamesTheBlockedMainThreadInsteadOfAccessibility(t *testing.T) {
	// The failure this fixes: an empty tree was reported as "no accessibility
	// elements", which points at the one subsystem that is working. An agent
	// that believes it re-reads the app's view code for several rounds.
	deps, probes := withSampler(hungAppDeps(t), blockedSampleReport, nil)

	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil {
		t.Fatal("an empty tree must still fail")
	}
	got := err.Error()
	for _, want := range []string{
		"main thread",        // what is actually wrong
		"com.example.nimbus", // which app
		"4242",               // and which process, so it can be sampled again
		"10 of 10",           // the evidence, not a bare claim
		"_Stdout.write",      // the frame that names the subsystem
		"touch",              // the other symptom, so a silent tap is explained too
		"draining",           // the cause an agent can act on
		"ao sim log",         // and the safe way to read the app instead
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error must mention %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "reported no accessibility elements") {
		t.Fatalf("a blocked app must not be reported as an accessibility result:\n%s", got)
	}
	if *probes == 0 {
		t.Fatal("the probe never ran")
	}
}

func TestSimAX_EmptyTreeFromAHealthyAppKeepsTodaysMessage(t *testing.T) {
	// The other direction, and the one that matters just as much: an app idling
	// in its run loop is NOT hung, and claiming it is would send an agent down
	// a different wrong path.
	deps, probes := withSampler(hungAppDeps(t), healthySampleReport, nil)

	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil {
		t.Fatal("an empty tree must still fail")
	}
	got := err.Error()
	if !strings.Contains(got, "reported no accessibility elements") {
		t.Fatalf("want today's message for a healthy app:\n%s", got)
	}
	if strings.Contains(strings.ToLower(got), "blocked") {
		t.Fatalf("a healthy app must never be reported as blocked:\n%s", got)
	}
	// One sample was enough to see the run loop; nothing pays for the second.
	if *probes != 1 {
		t.Fatalf("probes = %d, want the cheap check only", *probes)
	}
}

func TestSimAX_EmptyTreeDegradesWhenTheProbeCannotTell(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) Deps
	}{
		{
			name: "sample is not on this machine",
			build: func(t *testing.T) Deps {
				t.Helper()
				return hungAppDeps(t) // LookPath resolves xcrun only
			},
		},
		{
			name: "sample failed",
			build: func(t *testing.T) Deps {
				t.Helper()
				deps, _ := withSampler(hungAppDeps(t), "", errors.New("exit status 1"))
				return deps
			},
		},
		{
			name: "sample said something unreadable",
			build: func(t *testing.T) Deps {
				t.Helper()
				deps, _ := withSampler(hungAppDeps(t), "sample cannot examine process 4242\n", nil)
				return deps
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := executeCLI(t, tc.build(t), "sim", "ax")
			if err == nil {
				t.Fatal("an empty tree must still fail")
			}
			if !strings.Contains(err.Error(), "reported no accessibility elements") {
				t.Fatalf("a probe that cannot tell must fall back to today's message:\n%v", err)
			}
		})
	}
}

func TestSimAX_ProbesNothingWhenTheScreenReadsFine(t *testing.T) {
	// The probe costs a second of wall clock and must never be on the path of a
	// command that worked.
	deps, probes := withSampler(func() Deps {
		driver := &fakeSimDriver{snapshot: fixtureSnapshot()}
		d, _ := touchDeps(t, driver)
		return d
	}(), blockedSampleReport, nil)

	if _, _, err := executeCLI(t, deps, "sim", "ax"); err != nil {
		t.Fatalf("sim ax: %v", err)
	}
	if *probes != 0 {
		t.Fatalf("probes = %d on a healthy read, want none", *probes)
	}
}

func TestSimAX_BlockedAppWithNoPidToSampleKeepsTodaysMessage(t *testing.T) {
	// Accessibility can come back with nothing at all - no tree and no
	// frontmost app. There is nothing to sample, and inventing a verdict from
	// that would be worse than today's message.
	driver := &fakeSimDriver{snapshot: simbridge.Snapshot{}}
	deps, _ := touchDeps(t, driver)
	deps, probes := withSampler(deps, blockedSampleReport, nil)

	_, _, err := executeCLI(t, deps, "sim", "ax")
	if err == nil {
		t.Fatal("an empty tree must still fail")
	}
	if !strings.Contains(err.Error(), "reported no accessibility elements") {
		t.Fatalf("want today's message:\n%v", err)
	}
	if *probes != 0 {
		t.Fatalf("probes = %d with no pid to sample, want none", *probes)
	}
}
