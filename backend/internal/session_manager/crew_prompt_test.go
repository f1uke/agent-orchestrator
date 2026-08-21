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
	if strings.Contains(got, "The device becomes qa's the moment you claim it") {
		t.Fatal("a solo worker was told to hand the device to a qa it can never have")
	}
}

// TestBuildSystemPrompt_CrewDevKeepsEverythingUntilItHasAQA. Under lazy creation
// a crew-eligible dev is ALONE when its prompt is built, and may be alone for the
// whole task - so it keeps every block a solo worker has, the checklist protocol
// and the full simulator catalog included. Taking either away would leave a
// backend-only standard task with no checklist author, and would forbid the one
// act that ever creates a qa on an iOS task.
func TestBuildSystemPrompt_CrewDevKeepsEverythingUntilItHasAQA(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	got, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", domain.TaskSizeStandard, domain.CrewRoleDev)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Smoke-test checklist (AO)",                    // it owns the list until a qa exists
		"ao sim tap --label",                              // and it may drive the device itself
		"Most sessions open one pull request",             // the worker base, unchanged
		"## Orchestrator coordination",                    // dev is the one that reports
		"The device becomes qa's the moment you claim it", // and what changes then
		"AO creates a qa the first time you touch the app's runtime",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("crew dev prompt missing %q:\n%s", want, got)
		}
	}
	// The old block forbade the claim outright, which under lazy creation would
	// stop the only event that ever creates a qa.
	if strings.Contains(got, "do not claim the lease or drive the screen yourself") {
		t.Fatalf("crew dev is still forbidden the claim that creates its qa:\n%s", got)
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
	// It carries the protocol AND the window: the list is dev's until a qa exists,
	// which on a task that never touches a runtime surface is for ever.
	if !strings.Contains(dev, "## Smoke-test checklist (AO)") {
		t.Fatalf("crew dev cannot author a checklist for a task that may never get a qa:\n%s", dev)
	}
	for _, want := range []string{
		"The checklist is yours only while you have no qa",
		"REFUSED by AO for as long as a qa is on this task",
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
	if strings.Contains(solo, "The checklist is yours only while you have no qa") {
		t.Fatalf("a solo worker was told about a crew it can never have:\n%s", solo)
	}
	if !strings.Contains(solo, "## Smoke-test checklist (AO)") {
		t.Fatalf("a solo worker lost the checklist protocol:\n%s", solo)
	}
}

// THE ORDERING THAT MAKES THE PROMPT AN INTENT. dev's system prompt is fixed when
// its runtime launches, and under lazy creation its crew does not exist then and
// may never exist - so reading the ROW would answer "solo" for every dev, and
// nothing would ever tell dev what summons a qa or what changes when one arrives.
// The prompt is therefore built from the spawn's INTENT (promptCrewRole) and is
// written to be true on both sides of the join.
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
	if !strings.Contains(launched, "AO creates a qa the first time you touch the app's runtime") {
		t.Fatalf("dev was launched without being told what creates its qa:\n%s", launched)
	}
	if !strings.Contains(launched, "The checklist is yours only while you have no qa") {
		t.Fatalf("dev was launched without the checklist window:\n%s", launched)
	}
	if !strings.Contains(launched, "## Smoke-test checklist (AO)") {
		t.Fatalf("dev was launched unable to author the checklist it owns until a qa exists:\n%s", launched)
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
