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

// The regression this file exists for: a crew whose members have TERMINATED read
// "awake" forever, because the listing mirrored only isSuspended out of the three
// facts domain.Awake() is defined on. A finished task then claimed two awake
// members, which is both false and the exact opposite of the rule AO enforces.
func TestCrewStatus_DoesNotCallATerminatedMemberAwake(t *testing.T) {
	cfg := setConfigEnv(t)
	// Exactly the shape on the wire for a finished task: dev's PR merged (so its
	// derived status is "merged", not "terminated") and qa torn down. Neither
	// carries isSuspended.
	body := `{"sessions":[
		{"id":"demo-2","isTerminated":true,"status":"merged","crew":{"id":"demo-2","role":"dev"}},
		{"id":"demo-3","isTerminated":true,"status":"terminated","crew":{"id":"demo-2","role":"qa"}},
		{"id":"demo-4","isTodo":true,"crew":{"id":"demo-4","role":"dev"}}
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
	if strings.Contains(out, "awake") {
		t.Fatalf("a crew with no running agent reports a member awake:\n%s", out)
	}
	// And it says which kind of stopped each member is, rather than flattening a
	// torn-down task into the word for "waiting for its turn".
	if strings.Count(out, "finished") != 2 {
		t.Fatalf("both terminated members should read finished:\n%s", out)
	}
	if !strings.Contains(out, "not started") {
		t.Fatalf("a TODO member should not read awake or asleep:\n%s", out)
	}
	if strings.Contains(out, "asleep") {
		t.Fatalf("nothing here is merely suspended:\n%s", out)
	}
}

// The other half of the same rule: a LIVE crew still renders exactly as it did -
// the member holding the turn is awake, the one waiting is asleep.
func TestCrewStatus_LiveCrewStillReadsAwakeAndAsleep(t *testing.T) {
	cfg := setConfigEnv(t)
	body := `{"sessions":[
		{"id":"demo-2","isTerminated":false,"status":"working","crew":{"id":"demo-2","role":"dev"}},
		{"id":"demo-3","isTerminated":false,"isSuspended":true,"status":"idle","crew":{"id":"demo-2","role":"qa"}}
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
	if strings.Count(out, "awake") != 1 || !strings.Contains(out, "asleep") {
		t.Fatalf("a live crew no longer reads one awake and one asleep:\n%s", out)
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

// `ao crew add <id>` posts to the crew MEMBERS route with the role, and says
// what actually happened: the member exists and is ASLEEP. Getting that wrong -
// letting a person think they just added a second running agent - is the one
// misunderstanding this command can cause.
func TestCrewAdd_PostsToTheMembersRouteAndSaysTheMemberIsWorking(t *testing.T) {
	cfg := setConfigEnv(t)
	var paths []string
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		paths = append(paths, r.Method+" "+r.URL.Path)
		bodies = append(bodies, string(raw))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-2","session":{"id":"demo-9","crew":{"id":"demo-2","role":"qa","joinReason":"manual"}}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "add", "demo-2")
	if err != nil {
		t.Fatalf("ao crew add: %v stderr=%s", err, errOut)
	}
	want := "POST /api/v1/sessions/demo-2/crew/members"
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("requests = %v, want exactly [%s]", paths, want)
	}
	if !strings.Contains(bodies[0], `"role":"qa"`) {
		t.Fatalf("body = %s, want the default role qa", bodies[0])
	}
	// The NEW member's id, not the one that was typed: it is what `ao crew wake`
	// and `ao send` take next.
	if !strings.Contains(out, "demo-9") {
		t.Fatalf("output does not name the new member: %q", out)
	}
	if !strings.Contains(out, "working") {
		t.Fatalf("output does not say the member is already working: %q", out)
	}
}

// A refusal from the daemon must reach the user as the daemon's own message -
// "this task already has a qa", "this task is finished" - rather than a generic
// failure, because both are things the person can act on.
func TestCrewAdd_SurfacesTheDaemonsRefusal(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"conflict","code":"CREW_ROLE_TAKEN","message":"session: this task already has a member in that role"}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "add", "demo-2")
	if err == nil {
		t.Fatal("a refused attach exited 0")
	}
	if !strings.Contains(err.Error()+errOut, "already has a member") {
		t.Fatalf("the daemon's reason did not reach the user: %v / %s", err, errOut)
	}
}

// `ao crew add` IDENTIFIES ITSELF. The daemon cannot see who is at the keyboard -
// the agent's CLI and a person's CLI reach the same loopback route - so the one
// thing that separates them is $AO_SESSION_ID, which only an AO session's shell
// has. Sending it is what lets a crew-off project refuse the agent and still
// serve the human.
func TestCrewAdd_SendsTheCallingSessionID(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-7")
	cfg := setConfigEnv(t)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-2","session":{"id":"demo-9","crew":{"id":"demo-2","role":"qa"}}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "add", "demo-2"); err != nil {
		t.Fatalf("ao crew add: %v stderr=%s", err, errOut)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"from":"demo-7"`) {
		t.Fatalf("body = %v, want it to carry the calling session id", bodies)
	}
}

// A HUMAN'S SHELL SENDS NOTHING. $AO_SESSION_ID is set by AO when it launches an
// agent; a person's terminal has no such variable, and an absent `from` is what
// makes the manual escape hatch still work on a crew-off project. Sending an
// empty string instead would be the same bytes as an agent claiming to be one.
func TestCrewAdd_SendsNoSessionIDFromAnOrdinaryShell(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-2","session":{"id":"demo-9","crew":{"id":"demo-2","role":"qa"}}}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "add", "demo-2"); err != nil {
		t.Fatalf("ao crew add: %v stderr=%s", err, errOut)
	}
	if len(bodies) != 1 || strings.Contains(bodies[0], `"from"`) {
		t.Fatalf("body = %v, want no `from` at all from a shell with no AO_SESSION_ID", bodies)
	}
}

// The crew-off refusal reaches the agent as the daemon's own sentences, because
// every one of them is there to stop the next agent looking for a way around it.
func TestCrewAdd_SurfacesTheCrewOffRefusal(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-7")
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"conflict","code":"CREW_AUTO_FORMATION_OFF","message":"session: mer has \"Never form a crew automatically\" turned on, so an AO session may not attach a qa. A person can still add one - the + qa control in the app"}`)
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "crew", "add", "demo-2")
	if err == nil {
		t.Fatal("a refused attach exited 0")
	}
	if !strings.Contains(err.Error()+errOut, "Never form a crew automatically") {
		t.Fatalf("the daemon's reason did not reach the caller: %v / %s", err, errOut)
	}
	if !strings.Contains(err.Error()+errOut, "+ qa") {
		t.Fatalf("the caller was not told who can still add a qa: %v / %s", err, errOut)
	}
}
