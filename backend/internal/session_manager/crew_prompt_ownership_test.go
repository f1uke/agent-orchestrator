package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

// WHO OWNS THE CHECKLIST, AND WHO KNOWS THE VERB.
//
// A system prompt is fixed when a runtime launches; crew membership is not. That
// gap was survivable while dev's crew block was informational. It is not now: the
// block is the ONLY place dev learns that `ao crew review` exists, and no brief
// anywhere names it (#253's post-mortem found no AO prompt that taught an agent
// `ao crew add`, which is precisely how a project's own policy got walked past).
//
// A dev's prompt has THREE composition sites, not one - Spawn, StartTodo and
// relaunchRestoredSession - and getting one of the three is not getting it.

// TestPromptCrewRoleOf_ARestoredDevKeepsTheVerb is the third site, and the bug it
// pins is silent.
//
// A `standard` dev with no qa yet carries NO crew columns: membership is written
// when a member is created, and under lazy creation that may be never. A restore
// that read the ROW therefore composed the SOLO prompt, and a restored dev
// stopped knowing how to ask for the qa it is the only one able to ask for.
func TestPromptCrewRoleOf_ARestoredDevKeepsTheVerb(t *testing.T) {
	project := domain.ProjectRecord{ID: "mer"}
	lone := domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard,
	}
	if got := promptCrewRoleOf(project, lone); got != domain.CrewRoleDev {
		t.Fatalf("a restored standard dev with no qa yet composes as %q, want %q", got, domain.CrewRoleDev)
	}
	// It agrees with the spawn seam, which is the whole point: one task's dev gets
	// one prompt whichever of the three sites built it.
	spawned := promptCrewRole(project, ports.SpawnConfig{Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard})
	if spawned != promptCrewRoleOf(project, lone) {
		t.Fatalf("Spawn composes %q and restore composes %q for the same task", spawned, promptCrewRoleOf(project, lone))
	}
}

// TestPromptCrewRoleOf_TheRowWinsWhenItSaysSomething. A real member is a fact,
// and eligibility only answers where the row is silent - otherwise a restored qa
// on a crew-eligible project would come back holding dev's prompt.
func TestPromptCrewRoleOf_TheRowWinsWhenItSaysSomething(t *testing.T) {
	project := domain.ProjectRecord{ID: "mer"}
	qa := domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard,
		CrewID: "mer-1", CrewRole: domain.CrewRoleQA,
	}
	if got := promptCrewRoleOf(project, qa); got != domain.CrewRoleQA {
		t.Fatalf("a restored qa composes as %q, want %q", got, domain.CrewRoleQA)
	}
}

// TestPromptCrewRoleOf_LeavesASoloTaskSolO. The population that must stay
// byte-for-byte what it was: a mechanical task, a crew-off project, an
// orchestrator, a workspace project. None of them may ever be taught a verb they
// would be refused.
func TestPromptCrewRoleOf_LeavesASoloTaskSolo(t *testing.T) {
	cases := []struct {
		name    string
		project domain.ProjectRecord
		rec     domain.SessionRecord
	}{
		{
			name:    "mechanical",
			project: domain.ProjectRecord{ID: "mer"},
			rec:     domain.SessionRecord{ID: "mer-1", Kind: domain.KindWorker, TaskSize: domain.TaskSizeMechanical},
		},
		{
			name:    "a crew-off project",
			project: domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{DisableAutoCrew: true}},
			rec:     domain.SessionRecord{ID: "mer-1", Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard},
		},
		{
			name:    "an orchestrator",
			project: domain.ProjectRecord{ID: "mer"},
			rec:     domain.SessionRecord{ID: "mer-1", Kind: domain.KindOrchestrator},
		},
		{
			name:    "a workspace project",
			project: domain.ProjectRecord{ID: "mer", Kind: domain.ProjectKindWorkspace},
			rec:     domain.SessionRecord{ID: "mer-1", Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := promptCrewRoleOf(tc.project, tc.rec); got != "" {
				t.Fatalf("composed as %q, want the solo prompt", got)
			}
		})
	}
}

// TestBuildSystemPrompt_SoloWorkerIsUnchangedByTheCrewBlock. The crew block is
// the only thing this change adds to a worker's prompt, so removing it from an
// eligible dev's prompt must leave exactly the prompt an ineligible one gets.
// Anything else means the change leaked into every solo session in the fleet.
func TestBuildSystemPrompt_SoloWorkerIsUnchangedByTheCrewBlock(t *testing.T) {
	build := func(t *testing.T, role domain.CrewRole) string {
		t.Helper()
		m := layeredManager(crewPromptStore(t), nil)
		sp, err := m.buildSystemPrompt(ctx, systemPromptSpec{
			Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: role,
		})
		if err != nil {
			t.Fatalf("buildSystemPrompt: %v", err)
		}
		return sp
	}
	dev, solo := build(t, domain.CrewRoleDev), build(t, "")
	// The crew block AND the simulator handover note: both are dev-only, and the
	// store this builds against has a simulator.
	stripped := strings.Replace(dev, prompts.CrewProtocol("dev"), "", 1)
	stripped = strings.Replace(stripped, prompts.SimulatorHandoverToQA(), "", 1)
	if stripped != solo {
		t.Fatalf("a solo worker's prompt is not byte-for-byte what a crew dev's is minus the crew blocks:\n--- dev minus crew blocks ---\n%s\n--- solo ---\n%s", stripped, solo)
	}
	// And the verb lives in exactly one of them.
	if strings.Contains(solo, "ao crew review") {
		t.Fatalf("a solo worker was taught a verb it would be refused:\n%s", solo)
	}
	if !strings.Contains(dev, "ao crew review") {
		t.Fatalf("a crew dev was not taught the one verb that gets it a qa:\n%s", dev)
	}
}

// TestAttachCrewMember_TellsDevItsPromptIsNowWrong is the live half of the same
// bug, and the one that is destructive rather than merely stale.
//
// A dev launched with the SOLO prompt has been told the smoke checklist is its
// own and handed `ao smoke set`, which REPLACES the whole list. The moment a
// person attaches a qa that instruction deletes the crewmate's cases. The prompt
// cannot be rewritten under a running agent, so the correction is delivered the
// one way a live agent can receive one.
func TestAttachCrewMember_TellsDevItsPromptIsNowWrong(t *testing.T) {
	m, _, _, _, msgr := newManagerWithMessenger()
	dev := spawnMechanical(t, m)

	if _, err := m.AttachCrewMember(ctx, dev.ID, domain.CrewRoleQA, ""); err != nil {
		t.Fatalf("AttachCrewMember: %v", err)
	}

	got := msgr.sentTo(dev.ID)
	if len(got) != 1 {
		t.Fatalf("dev was told %d times that it gained a crewmate, want 1: %q", len(got), got)
	}
	for _, want := range []string{
		"[AO]",                 // attributed to AO, not to a person or the new member
		"qa",                   // what joined
		"Never `ao smoke set`", // the instruction its own prompt got wrong
		"ao smoke add",         // and what to do instead
		"ao send --crew qa",    // how to reach it
		"one git index",        // the other thing a solo prompt does not know
	} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("the notice to dev is missing %q:\n%s", want, got[0])
		}
	}
}

// TestAttachCrewMember_SaysNothingToADevThatAskedForIt. dev running
// `ao crew review` reads the answer on its own stdout; a second copy delivered
// into its turn would be noise, and noise is how a real correction stops being
// read.
func TestAttachCrewMember_SaysNothingToADevThatAskedForIt(t *testing.T) {
	m, st, _, _, msgr := newManagerWithMessenger()
	dev := standardDev(t, m, st)

	if _, err := m.RequestCrewReview(ctx, dev.ID, domain.CrewRoleQA); err != nil {
		t.Fatalf("RequestCrewReview: %v", err)
	}

	if got := msgr.sentTo(dev.ID); len(got) != 0 {
		t.Fatalf("dev was messaged about a qa it asked for itself: %q", got)
	}
}
