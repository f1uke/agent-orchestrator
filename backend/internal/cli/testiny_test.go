package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

const testinyRunJSON = `{"link":{"sessionId":"app-1","runId":632,"linkedBy":"app-2","createdAt":"2026-10-06T09:00:00Z"},
"title":"MOBILITY-4839 Chat notice disclaimer - iOS","url":"https://app.testiny.io/MOB/testruns/tr/632","closed":false,
"counts":{"PASSED":6},"cases":[],"evidenceDir":"","fetchedAt":"2026-10-06T09:00:00Z"}`

func TestTestinyLinkSendsTheCallerAndPrintsTheRun(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "app-2")
	srv, capture := reviewServer(t, http.StatusOK, testinyRunJSON)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "testiny", "link", "app-1", "https://app.testiny.io/MOB/testruns/tr/632")
	if err != nil {
		t.Fatalf("link: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/app-1/testiny/runs" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(capture.body), &body); err != nil {
		t.Fatal(err)
	}
	if body["ref"] != "https://app.testiny.io/MOB/testruns/tr/632" || body["from"] != "app-2" {
		t.Fatalf("body = %s", capture.body)
	}
	if want := `linked TR-632 "MOBILITY-4839 Chat notice disclaimer - iOS" (6 cases: 6 passed)` + "\n"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

func TestTestinyLinkFromAPersonsShellSendsNoCaller(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, capture := reviewServer(t, http.StatusOK, testinyRunJSON)
	writeRunFileFor(t, cfg, srv)
	if _, _, err := executeCLI(t, aliveDeps(), "testiny", "link", "app-1", "632"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capture.body, "from") {
		t.Fatalf("body = %s, want no from", capture.body)
	}
}

func TestTestinyErrorsExitByKind(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		exit   int
	}{
		{http.StatusBadRequest, "TESTINY_BAD_RUN_REF", 2},
		{http.StatusConflict, "TESTINY_OFF", 2},
		{http.StatusNotFound, "TESTINY_RUN_NOT_FOUND", 1},
		{http.StatusUnprocessableEntity, "TESTINY_RUN_WRONG_PROJECT", 1},
		{http.StatusBadGateway, "TESTINY_AUTH", 1},
	} {
		cfg := setConfigEnv(t)
		srv, _ := reviewServer(t, tc.status, `{"code":"`+tc.code+`","message":"the daemon's words"}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, aliveDeps(), "testiny", "link", "app-1", "632")
		if err == nil || ExitCode(err) != tc.exit || !strings.Contains(err.Error(), "the daemon's words") {
			t.Errorf("%s: err = %v (exit %d), want exit %d with the daemon's message", tc.code, err, ExitCode(err), tc.exit)
		}
	}
}

func TestTestinyUnlink(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusNoContent, "")
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "testiny", "unlink", "app-1", "TR-632")
	if err != nil {
		t.Fatal(err)
	}
	if capture.method != http.MethodDelete || capture.path != "/api/v1/sessions/app-1/testiny/runs/TR-632" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	if out != "unlinked TR-632 from app-1\n" {
		t.Fatalf("output = %q", out)
	}
}

const testinyRunsJSON = `{"project":"MOB","runs":[
{"link":{"sessionId":"app-1","runId":632,"linkedBy":"","createdAt":"2026-10-06T09:00:00Z"},
 "title":"MOBILITY-4839 Chat notice disclaimer - iOS","url":"https://app.testiny.io/MOB/testruns/tr/632","closed":false,
 "plan":{"id":193,"title":"Chat session logout"},"milestone":{"id":80,"title":"Sprint 2026-20"},
 "counts":{"PASSED":4,"FAILED":1,"NOTRUN":1},
 "cases":[{"id":7166,"title":"Fund disclaimer","status":"PASSED","script":"projects/nter/cases/chat/fund.yaml"},
          {"id":7167,"title":"Bond disclaimer","status":"FAILED"},
          {"id":7168,"title":"Chat list","status":"NOTRUN"}],
 "evidenceDir":"/Users/me/Desktop/QA Evidence/MOBILITY/2026/Sprint 2026-20/TP-193 - Chat/TR-632 - Chat",
 "fetchedAt":"2026-10-06T09:00:00Z",
 "fetchError":{"kind":"auth","message":"Unauthenticated user (AUTH_ACCESS_DENIED)"}},
{"link":{"sessionId":"app-1","runId":999,"linkedBy":"","createdAt":"2026-10-06T09:01:00Z"},
 "title":"","url":"","closed":false,"counts":{},"cases":[],"evidenceDir":"",
 "fetchError":{"kind":"not_found","message":"The entity with id 999 was not found."}}]}`

func TestTestinyRunsPrintsOneBlockPerRun(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, testinyRunsJSON)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "testiny", "runs", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/sessions/app-1/testiny/runs" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	want := `TR-632 "MOBILITY-4839 Chat notice disclaimer - iOS" (6 cases: 4 passed, 1 failed, 1 not run)
  https://app.testiny.io/MOB/testruns/tr/632
  plan: Chat session logout, milestone: Sprint 2026-20
  FAILED  TC-7167 Bond disclaimer
  NOTRUN  TC-7168 Chat list
  evidence: /Users/me/Desktop/QA Evidence/MOBILITY/2026/Sprint 2026-20/TP-193 - Chat/TR-632 - Chat
  could not read it now (auth): Unauthenticated user (AUTH_ACCESS_DENIED). Showing the read from 2026-10-06T09:00:00Z.

TR-999 (no data)
  could not read it now (not_found): The entity with id 999 was not found.
`
	if out != want {
		t.Fatalf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestTestinyRunsJSONAndEmpty(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, http.StatusOK, testinyRunsJSON)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "testiny", "runs", "app-1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Project string            `json:"project"`
		Runs    []json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Project != "MOB" || len(got.Runs) != 2 {
		t.Fatalf("--json output = %s (%v)", out, err)
	}

	cfg = setConfigEnv(t)
	srv, _ = reviewServer(t, http.StatusOK, `{"project":"MOB","runs":[]}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err = executeCLI(t, aliveDeps(), "testiny", "runs", "app-1")
	if err != nil || out != "no Testiny runs linked to app-1\n" {
		t.Fatalf("empty: %q, %v", out, err)
	}
}

func TestTestinyArgsAreRequired(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{
		{"testiny", "link", "app-1"},
		{"testiny", "unlink", "app-1"},
		{"testiny", "runs"},
	} {
		if _, _, err := executeCLI(t, aliveDeps(), args...); ExitCode(err) != 2 {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
}

// An agent learns `ao testiny` from the skill: the catalog must point at the
// page, and the page must document every subcommand the CLI has.
func TestTestinySkillPageDocumentsEverySubcommand(t *testing.T) {
	doc := installedSkillPage(t, "testiny.md")
	var testiny *cobra.Command
	for _, cmd := range NewRootCommand(Deps{}).Commands() {
		if cmd.Name() == "testiny" {
			testiny = cmd
		}
	}
	if testiny == nil {
		t.Fatal("no `ao testiny` command")
	}
	for _, sub := range testiny.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		if !regexp.MustCompile(`\bao testiny ` + regexp.QuoteMeta(sub.Name()) + `\b`).MatchString(doc) {
			t.Errorf("`ao testiny %s` is not in testiny.md", sub.Name())
		}
	}

	dir := t.TempDir()
	if err := skillassets.Install(dir); err != nil {
		t.Fatal(err)
	}
	catalog, err := os.ReadFile(filepath.Join(skillassets.Dir(dir, false), "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(catalog), "[commands/testiny.md](commands/testiny.md)") {
		t.Fatal("SKILL.md has no row for `ao testiny`")
	}
}

const testinyRecordedJSON = `{"link":{"sessionId":"app-1","runId":640,"linkedBy":"","createdAt":"2026-10-07T09:00:00Z"},
"title":"[AO-TEST] results","url":"https://app.testiny.io/MOB/testruns/tr/640","closed":false,
"counts":{"PASSED":1,"FAILED":1,"NOTRUN":1},
"cases":[{"id":7201,"title":"Opens","status":"PASSED"},{"id":7202,"title":"Confirms","status":"FAILED"},{"id":7203,"title":"Cancels","status":"NOTRUN"}],
"evidenceDir":"","fetchedAt":"2026-10-07T09:00:00Z"}`

// gitHead answers `git rev-parse --short HEAD` with sha, or fails when sha is "".
func gitHead(sha string) Deps {
	d := aliveDeps()
	d.CommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "git" || strings.Join(args, " ") != "rev-parse --short HEAD" {
			return nil, fmt.Errorf("unexpected command %s %v", name, args)
		}
		if sha == "" {
			return nil, errors.New("fatal: not a git repository")
		}
		return []byte(sha + "\n"), nil
	}
	return d
}

func TestTestinyResultRecordsOneCase(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "app-2")
	srv, capture := reviewServer(t, http.StatusOK, testinyRecordedJSON)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, gitHead("4f2c9e1"), "testiny", "result", "app-1", "TR-640", "TC-7202", "--status", "FAILED", "--comment", "ปุ่มยืนยันไม่แสดง")
	if err != nil {
		t.Fatalf("result: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/app-1/testiny/runs/TR-640/results" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	want := `{"results":[{"caseId":7202,"status":"FAILED","comment":"ปุ่มยืนยันไม่แสดง"}],"from":"app-2","sha":"4f2c9e1"}`
	if strings.TrimSpace(capture.body) != want {
		t.Fatalf("body = %s\nwant %s", capture.body, want)
	}
	wantOut := `recorded in TR-640 "[AO-TEST] results"
  FAILED  TC-7202 Confirms
TR-640 now has 3 cases: 1 passed, 1 failed, 1 not run
`
	if out != wantOut {
		t.Fatalf("output =\n%s\nwant\n%s", out, wantOut)
	}
}

func TestTestinyResultFromAFileOutsideAGitCheckout(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	srv, capture := reviewServer(t, http.StatusOK, testinyRecordedJSON)
	writeRunFileFor(t, cfg, srv)

	deps := gitHead("")
	deps.In = strings.NewReader(`[{"caseId":7201,"status":"PASSED"},{"caseId":7203,"status":"NOTRUN","comment":""}]`)
	out, errOut, err := executeCLI(t, deps, "testiny", "result", "app-1", "640", "--from-file", "-")
	if err != nil {
		t.Fatalf("result: %v\nstderr=%s", err, errOut)
	}
	want := `{"results":[{"caseId":7201,"status":"PASSED"},{"caseId":7203,"status":"NOTRUN"}]}`
	if strings.TrimSpace(capture.body) != want {
		t.Fatalf("body = %s\nwant %s (no from from a person's shell, no sha outside a checkout)", capture.body, want)
	}
	if !strings.Contains(out, "  PASSED  TC-7201 Opens\n  NOTRUN  TC-7203 Cancels\n") {
		t.Fatalf("output = %s", out)
	}
}

func TestTestinyResultUsage(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{
		{"testiny", "result", "app-1", "640"},
		{"testiny", "result", "app-1", "640", "7201"},
		{"testiny", "result", "app-1", "640", "abc", "--status", "PASSED"},
		{"testiny", "result", "app-1", "640", "7201", "--status", "PASSED", "--from-file", "-"},
		{"testiny", "result", "app-1", "640", "--from-file", "-", "--comment", "x"},
	} {
		if _, _, err := executeCLI(t, gitHead(""), args...); ExitCode(err) != 2 {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
	deps := gitHead("")
	deps.In = strings.NewReader(`{"caseId":7201}`)
	if _, _, err := executeCLI(t, deps, "testiny", "result", "app-1", "640", "--from-file", "-"); ExitCode(err) != 2 {
		t.Errorf("a file that is not a JSON array: err = %v, want a usage error", err)
	}
}

func TestTestinyResultErrorsExitByKind(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		exit   int
	}{
		{http.StatusBadRequest, "TESTINY_RESULT_INVALID", 2},
		{http.StatusConflict, "TESTINY_RESULT_SET_BY_PERSON", 2},
		{http.StatusForbidden, "TESTINY_WRITE_NOT_YOURS", 2},
		{http.StatusNotFound, "TESTINY_RUN_NOT_LINKED", 1},
		{http.StatusBadGateway, "TESTINY_UNAVAILABLE", 1},
	} {
		cfg := setConfigEnv(t)
		srv, _ := reviewServer(t, tc.status, `{"code":"`+tc.code+`","message":"the daemon's words"}`)
		writeRunFileFor(t, cfg, srv)
		_, _, err := executeCLI(t, gitHead(""), "testiny", "result", "app-1", "640", "7201", "--status", "PASSED")
		if err == nil || ExitCode(err) != tc.exit || !strings.Contains(err.Error(), "the daemon's words") {
			t.Errorf("%s: err = %v (exit %d), want exit %d with the daemon's message", tc.code, err, ExitCode(err), tc.exit)
		}
	}
}
