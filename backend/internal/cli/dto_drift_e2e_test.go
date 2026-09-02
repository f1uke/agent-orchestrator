package cli

// dto_drift_e2e_test.go is the DTO-drift guard for the `ao spawn` and
// `ao project add` commands. The CLI defines its OWN request structs
// (spawnRequest in spawn.go, addProjectRequest in project.go) that are separate
// copies of the daemon's canonical request DTOs (controllers.SpawnSessionRequest
// and project.AddInput). Nothing else verifies the two sides agree on JSON field
// names — a renamed `json:"..."` tag on either side compiles fine but silently
// breaks at runtime.
//
// This test stands up the REAL daemon HTTP router + REAL controllers (with fakes
// only BELOW the controller, at the service layer) and drives the actual CLI
// commands through the actual postJSON client over a real loopback HTTP round
// trip. If the CLI's JSON field names diverge from what the controllers decode,
// the captured values are wrong/empty and the subtests fail.
//
// (This lives in a separate file from the build-tagged e2e_test.go so it runs in
// the normal `go test ./...` lane — it binds no extra ports beyond httptest and
// spawns no processes.)

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	agentsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/agent"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// fakeSessionService captures the ports.SpawnConfig the controller decodes from
// the CLI's request body. Every other method is a no-op so it satisfies the
// controllers.SessionService interface.
type fakeSessionService struct {
	spawned    ports.SpawnConfig
	previewErr error
}

var _ controllers.SessionService = (*fakeSessionService)(nil)

// This fake has no crew, so every session is its own task.
func (f *fakeSessionService) TaskDevOf(_ context.Context, id domain.SessionID) (domain.SessionID, error) {
	return id, nil
}

func (f *fakeSessionService) List(context.Context, sessionsvc.ListFilter) ([]domain.Session, error) {
	return nil, nil
}

func (f *fakeSessionService) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	f.spawned = cfg
	return domain.Session{
		SessionRecord: domain.SessionRecord{ID: domain.SessionID(string(cfg.ProjectID) + "-1")},
		Status:        domain.StatusIdle,
	}, nil
}

func (f *fakeSessionService) PrepareTodo(_ context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	f.spawned = cfg
	return domain.Session{
		SessionRecord: domain.SessionRecord{ID: domain.SessionID(string(cfg.ProjectID) + "-1"), IsTodo: true},
		Status:        domain.StatusTodo,
	}, nil
}

func (f *fakeSessionService) StartTodo(_ context.Context, id domain.SessionID) (domain.Session, error) {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id}, Status: domain.StatusIdle}, nil
}

func (f *fakeSessionService) UpdateTodoSpec(_ context.Context, id domain.SessionID, _ ports.TodoSpecPatch) (domain.Session, error) {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id, IsTodo: true}, Status: domain.StatusTodo}, nil
}

func (f *fakeSessionService) SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, _ bool) (domain.Session, error) {
	return f.Spawn(ctx, ports.SpawnConfig{ProjectID: projectID, Kind: domain.KindOrchestrator})
}

func (f *fakeSessionService) Get(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) Restore(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) Restart(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) Wake(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) WakeCrewMember(context.Context, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) AttachCrewMember(context.Context, domain.SessionID, domain.CrewRole, domain.SessionID) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) Kill(context.Context, domain.SessionID, sessionsvc.KillInput) (sessionsvc.KillOutcome, error) {
	return sessionsvc.KillOutcome{Terminated: true, Freed: true}, nil
}

func (f *fakeSessionService) Delete(context.Context, domain.SessionID, bool) error {
	return nil
}

func (f *fakeSessionService) RollbackSpawn(context.Context, domain.SessionID) (sessionsvc.RollbackOutcome, error) {
	return sessionsvc.RollbackOutcome{}, nil
}

func (f *fakeSessionService) Cleanup(context.Context, domain.ProjectID) (sessionsvc.CleanupOutcome, error) {
	return sessionsvc.CleanupOutcome{}, nil
}

func (f *fakeSessionService) Rename(context.Context, domain.SessionID, string) error {
	return nil
}

func (f *fakeSessionService) SetPreview(context.Context, domain.SessionID, string) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) SetPreviewFromAgent(context.Context, domain.SessionID, string) (domain.Session, error) {
	return domain.Session{}, nil
}

// previewErr, when set, is what EnsurePreviewAllowed returns — standing in for a
// project whose config has no web UI.
func (f *fakeSessionService) EnsurePreviewAllowed(context.Context, domain.SessionID) error {
	return f.previewErr
}

func (f *fakeSessionService) SetAutoNudge(context.Context, domain.SessionID, *bool) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) SetAutoResolve(context.Context, domain.SessionID, *bool) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) SetKeepWarmOnMerge(context.Context, domain.SessionID, bool) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) SetTargetBranch(context.Context, domain.SessionID, string) (domain.Session, error) {
	return domain.Session{}, nil
}

func (f *fakeSessionService) Send(context.Context, domain.SessionID, string) (ports.SendOutcome, error) {
	return ports.SendOutcome{}, nil
}

func (f *fakeSessionService) SendFrom(context.Context, domain.SessionID, string, sessionsvc.CrewTalk) (ports.SendOutcome, error) {
	return ports.SendOutcome{}, nil
}

func (f *fakeSessionService) SendToCrewmate(_ context.Context, from domain.SessionID, in sessionsvc.CrewSend) (sessionsvc.CrewSendResult, error) {
	return sessionsvc.CrewSendResult{Peer: domain.SessionID(string(from) + "-" + string(in.Role)), Message: in.Message}, nil
}

func (f *fakeSessionService) DispatchCommentToWorker(context.Context, domain.SessionID, string, string, string) error {
	return nil
}

func (f *fakeSessionService) ReplyToThread(context.Context, domain.SessionID, string, string, string) (sessionsvc.PRThreadComment, error) {
	return sessionsvc.PRThreadComment{}, nil
}

func (f *fakeSessionService) ResolveThread(context.Context, domain.SessionID, string, string) error {
	return nil
}

func (f *fakeSessionService) ListPRSummaries(context.Context, domain.SessionID) ([]sessionsvc.PRSummary, error) {
	return nil, nil
}

func (f *fakeSessionService) ListPRCommentThreads(context.Context, domain.SessionID) ([]sessionsvc.PRCommentGroup, error) {
	return nil, nil
}

func (f *fakeSessionService) ClaimPR(context.Context, domain.SessionID, string, sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	return sessionsvc.ClaimPRResult{}, nil
}

func (f *fakeSessionService) DiffContext(context.Context, domain.SessionID, sessionsvc.DiffContextQuery) (sessionsvc.DiffContextResult, error) {
	return sessionsvc.DiffContextResult{}, nil
}

func (f *fakeSessionService) ResolveWorkspaceRef(context.Context, domain.SessionID, string) ([]sessionsvc.ResolveCandidate, error) {
	return nil, nil
}

func (f *fakeSessionService) ReadWorkspaceFile(context.Context, domain.SessionID, string) (sessionsvc.WorkspaceFileResult, error) {
	return sessionsvc.WorkspaceFileResult{}, nil
}

func (f *fakeSessionService) WriteWorkspaceFile(context.Context, domain.SessionID, sessionsvc.WriteWorkspaceFileInput) (sessionsvc.WriteWorkspaceFileResult, error) {
	return sessionsvc.WriteWorkspaceFileResult{}, nil
}

func (f *fakeSessionService) WorkspaceChanges(context.Context, domain.SessionID) (sessionsvc.WorkspaceChangesResult, error) {
	return sessionsvc.WorkspaceChangesResult{}, nil
}

func (f *fakeSessionService) ListWorkspaceFiles(context.Context, domain.SessionID) (sessionsvc.WorkspaceFilesResult, error) {
	return sessionsvc.WorkspaceFilesResult{}, nil
}

func (f *fakeSessionService) SearchWorkspace(context.Context, domain.SessionID, sessionsvc.SearchQuery) (sessionsvc.SearchResult, error) {
	return sessionsvc.SearchResult{}, nil
}

func (f *fakeSessionService) WorkspaceFileDiff(
	context.Context, domain.SessionID, sessionsvc.FileDiffQuery,
) (sessionsvc.DiffContextResult, error) {
	return sessionsvc.DiffContextResult{}, nil
}

type fakeAgentCatalog struct{}

var _ controllers.AgentCatalog = (*fakeAgentCatalog)(nil)

func (f *fakeAgentCatalog) List(context.Context) (agentsvc.Inventory, error) {
	return authorizedCodexInventory(), nil
}

func (f *fakeAgentCatalog) Refresh(context.Context) (agentsvc.Inventory, error) {
	return authorizedCodexInventory(), nil
}

func (f *fakeAgentCatalog) Probe(_ context.Context, agentID string) (agentsvc.ProbeResult, error) {
	info := agentsvc.Info{ID: agentID, Label: agentID, AuthStatus: "authorized"}
	return agentsvc.ProbeResult{Agent: info, Supported: true, Installed: true}, nil
}

func authorizedCodexInventory() agentsvc.Inventory {
	info := agentsvc.Info{ID: "codex", Label: "Codex", AuthStatus: "authorized"}
	return agentsvc.Inventory{
		Supported:  []agentsvc.Info{info},
		Installed:  []agentsvc.Info{info},
		Authorized: []agentsvc.Info{info},
	}
}

// fakeProjectManager captures the project.AddInput and project.SetConfigInput
// the controller decodes from the CLI's request body, and serves the config the
// CLI reads back. Every other method is a no-op so it satisfies the
// projectsvc.Manager interface.
type fakeProjectManager struct {
	added projectsvc.AddInput
	// stored is the config GET /projects/{id} serves. nil keeps the project
	// config-less, which is what every test that does not care about config wants.
	stored *domain.ProjectConfig
	// setConfig is the config the daemon was asked to persist, and setConfigCalls
	// counts the PUTs, so a test can assert no write was attempted at all.
	setConfig      domain.ProjectConfig
	setConfigCalls int
}

var _ projectsvc.Manager = (*fakeProjectManager)(nil)

func (f *fakeProjectManager) List(context.Context) ([]projectsvc.Summary, error) {
	return nil, nil
}

func (f *fakeProjectManager) Get(_ context.Context, id domain.ProjectID) (projectsvc.GetResult, error) {
	project := projectsvc.Project{ID: id, Path: "/repo/" + string(id), Config: f.stored}
	return projectsvc.GetResult{Status: "ok", Project: &project}, nil
}

func (f *fakeProjectManager) Add(_ context.Context, in projectsvc.AddInput) (projectsvc.Project, error) {
	f.added = in
	id := domain.ProjectID("demo")
	if in.ProjectID != nil {
		id = domain.ProjectID(*in.ProjectID)
	}
	return projectsvc.Project{ID: id, Path: in.Path}, nil
}

func (f *fakeProjectManager) SetConfig(_ context.Context, id domain.ProjectID, in projectsvc.SetConfigInput) (projectsvc.Project, error) {
	cfg := in.Config
	f.setConfig = cfg
	f.setConfigCalls++
	return projectsvc.Project{ID: id, Config: &cfg}, nil
}

func (f *fakeProjectManager) Remove(context.Context, domain.ProjectID) (projectsvc.RemoveResult, error) {
	return projectsvc.RemoveResult{}, nil
}

func (f *fakeProjectManager) ListBranches(context.Context, domain.ProjectID) ([]string, error) {
	return nil, nil
}

// startDriftTestDaemon stands up the real router+controllers backed by the
// supplied fakes and points the CLI's run-file at it. The CLI discovers the
// server purely via AO_RUN_FILE + the run-file port, so this is a genuine
// loopback round trip through postJSON.
func startDriftTestDaemon(t *testing.T, sessions controllers.SessionService, projects projectsvc.Manager) {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Agents:   &fakeAgentCatalog{},
		Sessions: sessions,
		Projects: projects,
	}, httpd.ControlDeps{})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	port := srv.Listener.Addr().(*net.TCPAddr).Port

	rfPath := filepath.Join(t.TempDir(), "running.json")
	t.Setenv("AO_RUN_FILE", rfPath)
	if err := runfile.Write(rfPath, runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Now()}); err != nil {
		t.Fatalf("write run-file: %v", err)
	}
}

func TestE2E_SpawnAndProjectAddDTORoundTrip(t *testing.T) {
	t.Run("spawn", func(t *testing.T) {
		sessions := &fakeSessionService{}
		startDriftTestDaemon(t, sessions, &fakeProjectManager{})

		var out bytes.Buffer
		root := NewRootCommand(Deps{
			Out:          &out,
			Err:          &out,
			HTTPClient:   &http.Client{},
			ProcessAlive: func(int) bool { return true },
		})
		root.SetArgs([]string{
			"spawn",
			"--project", "mer",
			"--harness", "codex",
			"--from", "main",
			"--branch", "feat/x",
			"--prompt", "hi",
			"--issue", "ISS-1",
			"--name", "my worker",
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("spawn execute: %v\noutput: %s", err, out.String())
		}

		got := sessions.spawned
		if got.ProjectID != "mer" {
			t.Errorf("ProjectID = %q, want %q (CLI json:\"projectId\" vs SpawnSessionRequest)", got.ProjectID, "mer")
		}
		if got.Harness != "codex" {
			t.Errorf("Harness = %q, want %q", got.Harness, "codex")
		}
		if got.Branch != "feat/x" {
			t.Errorf("Branch = %q, want %q", got.Branch, "feat/x")
		}
		if got.BaseBranch != "main" {
			t.Errorf("BaseBranch = %q, want %q (CLI json:\"baseBranch\" vs SpawnSessionRequest)", got.BaseBranch, "main")
		}
		if got.Prompt != "hi" {
			t.Errorf("Prompt = %q, want %q", got.Prompt, "hi")
		}
		if got.IssueID != "ISS-1" {
			t.Errorf("IssueID = %q, want %q", got.IssueID, "ISS-1")
		}
		if got.DisplayName != "my worker" {
			t.Errorf("DisplayName = %q, want %q (CLI json:\"displayName\" vs SpawnSessionRequest)", got.DisplayName, "my worker")
		}
		if !bytes.Contains(out.Bytes(), []byte("spawned session")) {
			t.Errorf("output missing %q; got: %s", "spawned session", out.String())
		}
	})

	t.Run("project add", func(t *testing.T) {
		projects := &fakeProjectManager{}
		startDriftTestDaemon(t, &fakeSessionService{}, projects)

		var out bytes.Buffer
		root := NewRootCommand(Deps{
			Out:          &out,
			Err:          &out,
			HTTPClient:   &http.Client{},
			ProcessAlive: func(int) bool { return true },
		})
		root.SetArgs([]string{
			"project", "add",
			"--path", "/repo/mer",
			"--id", "demo",
			"--name", "Demo",
			"--worker-agent", "codex",
			"--orchestrator-agent", "claude-code",
			"--as-workspace",
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("project add execute: %v\noutput: %s", err, out.String())
		}

		got := projects.added
		if got.Path != "/repo/mer" {
			t.Errorf("Path = %q, want %q", got.Path, "/repo/mer")
		}
		if got.ProjectID == nil || *got.ProjectID != "demo" {
			t.Errorf("ProjectID = %v, want %q (CLI json:\"projectId\" vs AddInput)", got.ProjectID, "demo")
		}
		if got.Name == nil || *got.Name != "Demo" {
			t.Errorf("Name = %v, want %q", got.Name, "Demo")
		}
		if got.Config == nil {
			t.Fatal("Config = nil, want role agent config")
		}
		if got.Config.Worker.Harness != domain.HarnessCodex {
			t.Errorf("Config.Worker.Harness = %q, want codex", got.Config.Worker.Harness)
		}
		if got.Config.Orchestrator.Harness != domain.HarnessClaudeCode {
			t.Errorf("Config.Orchestrator.Harness = %q, want claude-code", got.Config.Orchestrator.Harness)
		}
		if !got.AsWorkspace {
			t.Errorf("AsWorkspace = false, want true (CLI json:\"asWorkspace\" vs AddInput)")
		}
		if !bytes.Contains(out.Bytes(), []byte("registered project")) {
			t.Errorf("output missing %q; got: %s", "registered project", out.String())
		}
	})
}

// TestE2E_PreviewRefusedWhenProjectHasNoWebUI drives the real `ao preview`
// command against a real router: what matters is what the AGENT sees. A worker
// in a project with no web UI must get a non-zero exit and a message that says
// the project has it disabled, not a silent success it would take as proof the
// change was demoed.
func TestE2E_PreviewRefusedWhenProjectHasNoWebUI(t *testing.T) {
	sessions := &fakeSessionService{previewErr: apierr.Conflict(
		"WEB_PREVIEW_DISABLED",
		`Project "demo-ios-app" has no web UI, so there is nothing to preview and `+"`ao preview`"+` is disabled for it. Turn on "Web UI" in the project's settings if it does render in a browser.`,
		nil,
	)}
	startDriftTestDaemon(t, sessions, &fakeProjectManager{})
	t.Setenv("AO_SESSION_ID", "mer-1")

	var out bytes.Buffer
	root := NewRootCommand(Deps{Out: &out, Err: &out, HTTPClient: &http.Client{}, ProcessAlive: func(int) bool { return true }})
	root.SetArgs([]string{"preview", "http://localhost:5173"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("ao preview must fail for a project with no web UI; output: %s", out.String())
	}
	msg := err.Error() + out.String()
	if !strings.Contains(msg, "no web UI") {
		t.Errorf("error should say the project has no web UI, got: %s", msg)
	}
	if !strings.Contains(msg, "Web UI") {
		t.Errorf("error should name the setting to turn on, got: %s", msg)
	}
}

// TestE2E_ProjectSetConfigRoundTripKeepsEveryField is the DTO-drift guard for
// the `ao project set-config` WRITE path. `set-config` replaces the stored
// config wholesale, so the read side and the write side have to agree on every
// field: a key the CLI cannot represent is not merely unprinted, it is
// destroyed, and the command still exits 0.
//
// The assertion is a PROPERTY, not a field list: a config in which every field
// of domain.ProjectConfig is populated (by reflection, so a field added
// tomorrow is populated too) is read back through the real `ao project get
// --json` and PUT back through the real `ao project set-config --config-json`.
// What the daemon is asked to store must equal what it served. Listing today's
// field names here would pass while the next field added to ProjectConfig died
// silently - which is exactly how this bug shipped.
func TestE2E_ProjectSetConfigRoundTripKeepsEveryField(t *testing.T) {
	full := filledProjectConfig(t)

	t.Run("unmodified round trip loses nothing", func(t *testing.T) {
		projects := &fakeProjectManager{stored: &full}
		startDriftTestDaemon(t, &fakeSessionService{}, projects)

		got := runProjectConfigRoundTrip(t, projects, nil)
		if !reflect.DeepEqual(got, full) {
			t.Fatalf("round trip changed the config.\nlost/changed keys: %v\n served: %s\nstored: %s",
				changedConfigKeys(t, full, got), mustJSON(t, full), mustJSON(t, got))
		}
	})

	t.Run("an explicit edit applies and nothing else is lost", func(t *testing.T) {
		projects := &fakeProjectManager{stored: &full}
		startDriftTestDaemon(t, &fakeSessionService{}, projects)

		got := runProjectConfigRoundTrip(t, projects, func(cfg map[string]any) {
			cfg["defaultBranch"] = "release/2.0"
		})
		if got.DefaultBranch != "release/2.0" {
			t.Errorf("DefaultBranch = %q, want the edit %q to be applied", got.DefaultBranch, "release/2.0")
		}
		want := full
		want.DefaultBranch = "release/2.0"
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("edit round trip changed more than the edit.\nlost/changed keys: %v\nwant: %s\ngot:  %s",
				changedConfigKeys(t, want, got), mustJSON(t, want), mustJSON(t, got))
		}
	})
}

// runProjectConfigRoundTrip drives the two real CLI commands a human uses to
// edit a project config - `ao project get --json`, then `ao project set-config
// --config-json` with what the first one printed - and returns the config the
// daemon was asked to persist. edit, when non-nil, mutates the JSON object in
// between, standing in for the human's edit.
func runProjectConfigRoundTrip(t *testing.T, projects *fakeProjectManager, edit func(map[string]any)) domain.ProjectConfig {
	t.Helper()

	var out bytes.Buffer
	root := NewRootCommand(Deps{Out: &out, Err: &out, HTTPClient: &http.Client{}, ProcessAlive: func(int) bool { return true }})
	root.SetArgs([]string{"project", "get", "demo", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("project get execute: %v\noutput: %s", err, out.String())
	}

	var read struct {
		Project struct {
			Config map[string]any `json:"config"`
		} `json:"project"`
	}
	if err := json.Unmarshal(out.Bytes(), &read); err != nil {
		t.Fatalf("decode `project get --json` output: %v\noutput: %s", err, out.String())
	}
	if len(read.Project.Config) == 0 {
		t.Fatalf("`project get --json` printed no config at all; output: %s", out.String())
	}
	if edit != nil {
		edit(read.Project.Config)
	}

	out.Reset()
	root = NewRootCommand(Deps{Out: &out, Err: &out, HTTPClient: &http.Client{}, ProcessAlive: func(int) bool { return true }})
	root.SetArgs([]string{"project", "set-config", "demo", "--config-json", string(mustJSON(t, read.Project.Config))})
	if err := root.Execute(); err != nil {
		t.Fatalf("project set-config execute: %v\noutput: %s", err, out.String())
	}
	if projects.setConfigCalls != 1 {
		t.Fatalf("SetConfig called %d times, want exactly 1", projects.setConfigCalls)
	}
	return projects.setConfig
}

// filledProjectConfig returns a domain.ProjectConfig with every field - and
// every field of every nested struct - set to a distinctive non-zero value. It
// walks the type by reflection on purpose: the point of the round-trip guard is
// that it covers fields nobody has written yet.
func filledProjectConfig(t *testing.T) domain.ProjectConfig {
	t.Helper()
	var cfg domain.ProjectConfig
	fillEveryField(t, reflect.ValueOf(&cfg).Elem(), "config")
	return cfg
}

// fillEveryField sets v, and everything reachable from it, to a non-zero value
// derived from its path so a value landing in the wrong field is visible. It
// fails the test on a kind it does not know how to fill rather than leaving a
// silent hole in the guard.
func fillEveryField(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString(path)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(7)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillEveryField(t, v.Elem(), path)
	case reflect.Slice:
		elem := reflect.New(v.Type().Elem()).Elem()
		fillEveryField(t, elem, path+"[0]")
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), elem))
	case reflect.Map:
		key := reflect.New(v.Type().Key()).Elem()
		fillEveryField(t, key, path+".key")
		val := reflect.New(v.Type().Elem()).Elem()
		fillEveryField(t, val, path+".value")
		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(key, val)
		v.Set(m)
	case reflect.Struct:
		for i := range v.NumField() {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			fillEveryField(t, v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	default:
		t.Fatalf("fillEveryField: unhandled kind %s at %s - teach the filler about it so the round-trip guard stays complete", v.Kind(), path)
	}
}

// changedConfigKeys names the top-level config keys that differ between want
// and got, so a failure says WHICH field the round trip dropped.
func changedConfigKeys(t *testing.T, want, got domain.ProjectConfig) []string {
	t.Helper()
	var wantMap, gotMap map[string]any
	if err := json.Unmarshal(mustJSON(t, want), &wantMap); err != nil {
		t.Fatalf("decode want config: %v", err)
	}
	if err := json.Unmarshal(mustJSON(t, got), &gotMap); err != nil {
		t.Fatalf("decode got config: %v", err)
	}
	seen := map[string]bool{}
	var keys []string
	for k := range wantMap {
		seen[k] = true
		if !reflect.DeepEqual(wantMap[k], gotMap[k]) {
			keys = append(keys, k)
		}
	}
	for k := range gotMap {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
