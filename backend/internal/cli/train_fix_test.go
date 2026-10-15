package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fixRoute struct {
	status int
	body   string
}

func runTrainFix(t *testing.T, routes map[string]fixRoute, args ...string) (string, []trainCall, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "s-9")
	var calls []trainCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.HasPrefix(r.URL.Path, "/internal/") {
			calls = append(calls, trainCall{r.Method, r.URL.EscapedPath(), string(body)})
		}
		route, ok := routes[r.Method+" "+r.URL.EscapedPath()]
		if !ok {
			route = fixRoute{http.StatusNotFound, `{"code":"NOT_FOUND"}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(route.status)
		_, _ = io.WriteString(w, route.body)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	deps := aliveDeps()
	deps.Now = func() time.Time { return time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC) }
	out, _, err := executeCLI(t, deps, append([]string{"train"}, args...)...)
	return out, calls, err
}

const e1PlanJSON = `{"plan":{"hit":"E1~v/20-2026/di","rule":"E1","title":"Mark iOS 3.4.0 released","mode":"queue","fingerprint":"fp-1",
 "steps":[{"label":"Jira release iOS 3.4.0","before":"unreleased, release date -","after":"released, release date 2026-10-05",
  "effect":{"kind":"jira_version_update","site":"https://jira.example.com","versionId":"5001","before":{"released":false},"after":{"released":true,"releaseDate":"2026-10-05"}},
  "request":{"method":"PUT","url":"https://jira.example.com/rest/api/3/version/5001","body":{"released":true,"releaseDate":"2026-10-05"}}}]}}`

const e1FixJSON = `{"fix":{"id":"fx-1","hit":"E1~v/20-2026/di","rule":"E1","state":"dry_run","createdAt":"2026-10-15T03:00:00Z","updatedAt":"2026-10-15T03:00:00Z",
 "plan":{"hit":"E1~v/20-2026/di","rule":"E1","title":"Mark iOS 3.4.0 released","mode":"queue","fingerprint":"fp-1","steps":[]},
 "events":[{"seq":1,"at":"2026-10-15T03:00:00Z","actor":"cli:s-9","kind":"dry_run_recorded","detail":{}}]}}`

func TestTrainFixShowsThePlanAndWritesNothing(t *testing.T) {
	out, calls, err := runTrainFix(t, map[string]fixRoute{"POST /api/v1/release-trains/fixes/plan": {200, e1PlanJSON}}, "fix", "E1~v/20-2026/di")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].rawPath != "/api/v1/release-trains/fixes/plan" || strings.TrimSpace(calls[0].body) != `{"hitId":"E1~v/20-2026/di"}` {
		t.Fatalf("calls = %+v; want only the plan request", calls)
	}
	for _, want := range []string{
		"E1  Mark iOS 3.4.0 released  [queue]",
		"unreleased, release date - -> released, release date 2026-10-05",
		"PUT https://jira.example.com/rest/api/3/version/5001",
		`{"released":true,"releaseDate":"2026-10-05"}`,
		"dry run: nothing is sent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestTrainFixConfirmRecordsWithTheReviewedFingerprint(t *testing.T) {
	out, calls, err := runTrainFix(t, map[string]fixRoute{
		"POST /api/v1/release-trains/fixes/plan": {200, e1PlanJSON},
		"POST /api/v1/release-trains/fixes":      {200, e1FixJSON},
	}, "fix", "E1~v/20-2026/di", "--confirm")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.Contains(calls[1].body, `"planFingerprint":"fp-1"`) || !strings.Contains(calls[1].body, `"via":"cli","session":"s-9"`) {
		t.Fatalf("confirm call = %+v", calls)
	}
	if !strings.Contains(out, "fx-1 recorded (dry run, nothing sent): Mark iOS 3.4.0 released") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestTrainFixConfirmOn409SaysToReview(t *testing.T) {
	_, _, err := runTrainFix(t, map[string]fixRoute{
		"POST /api/v1/release-trains/fixes/plan": {200, e1PlanJSON},
		"POST /api/v1/release-trains/fixes":      {409, `{"code":"FIX_PLAN_CHANGED","message":"the data behind this fix changed"}`},
	}, "fix", "E1~v/20-2026/di", "--confirm")
	if err == nil || !strings.Contains(err.Error(), "run the command again") {
		t.Fatalf("err = %v", err)
	}
}

func TestTrainFixConfirmOfAChooseNeedsAChoice(t *testing.T) {
	choose := `{"plan":{"hit":"A5~v/21-2026/di","rule":"A5","title":"Make the dates match","mode":"choose","fingerprint":"fp-2","steps":[],
	 "choices":[{"id":"jira","label":"Jira ← milestone","steps":[]},{"id":"gitlab","label":"Milestone ← Jira","steps":[]}]}}`
	out, calls, err := runTrainFix(t, map[string]fixRoute{"POST /api/v1/release-trains/fixes/plan": {200, choose}}, "fix", "A5~v/21-2026/di", "--confirm")
	if err == nil || !strings.Contains(err.Error(), "--choice jira or gitlab") || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
	if !strings.Contains(out, "--choice gitlab  Milestone ← Jira") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestTrainFixesAndUndo(t *testing.T) {
	list := `{"fixes":[` + strings.TrimSuffix(strings.TrimPrefix(e1FixJSON, `{"fix":`), "}") + `]}`
	out, _, err := runTrainFix(t, map[string]fixRoute{"GET /api/v1/release-trains/fixes": {200, list}}, "fixes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fx-1  E1    dry_run  cli:s-9  2026-10-15 03:00  Mark iOS 3.4.0 released") {
		t.Fatalf("fixes:\n%s", out)
	}
	withdrawn := strings.Replace(e1FixJSON, `"state":"dry_run"`, `"state":"withdrawn"`, 1)
	out, calls, err := runTrainFix(t, map[string]fixRoute{"POST /api/v1/release-trains/fixes/fx-1/undo": {200, withdrawn}}, "fix", "undo", "fx-1")
	if err != nil || calls[0].method != http.MethodPost || !strings.Contains(out, "fx-1 withdrawn") {
		t.Fatalf("undo = %q %+v %v", out, calls, err)
	}
}
