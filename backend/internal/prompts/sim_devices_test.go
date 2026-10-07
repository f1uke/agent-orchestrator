package prompts

import (
	"strings"
	"testing"
)

// Every iOS device block names the session's own clone and rules out the old
// fallback, and carries the mock rule exactly once; Android has no
// `bin/flow --mocks`, so its block carries none.
func TestIOSDeviceBlocks_OwnCloneAndMockRule(t *testing.T) {
	scripts := MobileScripts{Product: "nter", IOS: true, Store: "/store"}
	skill := scripts
	skill.Skill = "/store/projects/nter/verify"
	for name, block := range map[string]string{
		"catalog": SimulatorGuidance(),
		"scripts": MobileScriptGuidance(scripts),
		"skill":   MobileScriptGuidance(skill),
	} {
		for _, want := range []string{"Your device is `$AO_SIM_UDID`", "Never fall back to whichever device is booted", "Never claim, boot, install on or drive a base", "failures first, success last"} {
			if !strings.Contains(block, want) {
				t.Errorf("%s block is missing %q", name, want)
			}
		}
		if n := strings.Count(block, "### Real API or mock (AO)"); n != 1 {
			t.Errorf("%s block carries the mock rule %d times, want 1", name, n)
		}
		if strings.Contains(block, "none was free") || strings.Contains(block, "scratch device") {
			t.Errorf("%s block still sends the agent to a scratch device:\n%s", name, block)
		}
	}
	if strings.Contains(SimulatorGuidance(), "bin/flow") {
		t.Error("the catalog names bin/flow, which a project without mobile scripts does not have")
	}
	android := MobileScriptGuidance(MobileScripts{Product: "nter", Store: "/store"})
	if strings.Contains(android, "Real API or mock") {
		t.Error("the Android block carries the iOS-only mock rule")
	}
}

// The device never changes hands: dev keeps its own, names the build, and qa
// installs that build on its own clone and checks it before playing.
func TestCrewHandover_HandsOverTheBuildNotTheDevice(t *testing.T) {
	dev := SimulatorHandoverToQA() + CrewProtocol("dev")
	for _, want := range []string{"qa gets its own device", "Name the exact build qa must install", "`Build:`", "Your device stays yours"} {
		if !strings.Contains(dev, want) {
			t.Errorf("dev handover is missing %q:\n%s", want, dev)
		}
	}
	for _, gone := range []string{"release the lease and leave the device", "release the lease and hand the verification over", "the device, the worktree and the git index"} {
		if strings.Contains(dev, gone) {
			t.Errorf("dev is still told to hand its device to qa (%q)", gone)
		}
	}
	for name, qa := range map[string]string{
		"catalog": RecordedFlowLoop(),
		"scripts": MobileScriptPlay(MobileScripts{Product: "nter", IOS: true, Store: "/store"}),
	} {
		for _, want := range []string{"You test on your own device", "`ao sim install <path>`", "`ao sim doctor --app <bundle id> --expect <that .app>`"} {
			if !strings.Contains(qa, want) {
				t.Errorf("%s qa block is missing %q", name, want)
			}
		}
	}
}

// A one-shot action is played for real once, last, wherever cases are played.
func TestCasePlay_OneShotOrderReplacesPersonOnly(t *testing.T) {
	for name, block := range map[string]string{
		"testiny": TestinyProtocol("MOB", "mer", "qa", nil),
		"android": MobileScriptPlay(MobileScripts{Product: "nter", Store: "/store"}),
	} {
		if !strings.Contains(block, "failures first, success last") {
			t.Errorf("%s play block does not carry the one-shot order", name)
		}
		if strings.Contains(block, "stays a person's") || strings.Contains(block, "A person plays that step") {
			t.Errorf("%s play block still leaves every irreversible step to a person", name)
		}
	}
	if !strings.Contains(TestinyProtocol("MOB", "mer", "qa", nil), "whether each case ran against the real API or which mock set and why") {
		t.Error("qa's handback does not say which API each case ran against")
	}
}
