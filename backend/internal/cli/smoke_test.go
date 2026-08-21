package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

func TestSmokeSetReadsStdinCases(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"fix gl note","checks":[{"id":"a","seq":1,"name":"A","verdict":"pending"}]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"cases":[{"name":"A fresh MR shows up","why":"w","steps":["s1","s2"],"expected":"e","prNum":36,"fileRef":"f.go:1"}]}`)

	out, errOut, err := executeCLI(t, deps, "smoke", "set", "w1", "--from-file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != "PUT" || capture.path != "/api/v1/sessions/w1/smoke-checks" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var req authorSmokeChecksRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.Cases) != 1 || req.Cases[0].Name != "A fresh MR shows up" || req.Cases[0].PRNum != 36 || req.Cases[0].FileRef != "f.go:1" {
		t.Fatalf("cases = %+v", req.Cases)
	}
	if len(req.Cases[0].Steps) != 2 {
		t.Fatalf("steps = %+v", req.Cases[0].Steps)
	}
	if !strings.Contains(out, "authored 1 smoke check(s) for w1") {
		t.Fatalf("output = %q", out)
	}
}

func TestSmokeSetAcceptsBareArray(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"w","checks":[{"id":"a","seq":1,"name":"A","verdict":"pending"}]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`[{"name":"Only case","expected":"ok"}]`)

	if _, errOut, err := executeCLI(t, deps, "smoke", "set", "w1", "--from-file", "-"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req authorSmokeChecksRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.Cases) != 1 || req.Cases[0].Name != "Only case" {
		t.Fatalf("cases = %+v", req.Cases)
	}
}

func TestSmokeSetUnderscoreFlagNormalizes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"w","checks":[]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"cases":[{"name":"A"}]}`)
	// Agents often type --from_file with an underscore.
	if _, errOut, err := executeCLI(t, deps, "smoke", "set", "w1", "--from_file", "-"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/w1/smoke-checks" {
		t.Fatalf("path = %s", capture.path)
	}
}

func TestSmokeSetRequiresSessionAndFile(t *testing.T) {
	setConfigEnv(t)
	if _, _, err := executeCLI(t, aliveDeps(), "smoke", "set", "--from-file", "-"); err == nil || !strings.Contains(err.Error(), "session id is required") {
		t.Fatalf("err = %v, want session-required", err)
	}
	if _, _, err := executeCLI(t, aliveDeps(), "smoke", "set", "w1"); err == nil || !strings.Contains(err.Error(), "--from-file") {
		t.Fatalf("err = %v, want from-file-required", err)
	}
}

func TestSmokeListPrintsChecklist(t *testing.T) {
	cfg := setConfigEnv(t)
	resp := `{"worker":"fix gl note","checks":[{"id":"a","seq":1,"name":"A fresh MR shows up","verdict":"pass","note":"looked good","prNum":36,"fileRef":"f.go:1","evidence":[{"id":"ev1","kind":"image"}]}]}`
	srv, capture := reviewServer(t, 200, resp)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != "GET" || capture.path != "/api/v1/sessions/w1/smoke-checks" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	for _, want := range []string{"CHECK 1 [PASS] A fresh MR shows up", "PR #36 · f.go:1", "note: looked good", "evidence: 1 attached"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSmokeListJSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, 200, `{"worker":"w","checks":[{"id":"a","seq":1,"name":"A","verdict":"pending"}]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `"worker": "w"`) || !strings.Contains(out, `"name": "A"`) {
		t.Fatalf("json output = %q", out)
	}
}

// daemonSmokeBody renders the exact bytes the daemon puts on the wire for
// GET .../smoke-checks. Building it from the real response DTO (rather than a
// hand-written string) means a json tag renamed on either side of the wire
// fails these tests instead of silently dropping the field — which is how
// why/steps/expected went missing from `ao smoke list` in the first place.
func daemonSmokeBody(t *testing.T, worker string, checks ...domain.SmokeCheck) string {
	t.Helper()
	raw, err := json.Marshal(controllers.ListSmokeChecksResponse{Worker: worker, Checks: checks})
	if err != nil {
		t.Fatalf("marshal daemon body: %v", err)
	}
	return string(raw)
}

// playableCase is a fictional case carrying every author-supplied field.
func playableCase() domain.SmokeCheck {
	return domain.SmokeCheck{
		ID:       "widget-label-survives-sort",
		Seq:      1,
		Name:     "Sorting a widget keeps its label",
		Why:      "The relabel path is the one this change rewrote.",
		Steps:    []string{"Open the Widgets tab.", "Drag widget B above widget A.", "Reload the page."},
		Expected: "Widget B keeps its label and stays above widget A.",
		PRNum:    7,
		FileRef:  "widget.go:42",
		Verdict:  domain.SmokePending,
	}
}

// TestSmokeListPrintsPlayableCase is the core regression test: a worker must be
// able to play the checklist straight from the command's own output.
func TestSmokeListPrintsPlayableCase(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, 200, daemonSmokeBody(t, "widget sorter", playableCase()))
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{
		"why: The relabel path is the one this change rewrote.",
		"steps:",
		"expected: Widget B keeps its label and stays above widget A.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// Steps must be numbered and keep the authored order.
	prev := -1
	for i, want := range []string{
		"1. Open the Widgets tab.",
		"2. Drag widget B above widget A.",
		"3. Reload the page.",
	} {
		at := strings.Index(out, want)
		if at < 0 {
			t.Fatalf("output missing step %d %q:\n%s", i+1, want, out)
		}
		if at <= prev {
			t.Fatalf("step %d %q is out of order:\n%s", i+1, want, out)
		}
		prev = at
	}
}

// TestSmokeListSkipsEmptyAuthoredFields guards the older checklists that carry
// none of the three fields: no empty scaffolding, no crash.
func TestSmokeListSkipsEmptyAuthoredFields(t *testing.T) {
	cfg := setConfigEnv(t)
	bare := domain.SmokeCheck{ID: "bare", Seq: 1, Name: "A bare case", Verdict: domain.SmokePass}
	srv, _ := reviewServer(t, 200, daemonSmokeBody(t, "widget sorter", bare))
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "CHECK 1 [PASS] A bare case") {
		t.Fatalf("output = %q", out)
	}
	for _, unwanted := range []string{"why:", "steps:", "expected:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("output has empty scaffolding %q:\n%s", unwanted, out)
		}
	}
}

// TestSmokeListBriefCondenses covers the opt-in one-line-per-case form for
// scanning verdicts across a long checklist.
func TestSmokeListBriefCondenses(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, 200, daemonSmokeBody(t, "widget sorter", playableCase()))
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1", "--brief")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "CHECK 1 [to check] Sorting a widget keeps its label") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, "PR #7 · widget.go:42") {
		t.Fatalf("brief output should keep the ref line:\n%s", out)
	}
	for _, unwanted := range []string{"why:", "steps:", "expected:", "Reload the page."} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("--brief should not print %q:\n%s", unwanted, out)
		}
	}
}

// TestSmokeListJSONKeepsAuthoredFields pins the --json path: it re-encodes the
// CLI's own struct, so a field the struct lacks is dropped from the output
// even though the daemon sent it.
func TestSmokeListJSONKeepsAuthoredFields(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, 200, daemonSmokeBody(t, "widget sorter", playableCase()))
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	// Decode the printed JSON generically: a field the CLI struct lacks is
	// simply absent from the output, which a typed decode would hide.
	var got struct {
		Checks []map[string]any `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\n%s", err, out)
	}
	if len(got.Checks) != 1 {
		t.Fatalf("checks = %+v", got.Checks)
	}
	check := got.Checks[0]
	want := playableCase()
	if check["why"] != want.Why {
		t.Errorf("why = %v, want %q", check["why"], want.Why)
	}
	if check["expected"] != want.Expected {
		t.Errorf("expected = %v, want %q", check["expected"], want.Expected)
	}
	steps, _ := check["steps"].([]any)
	if len(steps) != len(want.Steps) {
		t.Fatalf("steps = %v, want %v", check["steps"], want.Steps)
	}
	for i, step := range want.Steps {
		if steps[i] != step {
			t.Errorf("steps[%d] = %v, want %q", i, steps[i], step)
		}
	}
}

// TestSmokeSetSurfacesResultsAtRiskAsUsageError: the daemon refuses a payload
// that would delete results the user recorded. That is the caller's payload to
// fix, so it has to exit 2 with the daemon's message (which names the cases and
// their ids) intact — not a bare "daemon returned HTTP 422".
func TestSmokeSetSurfacesResultsAtRiskAsUsageError(t *testing.T) {
	cfg := setConfigEnv(t)
	const msg = `smoke: author would discard recorded results: 1 case the user already played is missing from the payload: "A fresh MR shows up" (id "a-fresh-mr-shows-up", verdict pass, 1 evidence file). A case id is derived from its name, so rewording a name drops the old case: keep each id in the payload (add "id": "a-fresh-mr-shows-up" to the case that replaces it), or ask the user to Reset the case in the Tests tab before dropping it`
	body, err := json.Marshal(map[string]string{"message": msg, "code": "SMOKE_RESULTS_AT_RISK"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	srv, _ := reviewServer(t, 422, string(body))
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"cases":[{"name":"A fresh MR shows up in the Reviews tab"}]}`)

	_, _, err = executeCLI(t, deps, "smoke", "set", "w1", "--from-file", "-")
	if err == nil {
		t.Fatal("err = nil, want the refusal")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
	for _, want := range []string{`"A fresh MR shows up"`, `"a-fresh-mr-shows-up"`, "Reset the case"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %s: %v", want, err)
		}
	}
}

// The daemon cannot tell a crew's dev from its qa by the PATH - both author
// against the crew id, which is dev's session id - so `ao` names the sender the
// same way `ao send` does. Without this field the refusal can never fire.
func TestSmokeSetSendsTheCallersOwnSessionID(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"w","checks":[]}`)
	writeRunFileFor(t, cfg, srv)
	t.Setenv("AO_SESSION_ID", "mer-2")

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"cases":[{"name":"A"}]}`)
	if _, errOut, err := executeCLI(t, deps, "smoke", "set", "mer-1", "--from-file", "-"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req authorSmokeChecksRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if req.From != "mer-2" {
		t.Fatalf("from = %q, want the caller's own session id", req.From)
	}
}

// A crew dev refused by the daemon has nothing to fix in its payload - it sent
// the wrong command entirely - so the refusal must arrive as the daemon wrote
// it (naming the qa and how to hand over), at exit 2, not as "HTTP 409".
func TestSmokeSetSurfacesTheQAOwnsChecklistRefusal(t *testing.T) {
	cfg := setConfigEnv(t)
	const msg = "smoke: qa owns this task's checklist: qa @mer-2 owns this task's checklist - it authors the cases, runs them and retires them. If a brief asked you for smoke cases, that brief predates the crew: say so, and hand it over with `ao send --crew qa --about <sha>`"
	body, err := json.Marshal(map[string]string{"message": msg, "code": "SMOKE_QA_OWNS_CHECKLIST"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	srv, _ := reviewServer(t, 409, string(body))
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"cases":[{"name":"A"}]}`)

	_, _, err = executeCLI(t, deps, "smoke", "set", "mer-1", "--from-file", "-")
	if err == nil {
		t.Fatal("err = nil, want the refusal")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
	for _, want := range []string{"@mer-2", "ao send --crew qa"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %s: %v", want, err)
		}
	}
}
