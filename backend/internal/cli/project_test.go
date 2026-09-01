package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

type projectCapture struct {
	method string
	path   string
	body   []byte
	// configPuts counts writes to the set-config endpoint specifically. The
	// fields above hold the LAST request only, and every CLI run also posts a
	// telemetry ping, so "was the config written?" needs its own counter.
	configPuts int
}

func projectServer(t *testing.T, status int, respBody string) (*httptest.Server, *projectCapture) {
	t.Helper()
	capture := &projectCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.method = r.Method
		capture.path = r.URL.Path
		capture.body, _ = io.ReadAll(r.Body)
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/config") {
			capture.configPuts++
		}
		if !strings.HasPrefix(r.URL.Path, "/api/v1/projects") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func TestProjectSetConfig_TrackerIntakeFlags(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--tracker-intake", "--tracker-repo", "acme/demo", "--tracker-assignee", "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPut || capture.path != "/api/v1/projects/demo/config" {
		t.Fatalf("request = %s %s, want PUT /api/v1/projects/demo/config", capture.method, capture.path)
	}
	var got projectsvc.SetConfigInput
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
	}
	if !got.Config.TrackerIntake.Enabled || got.Config.TrackerIntake.Provider != "github" || got.Config.TrackerIntake.Repo != "acme/demo" || got.Config.TrackerIntake.Assignee != "alice" {
		t.Fatalf("tracker intake request = %#v", got.Config.TrackerIntake)
	}
}

func TestProjectSetConfig_TrackerIntakeJSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--config-json", `{"trackerIntake":{"enabled":true,"provider":"github","assignee":"alice"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got projectsvc.SetConfigInput
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
	}
	if !got.Config.TrackerIntake.Enabled || got.Config.TrackerIntake.Provider != "github" || got.Config.TrackerIntake.Assignee != "alice" {
		t.Fatalf("tracker intake request = %#v", got.Config.TrackerIntake)
	}
}

func TestBuildProjectConfigTrackerIntakeFlags(t *testing.T) {
	got, err := buildProjectConfig(projectSetConfigOptions{
		trackerIntake:   true,
		trackerRepo:     "acme/demo",
		trackerAssignee: "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.TrackerIntake.Enabled || got.TrackerIntake.Provider != "github" || got.TrackerIntake.Repo != "acme/demo" || got.TrackerIntake.Assignee != "alice" {
		t.Fatalf("tracker intake config = %#v", got.TrackerIntake)
	}
}

func TestBuildProjectConfigTrackerProviderGitLab(t *testing.T) {
	got, err := buildProjectConfig(projectSetConfigOptions{
		trackerIntake:   true,
		trackerProvider: "gitlab",
		trackerRepo:     "group/sub/proj",
		trackerAssignee: "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.TrackerIntake.Enabled || got.TrackerIntake.Provider != "gitlab" || got.TrackerIntake.Repo != "group/sub/proj" {
		t.Fatalf("tracker intake config = %#v", got.TrackerIntake)
	}
}

func TestBuildProjectConfigTrackerProviderInvalidIsUsageError(t *testing.T) {
	_, err := buildProjectConfig(projectSetConfigOptions{trackerIntake: true, trackerProvider: "bitbucket"})
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want usage error for unknown provider", err, ExitCode(err))
	}
}

func TestProjectSetConfig_GitConventionFlags(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--git-workflow", "custom", "--branch-prefix", "feat/")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got projectsvc.SetConfigInput
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
	}
	if got.Config.GitConvention.Workflow != "custom" || got.Config.GitConvention.BranchPrefix != "feat/" {
		t.Fatalf("git convention request = %#v", got.Config.GitConvention)
	}
}

func TestBuildProjectConfigGitConventionFlags(t *testing.T) {
	got, err := buildProjectConfig(projectSetConfigOptions{gitWorkflow: "Gitflow"})
	if err != nil {
		t.Fatal(err)
	}
	// Workflow is lowercased so "Gitflow" round-trips to the domain vocabulary.
	if got.GitConvention.Workflow != "gitflow" {
		t.Fatalf("git workflow = %q, want gitflow", got.GitConvention.Workflow)
	}
}

func TestBuildProjectConfigGitWorkflowNoneNormalizesToEmpty(t *testing.T) {
	// "none" is the CLI spelling of the default; it must store as unset so an
	// otherwise-empty convention persists as NULL. Paired with another flag so the
	// config is not entirely empty (which would trip the "provide a flag" guard).
	got, err := buildProjectConfig(projectSetConfigOptions{gitWorkflow: "none", defaultBranch: "develop"})
	if err != nil {
		t.Fatal(err)
	}
	if got.GitConvention.Workflow != "" {
		t.Fatalf("git workflow = %q, want empty (none)", got.GitConvention.Workflow)
	}
}

func TestProjectList_Success(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"projects":[{"id":"zeta","name":"Zeta","sessionPrefix":"zeta"},{"id":"alpha","name":"Alpha","sessionPrefix":"alpha","resolveError":"config missing"}]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/projects" {
		t.Fatalf("request = %s %s, want GET /api/v1/projects", capture.method, capture.path)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "SESSION PREFIX") {
		t.Fatalf("output missing table header:\n%s", out)
	}
	if strings.Index(out, "alpha") > strings.Index(out, "zeta") {
		t.Fatalf("projects should be sorted by id in output:\n%s", out)
	}
	if !strings.Contains(out, "degraded: config missing") {
		t.Fatalf("output missing degraded status:\n%s", out)
	}
}

func TestProjectList_JSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusOK, `{"projects":[{"id":"demo","name":"Demo","sessionPrefix":"demo"}]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "ls", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got projectListResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\nout=%s", err, out)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "demo" {
		t.Fatalf("projects = %#v, want demo", got.Projects)
	}
}

func TestProjectList_Empty(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusOK, `{"projects":[]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "No projects registered") || !strings.Contains(out, "ao project add --path") {
		t.Fatalf("empty output missing hint:\n%s", out)
	}
}

func TestProjectGet_Success(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"git@example.com:demo.git","defaultBranch":"main","agent":"codex"}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "get", "demo")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want GET /api/v1/projects/demo", capture.method, capture.path)
	}
	for _, want := range []string{"Project demo (ok)", "name: Demo", "path: /repo/demo", "default branch: main", "agent: codex"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestProjectGet_JSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"status":"degraded","project":{"id":"demo","name":"Demo","path":"/repo/demo","resolveError":"config missing"}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "get", "demo", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want GET /api/v1/projects/demo", capture.method, capture.path)
	}
	var got projectGetResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\nout=%s", err, out)
	}
	if got.Status != "degraded" || got.Project.ID != "demo" || got.Project.ResolveError != "config missing" {
		t.Fatalf("get json = %#v, want degraded demo with resolve error", got)
	}
}

func TestProjectGet_MissingArg(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "project", "get")
	if err == nil {
		t.Fatal("expected missing arg error")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestProjectGet_NotFound(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusNotFound, `{"error":"not_found","code":"PROJECT_NOT_FOUND","message":"Unknown project"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "get", "missing")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "PROJECT_NOT_FOUND") && !strings.Contains(errOut, "PROJECT_NOT_FOUND") {
		t.Fatalf("error did not surface not found envelope: %v\nstderr=%s", err, errOut)
	}
}

func TestProjectRemove_RequiresID(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "project", "rm")
	if err == nil {
		t.Fatal("expected missing id error")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
}

func TestProjectRemove_NotFound(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusNotFound, `{"error":"not_found","code":"PROJECT_NOT_FOUND","message":"Unknown project"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "missing", "--yes")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "PROJECT_NOT_FOUND") && !strings.Contains(errOut, "PROJECT_NOT_FOUND") {
		t.Fatalf("error did not surface not found envelope: %v\nstderr=%s", err, errOut)
	}
}

func TestProjectRemove_AbortsWhenConfirmationDoesNotMatch(t *testing.T) {
	setConfigEnv(t)
	out, _, err := executeCLI(t, Deps{
		In: strings.NewReader("nope\n"),
	}, "project", "rm", "demo")
	if err != nil {
		t.Fatalf("unexpected abort error: %v", err)
	}
	if !strings.Contains(out, "Type the project id to confirm") || !strings.Contains(out, "aborted") {
		t.Fatalf("output missing prompt/abort:\n%s", out)
	}
}

func TestProjectRemove_DeletesAfterConfirmation(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"ok":true,"id":"demo"}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("demo\n"),
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodDelete || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want DELETE /api/v1/projects/demo", capture.method, capture.path)
	}
	if !strings.Contains(out, "removed project demo") {
		t.Fatalf("output missing removal message:\n%s", out)
	}
}

func TestProjectRemove_JSONDocumentedEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"ok":true,"id":"demo"}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("wrong\n"),
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo", "--yes", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodDelete || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want DELETE /api/v1/projects/demo", capture.method, capture.path)
	}
	var got projectRemoveResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\nout=%s", err, out)
	}
	if !got.OK || got.ID != "demo" || got.ProjectID != "" {
		t.Fatalf("remove json = %#v, want documented ok/id envelope", got)
	}
}

func TestProjectRemove_JSONBackendEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	removedStorageDir := false
	srv, _ := projectServer(t, http.StatusOK, `{"projectId":"demo","removedStorageDir":false}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo", "--yes", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got projectRemoveResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode json output: %v\nout=%s", err, out)
	}
	if got.ProjectID != "demo" || got.RemovedStorageDir == nil || *got.RemovedStorageDir != removedStorageDir {
		t.Fatalf("remove json = %#v, want backend projectId/removedStorageDir envelope", got)
	}
}

func TestProjectRemove_EmptySuccessFallsBackToRequestedID(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := projectServer(t, http.StatusNoContent, ``)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo", "--yes")
	if err != nil {
		t.Fatalf("unexpected error for empty 2xx body: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "removed project demo") {
		t.Fatalf("output missing fallback removal id:\n%s", out)
	}
}

func TestProjectRemove_YesSkipsConfirmationAndSupportsBackendRemoveEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"projectId":"demo","removedStorageDir":false}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader("wrong\n"),
		ProcessAlive: func(int) bool { return true },
	}, "project", "rm", "demo", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodDelete || capture.path != "/api/v1/projects/demo" {
		t.Fatalf("request = %s %s, want DELETE /api/v1/projects/demo", capture.method, capture.path)
	}
	if strings.Contains(out, "Type the project id") || !strings.Contains(out, "removed project demo") {
		t.Fatalf("--yes output should skip prompt and print removal:\n%s", out)
	}
}

// The two facts that decide which inspector tabs a project gets - a web UI to
// preview, an iOS Simulator to watch - are part of the config the daemon
// stores, but the CLI's mirror of that config did not have them: passing them
// in --config-json printed the updated project and changed nothing, which is
// the "reports success, changed nothing" failure this surface exists to avoid.
func TestProjectSetConfig_CarriesTheTabFacts(t *testing.T) {
	for name, args := range map[string][]string{
		"flags": {"project", "set-config", "demo", "--ios-simulator", "--web-ui"},
		"json":  {"project", "set-config", "demo", "--config-json", `{"hasIOSSimulator":true,"hasWebUI":true}`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
			writeRunFileFor(t, cfg, srv)

			if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...); err != nil {
				t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
			}
			var got projectsvc.SetConfigInput
			if err := json.Unmarshal(capture.body, &got); err != nil {
				t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
			}
			if !got.Config.HasIOSSimulator || !got.Config.HasWebUI {
				t.Fatalf("config = %#v, want both tab facts carried to the daemon", got.Config)
			}
		})
	}
}

// Turning AUTOMATIC crew formation off is a per-project setting a human must be
// able to set from BOTH surfaces - "a flag you cannot see is a flag you forget"
// applies just as much to one you cannot script. Both spellings have to reach
// the daemon, for the same reason the tab facts above do.
func TestProjectSetConfig_CarriesDisableAutoCrew(t *testing.T) {
	for name, args := range map[string][]string{
		"flag": {"project", "set-config", "demo", "--no-auto-crew"},
		"json": {"project", "set-config", "demo", "--config-json", `{"disableAutoCrew":true}`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
			writeRunFileFor(t, cfg, srv)

			if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...); err != nil {
				t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
			}
			var got projectsvc.SetConfigInput
			if err := json.Unmarshal(capture.body, &got); err != nil {
				t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
			}
			if !got.Config.DisableAutoCrew {
				t.Fatalf("config = %#v, want automatic crew formation turned off", got.Config)
			}
		})
	}
}

// A key the CLI does not recognize must be refused, not swallowed. `set-config`
// replaces the whole stored config, so a typo (or a config produced by a newer
// daemon than this CLI) that decodes "successfully" into a struct without that
// field writes the key out of existence and still exits 0. Erroring is the only
// safe answer on a write path.
func TestProjectSetConfig_RefusesUnknownConfigJSONField(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--config-json", `{"defaultBranch":"main","responseLanguag":"Thai"}`)
	if err == nil {
		t.Fatalf("unknown config key must fail; stdout=%s", out)
	}
	if ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", ExitCode(err))
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, "responseLanguag") {
		t.Errorf("error should name the unrecognized key, got: %s", msg)
	}
	if !strings.Contains(msg, "--config-json") {
		t.Errorf("error should name the flag at fault, got: %s", msg)
	}
	if capture.configPuts != 0 {
		t.Errorf("a rejected config must not be written; got %d PUTs to the config endpoint", capture.configPuts)
	}
}

// Malformed JSON keeps failing as a plain usage error - the strict decoding
// added for unknown keys must not blur the message for input that is not JSON
// at all.
func TestProjectSetConfig_MalformedConfigJSONErrorsClearly(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--config-json", `{"defaultBranch":`)
	if err == nil {
		t.Fatalf("malformed --config-json must fail; stdout=%s", out)
	}
	if ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", ExitCode(err))
	}
	msg := err.Error() + errOut
	if !strings.Contains(msg, "--config-json") || !strings.Contains(msg, "valid JSON object") {
		t.Errorf("error should say --config-json is not a valid JSON object, got: %s", msg)
	}
	if capture.configPuts != 0 {
		t.Errorf("a rejected config must not be written; got %d PUTs to the config endpoint", capture.configPuts)
	}
}

// A config that leaves optional fields out must be sent as-is: set-config
// replaces the stored config, so inventing defaults here would write settings
// the human never asked for.
func TestProjectSetConfig_ConfigJSONAddsNoDefaults(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
	}, "project", "set-config", "demo", "--config-json", `{"sessionPrefix":"demo","trackerIntake":{"enabled":true}}`); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var got projectsvc.SetConfigInput
	if err := json.Unmarshal(capture.body, &got); err != nil {
		t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
	}
	// Deliberately omits defaultBranch and the tracker provider: those are the
	// two fields the daemon's WithDefaults() would fill in. Filling them here
	// would store settings the human never wrote.
	want := domain.ProjectConfig{
		SessionPrefix: "demo",
		TrackerIntake: domain.TrackerIntakeConfig{Enabled: true},
	}
	if !reflect.DeepEqual(got.Config, want) {
		t.Fatalf("config = %#v, want exactly %#v (no invented defaults)", got.Config, want)
	}
}

// The check-in gate is a per-project setting a human must be able to set from
// BOTH surfaces, for the same reason DisableAutoCrew is. --config-json is the
// sharper half of the test: set-config REPLACES the whole config, so a key the
// CLI could not represent would be written out of existence by a command that
// exits 0 - decodeConfigJSON refuses unknown keys, and this proves the new one
// is known.
func TestProjectSetConfig_CarriesPauseBeforeImplementing(t *testing.T) {
	for name, args := range map[string][]string{
		"flag": {"project", "set-config", "demo", "--pause-before-implementing"},
		"json": {"project", "set-config", "demo", "--config-json", `{"pauseBeforeImplementing":true}`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, capture := projectServer(t, http.StatusOK, `{"project":{"id":"demo","path":"/repo/demo"}}`)
			writeRunFileFor(t, cfg, srv)

			if _, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...); err != nil {
				t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
			}
			var got projectsvc.SetConfigInput
			if err := json.Unmarshal(capture.body, &got); err != nil {
				t.Fatalf("decode request: %v\nbody=%s", err, capture.body)
			}
			if !got.Config.PauseBeforeImplementing {
				t.Fatalf("config = %#v, want the check-in gate carried to the daemon", got.Config)
			}
		})
	}
}
