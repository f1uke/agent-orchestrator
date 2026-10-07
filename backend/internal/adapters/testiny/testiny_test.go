package testiny

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The fixtures under testdata are real outputs of the testiny CLI, recorded
// read-only: stdout of a successful call, stderr of a failed one. Every case's
// Test Data is replaced with fake values.

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
	f := &fakeCLI{answers: map[string]Output{"run show 632 --with case": ok(t, "run_show_632.json")}}
	run, err := newClient(f, time.Now).Run(context.Background(), 632)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := Run{ID: 632, Title: "MOBILITY-4839 Chat notice disclaimer - iOS", ProjectID: 1, Steps: map[int64][]domain.TestinyRunStep{}}
	if !reflect.DeepEqual(run, want) {
		t.Fatalf("Run = %+v, want %+v", run, want)
	}
}

func TestRunReadsEachCasesStepResults(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"run show 625 --with case": ok(t, "run_show_625.json")}}
	run, err := newClient(f, time.Now).Run(context.Background(), 625)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := map[int64][]domain.TestinyRunStep{
		6544: {
			{N: 1, RID: "TlKNJNxnUS", Status: "PASSED"},
			{N: 2, RID: "ywmnkYOylj", Status: "PASSED"},
			{N: 3, RID: "Pa3mFn9Zko", Status: "PASSED"},
			{N: 4, RID: "QLmk5OGCY3", Status: "PASSED"},
			{N: 5, RID: "QQv0Xsgeij", Status: "PASSED"},
		},
	}
	if !reflect.DeepEqual(run.Steps, want) {
		t.Fatalf("Steps = %+v, want %+v", run.Steps, want)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %q, want the one run show", f.calls)
	}
}

func TestRunStepResultsTolerateEveryShapeOfTheMapping(t *testing.T) {
	for name, values := range map[string]string{
		"one case as an object": `{"testcase_id":9,"result_per_step":[{"idx":1,"rid":"b","res":"FAILED"},{"idx":0,"rid":"","res":"PASSED"}]}`,
		"a list":                `[{"testcase_id":9,"result_per_step":[{"idx":1,"rid":"b","res":"FAILED"},{"idx":0,"rid":"","res":"PASSED"}]},{"testcase_id":10,"result_per_step":null}]`,
	} {
		out := Output{Stdout: []byte(`{"data":{"id":5,"title":"t","project_id":1,"testrun_testcase_values":` + values + `}}`)}
		f := &fakeCLI{answers: map[string]Output{"run show 5 --with case": out}}
		run, err := newClient(f, time.Now).Run(context.Background(), 5)
		if err != nil {
			t.Fatalf("%s: Run: %v", name, err)
		}
		want := map[int64][]domain.TestinyRunStep{9: {{N: 1, Status: "PASSED"}, {N: 2, RID: "b", Status: "FAILED"}}}
		if !reflect.DeepEqual(run.Steps, want) {
			t.Fatalf("%s: Steps = %+v, want %+v", name, run.Steps, want)
		}
	}
	for name, values := range map[string]string{"no cases": `null`, "an empty list": `[]`} {
		out := Output{Stdout: []byte(`{"data":{"id":5,"title":"t","project_id":1,"testrun_testcase_values":` + values + `}}`)}
		f := &fakeCLI{answers: map[string]Output{"run show 5 --with case": out}}
		run, err := newClient(f, time.Now).Run(context.Background(), 5)
		if err != nil || len(run.Steps) != 0 {
			t.Fatalf("%s: Run = %+v, %v; want no step results", name, run, err)
		}
	}
}

func TestRunReadsPlanAndMilestoneIDs(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"run show 625 --with case": ok(t, "run_show_625.json")}}
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
	want := Milestone{
		ID: 80, Title: "Sprint 2026-20",
		StartAt:   time.Date(2026, 9, 22, 5, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, 9, 22, 4, 22, 52, 969000000, time.UTC),
	}
	if err != nil || ms.ID != want.ID || ms.Title != want.Title || !ms.StartAt.Equal(want.StartAt) || !ms.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("Milestone = %+v, %v; want %+v", ms, err, want)
	}
}

func TestMilestoneWithoutAStartDate(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{
		"milestone show 81": {Stdout: []byte(`{"data":{"id":81,"title":"Backlog","created_at":"2025-12-01T00:00:00.000Z","start_at":null},"meta":null}`)},
	}}
	ms, err := newClient(f, time.Now).Milestone(context.Background(), 81)
	if err != nil || !ms.StartAt.IsZero() || ms.CreatedAt.Year() != 2025 {
		t.Fatalf("Milestone = %+v, %v; want no start and the created date", ms, err)
	}
}

const commentFind565 = `raw POST /comment/find --body={"filter":{"type":"TEXT"},"map":{"entities":["comment","testrun","testcase"],"ids":{"testrun_id":565}},"pagination":{"limit":1000,"offset":0}}`

func TestResultCommentsReadsEveryLinkOnEachResult(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{commentFind565: ok(t, "comment_find_565.json")}}
	got, err := newClient(f, time.Now).ResultComments(context.Background(), 565)
	if err != nil {
		t.Fatalf("ResultComments: %v", err)
	}
	want := []ResultComment{
		{ID: 2562, CaseID: 3829},
		{ID: 2568, CaseID: 6538, URLs: []string{"https://drive.google.com/file/d/1FakeDriveIdOne/view?usp=drive_link"}},
		{ID: 2570, CaseID: 6542, URLs: []string{"https://drive.google.com/open?id=1FakeDriveIdFour"}},
		{ID: 2573, CaseID: 6542, URLs: []string{
			"https://drive.google.com/file/d/1FakeDriveIdTwo/view?usp=drive_link",
			"https://drive.google.com/file/d/1FakeDriveIdThree/view?usp=drive_link",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResultComments =\n%+v\nwant (oldest first, the deleted comment left out)\n%+v", got, want)
	}
}

func TestResultCommentsPagesUntilAShortPage(t *testing.T) {
	page := func(offset, n int) Output {
		var data []string
		for i := range n {
			data = append(data, fmt.Sprintf(`{"id":%d,"deleted_at":null,"text":"plain https://drive.google.com/open?id=x%d","comment_testrun_values":{"testcase_id":1}}`, offset+i+1, offset+i+1))
		}
		return Output{Stdout: []byte(`{"meta":{"count":` + strconv.Itoa(n) + `},"data":[` + strings.Join(data, ",") + `]}`)}
	}
	at := func(offset int) string {
		return strings.Replace(commentFind565, `"offset":0`, `"offset":`+strconv.Itoa(offset), 1)
	}
	f := &fakeCLI{answers: map[string]Output{at(0): page(0, 1000), at(1000): page(1000, 1000), at(2000): page(2000, 3)}}
	got, err := newClient(f, time.Now).ResultComments(context.Background(), 565)
	if err != nil {
		t.Fatalf("ResultComments: %v", err)
	}
	if len(got) != 2003 || got[2002].URLs[0] != "https://drive.google.com/open?id=x2003" {
		t.Fatalf("read %d comments, want 2003 across three pages", len(got))
	}
	if n := len(f.calls); n != 3 {
		t.Fatalf("%d calls, want 3", n)
	}
}

func TestCommentURLsReadsPlainText(t *testing.T) {
	got := commentURLs("see https://drive.google.com/file/d/1a/view, and (https://example.com/x).")
	want := []string{"https://drive.google.com/file/d/1a/view", "https://example.com/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commentURLs = %q, want %q", got, want)
	}
}

func TestCommentOnResult(t *testing.T) {
	text := "https://drive.google.com/file/d/1a/view?usp=drive_link\nhttps://drive.google.com/file/d/1b/view?usp=drive_link"
	argv := "run results comment --run=565 --case=6538 --project-id=1 --text=" + text
	f := &fakeCLI{answers: map[string]Output{argv: {Stdout: []byte(`{"data":{"id":2601,"type":"TEXT","target":"TRTC","project_id":1},"meta":null}`)}}}
	id, err := newClient(f, time.Now).CommentOnResult(context.Background(), 565, 6538, 1, text)
	if err != nil || id != 2601 {
		t.Fatalf("CommentOnResult = %d, %v; want 2601", id, err)
	}
	if last := f.argvs[0][len(f.argvs[0])-1]; last != "--text="+text {
		t.Fatalf("the text is not one argv element: %q", f.argvs[0])
	}
}

func TestCommentOnResultRefusedForACaseNotInTheRun(t *testing.T) {
	argv := "run results comment --run=565 --case=9 --project-id=1 --text=x"
	f := &fakeCLI{answers: map[string]Output{argv: {
		Stderr:   []byte(`{"error":{"kind":"usage","message":"case 9 is not in run 565, so a comment on its result would show nowhere"},"exit_code":2}`),
		ExitCode: 2,
	}}}
	if _, err := newClient(f, time.Now).CommentOnResult(context.Background(), 565, 9, 1, "x"); !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
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
			f := &fakeCLI{answers: map[string]Output{"run show 999999 --with case": tc.out}}
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
	f := &fakeCLI{answers: map[string]Output{"run show 632 --with case": ok(t, "run_show_632.json")}}

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
	if want := bin + " run show 632 --with case"; f.calls[0] != want {
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

func TestSetResultsSendsStepsOnTheCasesOwnCall(t *testing.T) {
	steps := "run results set --run=632 --case=7166 --status=PASSED --step=1=PASSED --step=2=FAILED"
	commented := "run results set --run=632 --project-id=1 --case=7167 --status=FAILED --comment=step 2 --step=2=FAILED"
	batch := "run results set --run=632 --result=7168=NOTRUN"
	f := &fakeCLI{answers: map[string]Output{steps: {}, commented: {}, batch: {}}}
	results := []domain.TestinyResult{
		{CaseID: 7166, Status: domain.TestinyPassed, Steps: []domain.TestinyStepResult{{N: 1, Status: domain.TestinyPassed}, {N: 2, Status: domain.TestinyFailed}}},
		{CaseID: 7167, Status: domain.TestinyFailed, Comment: "step 2", Steps: []domain.TestinyStepResult{{N: 2, Status: domain.TestinyFailed}}},
		{CaseID: 7168, Status: domain.TestinyNotRun},
	}
	written, err := newClient(f, time.Now).SetResults(context.Background(), 632, 1, results)
	if err != nil {
		t.Fatalf("SetResults: %v", err)
	}
	wantArgv := [][]string{
		{"run", "results", "set", "--run=632", "--case=7166", "--status=PASSED", "--step=1=PASSED", "--step=2=FAILED"},
		{"run", "results", "set", "--run=632", "--project-id=1", "--case=7167", "--status=FAILED", "--comment=step 2", "--step=2=FAILED"},
		{"run", "results", "set", "--run=632", "--result=7168=NOTRUN"},
	}
	if !reflect.DeepEqual(f.argvs, wantArgv) {
		t.Fatalf("argv =\n%q\nwant\n%q", f.argvs, wantArgv)
	}
	if !reflect.DeepEqual(written, results) {
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

func TestCaseReadsAStepsCase(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{
		"case view 7166":                 ok(t, "case_view_7166.json"),
		"case show 7166 --with workitem": ok(t, "case_show_7166_workitem.json"),
	}}
	got, err := newClient(f, time.Now).Case(context.Background(), 7166)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	want := domain.TestinyCaseDetail{
		ID:          7166,
		Title:       "[Finno Chat] Fund disclaimer is displayed in chat",
		Priority:    &domain.TestinyCasePriority{Level: 1, Label: "High"},
		Type:        "FUNCTIONAL",
		Template:    domain.TestinyTemplateSteps,
		Platforms:   []string{"ADR", "iOS"},
		Jira:        "MOBILITY-4839",
		Features:    "Finnomena Chat",
		SubFeatures: "Notice disclaimer",
		TestData:    "qa@example.com / fake-password",
		Precondition: "- Logged in to the Main app (Android or iOS)\n" +
			"- A chat room exists where a fund disclaimer notice message has been sent",
		Automation: []string{"Manual"},
		Steps: []domain.TestinyCaseStep{
			{N: 1, RID: "5quWOzIAc5", Action: "Open the Main app and go to Finnomena Chat", Expected: "Chat list is displayed"},
			{N: 2, RID: "g0Gf9X8Vbe", Action: "Open the chat room that contains the fund disclaimer message", Expected: "Chat room opens and scrolls to the latest messages"},
			{N: 3, RID: "pCqV0Dbrt8", Action: "Look at the fund disclaimer message", Expected: "Disclaimer is shown as a notice message (not a normal chat bubble) with the full fund disclaimer text, readable and not truncated"},
			{N: 4, RID: "HEcuShOZ5K", Action: "If the disclaimer has a header image, check it", Expected: "Header image loads at the correct width, with no broken-image icon inside the text"},
		},
		Requirements: []domain.TestinyRequirement{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Case =\n%+v\nwant\n%+v", got, want)
	}
	if f.count("case view 7166") != 1 || f.count("case show 7166 --with workitem") != 1 || len(f.calls) != 2 {
		t.Fatalf("calls = %q, want one case view and one case show", f.calls)
	}
}

// A case's Jira links come from `case show --with workitem`, which `case view`
// does not carry. Case 387 is linked to three issues.
func TestCaseReadsItsRequirements(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{
		"case view 387":                 ok(t, "case_view_387.json"),
		"case show 387 --with workitem": ok(t, "case_show_387_workitem.json"),
	}}
	got, err := newClient(f, time.Now).Case(context.Background(), 387)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	want := []domain.TestinyRequirement{
		{Key: "STAR-2254", Summary: "QA: Web - E-Coupon 3.0 - Search & Validate coupon by code API - Test", Status: "Done"},
		{Key: "STAR-2253", Summary: "QA: Web - E-Coupon 3.0 -  Search & Validate coupon by code API - Design Test Case", Status: "Ready for UAT"},
		{Key: "MOBILITY-4166", Summary: "[MOBILITY] Chat and notification tab adjustment", Status: "Discovering"},
	}
	if !reflect.DeepEqual(got.Requirements, want) {
		t.Fatalf("Requirements =\n%+v\nwant\n%+v", got.Requirements, want)
	}
}

// caseShow is the 387 `case show --with workitem` fixture with its links
// changed by edit.
func caseShow(t *testing.T, edit func(links []any) any) Output {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
		Meta any            `json:"meta"`
	}
	if err := json.Unmarshal(fixture(t, "case_show_387_workitem.json"), &env); err != nil {
		t.Fatal(err)
	}
	env.Data["wi_tc_values"] = edit(env.Data["wi_tc_values"].([]any))
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return Output{Stdout: b}
}

// A defect link is a bug found against the case, not what the case tests, and
// a link whose issue Testiny could not name says nothing a person can act on.
func TestCaseRequirementsLeaveOutDefectsAndUnnamedIssues(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{
		"case view 387": ok(t, "case_view_387.json"),
		"case show 387 --with workitem": caseShow(t, func(links []any) any {
			links[0].(map[string]any)["workitem_type"] = "DEFECT"
			links[1].(map[string]any)["workitem_key"] = nil
			return links
		}),
	}}
	got, err := newClient(f, time.Now).Case(context.Background(), 387)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if len(got.Requirements) != 1 || got.Requirements[0].Key != "MOBILITY-4166" {
		t.Fatalf("Requirements = %+v, want only MOBILITY-4166", got.Requirements)
	}
}

// The CLI reports a relation it could not read as null rather than failing the
// read. Shown as no links, it would tell a person the case is not linked when
// nobody knows, so the read is unavailable instead and can be retried.
func TestCaseWhoseLinksCannotBeReadIsUnavailable(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{
		"case view 387":                 ok(t, "case_view_387.json"),
		"case show 387 --with workitem": caseShow(t, func([]any) any { return nil }),
	}}
	if _, err := newClient(f, time.Now).Case(context.Background(), 387); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Case err = %v, want ErrUnavailable", err)
	}
}

func TestCaseReadsTextCases(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{
		"case view 548":                 ok(t, "case_view_548.json"),
		"case show 548 --with workitem": noLinks,
		"case view 558":                 ok(t, "case_view_558.json"),
		"case show 558 --with workitem": noLinks,
	}}
	c := newClient(f, time.Now)
	got, err := c.Case(context.Background(), 548)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if got.Priority != nil || got.Template != domain.TestinyTemplateText || got.Type != "" || got.Jira != "" {
		t.Fatalf("priority %v template %q type %q jira %q, want none, TEXT and empty", got.Priority, got.Template, got.Type, got.Jira)
	}
	if got.TestData != "INT\nAdvisor: advisor@example.com | fake-password\nCustomer: customer@example.com | fake-password" {
		t.Fatalf("TestData = %q", got.TestData)
	}
	if got.Precondition != "1. Login user customer" || got.StepsText != "1. Advisor text to customer" ||
		got.ExpectedText != "1. Advisor profile shows on notification correctly" {
		t.Fatalf("precondition %q, steps %q, expected %q", got.Precondition, got.StepsText, got.ExpectedText)
	}
	if got.Platforms == nil || len(got.Platforms) != 0 || got.Steps == nil || len(got.Steps) != 0 {
		t.Fatalf("empty lists must be empty, not nil: platforms %#v steps %#v", got.Platforms, got.Steps)
	}

	got, err = c.Case(context.Background(), 558)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if got.Remark != "Test for import in jira" || got.TestData != "" || got.Description != "" {
		t.Fatalf("remark %q, test data %q, description %q", got.Remark, got.TestData, got.Description)
	}
	if !reflect.DeepEqual(got.Platforms, []string{"ADR", "API", "Web", "iOS"}) {
		t.Fatalf("platforms = %q", got.Platforms)
	}
}

// caseView is the 7166 fixture with its case changed by edit, for shapes no
// MOB case has.
func caseView(t *testing.T, edit func(c map[string]any)) Output {
	t.Helper()
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(fixture(t, "case_view_7166.json"), &env); err != nil {
		t.Fatal(err)
	}
	edit(env.Data[0])
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return Output{Stdout: b}
}

func TestCaseReadsABDDCase(t *testing.T) {
	feature := "Feature: Chat\n  Scenario: Disclaimer\n    Given a chat room"
	f := &fakeCLI{answers: map[string]Output{"case view 7166": caseView(t, func(c map[string]any) {
		c["template"], c["steps"], c["bdd"] = "BDD", nil, feature
	}), "case show 7166 --with workitem": noLinks}}
	got, err := newClient(f, time.Now).Case(context.Background(), 7166)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if got.Template != domain.TestinyTemplateBDD || got.BDD != feature || got.Steps == nil || len(got.Steps) != 0 {
		t.Fatalf("template %q bdd %q steps %#v", got.Template, got.BDD, got.Steps)
	}
}

// Custom fields are the project's own: any of them may hold a value of another
// type, and fields AO does not know come and go.
func TestCaseToleratesCustomFieldsOfAnyShape(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"case view 7166": caseView(t, func(c map[string]any) {
		c["a_key_testiny_added_later"] = map[string]any{"x": 1}
		c["custom"] = map[string]any{
			"cf__jira":             []any{"MOBILITY-4839", "MOBILITY-4840"},
			"cf__platform":         "iOS",
			"cf__automationstatus": true,
			"cf__section":          false,
			"cf__features":         7,
			"cf__description":      "What the case covers",
			"cf__remark":           "- first\n- second",
			"cf__newfield":         map[string]any{"nested": []any{1, 2}},
		}
	}), "case show 7166 --with workitem": noLinks}}
	got, err := newClient(f, time.Now).Case(context.Background(), 7166)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if got.Jira != "MOBILITY-4839, MOBILITY-4840" || got.Section != "false" || got.Features != "7" || got.SubFeatures != "" || got.TestData != "" {
		t.Fatalf("jira %q section %q features %q subfeatures %q test data %q", got.Jira, got.Section, got.Features, got.SubFeatures, got.TestData)
	}
	if !reflect.DeepEqual(got.Platforms, []string{"iOS"}) || got.Automation == nil || len(got.Automation) != 0 {
		t.Fatalf("platforms %#v automation %#v", got.Platforms, got.Automation)
	}
	if got.Description != "What the case covers" || got.Remark != "- first\n- second" {
		t.Fatalf("description %q remark %q", got.Description, got.Remark)
	}
}

// noLinks is a `case show --with workitem` of a case with no Jira links.
var noLinks = Output{Stdout: []byte(`{"data":{"id":1,"wi_tc_values":[]},"meta":null}`)}

func TestAMissingCaseIsNotFound(t *testing.T) {
	f := &fakeCLI{answers: map[string]Output{"case view 99999999": failed(t, "case_view_99999999.stderr.json", 3)}}
	if _, err := newClient(f, time.Now).Case(context.Background(), 99999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Case err = %v, want ErrNotFound", err)
	}
}

// A CLI built before `case view` existed refuses the subcommand. The person
// must update it; retrying never helps.
func TestACLIWithoutCaseViewIsTooOld(t *testing.T) {
	for name, out := range map[string]Output{
		"recorded":   failed(t, "case_view_old_cli.stderr.json", 2),
		"bare cobra": {Stderr: []byte("Error: unknown command \"view\" for \"testiny case\"\nRun 'testiny case --help' for usage.\n"), ExitCode: 1},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeCLI{answers: map[string]Output{"case view 7166": out}}
			_, err := newClient(f, time.Now).Case(context.Background(), 7166)
			if !errors.Is(err, ErrCLITooOld) {
				t.Fatalf("err = %v, want ErrCLITooOld", err)
			}
			if want := "Update the testiny CLI: cd ~/Documents/Projects/testiny-cli && git pull && go install ./cmd/testiny"; !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %q, want it to say %q", err, want)
			}
		})
	}
}
