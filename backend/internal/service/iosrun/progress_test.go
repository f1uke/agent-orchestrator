package iosrun

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	if _, err := svc.Start(context.Background(), "mer-9", "Nter", "Dev", ""); err != nil {
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
	if _, err := svc.Start(context.Background(), "mer-9", "Nter", "Dev", ""); err != nil {
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
	if _, err := svc.Start(context.Background(), "mer-9", "Nter", "Dev", ""); err != nil {
		t.Fatal(err)
	}
	writeProgress(t, svc, "mer-9", Progress{Stage: StageLaunching, StageStartedAt: time.Now()})
	if _, err := svc.Start(context.Background(), "mer-9", "Nter", "Dev", ""); err != nil {
		t.Fatal(err)
	}
	if run, _, _ := svc.Current(context.Background(), "mer-9"); run.Stage != "" {
		t.Fatalf("stage %q is the previous run's", run.Stage)
	}
}

func finishWith(t *testing.T, svc *Service, configuration string, seconds float64, at time.Time) {
	t.Helper()
	if _, err := svc.Start(context.Background(), "mer-9", "Nter", configuration, ""); err != nil {
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
	if _, err := restarted.Start(context.Background(), "mer-9", "Nter", "Dev", ""); err != nil {
		t.Fatal(err)
	}
	if run, _, _ := restarted.Current(context.Background(), "mer-9"); run.LastBuildSeconds != 102 {
		t.Fatalf("lastBuildSeconds = %v, want 102 from the last Dev build", run.LastBuildSeconds)
	}
	if _, err := restarted.Start(context.Background(), "mer-9", "Nter", "UAT", ""); err != nil {
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

	entry, ok := svc.lastBuild("nter", "Nter", "Dev")
	if !ok || entry.BuildSeconds != 40 {
		t.Fatalf("history = %+v, want the 10:00 build's 40s", entry)
	}
}

func TestCurrent_DoesNotRememberARunThatNeverBuilt(t *testing.T) {
	svc := stateful(t, worktree(t, "Nter.xcodeproj"), t.TempDir(), &fakeRuntime{alive: true})
	finishWith(t, svc, "Dev", 0, time.Now())
	if _, ok := svc.lastBuild("nter", "Nter", "Dev"); ok {
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
	if _, err := svc.Start(context.Background(), "mer-9", "Nter", "Dev", ""); err != nil {
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
