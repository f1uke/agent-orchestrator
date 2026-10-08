package cli

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
)

// interruptibleStream is a build that ends when it is interrupted, the way
// xcodebuild prints "** BUILD INTERRUPTED **" and exits on SIGINT.
type interruptibleStream struct {
	*fakeStream
	interrupts atomic.Int32
}

func (s *interruptibleStream) Interrupt() error {
	s.interrupts.Add(1)
	s.feed("** BUILD INTERRUPTED **\n")
	return nil
}

func TestSimRun_StopCancelsTheBuildByInterruptingIt(t *testing.T) {
	deps, _, calls, _ := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	var mu sync.Mutex
	var build *interruptibleStream
	deps.StartStream = func(context.Context, string, ...string) (ProcessStream, error) {
		mu.Lock()
		defer mu.Unlock()
		build = &interruptibleStream{fakeStream: newFakeStream()}
		go func() { _, _ = build.writer.Write([]byte("Compiling…\n")) }()
		return build, nil
	}
	dir := t.TempDir()
	t.Setenv(iosrun.EnvRunDir, dir)
	ctx, cancel := context.WithCancel(context.Background())
	_, done := executeCLIStreamingContext(ctx, t, deps, "sim", "run")
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return build != nil }, "the build never started")
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a stopped run is not a successful one")
	}

	if build.interrupts.Load() != 1 || build.stops() != 1 {
		t.Fatalf("interrupts=%d closes=%d, want xcodebuild asked to stop, then reaped", build.interrupts.Load(), build.stops())
	}
	verdict := readRunVerdict(t, filepath.Join(dir, iosrun.ResultFile))
	if verdict.State != iosrun.RunStopped || verdict.Summary != "Stopped while building Nter. Nothing was installed on iPhone 17 Pro Max." {
		t.Fatalf("verdict = %+v, want stopped while building", verdict)
	}
	if ranSimctl(*calls, "install") != nil {
		t.Fatal("a stopped build must install nothing")
	}
}

func attachedRun(t *testing.T) (Deps, *[][]string, *atomic.Bool) {
	t.Helper()
	previous := simRunAttachPoll
	simRunAttachPoll = 2 * time.Millisecond
	t.Cleanup(func() { simRunAttachPoll = previous })
	deps, _, calls, _ := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	var appAlive, launched atomic.Bool
	appAlive.Store(true)
	deps.ProcessAlive = func(pid int) bool { return pid != 51234 || appAlive.Load() }
	inner := deps.CommandOutput
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		out, err := inner(ctx, name, args...)
		if len(args) >= 2 && args[1] == "launch" {
			launched.Store(true)
		}
		if len(args) >= 2 && args[1] == "terminate" && launched.Load() {
			appAlive.Store(false)
		}
		return out, err
	}
	return deps, calls, &appAlive
}

func TestSimRun_AttachedStopTerminatesTheAppAndWaitsForIt(t *testing.T) {
	deps, calls, appAlive := attachedRun(t)
	dir := t.TempDir()
	t.Setenv(iosrun.EnvRunDir, dir)
	ctx, cancel := context.WithCancel(context.Background())
	_, done := executeCLIStreamingContext(ctx, t, deps, "sim", "run", "--attach")
	waitFor(t, func() bool {
		return readRunProgressIfAny(dir).Stage == iosrun.StageAppRunning
	}, "the run never said the app was running")
	if !appAlive.Load() {
		t.Fatal("precondition: the app is running")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("stopping a running app is the run working: %v", err)
	}
	terminated := 0
	for _, call := range *calls {
		if len(call) >= 3 && call[2] == "terminate" {
			terminated++
		}
	}
	if terminated < 2 || appAlive.Load() {
		t.Fatalf("terminate calls=%d alive=%v, want the app terminated after launch's own terminate", terminated, appAlive.Load())
	}
	verdict := readRunVerdict(t, filepath.Join(dir, iosrun.ResultFile))
	if verdict.State != iosrun.RunSucceeded || !strings.Contains(verdict.Summary, "until you stopped it") {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestSimRun_AttachedRunEndsWhenTheAppExits(t *testing.T) {
	deps, _, appAlive := attachedRun(t)
	dir := t.TempDir()
	t.Setenv(iosrun.EnvRunDir, dir)
	_, done := executeCLIStreamingContext(context.Background(), t, deps, "sim", "run", "--attach")
	waitFor(t, func() bool { return readRunProgressIfAny(dir).Stage == iosrun.StageAppRunning }, "never attached")
	appAlive.Store(false)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if verdict := readRunVerdict(t, filepath.Join(dir, iosrun.ResultFile)); !strings.Contains(verdict.Summary, "It has exited") {
		t.Fatalf("verdict = %+v", verdict)
	}
}
