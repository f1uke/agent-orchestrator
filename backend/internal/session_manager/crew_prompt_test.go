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

	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeMechanical})
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

	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleDev})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Smoke-test checklist (AO)",                             // it owns the list until a qa exists
		"ao sim tap --label",                                       // and it may drive the device itself
		"Most sessions open one pull request",                      // the worker base, unchanged
		"## Orchestrator coordination",                             // dev is the one that reports
		"Drive it while you work, then hand the verification over", // and what changes when it is done
		"`ao crew review`",                                         // THE verb: dev asks for its own qa
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("crew dev prompt missing %q:\n%s", want, got)
		}
	}
	// The first version of this block forbade the claim outright; the second told
	// dev that claiming CREATED its qa, which is the collision this change
	// removed. Neither may come back.
	if strings.Contains(got, "do not claim the lease or drive the screen yourself") {
		t.Fatalf("crew dev is forbidden the device it may need to build the change:\n%s", got)
	}
	if strings.Contains(got, "is what creates its **qa** member") || strings.Contains(got, "AO creates a qa the first time you touch") {
		t.Fatalf("crew dev is still told that driving the app creates its qa:\n%s", got)
	}
}

// TestBuildSystemPrompt_CrewQAIsItsOwnAgent: qa assembles from the qa base, gets
// the blocks that moved to it, and is never handed the orchestrator report block
// - so it cannot report even if it wanted to.
func TestBuildSystemPrompt_CrewQAIsItsOwnAgent(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleQA})
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

	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleQA})
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

	qa, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleQA})
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
		got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: role})
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

	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleQA})
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

// The checklist block dev is assembled with, after the reversal. It used to be a
// negative ("the checklist is yours only while you have no qa", and `ao smoke
// set` from you is REFUSED); the human reversed that, so what dev now carries is
// a capability plus the one mechanical trap in it - `set` replaces the whole
// list, so two members using it erase each other.
func TestBuildSystemPrompt_CrewDevIsToldTheChecklistIsShared(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	dev, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleDev})
	if err != nil {
		t.Fatal(err)
	}
	// It carries the protocol AND the window: the list is dev's until a qa exists,
	// which on a task that never touches a runtime surface is for ever.
	if !strings.Contains(dev, "## Smoke-test checklist (AO)") {
		t.Fatalf("crew dev cannot author a checklist for a task that may never get a qa:\n%s", dev)
	}
	for _, want := range []string{
		"The smoke checklist is SHARED",
		"Never `ao smoke set` once there are two of you",
		// The half of the old split that survived: cases are shared, machine
		// results are not.
		"Cases are shared; RESULTS are not",
	} {
		if !strings.Contains(dev, want) {
			t.Fatalf("crew dev prompt missing the shared-checklist block %q:\n%s", want, dev)
		}
	}
	// The reversed refusal must not survive anywhere in what dev is assembled
	// with: a prompt asserting an enforcement AO no longer performs is worse than
	// one that says nothing.
	for _, gone := range []string{"REFUSED by AO", "that brief predates the crew"} {
		if strings.Contains(dev, gone) {
			t.Fatalf("crew dev still carries the reversed refusal %q:\n%s", gone, dev)
		}
	}

	// A solo worker is in no crew, so it is told none of this and keeps the
	// protocol it has always had.
	solo, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(solo, "The smoke checklist is SHARED") {
		t.Fatalf("a solo worker was told about a crewmate it can never have:\n%s", solo)
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
	if !strings.Contains(launched, "ao crew review") {
		t.Fatalf("dev was launched without being told how to ask for its qa:\n%s", launched)
	}
	if !strings.Contains(launched, "The smoke checklist is SHARED") {
		t.Fatalf("dev was launched without being told the checklist is shared:\n%s", launched)
	}
	if !strings.Contains(launched, "## Smoke-test checklist (AO)") {
		t.Fatalf("dev was launched unable to author the checklist it co-owns:\n%s", launched)
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

// The lease rule has to reach BOTH members. dev clobbers qa's device exactly as
// easily as qa clobbered dev's in the incident this came from, so the rule is
// written symmetrically and injected with the catalog every worker on an iOS
// project gets - and it names the tool that actually caused it, a raw
// `xcodebuild -destination`, which never consults the lease at all.
func TestBuildSystemPrompt_EveryMemberIsToldNotToDriveADeviceItDoesNotHold(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	for _, role := range []domain.CrewRole{domain.CrewRoleDev, domain.CrewRoleQA, ""} {
		got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: role})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"A lease guards the device, not the command",
			"xcodebuild -destination",       // the hole: it never asks the lease
			"dev and qa clobber each other", // symmetric; this is not a qa rule
			"A refusal names the holder",    // wait or say so, do not take it
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("role %q was not told to leave a device it does not hold alone (%q):\n%s", role, want, got)
			}
		}
	}

	// A project with no simulator has no device to contend over, and an
	// instruction an agent cannot follow is worse than none.
	st := crewPromptStore(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{HasIOSSimulator: false}}
	plain, err := layeredManager(st, nil).buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleDev})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "A lease guards the device, not the command") {
		t.Fatalf("a project with no simulator was given a simulator lease rule:\n%s", plain)
	}
}

// qa is created part-way through a task, so the always-injected protocol's
// timing - author the list once the change is done, before the PR - leaves a
// window where a qa is working and nothing says what it intends to verify. qa is
// re-timed to publish its intent up front, and pointed at `ao smoke stand-down`
// when the answer is that nothing needs a human - which is the surface that now
// stops an empty Tests tab meaning "still thinking" and "nothing to check" at
// the same time. dev and a solo worker keep the timing they had: theirs is the
// last thing they do, and there is nobody to tell.
func TestBuildSystemPrompt_OnlyQAPublishesTheChecklistAsIntent(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	qa, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleQA})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### Publish what you will verify, before you verify it (AO)",
		"before you start running things", // intent up front, not at the end
		"ao smoke stand-down",             // the surface, now that one exists
		"cannot tell your answer from nobody having looked",
		"## Smoke-test checklist (AO)", // it re-times that block, never replaces it
	} {
		if !strings.Contains(qa, want) {
			t.Fatalf("qa was not told to publish its intent early (%q):\n%s", want, qa)
		}
	}
	// The re-timing is about WHEN qa writes, not WHO writes: dev is still told the
	// list is shared and still carries the per-case verbs.
	dev, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleDev})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dev, "The smoke checklist is SHARED") {
		t.Fatalf("dev lost the shared-checklist block:\n%s", dev)
	}

	for _, role := range []domain.CrewRole{domain.CrewRoleDev, ""} {
		got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: role})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "### Publish what you will verify, before you verify it (AO)") {
			t.Fatalf("role %q was re-timed for a qa it does not have:\n%s", role, got)
		}
		// And the rule it would have overridden is still there, unweakened.
		if !strings.Contains(got, "BEFORE you open the PR/MR") {
			t.Fatalf("role %q lost the before-the-PR timing it has always had:\n%s", role, got)
		}
	}

	// A qa can be created by `ao preview` on a project with no simulator, and it
	// owns the same checklist there - so this block is not gated on iOS.
	st := crewPromptStore(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: domain.ProjectConfig{HasIOSSimulator: false}}
	plain, err := layeredManager(st, nil).buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleQA})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "### Publish what you will verify, before you verify it (AO)") {
		t.Fatalf("a qa on a non-iOS project lost the checklist timing it owns:\n%s", plain)
	}
}
