package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// skillAgent is a recordingAgent that can load skills from a leading command,
// the way Claude Code does, for exactly the skills it has installed.
type skillAgent struct {
	recordingAgent
	installed map[string]bool
}

func (a *skillAgent) SkillCommand(_ context.Context, skill string) (string, bool) {
	if !a.installed[skill] {
		return "", false
	}
	return "/" + skill, true
}

const preloadedBrief = "/pstack:poteto-mode\n\nbuild the thing"

func preloadManager(t *testing.T, agent ports.Agent) (*Manager, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	return m, st
}

func poteto() *skillAgent {
	return &skillAgent{installed: map[string]bool{devPreloadSkill: true}}
}

func TestSpawn_PreloadsPotetoModeOnlyForAStandardOrDeepDev(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  ports.SpawnConfig
		want string
	}{
		{"standard dev", ports.SpawnConfig{Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard}, preloadedBrief},
		{"deep dev", ports.SpawnConfig{Kind: domain.KindWorker, TaskSize: domain.TaskSizeDeep}, preloadedBrief},
		{"unset size is standard", ports.SpawnConfig{Kind: domain.KindWorker}, preloadedBrief},
		{"mechanical", ports.SpawnConfig{Kind: domain.KindWorker, TaskSize: domain.TaskSizeMechanical}, "build the thing"},
		{"orchestrator", ports.SpawnConfig{Kind: domain.KindOrchestrator}, "build the thing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := poteto()
			m, _ := preloadManager(t, agent)
			tc.cfg.ProjectID, tc.cfg.Prompt = "mer", "build the thing"
			if _, err := m.Spawn(ctx, tc.cfg); err != nil {
				t.Fatal(err)
			}
			if got := agent.lastLaunch.Prompt; got != tc.want {
				t.Fatalf("launched with %q, want %q", got, tc.want)
			}
		})
	}
}

// The prefix is how the launch opens, not part of the task: the row keeps the
// brief as written, so the board, a restore and a TODO replay all read the
// human's text.
func TestSpawn_PreloadIsNotPersistedIntoTheBrief(t *testing.T) {
	agent := poteto()
	m, st := preloadManager(t, agent)
	rec, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing"})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[rec.ID].Metadata.Prompt; got != "build the thing" {
		t.Fatalf("persisted prompt %q, want the brief alone", got)
	}
}

func TestSpawn_NoPreloadWhenTheSkillIsMissingOrTheAgentCannotLoadOne(t *testing.T) {
	for name, agent := range map[string]interface {
		ports.Agent
		launched() ports.LaunchConfig
	}{
		"skill not installed": &skillAgent{},
		"not a skill invoker": &recordingAgent{},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := preloadManager(t, agent)
			if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing"}); err != nil {
				t.Fatal(err)
			}
			if got := agent.launched().Prompt; got != "build the thing" {
				t.Fatalf("launched with %q, want the brief unchanged", got)
			}
		})
	}
}

// A promptless worker lands at an empty input box; a bare skill command would
// hand it a turn nobody asked for.
func TestSpawn_PromptlessDevIsNotPreloaded(t *testing.T) {
	agent := poteto()
	m, _ := preloadManager(t, agent)
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	if got := agent.lastLaunch.Prompt; got != "" {
		t.Fatalf("promptless dev launched with %q", got)
	}
}

func TestStartTodo_PreloadsPotetoModeForAStagedDev(t *testing.T) {
	for _, tc := range []struct {
		size domain.TaskSize
		want string
	}{
		{domain.TaskSizeStandard, preloadedBrief},
		{domain.TaskSizeDeep, preloadedBrief},
		{domain.TaskSizeMechanical, "build the thing"},
	} {
		t.Run(string(tc.size), func(t *testing.T) {
			agent := poteto()
			m, _ := preloadManager(t, agent)
			todo, err := m.PrepareTodo(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing",
				Harness: domain.HarnessClaudeCode, TaskSize: tc.size,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.StartTodo(ctx, todo.ID); err != nil {
				t.Fatal(err)
			}
			if got := agent.lastLaunch.Prompt; got != tc.want {
				t.Fatalf("started with %q, want %q", got, tc.want)
			}
		})
	}
}

func restorableWorker(st *fakeStore, size domain.TaskSize, role domain.CrewRole, agentSessionID string) {
	rec := domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		TaskSize: size,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: agentSessionID, Prompt: "build the thing",
		},
		IsTerminated: true,
		Activity:     domain.Activity{State: domain.ActivityExited},
	}
	if role != "" {
		rec.CrewID, rec.CrewRole, rec.CrewJoinReason = "mer-1", role, domain.CrewJoinSim
	}
	st.sessions["mer-1"] = rec
}

// A restore that cannot resume relaunches the brief from scratch, so the skill
// the old conversation had is gone and the dev must load it again.
func TestRestore_FreshRelaunchPreloadsADevAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		size domain.TaskSize
		role domain.CrewRole
		want string
	}{
		{"standard dev", domain.TaskSizeStandard, "", preloadedBrief},
		{"deep crew dev", domain.TaskSizeDeep, domain.CrewRoleDev, preloadedBrief},
		{"mechanical", domain.TaskSizeMechanical, "", "build the thing"},
		{"qa", domain.TaskSizeStandard, domain.CrewRoleQA, "build the thing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := poteto()
			m, st := preloadManager(t, agent)
			restorableWorker(st, tc.size, tc.role, "")
			if _, err := m.Restore(ctx, "mer-1"); err != nil {
				t.Fatal(err)
			}
			if got := agent.lastLaunch.Prompt; got != tc.want {
				t.Fatalf("relaunched with %q, want %q", got, tc.want)
			}
		})
	}
}

// A resumed conversation already holds the skill it loaded at spawn, and a
// resume sends no prompt at all, so nothing loads it a second time.
func TestRestore_ResumeDoesNotPreloadAgain(t *testing.T) {
	agent := poteto()
	m, st := preloadManager(t, agent)
	restorableWorker(st, domain.TaskSizeStandard, "", "agent-x")
	if _, err := m.Restore(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if agent.lastLaunch.SessionID != "" {
		t.Fatalf("a resumable dev was relaunched fresh with %q", agent.lastLaunch.Prompt)
	}
	if agent.lastRestore.Session.Metadata[ports.MetadataKeyAgentSessionID] != "agent-x" {
		t.Fatalf("restore did not resume the conversation: %+v", agent.lastRestore)
	}
}
