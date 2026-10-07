package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/promptoverrides"
	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

// manualChecksLine is the finish-report clause through which a worker that
// reports to the orchestrator names what a person must still check by hand.
const manualChecksLine = "anything a person must check by hand"

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
// must still be told to name the manual checks in its finish report, and get the
// full simulator catalog. There is nobody else to hand them to.
func TestBuildSystemPrompt_SoloWorkerKeepsEverything(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeMechanical})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		manualChecksLine,
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
// whole task - so it keeps every block a solo worker has, the manual-checks
// line of its finish report and the full simulator catalog included. Taking
// either away would leave a backend-only standard task with nobody naming what a
// person must check, and would forbid the one act that ever creates a qa on an
// iOS task.
func TestBuildSystemPrompt_CrewDevKeepsEverythingUntilItHasAQA(t *testing.T) {
	m := layeredManager(crewPromptStore(t), nil)

	got, err := m.buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: domain.CrewRoleDev})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		manualChecksLine,                                           // it owns the manual checks until a qa exists
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
		"## Handing back (AO)",              // qa's own floor
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
		"ao sim flow record start --name",
		"Commit the flow",
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
	// It still hands back - that part has nothing to do with a device.
	if !strings.Contains(got, "## Handing back (AO)") {
		t.Fatalf("qa lost its handback on a non-iOS project:\n%s", got)
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
	if !strings.Contains(launched, manualChecksLine) {
		t.Fatalf("dev was launched without being told to name the manual checks it owns:\n%s", launched)
	}
}

// A MECHANICAL task is dev alone, so it must be launched with the solo prompt it
// has always had - the manual-checks line included, because there is nobody else
// to name them.
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
	if !strings.Contains(launched, manualChecksLine) {
		t.Fatalf("a mechanical worker lost the manual checks it owns:\n%s", launched)
	}
	if strings.Contains(launched, "## Your crewmate (AO)") {
		t.Fatalf("a mechanical worker was told about a crew it does not have:\n%s", launched)
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

// scriptOnlyPrompt builds a worker prompt on a project with the given config.
func scriptOnlyPrompt(t *testing.T, cfg domain.ProjectConfig, role domain.CrewRole) string {
	t.Helper()
	st := crewPromptStore(t)
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}
	got, err := layeredManager(st, nil).buildSystemPrompt(ctx, systemPromptSpec{Kind: domain.KindWorker, ProjectID: "mer", TaskSize: domain.TaskSizeStandard, CrewRole: role})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

var allWorkerRoles = []domain.CrewRole{"", domain.CrewRoleDev, domain.CrewRoleQA}

// On a script-only iOS project every worker is taught the script workflow in
// place of the step-by-step catalog: the catalog teaches `ao sim tap`, the one
// thing the project rules out. qa plays its test cases with case scripts instead of
// recording flows into the repository.
func TestBuildSystemPrompt_ScriptOnlyIOSReplacesTheTapCatalog(t *testing.T) {
	cfg := domain.ProjectConfig{HasIOSSimulator: true, MobileScripts: &domain.MobileScriptsConfig{Product: "nter", Platform: domain.MobilePlatformIOS}}
	for _, role := range allWorkerRoles {
		got := scriptOnlyPrompt(t, cfg, role)
		for _, want := range []string{
			"## Driving the iOS Simulator: scripts only (AO)",
			domain.DefaultMobileScriptsStore + "/bin/flow run nter reach/<script>",
			"never finish the run by hand",
			"ao sim shot",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("role %q on a script-only iOS project is missing %q:\n%s", role, want, got)
			}
		}
		for _, gone := range []string{
			"## Driving the iOS Simulator (AO)",
			"ao sim tap --label",
			"## Turning a played scenario into a test (AO)",
		} {
			if strings.Contains(got, gone) {
				t.Fatalf("role %q on a script-only iOS project was still taught %q:\n%s", role, gone, got)
			}
		}
		play := strings.Contains(got, "## Playing test cases with Maestro scripts (AO)")
		if play != (role == domain.CrewRoleQA) {
			t.Fatalf("role %q: script play block present = %v, want it for qa only", role, play)
		}
		handover := strings.Contains(got, "Drive it while you work, then hand the verification over")
		if handover != (role == domain.CrewRoleDev) {
			t.Fatalf("role %q: handover note present = %v, want it for dev only", role, handover)
		}
	}
	if qa := scriptOnlyPrompt(t, cfg, domain.CrewRoleQA); !strings.Contains(qa, "`ao sim flow record` runs") {
		t.Fatalf("iOS qa was not told a human's one play can become the script:\n%s", qa)
	}
}

// Android has no `ao sim`, so its workers are taught `maestro --device` through
// the same runner and are never handed an `ao sim` command they cannot run - or
// the iOS-only lease note.
func TestBuildSystemPrompt_ScriptOnlyAndroidHasNoAOSim(t *testing.T) {
	cfg := domain.ProjectConfig{MobileScripts: &domain.MobileScriptsConfig{Product: "nter", Platform: domain.MobilePlatformAndroid, Store: "/opt/scripts"}}
	for _, role := range allWorkerRoles {
		got := scriptOnlyPrompt(t, cfg, role)
		for _, want := range []string{
			"## Driving the Android emulator: scripts only (AO)",
			"/opt/scripts/bin/flow run nter reach/<script> --platform android --device <serial>",
			"maestro --device <serial> hierarchy",
			"Nothing leases an emulator",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("role %q on a script-only Android project is missing %q:\n%s", role, want, got)
			}
		}
		for _, gone := range []string{"ao sim claim", "ao sim tap", "ao sim shot", "## Driving the iOS Simulator", "Drive it while you work"} {
			if strings.Contains(got, gone) {
				t.Fatalf("role %q on an Android project was handed %q:\n%s", role, gone, got)
			}
		}
		if play := strings.Contains(got, "## Playing test cases with Maestro scripts (AO)"); play != (role == domain.CrewRoleQA) {
			t.Fatalf("role %q: script play block present = %v, want it for qa only", role, play)
		}
	}
}

// The rule must not leak: a project that has not opted in - a plain one, or an
// iOS one without the setting - renders no script guidance at all.
func TestBuildSystemPrompt_NoMobileScriptsMeansNoScriptRule(t *testing.T) {
	for name, cfg := range map[string]domain.ProjectConfig{
		"plain": {},
		"ios":   {HasIOSSimulator: true},
	} {
		for _, role := range allWorkerRoles {
			got := scriptOnlyPrompt(t, cfg, role)
			for _, gone := range []string{"scripts only (AO)", "bin/flow", "## Playing test cases with Maestro scripts (AO)"} {
				if strings.Contains(got, gone) {
					t.Fatalf("%s project, role %q, was handed the script rule (%q):\n%s", name, role, gone, got)
				}
			}
		}
	}
}
