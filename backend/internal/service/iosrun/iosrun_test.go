package iosrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSessions struct {
	path  string
	found bool
	err   error
}

func (f fakeSessions) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	if f.err != nil {
		return domain.SessionRecord{}, false, f.err
	}
	rec := domain.SessionRecord{}
	rec.Metadata.WorkspacePath = f.path
	return rec, f.found, nil
}

type fakeRuntime struct {
	mu        sync.Mutex
	created   []ports.RuntimeConfig
	destroys  []string
	alive     bool
	aliveErr  error
	createErr error
}

func (f *fakeRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return ports.RuntimeHandle{}, f.createErr
	}
	f.created = append(f.created, cfg)
	return ports.RuntimeHandle{ID: string(cfg.SessionID)}, nil
}

func (f *fakeRuntime) Destroy(_ context.Context, h ports.RuntimeHandle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroys = append(f.destroys, h.ID)
	return nil
}

func (f *fakeRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return f.alive, f.aliveErr
}

// worktree is a session checkout, with or without an Xcode project in it.
func worktree(t *testing.T, entries ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, entry := range entries {
		if err := os.MkdirAll(filepath.Join(dir, entry), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func service(t *testing.T, dir string, listOutput string, rt *fakeRuntime) *Service {
	t.Helper()
	return New(fakeSessions{path: dir, found: true}, rt, "/usr/local/bin/ao",
		WithRunner(func(context.Context, string, string, ...string) ([]byte, error) {
			if listOutput == "" {
				return nil, errors.New("xcodebuild: command not found")
			}
			return []byte(listOutput), nil
		}))
}

// The run bar's whole visibility test. A worktree with no Xcode project is the
// ordinary case, not a failure, and the empty name is what makes the bar absent
// rather than disabled.
func TestProject_NoXcodeProjectIsAnAnswerNotAnError(t *testing.T) {
	svc := service(t, worktree(t, "backend", "frontend"), "", &fakeRuntime{})

	project, err := svc.Project(context.Background(), "mer-9", false)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if project.Name != "" || len(project.Schemes) != 0 {
		t.Fatalf("a Go worktree is not an iOS project: %+v", project)
	}
}

func TestProject_ReadsTheSchemesOffTheWorkspace(t *testing.T) {
	svc := service(t, worktree(t, "Nter.xcworkspace", "Nter.xcodeproj"),
		`{"workspace":{"schemes":["Nter","NterDev"]}}`, &fakeRuntime{})

	project, err := svc.Project(context.Background(), "mer-9", false)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if project.Name != "Nter.xcworkspace" || project.Kind != "workspace" {
		t.Fatalf("a workspace must win over the project beside it: %+v", project)
	}
	if strings.Join(project.Schemes, ",") != "Nter,NterDev" {
		t.Fatalf("schemes %v", project.Schemes)
	}
}

// A picker that is silently empty is indistinguishable from one still loading,
// so the project still renders and says which tool failed.
func TestProject_SaysWhyTheSchemesAreMissing(t *testing.T) {
	svc := service(t, worktree(t, "Nter.xcodeproj"), "", &fakeRuntime{})

	project, err := svc.Project(context.Background(), "mer-9", false)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if project.Name != "Nter.xcodeproj" {
		t.Fatalf("the project is real even when its schemes cannot be read: %+v", project)
	}
	if project.SchemesError == "" {
		t.Fatal("an empty scheme list must say why")
	}
	if strings.Contains(project.SchemesError, "\n") {
		t.Fatalf("the reason must be one line a picker can show: %q", project.SchemesError)
	}
}

// `xcodebuild -list` takes seconds on a real project, and the bar asks on every
// visit. The answer is reused until the project could plausibly have changed.
func TestProject_ReusesTheListing(t *testing.T) {
	dir := worktree(t, "Nter.xcodeproj")
	// Counted under a mutex because the two listings run concurrently.
	var mu sync.Mutex
	var calls int
	now := time.Now()
	svc := New(fakeSessions{path: dir, found: true}, &fakeRuntime{}, "ao",
		WithClock(func() time.Time { return now }),
		WithRunner(func(context.Context, string, string, ...string) ([]byte, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return []byte(`{"project":{"schemes":["Nter"],"configurations":["Debug","Release"]}}`), nil
		}))
	read := func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}

	for range 5 {
		if _, err := svc.Project(context.Background(), "mer-9", false); err != nil {
			t.Fatalf("project: %v", err)
		}
	}
	// One round: the schemes and the configurations, asked once between them.
	if read() != 2 {
		t.Fatalf("ran xcodebuild %d times, want 2 - one listing round, then the cache", read())
	}
	// A scheme added in Xcode has to appear without restarting AO.
	now = now.Add(2 * time.Minute)
	if _, err := svc.Project(context.Background(), "mer-9", false); err != nil {
		t.Fatalf("project: %v", err)
	}
	if read() != 4 {
		t.Fatalf("the listing never went stale: %d calls", read())
	}
	// 🗝 And the human who just ran `xcodegen` and opened a picker does not wait
	// out the TTL: refresh re-reads the project however fresh the cache is.
	if _, err := svc.Project(context.Background(), "mer-9", true); err != nil {
		t.Fatalf("project: %v", err)
	}
	if read() != 6 {
		t.Fatalf("refresh served the cache (%d calls); opening a picker must re-read the project", read())
	}
}

func TestStart_RunsTheCLIInTheSessionsWorktree(t *testing.T) {
	dir := worktree(t, "Nter.xcworkspace")
	rt := &fakeRuntime{}
	svc := service(t, dir, `{"workspace":{"schemes":["Nter","NterDev"]}}`, rt)

	run, err := svc.Start(context.Background(), "mer-9", "NterDev", "Dev", "UDID-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if run.HandleID != "iosrun-mer-9" {
		t.Fatalf("handle %q", run.HandleID)
	}
	if len(rt.created) != 1 {
		t.Fatalf("created %d panes", len(rt.created))
	}
	cfg := rt.created[0]
	if cfg.WorkspacePath != dir {
		t.Fatalf("the build must run in the session's worktree: %s", cfg.WorkspacePath)
	}
	argv := strings.Join(cfg.Argv, " ")
	if !strings.Contains(argv, "sim run --scheme NterDev --configuration Dev --udid UDID-1") {
		t.Fatalf("argv %q", argv)
	}
	if cfg.Argv[0] != "/usr/local/bin/ao" {
		t.Fatalf("the pane must run THIS daemon's ao, not whatever PATH finds: %q", cfg.Argv[0])
	}
	// Without this the run would take an anonymous lease, and a crewmate's run
	// would not contend with it at all.
	if cfg.Env["AO_SESSION_ID"] != "mer-9" {
		t.Fatalf("the run must take the CALLING session's lease: %v", cfg.Env)
	}
	if cfg.Env["AO_SIM_DESTINATION"] != "id=UDID-1" {
		t.Fatalf("env %v", cfg.Env)
	}
	// A pane left from the last run would fail tmux's new-session on a
	// duplicate name, so the old one goes first.
	if len(rt.destroys) != 1 || rt.destroys[0] != "iosrun-mer-9" {
		t.Fatalf("the previous pane was not cleared: %v", rt.destroys)
	}
}

func TestStart_RefusesOnAWorktreeWithNoProject(t *testing.T) {
	rt := &fakeRuntime{}
	svc := service(t, worktree(t, "backend"), "", rt)

	if _, err := svc.Start(context.Background(), "mer-9", "Nter", "Debug", ""); err == nil {
		t.Fatal("a worktree with no Xcode project has nothing to run")
	}
	if len(rt.created) != 0 {
		t.Fatalf("a pane was opened anyway: %v", rt.created)
	}
}

// The bar's list can go stale - somebody renames a scheme in Xcode - and a
// refusal that names the real ones beats a pane that opens only to print it.
func TestStart_RefusesASchemeTheProjectDoesNotHave(t *testing.T) {
	rt := &fakeRuntime{}
	svc := service(t, worktree(t, "Nter.xcodeproj"), `{"project":{"schemes":["Nter"]}}`, rt)

	_, err := svc.Start(context.Background(), "mer-9", "NterStaging", "Debug", "")
	if err == nil {
		t.Fatal("an unknown scheme must not open a pane")
	}
	if !strings.Contains(err.Error(), "NterStaging") {
		t.Fatalf("the refusal must name what was asked for: %v", err)
	}
	if len(rt.created) != 0 {
		t.Fatalf("a pane was opened anyway: %v", rt.created)
	}
}

func TestCurrent_ReadsLivenessFromTheRuntime(t *testing.T) {
	rt := &fakeRuntime{alive: true}
	svc := service(t, worktree(t, "Nter.xcodeproj"), `{"project":{"schemes":["Nter"]}}`, rt)

	if _, ok, _ := svc.Current(context.Background(), "mer-9"); ok {
		t.Fatal("a session that never ran anything has no run")
	}
	if _, err := svc.Start(context.Background(), "mer-9", "Nter", "Debug", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	run, ok, err := svc.Current(context.Background(), "mer-9")
	if err != nil || !ok || !run.Running {
		t.Fatalf("current: %+v ok=%v err=%v", run, ok, err)
	}
	// The pane is gone - closed in tmux, or the machine restarted. The bar must
	// stop saying it is running.
	rt.alive = false
	run, _, _ = svc.Current(context.Background(), "mer-9")
	if run.Running {
		t.Fatal("a pane that is gone must not be reported as running")
	}
}

// A failed probe is not proof the pane is dead (the hard rule), so the last
// known answer stands rather than the bar flickering to "finished".
func TestCurrent_AnUnreadableRuntimeIsNotADeadPane(t *testing.T) {
	rt := &fakeRuntime{alive: true, aliveErr: errors.New("tmux: server not found")}
	svc := service(t, worktree(t, "Nter.xcodeproj"), `{"project":{"schemes":["Nter"]}}`, rt)
	if _, err := svc.Start(context.Background(), "mer-9", "Nter", "Debug", ""); err != nil {
		t.Fatalf("start: %v", err)
	}

	run, ok, err := svc.Current(context.Background(), "mer-9")
	if err != nil || !ok || !run.Running {
		t.Fatalf("current: %+v ok=%v err=%v", run, ok, err)
	}
}

// workspaceWith writes a worktree whose .xcworkspace declares one project, the
// way a real CocoaPods workspace does - which is what makes the configuration
// listing reachable at all.
func workspaceWith(t *testing.T, name, inner string) string {
	t.Helper()
	dir := worktree(t, name, inner)
	contents := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Workspace version = \"1.0\">\n" +
		"   <FileRef location = \"group:" + inner + "\"></FileRef>\n</Workspace>\n"
	if err := os.WriteFile(filepath.Join(dir, name, "contents.xcworkspacedata"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The two axes, side by side on one payload. The bar needs both to render two
// pickers, and neither is derived from the other.
func TestProject_ReadsTheConfigurationsBesideTheSchemes(t *testing.T) {
	dir := workspaceWith(t, "NterWorkspace.xcworkspace", filepath.Join("NterApp", "NterApp.xcodeproj"))
	svc := service(t, dir, `{"workspace":{"schemes":["NterApp","FNCore"]},`+
		`"project":{"schemes":["NterApp"],"configurations":["Dev","UAT","Production"]}}`, &fakeRuntime{})

	project, err := svc.Project(context.Background(), "mer-9", false)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if strings.Join(project.Configurations, ",") != "Dev,UAT,Production" {
		t.Fatalf("configurations %v", project.Configurations)
	}
	if project.ConfigurationsError != "" {
		t.Fatalf("a list that was read must carry no error: %q", project.ConfigurationsError)
	}
}

// 🗝 The regression this whole change closes. A project whose configurations
// cannot be read must NOT fall back to Debug - nter-ios-app has no Debug, and a
// Debug build there dies minutes later on an empty PODS_ROOT.
func TestProject_NeverInventsDebugWhenTheConfigurationsCannotBeRead(t *testing.T) {
	svc := service(t, worktree(t, "Nter.xcworkspace"), `{"workspace":{"schemes":["Nter"]}}`, &fakeRuntime{})

	project, err := svc.Project(context.Background(), "mer-9", false)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(project.Configurations) != 0 {
		t.Fatalf("configurations %v, want none - nothing answered", project.Configurations)
	}
	if project.ConfigurationsError == "" {
		t.Fatal("an empty configuration list must say why, or an empty picker looks like a loading one")
	}
	if strings.Contains(project.ConfigurationsError, "\n") {
		t.Fatalf("the reason must be one line a picker can show: %q", project.ConfigurationsError)
	}
	// The schemes still came through: either question failing must not take the
	// other down with it.
	if strings.Join(project.Schemes, ",") != "Nter" {
		t.Fatalf("schemes %v", project.Schemes)
	}
}

func TestDefaultConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name           string
		configurations []string
		want           string
	}{
		{"the only one is used without asking", []string{"Dev"}, "Dev"},
		{"Debug is what Xcode's Run button builds", []string{"Debug", "Release"}, "Debug"},
		{"the project's own spelling wins", []string{"debug", "Release"}, "debug"},
		// nter-ios-app. Picking one of these for somebody picks which backend
		// their app talks to.
		{"no Debug, no default", []string{"Dev", "Mock-api", "Production", "Release", "UAT"}, ""},
		{"nothing to choose from", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultConfiguration(tc.configurations); got != tc.want {
				t.Fatalf("DefaultConfiguration(%v) = %q, want %q", tc.configurations, got, tc.want)
			}
		})
	}
}

// A run with no configuration is a run that would have to guess one, and this
// is the layer that refuses rather than the build that fails.
func TestStart_RefusesWithoutAConfiguration(t *testing.T) {
	rt := &fakeRuntime{}
	svc := service(t, worktree(t, "Nter.xcodeproj"), `{"project":{"schemes":["Nter"],"configurations":["Dev","UAT"]}}`, rt)

	_, err := svc.Start(context.Background(), "mer-9", "Nter", "", "")
	if err == nil {
		t.Fatal("a run with no configuration must not open a pane")
	}
	if len(rt.created) != 0 {
		t.Fatalf("a pane was opened anyway: %v", rt.created)
	}
}

func TestStart_RefusesAConfigurationTheProjectDoesNotHave(t *testing.T) {
	rt := &fakeRuntime{}
	svc := service(t, worktree(t, "Nter.xcodeproj"), `{"project":{"schemes":["Nter"],"configurations":["Dev","UAT"]}}`, rt)

	// Exactly the stale-list case: the bar offered Debug before this fix, and
	// the project never had one.
	_, err := svc.Start(context.Background(), "mer-9", "Nter", "Debug", "")
	if err == nil {
		t.Fatal("an unknown configuration must not open a pane")
	}
	if !strings.Contains(err.Error(), "Debug") {
		t.Fatalf("the refusal must name what was asked for: %v", err)
	}
	if len(rt.created) != 0 {
		t.Fatalf("a pane was opened anyway: %v", rt.created)
	}
}
