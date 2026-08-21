package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// crewRunServer records what the CLI asked for and replies with the supplied
// JSON body per path suffix.
func crewRunServer(t *testing.T, bodies map[string]string, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		for suffix, body := range bodies {
			if strings.HasSuffix(r.URL.Path, suffix) {
				_, _ = io.WriteString(w, body)
				return
			}
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withSession(t *testing.T, id string) {
	t.Helper()
	t.Setenv("AO_SESSION_ID", id)
}

// The bracket posts to the session's own crew/runs route and reports the open
// run back, so the member knows the detector is live before it starts building.
func TestCrewRunStart_OpensTheBracket(t *testing.T) {
	cfg := setConfigEnv(t)
	withSession(t, "demo-3")
	var seen []string
	srv := crewRunServer(t, map[string]string{
		"/crew/runs": `{"run":{"id":"run-1","kind":"test","detector":"live","genAtStart":7},"certified":true}`,
	}, &seen)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "run", "--start", "--kind", "test")
	if err != nil {
		t.Fatalf("ao crew run --start: %v stderr=%s", err, errOut)
	}
	want := "POST /api/v1/sessions/demo-3/crew/runs"
	if len(seen) != 1 || seen[0] != want {
		t.Fatalf("requests = %v, want exactly [%s]", seen, want)
	}
	if !strings.Contains(out, "run-1") {
		t.Fatalf("output does not name the open run: %q", out)
	}
}

// A start with no detector must SAY SO. Silence here would let a member build
// believing it is being watched, and record a result nothing can vouch for.
func TestCrewRunStart_WarnsWhenTheDetectorIsDown(t *testing.T) {
	cfg := setConfigEnv(t)
	withSession(t, "demo-3")
	var seen []string
	srv := crewRunServer(t, map[string]string{
		"/crew/runs": `{"run":{"id":"run-1","kind":"build","detector":"down","detectorReason":"too many directories"},"certified":false}`,
	}, &seen)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "run", "--start", "--kind", "build")
	if err != nil {
		t.Fatalf("ao crew run --start: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "UNCERTIFIED") || !strings.Contains(out, "too many directories") {
		t.Fatalf("a start with no detector did not say so: %q", out)
	}
}

// TestCrewRunStart_NamesACrewmatesOpenRun. Both members work in one worktree, so
// a build can now start while the other member is already running one. Nothing
// waits or refuses on that - what a member gets is the one piece of advice AO can
// honestly give, because two `xcodebuild` runs against ONE shared DerivedData is
// exactly the case nothing here verifies.
func TestCrewRunStart_NamesACrewmatesOpenRun(t *testing.T) {
	cfg := setConfigEnv(t)
	withSession(t, "demo-3")
	var seen []string
	srv := crewRunServer(t, map[string]string{
		"/crew/runs": `{"run":{"id":"run-2","kind":"build","detector":"live"},"certified":true,` +
			`"crewmateRun":{"id":"run-1","sessionId":"demo-1","role":"dev","kind":"build","label":"xcodebuild"}}`,
	}, &seen)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "run", "--start", "--kind", "build")
	if err != nil {
		t.Fatalf("ao crew run --start: %v stderr=%s", err, errOut)
	}
	// It says WHO and WHAT, and it does not pretend to be a refusal.
	for _, want := range []string{"dev", "build", "xcodebuild", "consider waiting", "run-2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the crewmate advisory is missing %q: %q", want, out)
		}
	}
}

// And a member whose crewmate is running NOTHING is told nothing: the advisory
// must not become noise on every start.
func TestCrewRunStart_SaysNothingWhenTheCrewmateIsQuiet(t *testing.T) {
	cfg := setConfigEnv(t)
	withSession(t, "demo-3")
	var seen []string
	srv := crewRunServer(t, map[string]string{
		"/crew/runs": `{"run":{"id":"run-2","kind":"build","detector":"live"},"certified":true}`,
	}, &seen)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "run", "--start", "--kind", "build")
	if err != nil {
		t.Fatalf("ao crew run --start: %v", err)
	}
	if strings.Contains(out, "NOTE:") {
		t.Fatalf("a quiet crewmate produced an advisory anyway: %q", out)
	}
}

// The end bracket's wording is the whole point of the third state: a discarded
// run must not read as the pass it reported.
func TestCrewRunEndLines(t *testing.T) {
	tests := []struct {
		name     string
		resp     crewRunEndResponse
		contains []string
		absent   []string
	}{
		{
			name: "trusted keeps the reported verdict",
			resp: crewRunEndResponse{
				Run: crewRunView{Outcome: "trusted", Result: "pass", EndedAt: strPtr("now")},
			},
			contains: []string{"TRUSTED", "PASS"},
		},
		{
			name: "discarded names what moved and asks for a retry",
			resp: crewRunEndResponse{
				Run:         crewRunView{Outcome: "discarded", Result: "pass", EndedAt: strPtr("now"), ChangedPaths: []string{"src/app.go"}},
				Retry:       true,
				Attempt:     1,
				MaxAttempts: 3,
			},
			contains: []string{"DISCARDED", "src/app.go", "attempt 1 of 3", "Run it again"},
			// The reported PASS must not survive into the message. That
			// substitution is exactly the laundering this mechanism prevents.
			absent: []string{"PASS", "TRUSTED"},
		},
		{
			name: "the third discard stops the retry and hands it to a human",
			resp: crewRunEndResponse{
				Run:         crewRunView{Outcome: "discarded", EndedAt: strPtr("now")},
				Escalated:   true,
				Attempt:     3,
				MaxAttempts: 3,
			},
			contains: []string{"DISCARDED", "NEEDS YOU", "Stop re-running"},
			absent:   []string{"Run it again"},
		},
		{
			name: "uncertified is not a pass",
			resp: crewRunEndResponse{
				Run: crewRunView{Outcome: "uncertified", Result: "pass", EndedAt: strPtr("now"), DetectorReason: "the daemon restarted"},
			},
			contains: []string{"UNCERTIFIED", "the daemon restarted"},
			absent:   []string{"TRUSTED", "PASS"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(crewRunEndLines(tt.resp), "\n")
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("output %q does not contain %q", got, want)
				}
			}
			for _, never := range tt.absent {
				if strings.Contains(got, never) {
					t.Fatalf("output %q must not contain %q", got, never)
				}
			}
		})
	}
}

// The end call reaches the /end route and carries the reported result.
func TestCrewRunEnd_PostsTheResult(t *testing.T) {
	cfg := setConfigEnv(t)
	withSession(t, "demo-3")
	var seen []string
	var body crewRunEndRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"run":{"id":"run-1","outcome":"trusted","result":"fail","endedAt":"now"},"attempt":1,"maxAttempts":3}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "run", "--end", "--result", "fail")
	if err != nil {
		t.Fatalf("ao crew run --end: %v stderr=%s", err, errOut)
	}
	want := "POST /api/v1/sessions/demo-3/crew/runs/end"
	if len(seen) != 1 || seen[0] != want {
		t.Fatalf("requests = %v, want exactly [%s]", seen, want)
	}
	if body.Result != "fail" {
		t.Fatalf("posted result = %q, want fail", body.Result)
	}
	if !strings.Contains(out, "TRUSTED") {
		t.Fatalf("output does not report the verdict: %q", out)
	}
}

// Outside a session there is no member to attribute a run to, and the command
// says so rather than guessing.
func TestCrewRun_RefusedOutsideASession(t *testing.T) {
	cfg := setConfigEnv(t)
	_ = os.Unsetenv("AO_SESSION_ID")
	var seen []string
	srv := crewRunServer(t, nil, &seen)
	writeRunFileFor(t, cfg, srv)

	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "run", "--start"); err == nil {
		t.Fatal("ao crew run outside a session succeeded")
	}
	if len(seen) != 0 {
		t.Fatalf("it still called the daemon: %v", seen)
	}
}

// Exactly one verb, and it is a usage error rather than a silent choice.
func TestCrewRun_RequiresExactlyOneVerb(t *testing.T) {
	cfg := setConfigEnv(t)
	withSession(t, "demo-3")
	var seen []string
	srv := crewRunServer(t, nil, &seen)
	writeRunFileFor(t, cfg, srv)

	for _, args := range [][]string{
		{"crew", "run"},
		{"crew", "run", "--start", "--end"},
	} {
		if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
}

// `ao crew status` says what a member is DOING, not merely that it is awake -
// and names a discard streak, which is the thing a human has to act on.
func TestCrewStatus_ReportsTheOpenRunAndTheDiscardStreak(t *testing.T) {
	cfg := setConfigEnv(t)
	body := `{"sessions":[
		{"id":"demo-2","isSuspended":true,"crew":{"id":"demo-2","role":"dev"}},
		{"id":"demo-3","isSuspended":false,"crew":{"id":"demo-2","role":"qa"},
		 "crewRun":{"kind":"test","label":"go test ./...","startedAt":"2026-08-21T10:00:00Z"},
		 "crewRunDiscards":2}
	]}`
	var seen []string
	srv := crewRunServer(t, map[string]string{"/sessions": body}, &seen)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "status")
	if err != nil {
		t.Fatalf("ao crew status: %v stderr=%s", err, errOut)
	}
	for _, want := range []string{"running a test", "go test ./...", "2 run(s) in a row discarded"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q does not contain %q", out, want)
		}
	}
}

// A crew that never brackets a run prints exactly what it printed before this
// existed: role, id, state, and nothing else.
func TestCrewStatus_UnchangedWithoutAnyRuns(t *testing.T) {
	cfg := setConfigEnv(t)
	body := `{"sessions":[
		{"id":"demo-2","isSuspended":true,"crew":{"id":"demo-2","role":"dev"}},
		{"id":"demo-3","isSuspended":false,"crew":{"id":"demo-2","role":"qa"}}
	]}`
	var seen []string
	srv := crewRunServer(t, map[string]string{"/sessions": body}, &seen)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "status")
	if err != nil {
		t.Fatalf("ao crew status: %v stderr=%s", err, errOut)
	}
	if strings.Contains(out, "running a") || strings.Contains(out, "discarded") {
		t.Fatalf("a crew with no bracketed runs grew a run line: %q", out)
	}
}

func strPtr(s string) *string { return &s }
