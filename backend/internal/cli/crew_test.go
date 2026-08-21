package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// `ao crew wake <id>` posts to the CREW wake route, not the ordinary user-open
// wake. The two are different verbs: /wake must never disturb another session,
// while this one deliberately puts the current holder to sleep.
func TestCrewWake_PostsToTheCrewRoute(t *testing.T) {
	cfg := setConfigEnv(t)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
			return
		}
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-3","session":{"id":"demo-3","crew":{"id":"demo-2","role":"qa"}}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "wake", "demo-3")
	if err != nil {
		t.Fatalf("ao crew wake: %v stderr=%s", err, errOut)
	}
	want := "POST /api/v1/sessions/demo-3/crew/wake"
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("requests = %v, want exactly [%s]", paths, want)
	}
	if !strings.Contains(out, "demo-3") || !strings.Contains(out, "qa") {
		t.Fatalf("output does not say who is awake now: %q", out)
	}
}

// `ao crew status` groups the board's sessions by crew and says which member
// holds the slot - and says so plainly when there are no crews at all, rather
// than printing an empty table.
func TestCrewStatus_GroupsByCrewAndNamesTheHolder(t *testing.T) {
	cfg := setConfigEnv(t)
	body := `{"sessions":[
		{"id":"demo-1","isSuspended":false,"taskSize":"mechanical"},
		{"id":"demo-2","isSuspended":true,"taskSize":"standard","crew":{"id":"demo-2","role":"dev"}},
		{"id":"demo-3","isSuspended":false,"taskSize":"standard","crew":{"id":"demo-2","role":"qa"}}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "status")
	if err != nil {
		t.Fatalf("ao crew status: %v stderr=%s", err, errOut)
	}
	for _, want := range []string{"demo-2", "dev", "asleep", "qa", "awake"} {
		if !strings.Contains(out, want) {
			t.Fatalf("crew status missing %q:\n%s", want, out)
		}
	}
	// The solo session is not a crew and must not be listed as a one-member one.
	if strings.Contains(out, "demo-1") {
		t.Fatalf("a solo session was listed as a crew:\n%s", out)
	}
}

func TestCrewStatus_SaysWhenThereAreNoCrews(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		_, _ = io.WriteString(w, `{"sessions":[{"id":"demo-1"}]}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "status")
	if err != nil {
		t.Fatalf("ao crew status: %v", err)
	}
	if !strings.Contains(out, "No crews") {
		t.Fatalf("expected a plain no-crews line, got:\n%s", out)
	}
}

// The `--task-size` help is the orchestrator's main channel for choosing the tag,
// and the tag now decides how many agents a task gets. The help has to say that,
// or the choice is made blind.
func TestSpawnHelp_SaysWhatTaskSizeCostsNow(t *testing.T) {
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--help")
	if err != nil {
		t.Fatalf("ao spawn --help: %v", err)
	}
	for _, want := range []string{"mechanical", "qa"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--task-size help does not mention %q:\n%s", want, out)
		}
	}
}
