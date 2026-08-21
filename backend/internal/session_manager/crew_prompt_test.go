package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

// The record -> flow -> retire loop is qa's, and it is DEVICE work: it goes to
// the member that drives the device, on a project that has one.
func TestBuildSystemPrompt_OnlyQAGetsTheRecordedFlowLoop(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	qa, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, domain.CrewRoleQA)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Turning a played scenario into a test (AO)",
		"ao sim record start --name",
		"ao smoke retire",
	} {
		if !strings.Contains(qa, want) {
			t.Fatalf("qa was not given the recorded-flow loop (%q):\n%s", want, qa)
		}
	}

	// dev hands the device over; a SOLO worker has nobody to ask for a play and
	// keeps the prompt it had.
	for _, role := range []domain.CrewRole{domain.CrewRoleDev, ""} {
		got, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, role)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "## Turning a played scenario into a test (AO)") {
			t.Fatalf("role %q was handed qa's recorded-flow loop:\n%s", role, got)
		}
	}
}

// A project with no simulator gets no device instructions at all: every command
// in the loop fails on that machine, and an instruction an agent cannot follow
// is worse than none - the same rule SimulatorGuidance is gated by.
func TestBuildSystemPrompt_NoSimulatorMeansNoRecordedFlowLoop(t *testing.T) {
	st := crewPromptStore(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{HasIOSSimulator: false}}
	m := layeredManager(st, nil)

	got, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, domain.CrewRoleQA)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "## Turning a played scenario into a test (AO)") {
		t.Fatalf("qa on a project with no device was told to record a device flow:\n%s", got)
	}
	// It still owns the checklist - that part has nothing to do with a device.
	if !strings.Contains(got, "## Smoke-test checklist (AO)") {
		t.Fatalf("qa lost the checklist protocol on a non-iOS project:\n%s", got)
	}
}

// LAYER 1 of the three-layer fix. dev's prompt used to say NOTHING about the
// checklist, so a brief asking it to author one met no contradiction and was
// simply obeyed - in both real crew runs. The block does not prevent that (the
// `ao smoke set` refusal does); it makes the override visible in dev's own
// output.
func TestBuildSystemPrompt_CrewDevIsToldTheChecklistIsQAs(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	dev, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, domain.CrewRoleDev)
	if err != nil {
		t.Fatal(err)
	}
	// It still does not carry the protocol itself - the negative REPLACES it.
	if strings.Contains(dev, "## Smoke-test checklist (AO)") {
		t.Fatalf("crew dev carries qa's checklist protocol again:\n%s", dev)
	}
	for _, want := range []string{
		"do not author or edit the smoke checklist",
		"that brief predates the crew",
	} {
		if !strings.Contains(dev, want) {
			t.Fatalf("crew dev prompt missing the checklist negative %q:\n%s", want, dev)
		}
	}

	// A solo worker is in no crew, so it is told none of this and keeps the
	// protocol it has always had.
	solo, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(solo, "do not author or edit the smoke checklist") {
		t.Fatalf("a solo worker was told not to author the checklist it owns:\n%s", solo)
	}
	if !strings.Contains(solo, "## Smoke-test checklist (AO)") {
		t.Fatalf("a solo worker lost the checklist protocol:\n%s", solo)
	}
}

// THE ORDERING THAT MADE THE SPLIT INERT. A crew is formed AFTER dev is
// materialized (formCrew needs the tree to share), and dev's system prompt is
// fixed when its runtime launches, several steps earlier - so reading the ROW
// answered "solo" for every dev that was about to get a qa. A real
// `--task-size standard` spawn therefore launched dev with the SOLO prompt: the
// smoke-checklist protocol still in it, no word of a crewmate, and the split
// only taking effect if something later restored the session.
func TestSpawn_StandardTaskLaunchesDevWithTheCrewPrompt(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, TaskSize: domain.TaskSizeStandard}); err != nil {
		t.Fatal(err)
	}
	launched := agent.lastLaunch.SystemPrompt
	if !strings.Contains(launched, "## Your crewmate (AO)") {
		t.Fatalf("dev was launched without knowing it has a crewmate:\n%s", launched)
	}
	if !strings.Contains(launched, "do not author or edit the smoke checklist") {
		t.Fatalf("dev was launched without the checklist negative:\n%s", launched)
	}
	if strings.Contains(launched, "## Smoke-test checklist (AO)") {
		t.Fatalf("dev was launched still carrying qa's checklist protocol:\n%s", launched)
	}
}

// A MECHANICAL task is dev alone, so it must be launched with the solo prompt it
// has always had - the checklist protocol included, because there is nobody else
// to keep it.
func TestSpawn_MechanicalTaskLaunchesTheSoloPrompt(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &recordingAgent{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})

	if _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, TaskSize: domain.TaskSizeMechanical}); err != nil {
		t.Fatal(err)
	}
	launched := agent.lastLaunch.SystemPrompt
	if !strings.Contains(launched, "## Smoke-test checklist (AO)") {
		t.Fatalf("a mechanical worker lost the checklist protocol it owns:\n%s", launched)
	}
	for _, gone := range []string{"## Your crewmate (AO)", "do not author or edit the smoke checklist"} {
		if strings.Contains(launched, gone) {
			t.Fatalf("a mechanical worker was told about a crew it does not have (%q):\n%s", gone, launched)
		}
	}
}
