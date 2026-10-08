package iosrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/xcodegen"
	"github.com/aoagents/agent-orchestrator/backend/internal/xcresultstream"
)

func writeProgress(t *testing.T, svc *Service, id string, progress Progress) {
	t.Helper()
	body, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc.runDir(domain.SessionID(id)), ProgressFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCurrent_CarriesTheProgressTheCommandReported(t *testing.T) {
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), &fakeRuntime{alive: true})
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", UDID: ""}); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	writeProgress(t, svc, "mer-9", Progress{
		PID: 4242, Stage: StageBuilding, StageStartedAt: started, BuildStartedAt: &started,
		Build: &BuildProgress{Phase: xcresultstream.PhaseCompiling, Counts: &xcresultstream.Counts{Done: 120, Total: 480, Fraction: 0.25}},
	})

	run, _, err := svc.Current(context.Background(), "mer-9")
	if err != nil {
		t.Fatal(err)
	}
	if run.Stage != StageBuilding || run.Build == nil || run.Build.Counts.Done != 120 || !run.BuildStartedAt.Equal(started) {
		t.Fatalf("run = %+v, want building 120/480 since %v", run, started)
	}
}

func TestCurrent_DropsProgressOnceTheRunHasAVerdict(t *testing.T) {
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), &fakeRuntime{alive: true})
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", UDID: ""}); err != nil {
		t.Fatal(err)
	}
	writeProgress(t, svc, "mer-9", Progress{Stage: StageLaunching, StageStartedAt: time.Now()})
	writeVerdict(t, svc.resultPath("mer-9"), Result{State: RunSucceeded, Summary: "Built."})

	run, _, _ := svc.Current(context.Background(), "mer-9")
	if run.Stage != "" || run.Build != nil {
		t.Fatalf("a finished run has no stage: %+v", run)
	}
}

func TestStart_ClearsThePreviousRunsProgress(t *testing.T) {
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), &fakeRuntime{alive: true})
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", UDID: ""}); err != nil {
		t.Fatal(err)
	}
	writeProgress(t, svc, "mer-9", Progress{Stage: StageLaunching, StageStartedAt: time.Now()})
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", UDID: ""}); err != nil {
		t.Fatal(err)
	}
	if run, _, _ := svc.Current(context.Background(), "mer-9"); run.Stage != "" {
		t.Fatalf("stage %q is the previous run's", run.Stage)
	}
}

func finishWith(t *testing.T, svc *Service, configuration string, seconds float64, at time.Time) {
	t.Helper()
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: configuration, UDID: ""}); err != nil {
		t.Fatal(err)
	}
	writeVerdict(t, svc.resultPath("mer-9"), Result{State: RunSucceeded, FinishedAt: &at, BuildSeconds: seconds})
	if _, _, err := svc.Current(context.Background(), "mer-9"); err != nil {
		t.Fatal(err)
	}
}

func TestCurrent_EstimatesFromTheLastSuccessfulBuildOfTheSameSchemeAndConfiguration(t *testing.T) {
	state := t.TempDir()
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), state, &fakeRuntime{alive: true})
	finishWith(t, svc, "Dev", 102, time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC))

	restarted := stateful(t, worktree(t, "Nter.xcodeproj"), state, &fakeRuntime{alive: true})
	if _, err := restarted.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", UDID: ""}); err != nil {
		t.Fatal(err)
	}
	if run, _, _ := restarted.Current(context.Background(), "mer-9"); run.LastBuildSeconds != 102 {
		t.Fatalf("lastBuildSeconds = %v, want 102 from the last Dev build", run.LastBuildSeconds)
	}
	if _, err := restarted.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "UAT", UDID: ""}); err != nil {
		t.Fatal(err)
	}
	if run, _, _ := restarted.Current(context.Background(), "mer-9"); run.LastBuildSeconds != 0 {
		t.Fatalf("lastBuildSeconds = %v for UAT, want none: only Dev has been built", run.LastBuildSeconds)
	}
}

func TestCurrent_KeepsTheNewestBuildDuration(t *testing.T) {
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), &fakeRuntime{alive: true})
	finishWith(t, svc, "Dev", 300, time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC))
	finishWith(t, svc, "Dev", 40, time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC))
	finishWith(t, svc, "Dev", 999, time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC))

	entry, ok := svc.lastBuild("nter", Run{Scheme: "Nter", Configuration: "Dev"})
	if !ok || entry.BuildSeconds != 40 {
		t.Fatalf("history = %+v, want the 10:00 build's 40s", entry)
	}
}

func TestCurrent_DoesNotRememberARunThatNeverBuilt(t *testing.T) {
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), &fakeRuntime{alive: true})
	finishWith(t, svc, "Dev", 0, time.Now())
	if _, ok := svc.lastBuild("nter", Run{Scheme: "Nter", Configuration: "Dev"}); ok {
		t.Fatal("a run with no successful build has no duration to estimate from")
	}
}

func TestProject_ShowsTheBarForAnXcodegenProjectThatWasNeverGenerated(t *testing.T) {
	dir := worktree(t)
	if err := os.WriteFile(filepath.Join(dir, "project.yml"), []byte("name: Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(fakeSessions{path: dir, found: true}, &fakeRuntime{}, "ao",
		WithXcodegen(xcodegen.New("", xcodegen.WithBinary(func() (string, error) { return "", exec.ErrNotFound }))))
	project, err := svc.Project(context.Background(), "mer-9", false)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "" || len(project.Xcodegen.Specs) != 1 || project.SchemesError == "" {
		t.Fatalf("project = %+v, want no Xcode project, one spec, and why there is nothing to build", project)
	}
}

func TestProject_IgnoresASpecDeepInANonIOSRepository(t *testing.T) {
	dir := worktree(t, "backend/internal/xctest")
	if err := os.WriteFile(filepath.Join(dir, "backend/internal/xctest/project.yml"), []byte("name: Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(fakeSessions{path: dir, found: true}, &fakeRuntime{}, "ao")
	project, err := svc.Project(context.Background(), "mer-9", false)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "" || len(project.Xcodegen.Specs) != 0 {
		t.Fatalf("project = %+v, want nothing: a test fixture's spec is not this repository's app", project)
	}
}

func stoppable(t *testing.T, args string) (*Service, *[]int) {
	t.Helper()
	var signalled []int
	svc := New(fakeSessions{path: worktree(t, "Nter.xcodeproj"), found: true}, &fakeRuntime{alive: true}, "ao",
		WithStateDir(t.TempDir()),
		WithRunner(func(context.Context, string, string, ...string) ([]byte, error) {
			return []byte(`{"project":{"schemes":["Nter"],"configurations":["Dev"]}}`), nil
		}),
		WithProcesses(
			func(context.Context, int) (string, error) { return args, nil },
			func(pid int) error { signalled = append(signalled, pid); return nil },
		))
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", UDID: ""}); err != nil {
		t.Fatal(err)
	}
	return svc, &signalled
}

func TestStop_InterruptsTheRunsOwnCommand(t *testing.T) {
	svc, signalled := stoppable(t, "/Applications/AO.app/ao sim run --scheme Nter --configuration Dev --attach")
	writeProgress(t, svc, "mer-9", Progress{PID: 4242, Stage: StageBuilding, StageStartedAt: time.Now()})
	if _, err := svc.Stop(context.Background(), "mer-9"); err != nil {
		t.Fatal(err)
	}
	if len(*signalled) != 1 || (*signalled)[0] != 4242 {
		t.Fatalf("signalled %v, want the command's pid 4242", *signalled)
	}
}

func TestStop_LeavesAReusedPidAlone(t *testing.T) {
	svc, signalled := stoppable(t, "/usr/bin/vim notes.txt")
	writeProgress(t, svc, "mer-9", Progress{PID: 4242, Stage: StageBuilding, StageStartedAt: time.Now()})
	if _, err := svc.Stop(context.Background(), "mer-9"); err == nil || len(*signalled) != 0 {
		t.Fatalf("err=%v signalled=%v, want a refusal and no signal: pid 4242 is somebody else's now", err, *signalled)
	}
}

func TestStop_RefusesARunThatHasEnded(t *testing.T) {
	svc, signalled := stoppable(t, "ao sim run")
	writeProgress(t, svc, "mer-9", Progress{PID: 4242, Stage: StageLaunching, StageStartedAt: time.Now()})
	writeVerdict(t, svc.resultPath("mer-9"), Result{State: RunSucceeded})
	if _, err := svc.Stop(context.Background(), "mer-9"); err == nil || len(*signalled) != 0 {
		t.Fatalf("err=%v signalled=%v, want nothing to stop", err, *signalled)
	}
}

func TestExcerptOpensAtTheFirstError(t *testing.T) {
	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	lines[119] = "/w/App/AppDelegate.swift:12:5: error: cannot find 'foo' in scope"
	lines[150] = "/w/App/Other.swift:3:1: error: second"
	log := excerpt(lines)
	if log.ErrorLine != 120 || log.FirstLine != 100 || log.Lines[log.ErrorLine-log.FirstLine] != lines[119] || log.TotalLines != 200 {
		t.Fatalf("log = first %d error %d (%d lines), want the window from 100 with line 120 in it", log.FirstLine, log.ErrorLine, len(log.Lines))
	}
}

func TestExcerptWithoutAnErrorIsTheEnd(t *testing.T) {
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = "warning: unused"
	}
	if log := excerpt(lines); log.ErrorLine != 0 || log.FirstLine != 221 || len(log.Lines) != 80 {
		t.Fatalf("log = %+v", log)
	}
}

func TestCurrent_NamesTheIssuesRelativeToTheWorktree(t *testing.T) {
	dir := worktree(t, "Nter.xcodeproj")
	svc := stateful(t, dir, t.TempDir(), &fakeRuntime{alive: true})
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", UDID: ""}); err != nil {
		t.Fatal(err)
	}
	writeVerdict(t, svc.resultPath("mer-9"), Result{State: RunFailed, Errors: 2, Warnings: 7, Issues: []xcresultstream.Issue{
		{Severity: "error", Message: "cannot find 'foo' in scope", File: filepath.Join(dir, "App", "AppDelegate.swift"), Line: 12, Column: 5},
		{Severity: "error", Message: "outside", File: "/elsewhere/Lib.swift", Line: 1},
	}})
	run, _, _ := svc.Current(context.Background(), "mer-9")
	if run.Errors != 2 || run.Warnings != 7 || run.Issues[0].File != filepath.Join("App", "AppDelegate.swift") || run.Issues[1].File != "/elsewhere/Lib.swift" {
		t.Fatalf("run = %+v", run)
	}
}

func TestStart_PassesTheModeToTheCommand(t *testing.T) {
	cases := map[Mode][]string{
		ModeRun:                {"--attach"},
		ModeRunWithoutBuilding: {"--attach", "--no-build"},
		ModeBuild:              {"--build-only"},
		ModeCleanBuild:         {"--attach", "--clean"},
	}
	for mode, want := range cases {
		rt := &fakeRuntime{}
		svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), rt)
		run, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		argv := strings.Join(rt.created[0].Argv, " ")
		for _, flag := range want {
			if !strings.Contains(argv, flag) {
				t.Errorf("%s: argv %q is missing %s", mode, argv, flag)
			}
		}
		if mode == ModeBuild && strings.Contains(argv, "--attach") {
			t.Errorf("a build-only run has no app to stay with: %q", argv)
		}
		if run.Mode != mode {
			t.Errorf("run.Mode = %q, want %q", run.Mode, mode)
		}
	}
}

func TestStart_RefusesAModeItDoesNotKnow(t *testing.T) {
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), &fakeRuntime{})
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", Mode: "archive"}); err == nil {
		t.Fatal("an unknown mode must be refused, not run as something else")
	}
}

func TestStart_ShowsTheConsoleWhenAsked(t *testing.T) {
	for _, c := range []struct {
		mode    Mode
		console bool
		want    bool
	}{{ModeRun, true, true}, {ModeRun, false, false}, {ModeBuild, true, false}} {
		rt := &fakeRuntime{}
		svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), rt)
		run, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", Mode: c.mode, Console: c.console})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(strings.Join(rt.created[0].Argv, " "), "--console"); got != c.want || run.Console != c.want {
			t.Errorf("%s console=%v: --console passed=%v run.Console=%v, want %v", c.mode, c.console, got, run.Console, c.want)
		}
	}
}

func TestCurrent_NamesAnIssueReachedThroughASymlinkRelativeToTheWorktree(t *testing.T) {
	realDir := t.TempDir()
	dir := filepath.Join(realDir, "wt")
	if err := os.MkdirAll(filepath.Join(dir, "Nter.xcodeproj"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "A.swift"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	svc := stateful(t, dir, t.TempDir(), &fakeRuntime{alive: true})
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev"}); err != nil {
		t.Fatal(err)
	}
	writeVerdict(t, svc.resultPath("mer-9"), Result{State: RunFailed, Errors: 1, Issues: []xcresultstream.Issue{
		{Severity: "error", Message: "x", File: filepath.Join(link, "wt", "A.swift"), Line: 1},
	}})
	if run, _, _ := svc.Current(context.Background(), "mer-9"); run.Issues[0].File != "A.swift" {
		t.Fatalf("file = %q, want A.swift: /tmp and /private/tmp are one directory", run.Issues[0].File)
	}
}

func TestWithoutWorktreePrefixShortensPathsInsideTheWorktree(t *testing.T) {
	got := withoutWorktreePrefix("/private/tmp/wt/App/A.swift:1:2: error: x\n/tmp/wt/App/B.swift:3:4: warning: y\n/elsewhere/C.swift:5: note", "/private/tmp/wt")
	want := "App/A.swift:1:2: error: x\nApp/B.swift:3:4: warning: y\n/elsewhere/C.swift:5: note"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestCurrent_EstimatesACleanBuildFromTheLastCleanBuild(t *testing.T) {
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), &fakeRuntime{alive: true})
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		mode    Mode
		seconds float64
	}{{ModeCleanBuild, 300}, {ModeRun, 40}} {
		if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", Mode: c.mode}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Hour)
		finished := at
		writeVerdict(t, svc.resultPath("mer-9"), Result{State: RunSucceeded, FinishedAt: &finished, BuildSeconds: c.seconds})
		if _, _, err := svc.Current(context.Background(), "mer-9"); err != nil {
			t.Fatal(err)
		}
	}
	for mode, want := range map[Mode]float64{ModeCleanBuild: 300, ModeRun: 40} {
		if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev", Mode: mode}); err != nil {
			t.Fatal(err)
		}
		if run, _, _ := svc.Current(context.Background(), "mer-9"); run.LastBuildSeconds != want {
			t.Errorf("%s: lastBuildSeconds = %v, want %v", mode, run.LastBuildSeconds, want)
		}
	}
}

func TestStart_ClearsTheVerdictAnInterruptedCommandWroteAsItDied(t *testing.T) {
	rt := &fakeRuntime{alive: true}
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), rt)
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev"}); err != nil {
		t.Fatal(err)
	}
	rt.onDestroy = func() {
		writeVerdict(t, svc.resultPath("mer-9"), Result{State: RunStopped, Summary: "Stopped while building Nter."})
	}
	if _, err := svc.Start(context.Background(), "mer-9", StartRequest{Scheme: "Nter", Configuration: "Dev"}); err != nil {
		t.Fatal(err)
	}
	if run, _, _ := svc.Current(context.Background(), "mer-9"); run.State != RunRunning {
		t.Fatalf("state %q: the old command's verdict landed on the new run", run.State)
	}
}

func TestXcodegen_OutlivesTheRequestThatStartedIt(t *testing.T) {
	dir := worktree(t, "Nter.xcodeproj")
	if err := os.WriteFile(filepath.Join(dir, "project.yml"), []byte("name: Nter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var sawCancelled bool
	gen := xcodegen.New("",
		xcodegen.WithBinary(func() (string, error) { return "/bin/xcodegen", nil }),
		xcodegen.WithExec(func(ctx context.Context, _, _ string, args ...string) ([]byte, int, error) {
			if args[0] == "generate" && ctx.Err() != nil {
				sawCancelled = true
			}
			return nil, 0, nil
		}))
	svc := New(fakeSessions{path: dir, found: true}, &fakeRuntime{}, "ao", WithXcodegen(gen))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Xcodegen(ctx, "mer-9"); err != nil {
		t.Fatal(err)
	}
	if sawCancelled {
		t.Fatal("generate ran under the request's context; the HTTP timeout would kill a long pod install")
	}
}

func TestProject_ListsToTheEndWhenTheRequestIsAbandoned(t *testing.T) {
	svc := New(fakeSessions{path: worktree(t, "Nter.xcodeproj"), found: true}, &fakeRuntime{}, "ao",
		WithRunner(func(ctx context.Context, _, _ string, _ ...string) ([]byte, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return []byte(`{"project":{"schemes":["Nter"],"configurations":["Dev"]}}`), nil
		}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	project, err := svc.Project(ctx, "mer-9", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Schemes) != 1 || project.SchemesError != "" {
		t.Fatalf("project = %+v: an abandoned poll must not cache a killed listing", project)
	}
}
