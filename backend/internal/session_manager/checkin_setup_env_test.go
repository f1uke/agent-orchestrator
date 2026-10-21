package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The Stop-hook refusal is on exactly where the check-in must show the
// preloaded skill's set-up: a dev on a standard or deep task, whose agent
// loads the installed skill, on a project with the check-in gate on.
func TestWithCheckInSetup_GatingMatrix(t *testing.T) {
	gated := domain.ProjectConfig{PauseBeforeImplementing: true}
	for _, tc := range []struct {
		name  string
		agent ports.Agent
		kind  domain.SessionKind
		size  domain.TaskSize
		role  domain.CrewRole
		cfg   domain.ProjectConfig
		want  bool
	}{
		{"standard solo dev, gate on", poteto(), domain.KindWorker, domain.TaskSizeStandard, "", gated, true},
		{"deep crew dev, gate on", poteto(), domain.KindWorker, domain.TaskSizeDeep, domain.CrewRoleDev, gated, true},
		{"unset size is standard", poteto(), domain.KindWorker, "", "", gated, true},
		{"gate off", poteto(), domain.KindWorker, domain.TaskSizeStandard, "", domain.ProjectConfig{}, false},
		{"mechanical", poteto(), domain.KindWorker, domain.TaskSizeMechanical, "", gated, false},
		{"qa", poteto(), domain.KindWorker, domain.TaskSizeStandard, domain.CrewRoleQA, gated, false},
		{"orchestrator", poteto(), domain.KindOrchestrator, domain.TaskSizeStandard, "", gated, false},
		{"skill not installed", &skillAgent{}, domain.KindWorker, domain.TaskSizeStandard, "", gated, false},
		{"agent that cannot load a skill (not Claude Code)", &recordingAgent{}, domain.KindWorker, domain.TaskSizeStandard, "", gated, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := withCheckInSetup(ctx, map[string]string{EnvCheckInSetup: "1"}, tc.agent, tc.kind, tc.size, tc.role, tc.cfg)
			if got := env[EnvCheckInSetup] == "1"; got != tc.want {
				t.Fatalf("%s set = %v, want %v (env %v)", EnvCheckInSetup, got, tc.want, env)
			}
			if _, set := env[EnvCheckInSetup]; set && !tc.want {
				t.Fatalf("%s must be absent, not empty, when off", EnvCheckInSetup)
			}
		})
	}
}

func gatedPreloadManager(t *testing.T, agent ports.Agent) (*Manager, *fakeStore, *fakeRuntime) {
	t.Helper()
	m, st := preloadManager(t, agent)
	p := st.projects["mer"]
	p.Config.PauseBeforeImplementing = true
	st.projects["mer"] = p
	return m, st, m.runtime.(*fakeRuntime)
}

// Every way a dev's agent is launched carries the flag: Spawn, StartTodo and
// a restore (resumed or relaunched).
func TestLaunches_CarryTheCheckInSetupFlag(t *testing.T) {
	t.Run("spawn", func(t *testing.T) {
		m, _, rt := gatedPreloadManager(t, poteto())
		if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing"}); err != nil {
			t.Fatal(err)
		}
		if rt.lastCfg.Env[EnvCheckInSetup] != "1" {
			t.Fatalf("spawned env %v lacks %s", rt.lastCfg.Env, EnvCheckInSetup)
		}
	})
	t.Run("start todo", func(t *testing.T) {
		m, _, rt := gatedPreloadManager(t, poteto())
		todo, err := m.PrepareTodo(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing", Harness: domain.HarnessClaudeCode})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.StartTodo(ctx, todo.ID); err != nil {
			t.Fatal(err)
		}
		if rt.lastCfg.Env[EnvCheckInSetup] != "1" {
			t.Fatalf("started env %v lacks %s", rt.lastCfg.Env, EnvCheckInSetup)
		}
	})
	for name, agentSessionID := range map[string]string{"restore, fresh relaunch": "", "restore, resume": "agent-x"} {
		t.Run(name, func(t *testing.T) {
			m, st, rt := gatedPreloadManager(t, poteto())
			restorableWorker(st, domain.TaskSizeStandard, "", agentSessionID)
			if _, err := m.Restore(ctx, "mer-1"); err != nil {
				t.Fatal(err)
			}
			for _, flag := range []string{EnvCheckInSetup, EnvPROpenGate} {
				if rt.lastCfg.Env[flag] != "1" {
					t.Fatalf("restored env %v lacks %s", rt.lastCfg.Env, flag)
				}
			}
		})
	}
	t.Run("restored qa", func(t *testing.T) {
		m, st, rt := gatedPreloadManager(t, poteto())
		restorableWorker(st, domain.TaskSizeStandard, domain.CrewRoleQA, "agent-x")
		if _, err := m.Restore(ctx, "mer-1"); err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{EnvCheckInSetup, EnvPROpenGate} {
			if _, set := rt.lastCfg.Env[flag]; set {
				t.Fatalf("a qa must not be checked: %v", rt.lastCfg.Env)
			}
		}
	})
}

func TestWithPROpenGate_GatingMatrix(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent ports.Agent
		kind  domain.SessionKind
		size  domain.TaskSize
		role  domain.CrewRole
		want  bool
	}{
		{"standard solo dev", poteto(), domain.KindWorker, domain.TaskSizeStandard, "", true},
		{"deep crew dev", poteto(), domain.KindWorker, domain.TaskSizeDeep, domain.CrewRoleDev, true},
		{"unset size is standard", poteto(), domain.KindWorker, "", "", true},
		{"mechanical", poteto(), domain.KindWorker, domain.TaskSizeMechanical, "", false},
		{"qa", poteto(), domain.KindWorker, domain.TaskSizeStandard, domain.CrewRoleQA, false},
		{"orchestrator", poteto(), domain.KindOrchestrator, domain.TaskSizeStandard, "", false},
		{"skill not installed", &skillAgent{}, domain.KindWorker, domain.TaskSizeStandard, "", false},
		{"agent that cannot load a skill (not Claude Code)", &recordingAgent{}, domain.KindWorker, domain.TaskSizeStandard, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := withPROpenGate(ctx, map[string]string{EnvPROpenGate: "1"}, tc.agent, tc.kind, tc.size, tc.role)
			if got := env[EnvPROpenGate] == "1"; got != tc.want {
				t.Fatalf("%s set = %v, want %v (env %v)", EnvPROpenGate, got, tc.want, env)
			}
			if _, set := env[EnvPROpenGate]; set && !tc.want {
				t.Fatalf("%s must be absent, not empty, when off", EnvPROpenGate)
			}
		})
	}
}

func TestSpawn_CarriesThePROpenGateWithoutTheCheckInGate(t *testing.T) {
	m, _ := preloadManager(t, poteto())
	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing"}); err != nil {
		t.Fatal(err)
	}
	env := m.runtime.(*fakeRuntime).lastCfg.Env
	if env[EnvPROpenGate] != "1" {
		t.Fatalf("spawned env %v lacks %s", env, EnvPROpenGate)
	}
	if _, set := env[EnvCheckInSetup]; set {
		t.Fatalf("ungated project got %s", EnvCheckInSetup)
	}
}
