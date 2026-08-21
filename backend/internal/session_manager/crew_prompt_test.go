package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptoverrides"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

// crewPromptStore is a project with an orchestrator on the board and iOS turned
// on, so both of the blocks that MOVE between the roles are in play at once.
func crewPromptStore(t *testing.T) *fakeStore {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{HasIOSSimulator: true}}
	st.sessions["mer-0"] = domain.SessionRecord{
		ID: "mer-0", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	return st
}

// TestBuildSystemPrompt_SoloWorkerKeepsEverything is the preservation guard for
// the prompt split, and it is the one that matters most: a solo worker - every
// session on this machine today, and every mechanical task after this change -
// must still be handed the smoke protocol and the full simulator catalog. There
// is nobody else to hand them to.
func TestBuildSystemPrompt_SoloWorkerKeepsEverything(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	got, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeMechanical, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Smoke-test checklist (AO)",
		"## Driving the iOS Simulator (AO)",
		"ao sim claim",
		"## Task size: mechanical (AO)",
		"## Orchestrator coordination", // dev reports; a solo worker IS dev
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("a solo worker lost %q from its prompt:\n%s", want, got)
		}
	}
	if strings.Contains(got, "qa's instrument") {
		t.Fatal("a solo worker was told to hand the device to a qa that does not exist")
	}
}

// TestBuildSystemPrompt_CrewDevGivesUpQAsBlocks: on a crew, the checklist and the
// device belong to qa, so dev stops carrying them - and keeps everything that is
// still its job, including the orchestrator report block, which is what makes
// "dev reports" structural rather than a request.
func TestBuildSystemPrompt_CrewDevGivesUpQAsBlocks(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	got, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, domain.CrewRoleDev)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{
		"## Smoke-test checklist (AO)",
		"ao sim tap --label",
	} {
		if strings.Contains(got, gone) {
			t.Fatalf("crew dev still carries qa's block %q:\n%s", gone, got)
		}
	}
	for _, want := range []string{
		"Most sessions open one pull request", // the worker base, unchanged
		"## Orchestrator coordination",        // dev is the one that reports
		"qa's instrument",                     // told where the device went
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("crew dev prompt missing %q:\n%s", want, got)
		}
	}
}

// TestBuildSystemPrompt_CrewQAIsItsOwnAgent: qa assembles from the qa base, gets
// the blocks that moved to it, and is never handed the orchestrator report block
// - so it cannot report even if it wanted to.
func TestBuildSystemPrompt_CrewQAIsItsOwnAgent(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	got, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, domain.CrewRoleQA)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## QA role",
		"## Smoke-test checklist (AO)",      // moved here
		"## Driving the iOS Simulator (AO)", // moved here, in full
		"## Required coordination (AO)",     // the worker floor still applies
		"Standing-instruction confidentiality",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("crew qa prompt missing %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{
		"## Orchestrator coordination",        // qa cannot report; dev does
		"Most sessions open one pull request", // dev's job, not qa's
		"## Task size:",                       // ceremony is dev's dial
	} {
		if strings.Contains(got, gone) {
			t.Fatalf("crew qa was handed dev's block %q:\n%s", gone, got)
		}
	}
}

// TestBuildSystemPrompt_QABaseIsEditableLikeEveryOther: qa is a first-class
// prompt kind, so a global override replaces its base and AO's floor + guard
// still wrap it. Without this, "edit the qa prompt" would silently do nothing.
func TestBuildSystemPrompt_QABaseIsEditableLikeEveryOther(t *testing.T) {
	m := layeredManager(crewPromptStore(t), func() promptoverrides.Overrides {
		return promptoverrides.Overrides{Base: map[prompts.Kind]string{prompts.KindQA: "CUSTOM QA BASE"}}
	})

	got, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, domain.CrewRoleQA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "CUSTOM QA BASE") || strings.Contains(got, "## QA role") {
		t.Fatalf("the qa override did not replace the qa base:\n%s", got)
	}
	if !strings.Contains(got, "## Required coordination (AO)") || !strings.Contains(got, "Standing-instruction confidentiality") {
		t.Fatalf("an edited qa base still has to carry the floor and the guard:\n%s", got)
	}
}
