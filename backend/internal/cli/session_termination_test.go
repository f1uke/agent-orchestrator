package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sessionGetServer serves one session detail body, so a test can pin exactly
// what `ao session get` renders for it.
func sessionGetServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/demo-1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The whole point of the record: someone asking "why did my worker disappear?"
// gets the answer from `ao session get`, not from four log files.
func TestSessionGet_ShowsWhoEndedTheSessionAndWhy(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := sessionGetServer(t, `{"session":{"id":"demo-1","projectId":"demo","kind":"worker","status":"terminated",
		"activity":{"state":"exited"},"isTerminated":true,
		"termination":{"source":"agent","reason":"prompt_input_exit","lastState":"active",
		"transcriptPath":"/transcripts/demo-1.jsonl","at":"2026-08-17T09:36:05Z"}}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1")
	if err != nil {
		t.Fatalf("session get failed: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{"ended by: agent", "end reason: prompt_input_exit", "was: active", "transcript: /transcripts/demo-1.jsonl", "ended: 2026-08-17T09:36:05Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// A live session has no ending to report, and printing empty ending fields on
// one would read as though it had stopped.
func TestSessionGet_LiveSessionShowsNoEnding(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := sessionGetServer(t, `{"session":{"id":"demo-1","projectId":"demo","kind":"worker","status":"working",
		"activity":{"state":"active"},"isTerminated":false}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "get", "demo-1")
	if err != nil {
		t.Fatalf("session get failed: %v\nstderr=%s", err, errOut)
	}
	for _, unwanted := range []string{"ended by:", "end reason:", "transcript:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("live session output contains %q:\n%s", unwanted, out)
		}
	}
}
