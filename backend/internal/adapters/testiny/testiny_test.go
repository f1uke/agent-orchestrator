package testiny

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The fixtures under testdata are real outputs of the testiny CLI, recorded
// read-only: stdout of a successful call, stderr of a failed one.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// fakeCLI answers each argv from a table and records every call.
type fakeCLI struct {
	mu      sync.Mutex
	answers map[string]Output
	calls   []string
	argvs   [][]string
}

func (f *fakeCLI) run(_ context.Context, name string, args ...string) (Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	argv := strings.Join(args, " ")
	f.calls = append(f.calls, name+" "+argv)
	f.argvs = append(f.argvs, append([]string(nil), args...))
	out, ok := f.answers[argv]
	if !ok {
		return Output{Stderr: []byte(`{"error":{"kind":"usage","message":"unexpected call"},"exit_code":2}`), ExitCode: 2}, nil
	}
	return out, nil
}

func (f *fakeCLI) count(argv string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasSuffix(c, " "+argv) {
			n++
		}
	}
	return n
}

func ok(t *testing.T, name string) Output { return Output{Stdout: fixture(t, name)} }

func failed(t *testing.T, name string, exit int) Output {
	return Output{Stderr: fixture(t, name), ExitCode: exit}
}

func onPath(string) (string, error) { return "/usr/local/bin/testiny", nil }

func newClient(f *fakeCLI, now func() time.Time) *Client {
	return New(Options{LookPath: onPath, Runner: f.run, Home: "/nonexistent", Now: now})
}

func TestRunReadsARunWithoutPlanOrMilestone(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"run show 632": ok(t, "run_show_632.json")}}
	run, err := newClient(f, time.Now).Run(context.Background(), 632)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Run{ID: 632, Title: "MOBILITY-4839 Chat notice disclaimer - iOS", ProjectID: 1}
	if run != want {
		t.Fatalf("Run = %+v, want %+v", run, want)
	}
}

func TestRunReadsPlanAndMilestoneIDs(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"run show 625": ok(t, "run_show_625.json")}}
	run, err := newClient(f, time.Now).Run(context.Background(), 625)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.PlanID != 193 || run.MilestoneID != 80 || run.ProjectID != 1 || run.Closed {
		t.Fatalf("Run = %+v, want plan 193, milestone 80, project 1, open", run)
	}
}

func TestResultsListsEveryCaseWithItsStatus(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"run results ls --run=632": ok(t, "run_results_632.json")}}
	res, err := newClient(f, time.Now).Results(context.Background(), 632)
	if err != nil {
		t.Fatalf("Results: %v", err)
	}
	if len(res.Cases) != 6 {
		t.Fatalf("cases = %d, want 6", len(res.Cases))
	}
	if first := res.Cases[0]; first != (Case{ID: 7166, Title: "[Finno Chat] Fund disclaimer is displayed in chat", Status: "PASSED"}) {
		t.Fatalf("first case = %+v", first)
	}
	if !reflect.DeepEqual(res.Summary, map[string]int{"PASSED": 6}) {
		t.Fatalf("summary = %v", res.Summary)
	}
}

func TestPlanAndMilestoneTitles(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{
		"plan show 193":     ok(t, "plan_show_193.json"),
		"milestone show 80": ok(t, "milestone_show_80.json"),
	}}
	c := newClient(f, time.Now)
	plan, err := c.Plan(context.Background(), 193)
	if err != nil || plan != (Ref{ID: 193, Title: "Chat session logout"}) {
		t.Fatalf("Plan = %+v, %v", plan, err)
	}
	ms, err := c.Milestone(context.Background(), 80)
	if err != nil || ms != (Ref{ID: 80, Title: "Sprint 2026-20"}) {
		t.Fatalf("Milestone = %+v, %v", ms, err)
	}
}

func TestProjectResolvesKeyNameOrIDIgnoringCase(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"project ls": ok(t, "project_ls.json")}}
	c := newClient(f, time.Now)
	mob := Project{ID: 1, Name: "MOBILITY", Key: "MOB"}
	for ref, want := range map[string]Project{
		"MOB":      mob,
		"mob":      mob,
		"Mobility": mob,
		"1":        mob,
		"kern":     {ID: 2, Name: "KERN", Key: "KERN"},
		"NEURON":   {ID: 10, Name: "NEURON", Key: "NEUR"},
		"ecoupon":  {ID: 4, Name: "ECOUPON", Key: ""},
	} {
		got, err := c.Project(context.Background(), ref)
		if err != nil || got != want {
			t.Errorf("Project(%q) = %+v, %v; want %+v", ref, got, err, want)
		}
	}
	if _, err := c.Project(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Project(nope) err = %v, want ErrNotFound", err)
	}
}

func TestProjectListIsCachedForTenMinutes(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"project ls": ok(t, "project_ls.json")}}
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	c := newClient(f, func() time.Time { return now })
	for range 3 {
		if _, err := c.Project(context.Background(), "MOB"); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count("project ls"); n != 1 {
		t.Fatalf("project ls ran %d times within the TTL, want 1", n)
	}
	now = now.Add(10*time.Minute + time.Second)
	if _, err := c.Project(context.Background(), "MOB"); err != nil {
		t.Fatal(err)
	}
	if n := f.count("project ls"); n != 2 {
		t.Fatalf("project ls ran %d times after the TTL, want 2", n)
	}
}

func TestFailedListIsNotCached(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"project ls": failed(t, "network_down.stderr.json", 4)}}
	c := newClient(f, time.Now)
	if _, err := c.Project(context.Background(), "MOB"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	f.answers["project ls"] = ok(t, "project_ls.json")
	if _, err := c.Project(context.Background(), "MOB"); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
}

func TestCLIErrorsMapToSentinels(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  Output
		want error
		msg  string
	}{
		{"404", failed(t, "run_show_999999.stderr.json", 3), ErrNotFound, "The entity with id 999999 was not found."},
		{"bogus key", failed(t, "run_show_bogus_key.stderr.json", 3), ErrAuth, "Unauthenticated user"},
		{"network down", failed(t, "network_down.stderr.json", 4), ErrUnavailable, "connection refused"},
		{"usage", failed(t, "usage.stderr.json", 2), ErrRejected, `"abc" is not an id`},
		{"other api rejection", Output{Stderr: []byte(`{"error":{"kind":"api","message":"Too many requests","code":"API_RATE_LIMIT","status":429},"exit_code":3}`), ExitCode: 3}, ErrRejected, "Too many requests"},
		{"401", Output{Stderr: []byte(`{"error":{"kind":"api","message":"No key","status":401},"exit_code":3}`), ExitCode: 3}, ErrAuth, "No key"},
		{"unparseable error", Output{Stderr: []byte("panic: boom"), ExitCode: 3}, ErrUnavailable, "panic: boom"},
		{"unparseable data", Output{Stdout: []byte("<html>")}, ErrUnavailable, "unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCLI{answers: map[string]Output{"run show 999999": tc.out}}
			_, err := newClient(f, time.Now).Run(context.Background(), 999999)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %q, want it to carry %q", err, tc.msg)
			}
		})
	}
}

func TestAMissingRunHasNoResultsButIsNotAnError(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"run results ls --run=999999": ok(t, "run_results_999999.json")}}
	res, err := newClient(f, time.Now).Results(context.Background(), 999999)
	if err != nil || len(res.Cases) != 0 {
		t.Fatalf("Results = %+v, %v", res, err)
	}
}

func TestACallThatOutlivesItsCapIsUnavailable(t *testing.T) {
	block := func(ctx context.Context, _ string, _ ...string) (Output, error) {
		<-ctx.Done()
		return Output{}, ctx.Err()
	}
	c := New(Options{LookPath: onPath, Runner: block, Home: "/nonexistent", Now: time.Now})
	c.callTimeout = 20 * time.Millisecond
	_, err := c.Run(context.Background(), 632)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want ErrUnavailable that says it timed out", err)
	}
}

func TestBinaryFallsBackToGoBin(t *testing.T) {
	home := t.TempDir()
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }
	f := &fakeCLI{answers: map[string]Output{"run show 632": ok(t, "run_show_632.json")}}

	c := New(Options{LookPath: notOnPath, Runner: f.run, Home: home, Now: time.Now})
	if _, err := c.Run(context.Background(), 632); !errors.Is(err, ErrBinaryMissing) {
		t.Fatalf("err = %v, want ErrBinaryMissing", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("ran %v without a binary", f.calls)
	}

	bin := filepath.Join(home, "go", "bin", "testiny")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil { // #nosec G306 -- test fixture must be executable
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), 632); err != nil {
		t.Fatalf("Run with the go/bin fallback: %v", err)
	}
	if want := bin + " run show 632"; f.calls[0] != want {
		t.Fatalf("call = %q, want %q", f.calls[0], want)
	}
}

// The real runner, against a stand-in CLI: a non-zero exit must reach the
// mapping as an exit code with its stderr, not as a spawn failure.
func TestExecRunnerReportsTheExitCodeAndStderr(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "testiny")
	script := "#!/bin/sh\necho 'note: looking' >&2\ncat '" + filepath.Join(mustAbs(t, "testdata"), "run_show_999999.stderr.json") + "' >&2\nexit 3\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { // #nosec G306 -- the stand-in CLI must be executable
		t.Fatal(err)
	}
	c := New(Options{LookPath: func(string) (string, error) { return bin, nil }, Home: dir, Now: time.Now})
	if _, err := c.Run(context.Background(), 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestSetResultsSendsEachCommentedResultAloneAndTheRestInOneBatch(t *testing.T) {
	comment := `ปุ่ม "ยืนยัน" ไม่แสดง --status=PASSED`
	commented := "run results set --run=632 --project-id=1 --case=7167 --status=FAILED --comment=" + comment
	blocked := "run results set --run=632 --project-id=1 --case=7170 --status=BLOCKED --comment=no device"
	batch := "run results set --run=632 --result=7166=PASSED --result=7168=NOTRUN"
	f := &fakeCLI{answers: map[string]Output{commented: {}, blocked: {}, batch: {}}}
	results := []domain.TestinyResult{
		{CaseID: 7166, Status: domain.TestinyPassed},
		{CaseID: 7167, Status: domain.TestinyFailed, Comment: comment},
		{CaseID: 7168, Status: domain.TestinyNotRun},
		{CaseID: 7170, Status: domain.TestinyBlocked, Comment: "no device"},
	}
	written, err := newClient(f, time.Now).SetResults(context.Background(), 632, 1, results)
	if err != nil {
		t.Fatalf("SetResults: %v", err)
	}
	wantArgv := [][]string{
		{"run", "results", "set", "--run=632", "--project-id=1", "--case=7167", "--status=FAILED", "--comment=" + comment},
		{"run", "results", "set", "--run=632", "--project-id=1", "--case=7170", "--status=BLOCKED", "--comment=no device"},
		{"run", "results", "set", "--run=632", "--result=7166=PASSED", "--result=7168=NOTRUN"},
	}
	if !reflect.DeepEqual(f.argvs, wantArgv) {
		t.Fatalf("argv =\n%q\nwant\n%q", f.argvs, wantArgv)
	}
	if !reflect.DeepEqual(written, []domain.TestinyResult{results[1], results[3], results[0], results[2]}) {
		t.Fatalf("written = %+v", written)
	}
}

func TestSetResultsWithoutCommentsIsOneCall(t *testing.T) {
	batch := "run results set --run=632 --result=7166=PASSED"
	f := &fakeCLI{answers: map[string]Output{batch: {}}}
	if _, err := newClient(f, time.Now).SetResults(context.Background(), 632, 1, []domain.TestinyResult{{CaseID: 7166, Status: domain.TestinyPassed}}); err != nil {
		t.Fatalf("SetResults: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %q, want only the batch", f.calls)
	}
}

func TestSetResultsStopsAtTheFirstFailureAndSaysWhatWasWritten(t *testing.T) {
	first := "run results set --run=632 --project-id=1 --case=7167 --status=FAILED --comment=a"
	second := "run results set --run=632 --project-id=1 --case=7170 --status=SKIPPED --comment=b"
	f := &fakeCLI{answers: map[string]Output{
		first:  {},
		second: failed(t, "run_show_bogus_key.stderr.json", 3),
	}}
	results := []domain.TestinyResult{
		{CaseID: 7166, Status: domain.TestinyPassed},
		{CaseID: 7167, Status: domain.TestinyFailed, Comment: "a"},
		{CaseID: 7170, Status: domain.TestinySkipped, Comment: "b"},
	}
	written, err := newClient(f, time.Now).SetResults(context.Background(), 632, 1, results)
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if !reflect.DeepEqual(written, []domain.TestinyResult{results[1]}) {
		t.Fatalf("written = %+v, want only TC-7167", written)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls = %q, want it to stop after the failure", f.calls)
	}
}
