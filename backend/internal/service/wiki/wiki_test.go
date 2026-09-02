package wiki

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSettings struct {
	mu      sync.Mutex
	vault   string
	harness string
}

func (f *fakeSettings) VaultPath() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.vault
}

func (f *fakeSettings) Harness() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.harness
}

func (f *fakeSettings) SetHarness(h string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.harness = h
	return nil
}

type fakeRuntime struct {
	created   []ports.RuntimeConfig
	destroyed []string
	alive     bool
	aliveErr  error
}

func (f *fakeRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	f.created = append(f.created, cfg)
	f.alive = true
	return ports.RuntimeHandle{ID: string(cfg.SessionID)}, nil
}

func (f *fakeRuntime) Destroy(_ context.Context, h ports.RuntimeHandle) error {
	f.destroyed = append(f.destroyed, h.ID)
	f.alive = false
	return nil
}

func (f *fakeRuntime) IsAlive(_ context.Context, _ ports.RuntimeHandle) (bool, error) {
	return f.alive, f.aliveErr
}

type fakeAgent struct {
	argv       []string
	launched   []ports.LaunchConfig
	preLaunchN int
}

func (f *fakeAgent) GetConfigSpec(context.Context) (ports.ConfigSpec, error) {
	return ports.ConfigSpec{}, nil
}

func (f *fakeAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	f.launched = append(f.launched, cfg)
	return f.argv, nil
}

func (f *fakeAgent) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryInCommand, nil
}

func (f *fakeAgent) GetAgentHooks(context.Context, ports.WorkspaceHookConfig) error { return nil }

func (f *fakeAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return nil, false, nil
}

func (f *fakeAgent) SessionInfo(context.Context, ports.SessionRef) (ports.SessionInfo, bool, error) {
	return ports.SessionInfo{}, false, nil
}

func (f *fakeAgent) PreLaunch(_ context.Context, _ ports.LaunchConfig) error {
	f.preLaunchN++
	return nil
}

type fakeResolver struct{ agent ports.Agent }

func (f fakeResolver) Agent(domain.AgentHarness) (ports.Agent, bool) {
	return f.agent, f.agent != nil
}

func newService(vault string) (*Service, *fakeSettings, *fakeRuntime, *fakeAgent) {
	set := &fakeSettings{vault: vault}
	rt := &fakeRuntime{}
	ag := &fakeAgent{argv: []string{"/bin/claude"}}
	return New(Deps{Settings: set, Agents: fakeResolver{agent: ag}, Runtime: rt}), set, rt, ag
}

func TestStatus_NoVault_IsUnconfigured(t *testing.T) {
	svc, _, _, _ := newService("")
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Configured || st.Running {
		t.Fatalf("status = %+v, want unconfigured", st)
	}
}

func TestStart_LaunchesBareInTheVault(t *testing.T) {
	vault := t.TempDir()
	svc, set, rt, ag := newService(vault)

	st, err := svc.Start(context.Background(), domain.HarnessClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.HandleID != HandleID || st.Harness != "claude-code" {
		t.Fatalf("status = %+v", st)
	}
	if len(rt.created) != 1 {
		t.Fatalf("created %d panes, want 1", len(rt.created))
	}
	cfg := rt.created[0]
	if cfg.WorkspacePath != vault {
		t.Fatalf("workspace = %q, want %q", cfg.WorkspacePath, vault)
	}
	if string(cfg.SessionID) != HandleID {
		t.Fatalf("handle = %q, want %q", cfg.SessionID, HandleID)
	}
	if cfg.Branch != "" || cfg.ProjectID != "" {
		t.Fatalf("wiki pane must carry no project or branch: %+v", cfg)
	}
	launch := ag.launched[0]
	if launch.Prompt != "" || launch.SystemPrompt != "" || launch.SystemPromptFile != "" {
		t.Fatalf("launch injected a prompt: %+v", launch)
	}
	if len(launch.AllowedTools) != 0 || len(launch.DisallowedTools) != 0 {
		t.Fatalf("launch restricted tools: %+v", launch)
	}
	// DEFAULT is the "no --permission-mode flag" value: AO must not decide how
	// much the agent may do to the user's notes without asking. Anything else
	// here — bypass included — is AO framing, which this launch has none of.
	if launch.Permissions != ports.PermissionModeDefault {
		t.Fatalf("permissions = %q, want the default (no flag forced)", launch.Permissions)
	}
	if ag.preLaunchN != 1 {
		t.Fatalf("PreLaunch called %d times, want 1", ag.preLaunchN)
	}
	if set.Harness() != "claude-code" {
		t.Fatalf("harness not remembered: %q", set.Harness())
	}
}

func TestStart_DestroysAnyPreviousPaneFirst(t *testing.T) {
	svc, _, rt, _ := newService(t.TempDir())
	if _, err := svc.Start(context.Background(), domain.HarnessClaudeCode); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Start(context.Background(), domain.HarnessCodex); err != nil {
		t.Fatal(err)
	}
	if len(rt.destroyed) != 2 {
		t.Fatalf("destroyed %v, want one teardown per start", rt.destroyed)
	}
	for _, id := range rt.destroyed {
		if id != HandleID {
			t.Fatalf("destroyed %q, want %q", id, HandleID)
		}
	}
}

func TestStart_UnknownHarness_IsInvalid(t *testing.T) {
	svc, _, _, _ := newService(t.TempDir())
	if _, err := svc.Start(context.Background(), domain.AgentHarness("not-an-agent")); err == nil {
		t.Fatal("want an error for an unknown harness")
	}
}

func TestStart_NoVault_IsRefused(t *testing.T) {
	svc, _, rt, _ := newService("")
	if _, err := svc.Start(context.Background(), domain.HarnessClaudeCode); err == nil {
		t.Fatal("want an error with no vault configured")
	}
	if len(rt.created) != 0 {
		t.Fatal("must not launch without a vault")
	}
}

func TestStop_TearsDownAndKeepsTheVaultReadable(t *testing.T) {
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "note.md"), "# hi")
	svc, _, _, _ := newService(vault)
	if _, err := svc.Start(context.Background(), domain.HarnessClaudeCode); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Stop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Running {
		t.Fatalf("still running after stop: %+v", st)
	}
	files, err := svc.ListFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Notes) != 1 {
		t.Fatalf("vault unreadable after stop: %+v", files)
	}
}

func TestRestart_ReusesTheRunningHarness(t *testing.T) {
	svc, _, rt, _ := newService(t.TempDir())
	if _, err := svc.Start(context.Background(), domain.HarnessCodex); err != nil {
		t.Fatal(err)
	}
	st, err := svc.Restart(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Harness != "codex" {
		t.Fatalf("restart switched agent to %q", st.Harness)
	}
	if len(rt.created) != 2 {
		t.Fatalf("created %d panes, want 2", len(rt.created))
	}
}

func TestRestart_WithNoAgentEverChosen_IsRefused(t *testing.T) {
	svc, _, _, _ := newService(t.TempDir())
	if _, err := svc.Restart(context.Background()); err == nil {
		t.Fatal("want an error restarting with no agent")
	}
}

func TestStatus_ProbeFailureDoesNotDeclareThePaneDead(t *testing.T) {
	svc, _, rt, _ := newService(t.TempDir())
	if _, err := svc.Start(context.Background(), domain.HarnessClaudeCode); err != nil {
		t.Fatal(err)
	}
	rt.aliveErr = errors.New("tmux unreachable")
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running {
		t.Fatal("a failed probe must not be read as death")
	}
}

func TestListFiles_SkipsDotDirectories(t *testing.T) {
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "index.md"), "# index")
	mustWrite(t, filepath.Join(vault, "llm", "context-window.md"), "# ctx")
	mustWrite(t, filepath.Join(vault, ".obsidian", "workspace.json"), "{}")
	mustWrite(t, filepath.Join(vault, ".hidden-note.md"), "secret")

	svc, _, _, _ := newService(vault)
	files, err := svc.ListFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, n := range files.Notes {
		paths = append(paths, n.Path)
	}
	want := []string{"index.md", "llm/context-window.md"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if files.Notes[0].ModifiedAt.IsZero() {
		t.Fatal("modified time not reported")
	}
}

func TestReadNote_ReturnsRawMarkdown(t *testing.T) {
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "llm", "note.md"), "# Title\n\nbody\n")
	svc, _, _, _ := newService(vault)
	note, err := svc.ReadNote(context.Background(), "llm/note.md")
	if err != nil {
		t.Fatal(err)
	}
	if note.Content != "# Title\n\nbody\n" || note.Path != "llm/note.md" {
		t.Fatalf("note = %+v", note)
	}
}

func TestReadNote_RefusesEscapingTheVault(t *testing.T) {
	parent := t.TempDir()
	vault := filepath.Join(parent, "vault")
	mustWrite(t, filepath.Join(vault, "ok.md"), "ok")
	mustWrite(t, filepath.Join(parent, "secret.md"), "secret")

	svc, _, _, _ := newService(vault)
	for _, bad := range []string{"../secret.md", "/etc/hosts", "~/.ssh/id_rsa", "", "llm/../../secret.md"} {
		if _, err := svc.ReadNote(context.Background(), bad); err == nil {
			t.Fatalf("ReadNote(%q) must be refused", bad)
		}
	}
}

func TestReadNote_RefusesASymlinkOutOfTheVault(t *testing.T) {
	parent := t.TempDir()
	vault := filepath.Join(parent, "vault")
	mustWrite(t, filepath.Join(vault, "ok.md"), "ok")
	mustWrite(t, filepath.Join(parent, "secret.md"), "secret")
	if err := os.Symlink(filepath.Join(parent, "secret.md"), filepath.Join(vault, "escape.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	svc, _, _, _ := newService(vault)
	if _, err := svc.ReadNote(context.Background(), "escape.md"); err == nil {
		t.Fatal("a symlink out of the vault must be refused")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadNote_ReportsBacklinksByEverySpellingOfTheName(t *testing.T) {
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "agents", "compaction.md"), "# Compaction")
	mustWrite(t, filepath.Join(vault, "index.md"), "see [[compaction]]")
	mustWrite(t, filepath.Join(vault, "llm", "caching.md"), "see [[agents/compaction.md|the note]]")
	mustWrite(t, filepath.Join(vault, "llm", "window.md"), "see [[compaction#tail]]")
	mustWrite(t, filepath.Join(vault, "unrelated.md"), "see [[something-else]]")

	svc, _, _, _ := newService(vault)
	note, err := svc.ReadNote(context.Background(), "agents/compaction.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"index.md", "llm/caching.md", "llm/window.md"}
	if strings.Join(note.Backlinks, ",") != strings.Join(want, ",") {
		t.Fatalf("backlinks = %v, want %v", note.Backlinks, want)
	}
}

func TestReadNote_NeverBacklinksToItself(t *testing.T) {
	vault := t.TempDir()
	mustWrite(t, filepath.Join(vault, "self.md"), "I link to [[self]] on purpose")
	svc, _, _, _ := newService(vault)
	note, err := svc.ReadNote(context.Background(), "self.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(note.Backlinks) != 0 {
		t.Fatalf("backlinks = %v, want none", note.Backlinks)
	}
}

func TestHomeRelative(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	if got := HomeRelative(filepath.Join(home, "Notes", "Vault")); got != "~/Notes/Vault" {
		t.Fatalf("HomeRelative(home/Notes/Vault) = %q", got)
	}
	if got := HomeRelative(home); got != "~" {
		t.Fatalf("HomeRelative(home) = %q", got)
	}
	// A path outside the home is left exactly as it is, rather than guessed at.
	if got := HomeRelative("/opt/notes"); got != "/opt/notes" {
		t.Fatalf("HomeRelative(/opt/notes) = %q", got)
	}
	// A sibling directory that merely starts with the home's characters is NOT
	// inside it, and must not be rewritten.
	if got := HomeRelative(home + "-backup/notes"); got != home+"-backup/notes" {
		t.Fatalf("HomeRelative(home-backup) = %q", got)
	}
}

func TestStatus_ReportsTheHomeRelativePath(t *testing.T) {
	vault := t.TempDir()
	svc, _, _, _ := newService(vault)
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.DisplayPath != HomeRelative(vault) {
		t.Fatalf("DisplayPath = %q, want %q", st.DisplayPath, HomeRelative(vault))
	}
}
