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

	"github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
	"github.com/aoagents/agent-orchestrator/backend/internal/xcodeproj"
)

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func readRunProgress(t *testing.T, dir string) iosrun.Progress {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, iosrun.ProgressFile))
	if err != nil {
		t.Fatalf("no progress was written: %v", err)
	}
	var progress iosrun.Progress
	if err := json.Unmarshal(body, &progress); err != nil {
		t.Fatalf("progress is not readable: %v (%s)", err, body)
	}
	return progress
}

func streamEvent(name, payload string) string {
	return fmt.Sprintf(`{"name":{"_value":%q},"structuredPayload":%s}`+"\n", name, payload)
}

func TestSimRun_ReportsBuildProgressFromTheResultStream(t *testing.T) {
	deps, _, _, _ := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	var build []string
	deps.StartStream = func(_ context.Context, name string, args ...string) (ProcessStream, error) {
		build = append([]string{name}, args...)
		events := streamEvent("logMessageEmitted", `{"message":{"title":{"_value":"Target dependency graph (1 target)"},"annotations":{"_values":[{"title":{"_value":"Target 'Nter' in project 'Nter' (no dependencies)"}}]}}}`) +
			streamEvent("logSectionCreated", `{"head":{"title":{"_value":"Compile AppDelegate.swift (arm64)"}}}`) +
			streamEvent("advisoryMessage", `{"message":{"_value":"Nter : 120 / 480"},"progress":{"_value":"0.25"}}`) +
			streamEvent("issueEmitted", `{"severity":{"_value":"warning"},"issue":{"message":{"_value":"unused"}}}`)
		if err := os.WriteFile(argAfter(args, "-resultStreamPath"), []byte(events), 0o600); err != nil {
			return nil, err
		}
		stream := newFakeStream()
		stream.feed("** BUILD SUCCEEDED **\n")
		return stream, nil
	}
	dir := filepath.Join(t.TempDir(), "iosrun", "mer-9")
	t.Setenv(iosrun.EnvRunDir, dir)

	if _, errOut, err := executeCLI(t, deps, "sim", "run"); err != nil {
		t.Fatalf("sim run failed: %v\nstderr=%s", err, errOut)
	}

	line := strings.Join(build, " ")
	if !strings.Contains(line, xcodeproj.ProgressFlag) || !strings.Contains(line, "-resultStreamPath") {
		t.Fatalf("a build the bar watches must ask for progress: %s", line)
	}
	progress := readRunProgress(t, dir)
	if progress.Stage != iosrun.StageLaunching || progress.PID != os.Getpid() || progress.BuildStartedAt == nil {
		t.Fatalf("progress = %+v, want the last stage, this pid and when the build started", progress)
	}
	if b := progress.Build; b == nil || b.Counts == nil || b.Counts.Done != 120 || b.Counts.Total != 480 || b.Phase != "compiling" || b.Warnings != 1 {
		t.Fatalf("build = %+v, want compiling 120/480 with one warning", b)
	}
	if verdict := readRunVerdict(t, filepath.Join(dir, iosrun.ResultFile)); verdict.BuildSeconds <= 0 {
		t.Fatalf("buildSeconds = %v, want the successful build's duration", verdict.BuildSeconds)
	}
}

func TestSimRun_AsksForNoProgressWhenNobodyWatches(t *testing.T) {
	deps, _, _, builds := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	t.Setenv(iosrun.EnvRunDir, "")

	if _, errOut, err := executeCLI(t, deps, "sim", "run"); err != nil {
		t.Fatalf("sim run failed: %v\nstderr=%s", err, errOut)
	}
	if line := strings.Join((*builds)[0], " "); strings.Contains(line, xcodeproj.ProgressFlag) {
		t.Fatalf("the flag costs a failed build its output; a typed command must not pass it: %s", line)
	}
}

func TestSimRun_ReplaysTheCompilerOutputXcodebuildKeptOffStdout(t *testing.T) {
	deps, _, _, _ := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	deps.StartStream = func(context.Context, string, ...string) (ProcessStream, error) {
		stream := newFakeStream()
		stream.feed("** BUILD FAILED **\n")
		stream.err = errors.New("exit status 65")
		return stream, nil
	}
	inner := deps.CommandOutput
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "xcrun" && len(args) > 1 && args[0] == "xcresulttool" {
			return []byte(`{"title":"Build","result":"failed","subsections":[{"title":"Compile AppDelegate.swift (arm64)","result":"failed",
				"commandInvocationDetails":{"emittedOutput":"/w/AppDelegate.swift:12:5: error: cannot find 'foo' in scope\n"}}]}`), nil
		}
		return inner(ctx, name, args...)
	}
	t.Setenv(iosrun.EnvRunDir, t.TempDir())

	_, errOut, err := executeCLI(t, deps, "sim", "run")
	if err == nil {
		t.Fatal("a failed build must fail the command")
	}
	if !strings.Contains(errOut, "AppDelegate.swift:12:5: error: cannot find 'foo' in scope") {
		t.Fatalf("the compiler's error must reach the terminal:\n%s", errOut)
	}
}

func TestOtherProgressBuilds(t *testing.T) {
	own := "/d/build-1/build.stream"
	ps := "  101 /bin/zsh\n" +
		"  202 xcodebuild -workspace A.xcworkspace " + xcodeproj.ProgressFlag + " -resultStreamPath " + own + " build\n" +
		"  303 xcodebuild -workspace B.xcworkspace build\n"
	if otherProgressBuilds([]byte(ps), own) {
		t.Fatal("our own build and a build that posts nothing are not competitors")
	}
	ps += "  404 xcodebuild -workspace C.xcworkspace " + xcodeproj.ProgressFlag + " -resultStreamPath /d/build-2/build.stream build\n"
	if !otherProgressBuilds([]byte(ps), own) {
		t.Fatal("another posting build makes the counts ambiguous")
	}
}
