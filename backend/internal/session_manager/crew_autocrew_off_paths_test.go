package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// THE OTHER TWO DOORS INTO A DEV'S SYSTEM PROMPT.
//
// crew_autocrew_off_test.go pins the flag at the door a task usually comes
// through: Spawn. It is not the only one. A dev's standing instructions are
// composed at THREE call sites - Spawn, StartTodo (a task staged with `ao spawn
// --todo` and started later) and relaunchRestoredSession (any restore, resume,
// restart or boot pass) - and each one answers "whose prompt is this?" for
// itself. A flag that is right at one door and wrong at another fails the same
// silent way the feature exists to prevent: nothing errors, and the human simply
// never gets a smoke checklist.
//
// These two tests stand at the other two doors.

// crewOffAgentManager is a manager whose single agent RECORDS what it was
// launched with, on a project that never forms a crew by itself.
func crewOffAgentManager(t *testing.T) (*Manager, *fakeStore, *recordingAgent) {
	t.Helper()
	st := newFakeStore()
	cfg := testRoleAgents()
	cfg.DisableAutoCrew = true
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	return m, st, agent
}

// DOOR TWO: A TASK THAT WAS STAGED BEFORE IT WAS STARTED.
//
// `ao spawn --todo` writes a row and launches nothing; StartTodo composes the
// prompt later, from the replayed spec, at its own call site. A human who stages
// their work has no reason to expect a different agent than one who spawns it
// directly - and getting this wrong is invisible, because a staged task looks
// identical from the outside right up to the point where no checklist is ever
// written.
func TestStartTodo_CrewOffProjectStartsDevWithTheSoloPrompt(t *testing.T) {
	for _, size := range []domain.TaskSize{domain.TaskSizeStandard, domain.TaskSizeDeep} {
		t.Run(string(size), func(t *testing.T) {
			m, st, agent := crewOffAgentManager(t)

			todo, err := m.PrepareTodo(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Prompt: "staged work",
				Harness: domain.HarnessClaudeCode, TaskSize: size,
			})
			if err != nil {
				t.Fatalf("PrepareTodo: %v", err)
			}
			if _, err := m.StartTodo(ctx, todo.ID); err != nil {
				t.Fatalf("StartTodo: %v", err)
			}
			if len(st.sessions) != 1 {
				t.Fatalf("a started %q TODO on a crew-off project is %d rows, want 1", size, len(st.sessions))
			}

			launched := agent.lastLaunch.SystemPrompt
			// The duty nobody else is coming to take.
			if !strings.Contains(launched, "## Smoke-test checklist (AO)") {
				t.Fatalf("a staged task's dev cannot author the checklist nobody else will:\n%s", launched)
			}
			// The instruction that hands the list to a qa this project never creates.
			for _, unwanted := range []string{
				"## Your crewmate (AO)",
				"The checklist is yours only while you have no qa",
			} {
				if strings.Contains(launched, unwanted) {
					t.Fatalf("a staged task's dev was told %q on a crew-off project:\n%s", unwanted, launched)
				}
			}
		})
	}
}

// DOOR THREE: A CREW THAT WAS ALREADY WORKING WHEN THE SWITCH WAS FLIPPED.
//
// Turning automatic crew off is a statement about FUTURE tasks. A task that is
// already two agents in one worktree keeps both, and this is the seam where that
// promise is actually kept or broken: a restore recomputes the system prompt from
// scratch, so if it asked eligibility - which now answers "no crew here" - a dev
// with a LIVE qa beside it would come back from a restart believing it is alone.
// It would then write the checklist qa owns and have it refused by AO, which is
// the #242 bug in the mirror.
//
// It is right for the opposite reason to the two doors above: the prompt is built
// from the ROW's crew role, which is a fact about this task, not from the
// project's appetite for new ones.
func TestRestore_CrewOffProjectLeavesAWorkingCrewsDevAlone(t *testing.T) {
	m, st, agent := crewOffAgentManager(t)

	// A task that became a crew BEFORE the project's switch was flipped: dev and
	// its qa, in one worktree, now terminated and being brought back.
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		TaskSize: domain.TaskSizeStandard,
		CrewID:   "mer-1", CrewRole: domain.CrewRoleDev, CrewJoinReason: domain.CrewJoinSim,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x", Prompt: "build the thing",
		},
		IsTerminated: true,
		Activity:     domain.Activity{State: domain.ActivityExited},
	}

	if _, err := m.Restore(ctx, "mer-1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored := agent.lastRestore.SystemPrompt
	if !strings.Contains(restored, "## Your crewmate (AO)") {
		t.Fatalf("a dev restored beside its live qa was handed the SOLO prompt because the project stopped forming new crews:\n%s", restored)
	}
	// And the row itself is untouched: setting the flag never stands a crew down.
	if row := st.sessions["mer-1"]; !row.InCrew() || !row.CrewRole.IsDev() {
		t.Fatalf("restored row crew=%q role=%q, want the crew it already had", row.CrewID, row.CrewRole)
	}
}
