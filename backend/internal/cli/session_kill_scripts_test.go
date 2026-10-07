package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionKill_RefusalForTheScriptsStoreNamesItsFilesAndDiscardDeletesIt(t *testing.T) {
	cfg := setConfigEnv(t)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/demo-1/kill" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if strings.Contains(string(raw), `"discardUncommitted":true`) {
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","freed":true,"terminated":true}`)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"conflict","code":"SESSION_HAS_UNPUBLISHED_SCRIPTS",`+
			`"message":"demo-1's scripts store worktree holds 1 uncommitted file(s), so it was not killed and nothing was torn down.",`+
			`"details":{"reason":"scripts_store_dirty","scriptsStore":{"path":"/data/store-worktrees/s/demo-1","branch":"ao/demo-1","baseBranch":"main",`+
			`"uncommitted":["projects/nter/draft.yaml"],"publish":{"outcome":"refused","hold":"publish_conflict","detail":"ao/demo-1 conflicts with main","files":["projects/nter/login.yaml"]}}}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "kill", "demo-1")
	for _, want := range []string{"/data/store-worktrees/s/demo-1", "uncommitted  projects/nter/draft.yaml", "publish refused (publish_conflict)", "projects/nter/login.yaml", "--discard-uncommitted"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v, want it to name %q", err, want)
		}
	}

	bodies = nil
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "kill", "demo-1", "--discard-uncommitted")
	if err != nil {
		t.Fatalf("deliberate end failed: %v\nstderr=%s", err, errOut)
	}
	if len(bodies) != 2 || !strings.Contains(out, "deleting the scripts store worktree") || !strings.Contains(out, "session demo-1 killed") {
		t.Fatalf("requests %v, output:\n%s", bodies, out)
	}
}
