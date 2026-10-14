package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type trainCall struct {
	method, rawPath, body string
}

func runTrainHuman(t *testing.T, status int, resp string, args ...string) (string, trainCall, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	var call trainCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		call = trainCall{r.Method, r.URL.EscapedPath(), string(body)}
		if r.URL.RawQuery != "" {
			call.rawPath += "?" + r.URL.RawQuery
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	deps := aliveDeps()
	deps.Now = func() time.Time { return time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC) }
	out, _, err := executeCLI(t, deps, append([]string{"train"}, args...)...)
	return out, call, err
}

const trainCatalogueJSON = `{"config":{"state":"ok","path":"/data/release-train.json"},"board":{"today":"2026-10-15","apps":[],"sources":[],"trains":[],
 "alerts":{"hits":[],"rules":[],"staleHides":[]},
 "catalogue":[
  {"id":"A7","group":"A","severity":"data","defaultOn":true,"on":true,"title":"An app's milestone dates differ from the rest of its train"},
  {"id":"C2","group":"C","severity":"info","defaultOn":false,"on":true,"title":"Allowed job failures on the release MR"},
  {"id":"C3","group":"C","severity":"info","defaultOn":false,"on":false,"title":"Release pipeline took over an hour"},
  {"id":"E1","group":"E","severity":"hygiene","defaultOn":true,"on":false,"title":"Published, Jira release not released"}
 ]}}`

func TestTrainRulesPrintsTheCatalogueAndMarksOverrides(t *testing.T) {
	out, call, err := runTrainHuman(t, http.StatusOK, trainCatalogueJSON, "rules")
	if err != nil {
		t.Fatal(err)
	}
	if call.method != http.MethodGet || call.rawPath != "/api/v1/release-trains/board" {
		t.Fatalf("request = %+v", call)
	}
	want := `RULE  GROUP  SEV      ON    TITLE
A7    A      data     on    An app's milestone dates differ from the rest of its train
C2    C      info     on*   Allowed job failures on the release MR
C3    C      info     off   Release pipeline took over an hour
E1    E      hygiene  off*  Published, Jira release not released
`
	if out != want {
		t.Fatalf("rules =\n%s\nwant\n%s", out, want)
	}
}

func TestTrainRulesEnableDisableReset(t *testing.T) {
	resp := `{"rule":{"id":"C2","group":"C","severity":"info","defaultOn":false,"on":true,"title":"Allowed job failures on the release MR"}}`
	for _, tc := range []struct {
		verb, body string
	}{
		{"enable", `{"enabled":true}`},
		{"disable", `{"enabled":false}`},
		{"reset", `{"enabled":null}`},
	} {
		out, call, err := runTrainHuman(t, http.StatusOK, resp, "rules", tc.verb, "c2")
		if err != nil {
			t.Fatalf("%s: %v", tc.verb, err)
		}
		if call.method != http.MethodPut || call.rawPath != "/api/v1/release-trains/rules/C2" || call.body != tc.body {
			t.Fatalf("%s request = %+v, want body %s", tc.verb, call, tc.body)
		}
		if !strings.Contains(out, "C2    C      info  on*") {
			t.Fatalf("%s output =\n%s", tc.verb, out)
		}
	}
	_, _, err := runTrainHuman(t, http.StatusNotFound, `{"code":"RELEASE_RULE_NOT_FOUND","message":"release trains: unknown rule: \"Z9\""}`, "rules", "enable", "Z9")
	if ExitCode(err) != 2 || !strings.Contains(err.Error(), "unknown rule") {
		t.Fatalf("unknown rule = %v (exit %d)", err, ExitCode(err))
	}
}

func TestTrainHideSendsTheEncodedHitAndReason(t *testing.T) {
	resp := `{"hitId":"B1~v/21-2026/da","severity":"risk","reason":"test","hiddenAt":"2026-10-15T03:00:00Z"}`
	out, call, err := runTrainHuman(t, http.StatusOK, resp, "hide", "B1~v/21-2026/da", "--reason", "test")
	if err != nil {
		t.Fatal(err)
	}
	if call.method != http.MethodPut || call.rawPath != "/api/v1/release-trains/hides/B1~v%2F21-2026%2Fda" || call.body != `{"reason":"test"}` {
		t.Fatalf("request = %+v", call)
	}
	if out != "hid B1~v/21-2026/da at risk\n" {
		t.Fatalf("output = %q", out)
	}
	_, _, err = runTrainHuman(t, http.StatusNotFound, `{"code":"RELEASE_HIT_NOT_FOUND","message":"no such hit on the board"}`, "hide", "B1~v/99-2026/da")
	if ExitCode(err) != 2 {
		t.Fatalf("hide of an unknown hit = %v (exit %d), want a usage error", err, ExitCode(err))
	}
}

func TestTrainUnhideOneOrStale(t *testing.T) {
	out, call, err := runTrainHuman(t, http.StatusNoContent, ``, "unhide", "B1~v/21-2026/da")
	if err != nil {
		t.Fatal(err)
	}
	if call.method != http.MethodDelete || call.rawPath != "/api/v1/release-trains/hides/B1~v%2F21-2026%2Fda" || out != "unhid B1~v/21-2026/da\n" {
		t.Fatalf("unhide = %+v %q", call, out)
	}

	out, call, err = runTrainHuman(t, http.StatusOK, `{"removed":["C5~v/20-2026/di","E1~v/20-2026/da"]}`, "unhide", "--stale")
	if err != nil {
		t.Fatal(err)
	}
	if call.method != http.MethodDelete || call.rawPath != "/api/v1/release-trains/hides?stale=true" || out != "unhid C5~v/20-2026/di\nunhid E1~v/20-2026/da\n" {
		t.Fatalf("unhide --stale = %+v %q", call, out)
	}
	if out, _, _ := runTrainHuman(t, http.StatusOK, `{"removed":[]}`, "unhide", "--stale"); out != "no stale hides\n" {
		t.Fatalf("unhide --stale with none = %q", out)
	}

	for _, args := range [][]string{{"unhide"}, {"unhide", "--stale", "B1~v/21-2026/da"}} {
		if _, _, err := runTrainHuman(t, http.StatusOK, `{}`, args...); ExitCode(err) != 2 {
			t.Fatalf("%v = %v, want a usage error", args, err)
		}
	}
}

const trainPlanJSON = `{"id":"demo-llb","name":"demo-llb","targetKind":"train","targetTrain":"22-2026","note":"lands with 22",
 "branches":[{"app":"da","branch":"feature/demo-llb"},{"app":"di","branch":"feature/demo-llb"}],"accepted":[],
 "version":2,"updatedBy":"cli","updatedAt":"2026-10-15T03:00:00Z"}`

func TestTrainPlanSetPutsTheWholePlan(t *testing.T) {
	out, call, err := runTrainHuman(t, http.StatusOK, `{"plan":`+trainPlanJSON+`}`, "plan", "set", "demo-llb",
		"--target", "22", "--note", "lands with 22", "--branch", "da:feature/demo-llb", "--branch", "di:feature/demo-llb", "--if-version", "1")
	if err != nil {
		t.Fatal(err)
	}
	if call.method != http.MethodPut || call.rawPath != "/api/v1/release-trains/llb-plans/demo-llb" {
		t.Fatalf("request = %+v", call)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(call.body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["via"] != "cli" || sent["targetKind"] != "train" || sent["targetTrain"] != "22-2026" || sent["ifVersion"] != float64(1) ||
		sent["name"] != "demo-llb" || len(sent["branches"].([]any)) != 2 {
		t.Fatalf("body = %s", call.body)
	}
	want := `PLAN      VERSION  TARGET  BRANCHES                                  UPDATED BY  NOTE
demo-llb  2        #22     da:feature/demo-llb, di:feature/demo-llb  cli         lands with 22
`
	if out != want {
		t.Fatalf("output =\n%s\nwant\n%s", out, want)
	}

	_, call, err = runTrainHuman(t, http.StatusOK, `{"plan":`+trainPlanJSON+`}`, "plan", "set", "demo-llb", "--target", "dropped")
	if err != nil || !strings.Contains(call.body, `"targetKind":"dropped"`) || strings.Contains(call.body, "targetTrain") {
		t.Fatalf("--target dropped body = %s (%v)", call.body, err)
	}
}

func TestTrainPlanSetErrors(t *testing.T) {
	_, _, err := runTrainHuman(t, http.StatusConflict, `{"code":"PLAN_VERSION_CONFLICT","message":"plan demo-llb is at version 2; reload it and try again"}`,
		"plan", "set", "demo-llb", "--target", "unplanned", "--if-version", "1")
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("stale version = %v (exit %d)", err, ExitCode(err))
	}
	for _, args := range [][]string{
		{"plan", "set", "demo-llb"},
		{"plan", "set", "demo-llb", "--target", "next"},
		{"plan", "set", "demo-llb", "--target", "22", "--branch", "feature/no-app"},
	} {
		if _, _, err := runTrainHuman(t, http.StatusOK, `{}`, args...); ExitCode(err) != 2 {
			t.Fatalf("%v = %v, want a usage error", args, err)
		}
	}
}

func TestTrainPlanLs(t *testing.T) {
	out, call, err := runTrainHuman(t, http.StatusOK, `{"plans":[`+trainPlanJSON+`]}`, "plan", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if call.method != http.MethodGet || call.rawPath != "/api/v1/release-trains/llb-plans" || !strings.HasPrefix(out, "PLAN") || !strings.Contains(out, "demo-llb  2") {
		t.Fatalf("plan ls = %+v\n%s", call, out)
	}
	if out, _, _ := runTrainHuman(t, http.StatusOK, `{"plans":[]}`, "plan", "ls"); out != "no long-lived branch plans\n" {
		t.Fatalf("plan ls with none = %q", out)
	}
}

func TestTrainAlertsHiddenShowsChangedAndStaleHides(t *testing.T) {
	body := strings.Replace(trainBoardJSON,
		`"text": "1 card(s) not Done", "bucket": "hidden"}`,
		`"text": "1 card(s) not Done", "bucket": "hidden", "changedSinceHidden": true}`, 1)
	body = strings.Replace(body, `"rules": [`, `"staleHides": [{"id": "D3~v/20-2026/di", "reason": "waiting on approver", "hiddenAt": "2026-10-10T03:00:00Z"}], "rules": [`, 1)
	out, err := runTrain(t, body, "alerts", "--hidden")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "E2~v/20-2026/di  hidden, changed since hidden") {
		t.Fatalf("alerts --hidden lost changed-since-hidden:\n%s", out)
	}
	if !strings.HasSuffix(out, "\nNo longer matching (clear with `ao train unhide --stale`)\n  D3~v/20-2026/di  waiting on approver\n") {
		t.Fatalf("alerts --hidden lost the stale hides:\n%s", out)
	}
	if out, _ := runTrain(t, body, "alerts"); strings.Contains(out, "No longer matching") {
		t.Fatalf("plain alerts shows stale hides:\n%s", out)
	}
}
