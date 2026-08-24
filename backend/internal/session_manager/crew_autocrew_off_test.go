package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A PROJECT THAT FORMS NO CREW BY ITSELF.
//
// `ProjectConfig.DisableAutoCrew` turns off AUTOMATIC crew formation for one
// project and nothing else. It is emphatically NOT `--task-size mechanical`:
// mechanical also authorizes an agent to skip the brainstorm -> plan -> TDD
// ceremony, and a `standard` or `deep` task on a crew-off project must still get
// the full ceremony - just with nobody else in the crew.
//
// The tests below pin the three things that can go wrong.

// crewOffManager is newManager with automatic crew formation turned off for the
// project, and nothing else changed.
func crewOffManager(t *testing.T) (*Manager, *fakeStore, *fakeRuntime, *fakeWorkspace) {
	t.Helper()
	m, st, rt, ws := newManager()
	cfg := testRoleAgents()
	cfg.DisableAutoCrew = true
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	return m, st, rt, ws
}

// THE TEST THIS FEATURE EXISTS FOR.
//
// Spawn builds dev's system prompt BEFORE anything could create a qa, from the
// spawn's INTENT (promptCrewRole). If the flag were read only where qa is
// created, dev would already have been handed the CREW prompt - which since #240
// tells dev that the smoke checklist belongs to qa and that `ao smoke set` from
// it is refused. On a project that will never create a qa that means NOBODY
// writes the checklist, silently, with no error anywhere.
//
// So the flag has to be resolved at the eligibility seam both the prompt and the
// trigger read, and dev's prompt on a crew-off project must be the SOLO one: no
// crew block, no "the checklist is qa's", and the checklist protocol it has
// always had.
func TestSpawn_CrewOffProjectLaunchesDevWithTheSoloPrompt(t *testing.T) {
	for _, size := range []domain.TaskSize{domain.TaskSizeStandard, domain.TaskSizeDeep} {
		t.Run(string(size), func(t *testing.T) {
			st := newFakeStore()
			cfg := testRoleAgents()
			cfg.DisableAutoCrew = true
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
			agent := &recordingAgent{}
			lookPath := func(string) (string, error) { return "/bin/true", nil }
			m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

			if _, err := m.Spawn(ctx, ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Prompt: "build the thing", TaskSize: size,
			}); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			if len(st.sessions) != 1 {
				t.Fatalf("a crew-off spawn produced %d rows, want exactly 1", len(st.sessions))
			}

			launched := agent.lastLaunch.SystemPrompt
			// THE DUTY IT MUST KEEP. Nobody else is coming, so dev owns the list.
			if !strings.Contains(launched, "## Smoke-test checklist (AO)") {
				t.Fatalf("dev on a crew-off project cannot author the checklist nobody else will:\n%s", launched)
			}
			// THE INSTRUCTION IT MUST NOT GET. Every sentence here hands the list to
			// an agent this project never creates.
			for _, unwanted := range []string{
				"The checklist is yours only while you have no qa",
				"REFUSED by AO for as long as a qa is on this task",
				"## Your crewmate (AO)",
				"AO creates a qa the first time you touch the app's runtime",
			} {
				if strings.Contains(launched, unwanted) {
					t.Fatalf("dev on a crew-off project was told %q, about a qa AO will never create:\n%s", unwanted, launched)
				}
			}
		})
	}
}

// CREW-OFF IS NOT MECHANICAL. The task-size directive is the one block that
// authorizes skipping the process skills, and it renders for `mechanical` only.
// A crew-off `standard` task must not gain it: the human asked for one agent, not
// for less rigor.
func TestBuildSystemPrompt_CrewOffDoesNotStripCeremony(t *testing.T) {
	m, _, _, _ := crewOffManager(t)

	got, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, promptCrewRole(
		domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{DisableAutoCrew: true}},
		ports.SpawnConfig{Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard},
	))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "## Task size: mechanical (AO)") {
		t.Fatalf("a standard task on a crew-off project was demoted to mechanical:\n%s", got)
	}
}

// THE TRIGGER IS OFF. dev claiming the simulator is what creates a qa on an
// ordinary project; on a crew-off one it must create nothing and say nothing -
// the touch is a command about something else, and failing it would be worse
// than forming no crew.
func TestNoteRuntimeTouch_CrewOffProjectCreatesNoQA(t *testing.T) {
	for _, reason := range []domain.CrewJoinReason{domain.CrewJoinSim, domain.CrewJoinPreview} {
		t.Run(string(reason), func(t *testing.T) {
			m, st, rt, _ := crewOffManager(t)
			dev := standardDev(t, m, st)
			if rt.aliveByHandle == nil {
				rt.aliveByHandle = map[string]bool{}
			}
			rt.aliveByHandle[dev.Metadata.RuntimeHandleID] = true

			m.NoteRuntimeTouch(ctx, dev.ID, reason)

			if len(st.sessions) != 1 {
				t.Fatalf("a %s touch on a crew-off project produced %d rows, want 1", reason, len(st.sessions))
			}
			if st.sessions[dev.ID].InCrew() {
				t.Fatalf("a %s touch put dev in a crew on a crew-off project", reason)
			}
		})
	}
}

// THE ESCAPE HATCH SURVIVES. The whole point of turning off AUTOMATIC crew rather
// than crew is that a human can still opt ONE task into a qa by hand - `ao crew
// add`, or the topbar's `+ qa`. That path does not run the eligibility test the
// flag gates (resolveCrewDev), and this test is what keeps it that way.
func TestAddCrewMember_StillWorksOnACrewOffProject(t *testing.T) {
	m, st, _, _ := crewOffManager(t)
	dev := standardDev(t, m, st)

	qa, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA)
	if err != nil {
		t.Fatalf("a human could not add a qa by hand on a crew-off project: %v", err)
	}
	if qa.CrewRole != domain.CrewRoleQA || qa.CrewID != dev.ID {
		t.Fatalf("manual qa row role=%q crew=%q, want qa/%s", qa.CrewRole, qa.CrewID, dev.ID)
	}
	// `manual` is the reason the flag must NOT block; sim/preview are the two it
	// does.
	if qa.CrewJoinReason != domain.CrewJoinManual {
		t.Fatalf("manual qa join reason = %q, want %q", qa.CrewJoinReason, domain.CrewJoinManual)
	}
}

// THE FLAG IS PER PROJECT. A second project on the same daemon keeps forming
// crews, so turning it off is never a global switch by accident.
func TestNoteRuntimeTouch_OtherProjectsStillFormCrews(t *testing.T) {
	m, st, rt, _ := crewOffManager(t)
	st.projects["other"] = domain.ProjectRecord{ID: "other", Config: testRoleAgents()}

	dev, err := m.Spawn(ctx, ports.SpawnConfig{
		ProjectID: "other", Kind: domain.KindWorker, Prompt: "build the thing", TaskSize: domain.TaskSizeStandard,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rt.aliveByHandle == nil {
		rt.aliveByHandle = map[string]bool{}
	}
	rt.aliveByHandle[dev.Metadata.RuntimeHandleID] = true

	m.NoteRuntimeTouch(ctx, dev.ID, domain.CrewJoinSim)

	if !st.sessions[dev.ID].InCrew() {
		t.Fatal("turning automatic crew off for one project turned it off for another")
	}
}
