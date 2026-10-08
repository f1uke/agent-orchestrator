package prompts

import "strings"

// simDevices is which simulators are a session's, written once for every iOS
// device block (the catalog, the script-only block and its verify-skill shape)
// so the three cannot drift apart on the one fact all of them depend on.
//
// AO clones a device per session from a base at spawn ($AO_SIM_UDID), so a
// worker never has to pick one, and never shares one with its crewmate. The
// failure this replaces is an agent reaching for "whichever device is booted":
// that was the human's, or the crewmate's, and the fallback is named here only
// to rule it out. Extra devices are clones too, made on demand by model or
// label, because a size check or the other side of a chat needs more than one
// device at a time, and running Maestro on each of them at once is the point of
// having them.
const simDevices = `- **Your device is ` + "`" + `$AO_SIM_UDID` + "`" + `**, cloned from the iPhone 17 Pro Max base for this session alone. ` + "`" + `ao sim` + "`" + ` means it by default; other tools need it named: ` + "`" + `xcodebuild -destination "$AO_SIM_DESTINATION"` + "`" + `, ` + "`" + `maestro --device "$AO_SIM_UDID"` + "`" + `. Unset: run ` + "`" + `ao sim claim` + "`" + `, which makes it or says what is missing. Never fall back to whichever device is booted.
- **Another size or a second device:** ` + "`" + `ao sim claim --model "iPhone SE"` + "`" + ` (or ` + "`" + `"iPad Pro 11-inch"` + "`" + `), or ` + "`" + `ao sim claim --device <label>` + "`" + ` for a second iPhone 17 Pro Max. Then ` + "`" + `--device <label>` + "`" + ` on any ` + "`" + `ao sim` + "`" + ` command, and ` + "`" + `"$(ao sim udid --device <label>)"` + "`" + ` for other tools. Each is a clone deleted when the session ends, or sooner by ` + "`" + `ao sim release --device <label>` + "`" + `; plain ` + "`" + `ao sim release` + "`" + ` only drops your lease. Run one Maestro run per device, on several at once, and never wait for another device's run. Labels and the rest are in the ao skill's ` + "`" + `commands/sim-devices.md` + "`" + `.
- **Never claim, boot, install on or drive a base** (iPhone 17 Pro Max, iPhone SE (3rd generation), iPad Pro 11-inch (M5)): they are templates. ` + "`" + `ao sim list` + "`" + ` shows each device's role.
- **Power on, nothing else**: no shutdown, reboot or erase. ` + "`" + `ao sim boot` + "`" + ` allows 4 booted machine-wide (at the cap AO shuts down the least recently used idle AO clone); boot only what you use.`

// simAPIMocks is the human's rule (2026-10-07) for which API a device run
// talks to, appended to every iOS block that verifies or plays something on a
// simulator. Mocks are cheap to reach for (Proxyman Map Local, `bin/flow
// --mocks`), and a run on a mock proves the app against a fixture, not against
// the backend it ships with - so the real int/uat API is the default and a mock
// needs one of two reasons. The one-shot order exists because an irreversible
// action can be played for real exactly once: playing every failure first
// records them all from the real API before the state is consumed, and the one
// success capture becomes the fixture for every replay.
//
// mockTools names what this project mocks with: a script-only project has
// `bin/flow --mocks` too, and a catalog project must not be told about bin/flow.
func simAPIMocks(mockTools string) string {
	return strings.Replace(simAPIMocksRule, "{{tools}}", mockTools, 1)
}

const (
	mockToolsCatalog = "Proxyman Map Local"
	mockToolsScripts = "Proxyman Map Local or `bin/flow --mocks`"
)

const simAPIMocksRule = "\n\n" + `### Real API or mock (AO)

- **Run against the real int/uat API by default.** Do not reach for {{tools}} by habit. Mock only when the feature's backend is not ready yet, or when the case can be played against the real API only once (redeem, buy, submit, delete), so repeat plays need a mock.
- **No API doc: build fixtures from real responses**, carefully: int/uat only, never production; prefer read-only calls; fire a one-shot action deliberately and once; strip tokens, cookies and personal data; note where each fixture came from.
- ` + oneShotOrder + `
- **Say per case** in your report or handback whether it ran against the real API or which mock set, and why it needed the mock.`

// oneShotOrder is how a case that consumes state is played. It is also the
// whole rule in the blocks that play cases without a device block above them
// (the Testiny loop on any project), so it reads on its own.
const oneShotOrder = `**A one-shot action: failures first, success last.** Play every failure case against the real API first (validation error, insufficient balance, expired, unauthorized), then fire the success case exactly once, capturing its request and response: that capture is the fixture for every repeat play.`

// simQADevBuild is qa's side of SimulatorHandoverToQA, for every iOS project.
// qa has its own device, so the one thing that has to come from dev is the
// build: a qa that rebuilds from the shared worktree may test a binary dev
// never meant to hand over, and `ao sim doctor --expect` is what proves the
// installed app is the bundle dev named.
const simQADevBuild = "\n\n" + `## Your device, dev's build (AO)

- **You test on your own device** (` + "`$AO_SIM_UDID`" + `, a clone of the same base dev works on), never dev's. dev keeps its device and may go on working while you test.
- **Install exactly the build dev handed over.** dev's message names a ` + "`.app`" + ` path and its ` + "`Build:`" + ` line. Install that bundle with ` + "`ao sim install <path>`" + ` - never rebuild it - and check it with ` + "`ao sim doctor --app <bundle id> --expect <that .app>`" + ` before you play anything. No build named yet, or a mismatch: ask dev for the current one rather than building your own.
- **Sizes:** for a layout case, also ` + "`ao sim claim --model \"iPhone SE\"`" + ` and ` + "`--model \"iPad Pro 11-inch\"`" + `, install the same bundle on each, and play the case on all of them at once.`
