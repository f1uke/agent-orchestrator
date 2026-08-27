package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

// `add` must be a PATCH on the collection, not a PUT: a PUT would replace the
// whole list, which is the exact loss the per-case verbs exist to prevent, and
// the mistake would be invisible from the command's own output.
func TestSmokeAddSendsAPatchWithOnlyItsCases(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"w","checks":[{"id":"a","seq":1,"name":"A","verdict":"pending"},{"id":"b","seq":2,"name":"B","verdict":"pending"}]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"cases":[{"id":"b","name":"B","steps":["s1"]}]}`)
	t.Setenv("AO_SESSION_ID", "mer-2")

	out, errOut, err := executeCLI(t, deps, "smoke", "add", "mer-1", "--from-file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != "PATCH" || capture.path != "/api/v1/sessions/mer-1/smoke-checks" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var req addSmokeCasesRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.Cases) != 1 || req.Cases[0].ID != "b" {
		t.Fatalf("cases = %+v", req.Cases)
	}
	// The write is attributed to the CALLER, not to the target: both crew members
	// write to the same target, so the target cannot say which of them it was.
	if req.From != "mer-2" {
		t.Fatalf("from = %q, want the caller's own session id", req.From)
	}
	if !strings.Contains(out, "now has 2") {
		t.Fatalf("output = %q", out)
	}
}

// A flag the caller did not pass must be ABSENT from the JSON, not present as a
// zero value. This is the whole contract of a partial edit: an empty string on
// the wire would blank the stored field, which is the wide write again wearing a
// narrow command's name.
func TestSmokeEditOmitsFlagsItWasNotGiven(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"check":{"id":"c1","seq":1,"name":"A","verdict":"pass"}}`)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("AO_SESSION_ID", "mer-1")

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "edit", "mer-1", "--case", "c1", "--pr", "264")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != "PATCH" || capture.path != "/api/v1/sessions/mer-1/smoke-checks/c1" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(capture.body), &raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if raw["prNum"] != float64(264) {
		t.Fatalf("prNum = %v", raw["prNum"])
	}
	for _, absent := range []string{"name", "why", "steps", "expected", "fileRef"} {
		if _, ok := raw[absent]; ok {
			t.Fatalf("an unspecified field was sent and would blank the stored value: %s in %s", absent, capture.body)
		}
	}
	// The output says the user's verdict is untouched, because that is the thing
	// a caller editing a played case most needs to know did not happen.
	if !strings.Contains(out, "verdict is unchanged (PASS)") {
		t.Fatalf("output = %q", out)
	}
}

// An explicit empty value is a real edit ("clear the fileRef"), so it must
// travel where an omitted flag does not.
func TestSmokeEditSendsAnExplicitEmptyValue(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"check":{"id":"c1","seq":1,"name":"A","verdict":"pending"}}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, aliveDeps(), "smoke", "edit", "mer-1", "--case", "c1", "--file-ref", ""); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(capture.body), &raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got, ok := raw["fileRef"]; !ok || got != "" {
		t.Fatalf("an explicitly cleared field did not travel: %s", capture.body)
	}
}

func TestSmokeEditRequiresACaseAndAField(t *testing.T) {
	setConfigEnv(t)
	if _, _, err := executeCLI(t, aliveDeps(), "smoke", "edit", "mer-1", "--pr", "1"); err == nil || !strings.Contains(err.Error(), "--case") {
		t.Fatalf("err = %v, want case-required", err)
	}
	if _, _, err := executeCLI(t, aliveDeps(), "smoke", "edit", "mer-1", "--case", "c1"); err == nil || !strings.Contains(err.Error(), "name at least one field") {
		t.Fatalf("err = %v, want a field-required usage error", err)
	}
}

func TestSmokeRemoveSendsADelete(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"w","checks":[]}`)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("AO_SESSION_ID", "mer-1")

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "remove", "mer-1", "--case", "c1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != "DELETE" || capture.path != "/api/v1/sessions/mer-1/smoke-checks/c1" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	if !strings.Contains(out, "now has 0 case(s)") {
		t.Fatalf("output = %q", out)
	}
}

// Removing a played case is refused by the daemon, and the refusal is the one
// thing that gets the caller unstuck - so it must arrive as written, at exit 2.
func TestSmokeRemoveSurfacesThePlayedCaseRefusal(t *testing.T) {
	cfg := setConfigEnv(t)
	const msg = `smoke: author would discard recorded results: the user already played "Drag scrolls" (id "drag-scroll", verdict fail), so it is retired rather than deleted: ` + "`ao smoke retire <session> --case drag-scroll --reason \"…\"`"
	body, err := json.Marshal(map[string]string{"message": msg, "code": "SMOKE_RESULTS_AT_RISK"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	srv, _ := reviewServer(t, 422, string(body))
	writeRunFileFor(t, cfg, srv)

	_, _, err = executeCLI(t, aliveDeps(), "smoke", "remove", "mer-1", "--case", "drag-scroll")
	if err == nil {
		t.Fatal("err = nil, want the refusal")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
	if !strings.Contains(err.Error(), "ao smoke retire") {
		t.Errorf("the refusal does not point at retire: %v", err)
	}
}

func TestSmokeStandDownPostsTheReason(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"w","checks":[]}`)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("AO_SESSION_ID", "mer-2")

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "stand-down", "mer-1", "--reason", "pure refactor; covered by TestFoo")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != "POST" || capture.path != "/api/v1/sessions/mer-1/smoke-checks/stand-down" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var req standDownSmokeRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if req.Reason != "pure refactor; covered by TestFoo" || req.From != "mer-2" {
		t.Fatalf("body = %+v", req)
	}
	if !strings.Contains(out, "stood down on mer-1") {
		t.Fatalf("output = %q", out)
	}
}

func TestSmokeStandDownRequiresAReason(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, aliveDeps(), "smoke", "stand-down", "mer-1")
	if err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("err = %v, want a reason-required usage error", err)
	}
}

// THE TWO STATES, on the command that a worker reads. An empty checklist and a
// stood-down one used to print the same line, which is the same ambiguity the
// Tests tab had.
func TestSmokeListTellsEmptyFromStoodDown(t *testing.T) {
	cfg := setConfigEnv(t)

	srv, _ := reviewServer(t, 200, `{"worker":"w","checks":[]}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("list an empty checklist: %v", err)
	}
	if !strings.Contains(out, "nobody has decided") {
		t.Fatalf("an empty checklist must say nobody has decided yet:\n%s", out)
	}

	at := time.Unix(1_700_000_000, 0).UTC()
	body, err := json.Marshal(controllers.ListSmokeChecksResponse{
		Worker: "w", Checks: []domain.SmokeCheck{},
		StandDown: &domain.SmokeStandDown{SessionID: "w1", At: at, By: "mer-2", ByRole: domain.CrewRoleQA, Reason: "pure refactor"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv2, _ := reviewServer(t, 200, string(body))
	writeRunFileFor(t, cfg, srv2)
	out, _, err = executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("list a stood-down checklist: %v", err)
	}
	for _, want := range []string{"qa stood down", "pure refactor"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// A case's author line is part of reading a SHARED list: the human asked to see
// which cases came from dev and which from qa.
func TestSmokeListNamesEachCasesAuthor(t *testing.T) {
	cfg := setConfigEnv(t)
	at := time.Unix(1_700_000_000, 0).UTC()
	body, err := json.Marshal(controllers.ListSmokeChecksResponse{Worker: "w", Checks: []domain.SmokeCheck{
		{ID: "c1", Seq: 1, Name: "From dev", Verdict: domain.SmokePending, AuthoredBy: "mer-1", AuthoredByRole: domain.CrewRoleDev, AuthoredAt: &at},
		{ID: "c2", Seq: 2, Name: "From nobody AO knows", Verdict: domain.SmokePending},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv, _ := reviewServer(t, 200, string(body))
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "by: dev @mer-1") {
		t.Fatalf("an attributed case does not name its author:\n%s", out)
	}
	// An unattributable case prints no author rather than a guessed one.
	tail := out[strings.Index(out, "From nobody AO knows"):]
	if strings.Contains(tail, "by:") {
		t.Fatalf("an unattributed case was given an author:\n%s", out)
	}
}
