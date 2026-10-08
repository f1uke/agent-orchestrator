package sessionmanager

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/claudeprofile"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const agentViewEnv = "CLAUDE_CODE_DISABLE_AGENT_VIEW"

type launchEnvAgent struct{ recordingAgent }

func (*launchEnvAgent) LaunchEnv() map[string]string { return map[string]string{agentViewEnv: "1"} }

type profileFixture struct {
	m        *Manager
	st       *fakeStore
	rt       *fakeRuntime
	agent    *launchEnvAgent
	work     string
	profiles *claudeprofile.Store
}

func newProfileFixture(t *testing.T) profileFixture {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "work.json")
	if err := os.WriteFile(work, []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{"env":`), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles, err := claudeprofile.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.SetUser([]claudeprofile.Profile{
		{Name: "Work", SettingsFile: work},
		{Name: "Broken", SettingsFile: broken},
		{Name: "Gone", SettingsFile: filepath.Join(dir, "missing.json")},
	}); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	agent := &launchEnvAgent{}
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }, ClaudeProfiles: profiles})
	return profileFixture{m: m, st: st, rt: rt, agent: agent, work: work, profiles: profiles}
}

func (f profileFixture) liveClaudeSession(id domain.SessionID, profile string, state domain.ActivityState) {
	f.st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, ClaudeProfile: profile,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/" + string(id), Branch: "b", AgentSessionID: "agent-x", RuntimeHandleID: "h1"},
		Activity: domain.Activity{State: state},
	}
}

func TestSpawn_ClaudeProfileOverrideIsStoredCanonicalAndLaunchedWith(t *testing.T) {
	f := newProfileFixture(t)

	rec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ClaudeProfile: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ClaudeProfile != "Work" {
		t.Fatalf("stored profile = %q, want the canonical Work", rec.ClaudeProfile)
	}
	if f.agent.lastLaunch.SettingsFile != f.work {
		t.Fatalf("launch settings file = %q, want %s", f.agent.lastLaunch.SettingsFile, f.work)
	}
	if f.rt.lastCfg.Env[agentViewEnv] != "1" {
		t.Fatalf("runtime env %s = %q, want 1", agentViewEnv, f.rt.lastCfg.Env[agentViewEnv])
	}
}

func TestSpawn_ClaudeProfileFallsBackToProjectDefaultThenSubscription(t *testing.T) {
	f := newProfileFixture(t)

	rec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ClaudeProfile != "Subscription" || f.agent.lastLaunch.SettingsFile != "" {
		t.Fatalf("no default: profile %q, settings file %q; want Subscription and none", rec.ClaudeProfile, f.agent.lastLaunch.SettingsFile)
	}

	project := f.st.projects["mer"]
	project.Config.ClaudeProfile = "Work"
	f.st.projects["mer"] = project
	rec, err = f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ClaudeProfile != "Work" || f.agent.lastLaunch.SettingsFile != f.work {
		t.Fatalf("project default: profile %q, settings file %q; want Work and %s", rec.ClaudeProfile, f.agent.lastLaunch.SettingsFile, f.work)
	}
}

func TestSpawn_NonClaudeHarnessIgnoresTheProfile(t *testing.T) {
	f := newProfileFixture(t)

	rec, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, ClaudeProfile: "nope"})
	if err != nil {
		t.Fatalf("a codex spawn failed on a Claude profile it does not use: %v", err)
	}
	if rec.ClaudeProfile != "" || f.agent.lastLaunch.SettingsFile != "" {
		t.Fatalf("codex: profile %q, settings file %q; want none", rec.ClaudeProfile, f.agent.lastLaunch.SettingsFile)
	}
}

func TestSpawn_RefusesAnUnusableProfileBeforeAnyRowExists(t *testing.T) {
	cases := map[string]error{"nope": claudeprofile.ErrUnknownProfile, "Broken": claudeprofile.ErrSettingsFile, "Gone": claudeprofile.ErrSettingsFile}
	for profile, want := range cases {
		t.Run(profile, func(t *testing.T) {
			f := newProfileFixture(t)
			_, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ClaudeProfile: profile})
			if !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
			if len(f.st.sessions) != 0 || f.st.num != 0 || f.rt.created != 0 {
				t.Fatalf("a refused spawn left state behind: rows=%d creates=%d runtimes=%d", len(f.st.sessions), f.st.num, f.rt.created)
			}
		})
	}
}

func TestSpawn_ProjectEnvCannotTurnTheAgentViewBackOn(t *testing.T) {
	f := newProfileFixture(t)
	project := f.st.projects["mer"]
	project.Config.Env = map[string]string{agentViewEnv: "0"}
	f.st.projects["mer"] = project

	if _, err := f.m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	if got := f.rt.lastCfg.Env[agentViewEnv]; got != "1" {
		t.Fatalf("runtime env %s = %q, want the adapter's 1 over the project's 0", agentViewEnv, got)
	}
}

func TestTodo_ClaudeProfileOverrideIsValidatedAtPrepareAndResolvedAtStart(t *testing.T) {
	f := newProfileFixture(t)

	if _, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ClaudeProfile: "nope"}); !errors.Is(err, claudeprofile.ErrUnknownProfile) {
		t.Fatalf("prepare with an unknown profile: %v, want ErrUnknownProfile", err)
	}
	if len(f.st.sessions) != 0 {
		t.Fatal("a refused prepare left a row")
	}

	withOverride, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "go", ClaudeProfile: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if withOverride.ClaudeProfile != "Work" {
		t.Fatalf("prepared override = %q, want Work", withOverride.ClaudeProfile)
	}
	inherits, err := f.m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}

	project := f.st.projects["mer"]
	project.Config.ClaudeProfile = "Work"
	f.st.projects["mer"] = project
	started, err := f.m.StartTodo(ctx, inherits.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.ClaudeProfile != "Work" || f.agent.lastLaunch.SettingsFile != f.work {
		t.Fatalf("started TODO: profile %q, settings %q; want the project default Work at Start", started.ClaudeProfile, f.agent.lastLaunch.SettingsFile)
	}

	cleared := ""
	updated, err := f.m.UpdateTodoSpec(ctx, withOverride.ID, ports.TodoSpecPatch{ClaudeProfile: &cleared})
	if err != nil || updated.ClaudeProfile != "" {
		t.Fatalf("clearing the override: %q, %v; want empty", updated.ClaudeProfile, err)
	}
	bad := "nope"
	if _, err := f.m.UpdateTodoSpec(ctx, withOverride.ID, ports.TodoSpecPatch{ClaudeProfile: &bad}); !errors.Is(err, claudeprofile.ErrUnknownProfile) {
		t.Fatalf("patching an unknown profile: %v, want ErrUnknownProfile", err)
	}
}

func TestRestart_RelaunchesWithTheCurrentProfileFileAndClearsThePendingFlag(t *testing.T) {
	f := newProfileFixture(t)
	f.liveClaudeSession("mer-1", "Work", domain.ActivityIdle)
	rec := f.st.sessions["mer-1"]
	rec.RestartPending = true
	f.st.sessions["mer-1"] = rec

	if _, err := f.m.Restart(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if f.agent.lastRestore.SettingsFile != f.work {
		t.Fatalf("restore settings file = %q, want %s", f.agent.lastRestore.SettingsFile, f.work)
	}
	if f.rt.lastCfg.Env[agentViewEnv] != "1" {
		t.Fatalf("relaunch env %s = %q, want 1", agentViewEnv, f.rt.lastCfg.Env[agentViewEnv])
	}
	if f.st.sessions["mer-1"].RestartPending {
		t.Fatal("the relaunch left restart_pending set")
	}
}

func TestRestart_LegacyRowWithNoProfileRelaunchesAsSubscription(t *testing.T) {
	f := newProfileFixture(t)
	f.liveClaudeSession("mer-1", "", domain.ActivityIdle)

	if _, err := f.m.Restart(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if f.agent.lastRestore.SettingsFile != "" {
		t.Fatalf("legacy row settings file = %q, want none", f.agent.lastRestore.SettingsFile)
	}
}

func TestRestart_BrokenProfileFileRefusesBeforeDestroyingTheRuntime(t *testing.T) {
	f := newProfileFixture(t)
	f.liveClaudeSession("mer-1", "Broken", domain.ActivityIdle)

	if _, err := f.m.Restart(ctx, "mer-1"); !errors.Is(err, claudeprofile.ErrSettingsFile) {
		t.Fatalf("Restart = %v, want ErrSettingsFile", err)
	}
	if f.rt.destroyed != 0 || f.st.sessions["mer-1"].IsTerminated {
		t.Fatalf("a refused restart touched the live agent: destroyed=%d terminated=%v", f.rt.destroyed, f.st.sessions["mer-1"].IsTerminated)
	}
}

func TestSetClaudeProfile(t *testing.T) {
	t.Run("idle session restarts now on the new profile", func(t *testing.T) {
		f := newProfileFixture(t)
		f.liveClaudeSession("mer-1", "Subscription", domain.ActivityIdle)
		rec, outcome, err := f.m.SetClaudeProfile(ctx, "mer-1", "work", true)
		if err != nil {
			t.Fatal(err)
		}
		if outcome != ClaudeProfileRestarted || rec.ClaudeProfile != "Work" || f.rt.destroyed != 1 || f.agent.lastRestore.SettingsFile != f.work {
			t.Fatalf("outcome %q profile %q destroyed %d settings %q; want restarted on Work", outcome, rec.ClaudeProfile, f.rt.destroyed, f.agent.lastRestore.SettingsFile)
		}
	})
	t.Run("parked session restarts now", func(t *testing.T) {
		f := newProfileFixture(t)
		f.liveClaudeSession("mer-1", "Subscription", domain.ActivityParked)
		if _, outcome, err := f.m.SetClaudeProfile(ctx, "mer-1", "Work", true); err != nil || outcome != ClaudeProfileRestarted {
			t.Fatalf("outcome %q, err %v; want restarted", outcome, err)
		}
	})
	for _, state := range []domain.ActivityState{domain.ActivityActive, domain.ActivityWaitingInput, domain.ActivityBackground} {
		t.Run(string(state)+" session waits for idle", func(t *testing.T) {
			f := newProfileFixture(t)
			f.liveClaudeSession("mer-1", "Subscription", state)
			rec, outcome, err := f.m.SetClaudeProfile(ctx, "mer-1", "Work", true)
			if err != nil {
				t.Fatal(err)
			}
			if outcome != ClaudeProfileRestartPending || !rec.RestartPending || rec.ClaudeProfile != "Work" || f.rt.destroyed != 0 {
				t.Fatalf("outcome %q pending %v profile %q destroyed %d; want pending on Work, agent untouched", outcome, rec.RestartPending, rec.ClaudeProfile, f.rt.destroyed)
			}
		})
	}
	t.Run("terminated session applies on its next launch", func(t *testing.T) {
		f := newProfileFixture(t)
		f.liveClaudeSession("mer-1", "Subscription", domain.ActivityExited)
		rec := f.st.sessions["mer-1"]
		rec.IsTerminated = true
		f.st.sessions["mer-1"] = rec
		got, outcome, err := f.m.SetClaudeProfile(ctx, "mer-1", "Work", true)
		if err != nil || outcome != ClaudeProfileNextLaunch || got.RestartPending || got.ClaudeProfile != "Work" || f.rt.created != 0 {
			t.Fatalf("outcome %q pending %v profile %q err %v; want next launch on Work, nothing relaunched", outcome, got.RestartPending, got.ClaudeProfile, err)
		}
	})
	t.Run("no restart asked applies on the next restart", func(t *testing.T) {
		f := newProfileFixture(t)
		f.liveClaudeSession("mer-1", "Subscription", domain.ActivityIdle)
		if _, outcome, err := f.m.SetClaudeProfile(ctx, "mer-1", "Work", false); err != nil || outcome != ClaudeProfileNextLaunch || f.rt.destroyed != 0 {
			t.Fatalf("outcome %q err %v destroyed %d; want next launch", outcome, err, f.rt.destroyed)
		}
	})
	t.Run("refusals change nothing", func(t *testing.T) {
		f := newProfileFixture(t)
		f.liveClaudeSession("mer-1", "Subscription", domain.ActivityIdle)
		codex := f.st.sessions["mer-1"]
		codex.ID, codex.Harness = "mer-2", domain.HarnessCodex
		f.st.sessions["mer-2"] = codex
		if _, _, err := f.m.SetClaudeProfile(ctx, "mer-2", "Work", true); !errors.Is(err, ErrClaudeProfileUnsupported) {
			t.Fatalf("codex: %v, want ErrClaudeProfileUnsupported", err)
		}
		if _, _, err := f.m.SetClaudeProfile(ctx, "mer-1", "nope", true); !errors.Is(err, claudeprofile.ErrUnknownProfile) {
			t.Fatalf("unknown: %v, want ErrUnknownProfile", err)
		}
		if _, _, err := f.m.SetClaudeProfile(ctx, "mer-1", "Broken", true); !errors.Is(err, claudeprofile.ErrSettingsFile) {
			t.Fatalf("broken file: %v, want ErrSettingsFile", err)
		}
		if f.st.sessions["mer-1"].ClaudeProfile != "Subscription" || f.rt.destroyed != 0 {
			t.Fatalf("a refusal changed the session: profile %q destroyed %d", f.st.sessions["mer-1"].ClaudeProfile, f.rt.destroyed)
		}
	})
}

func TestRestartPendingSessions(t *testing.T) {
	f := newProfileFixture(t)
	pending := func(id domain.SessionID, state domain.ActivityState) {
		f.liveClaudeSession(id, "Work", state)
		rec := f.st.sessions[id]
		rec.RestartPending = true
		f.st.sessions[id] = rec
	}
	pending("mer-1", domain.ActivityActive)
	pending("mer-2", domain.ActivityIdle)
	pending("mer-3", domain.ActivityExited)
	gone := f.st.sessions["mer-3"]
	gone.IsTerminated = true
	f.st.sessions["mer-3"] = gone
	pending("mer-4", domain.ActivityParked)
	asleep := f.st.sessions["mer-4"]
	asleep.IsSuspended = true
	f.st.sessions["mer-4"] = asleep
	pending("mer-5", domain.ActivityIdle)
	broken := f.st.sessions["mer-5"]
	broken.ClaudeProfile = "Broken"
	f.st.sessions["mer-5"] = broken

	if err := f.m.RestartPendingSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if !f.st.sessions["mer-1"].RestartPending || f.st.sessions["mer-1"].IsTerminated {
		t.Error("the active session was restarted or lost its pending flag; it must wait for idle")
	}
	if f.st.sessions["mer-2"].RestartPending || f.rt.created != 1 {
		t.Errorf("idle session: pending %v, relaunches %d; want restarted once and cleared", f.st.sessions["mer-2"].RestartPending, f.rt.created)
	}
	if f.st.sessions["mer-3"].RestartPending || f.st.sessions["mer-4"].RestartPending {
		t.Error("terminated and suspended sessions keep the flag; their next relaunch applies the profile")
	}
	if f.st.sessions["mer-5"].RestartPending {
		t.Error("a failed restart kept the flag, so the sweep would retry it forever")
	}

	f.rt.created = 0
	if err := f.m.RestartPendingSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if f.rt.created != 0 {
		t.Fatalf("a second sweep relaunched %d sessions, want 0", f.rt.created)
	}
}
