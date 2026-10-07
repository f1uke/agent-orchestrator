package prompts

import "strings"

// MobileScripts names the scripts that drive a script-only mobile project's
// devices. It mirrors domain.MobileScriptsConfig, already validated, with the
// store resolved to the path the agent is told to use.
type MobileScripts struct {
	// Product is the product's folder in the store (projects/<product>).
	Product string
	// IOS is true for an iOS repository and false for an Android one.
	IOS bool
	// Store is the checkout the agent runs and writes scripts in: its task's
	// own worktree of the store when Isolated, else the main checkout.
	Store string
	// Root is the store's main checkout. accounts/ is git-ignored, so it lives
	// only there, and a worktree's bin/flow reads it from there too.
	Root string
	// Isolated is true when Store is the task's own worktree of the store,
	// published into Base with `ao scripts publish`. False when AO could not
	// make one (the store is not a git checkout on a branch): Store is then
	// the one checkout every session shares.
	Isolated bool
	Base     string
	// Skill is the folder of the project's verify skill (holding SKILL.md),
	// or "" when the project names none. When set, the device block defers to
	// the skill instead of restating how to build, drive and check the app.
	Skill string
}

// MobileScriptGuidance is the device block a worker on a SCRIPT-ONLY mobile
// project gets, in place of SimulatorGuidance's step-by-step catalog. Injected
// only when the project has opted in (ProjectConfig.MobileScripts), so a
// project that says nothing - every non-mobile project, and an iOS project that
// has not opted in - keeps its prompt byte for byte.
//
// The rule it carries is the human's (2026-10-03), made after a measured study
// of scripts against two AI drivers: on a known route a fixed Maestro script was
// as reliable as an agent driving the app and many times faster, and free. A
// script only stays reliable while it starts from a fresh app state, so the
// block teaches the whole loop rather than the run command alone - find the
// script, put the build under test on the device first, run it, judge the end
// state by READING the screen, author a missing script once, and never finish a
// failed run by hand, which is how a script silently stops being reusable.
//
// Reading the screen stays the agent's (`ao sim shot/ax/log`): judging what a
// script left behind is the one step that needs an AI, and it never moves the
// app. Gestures are named only to be ruled out, with the one exception the rule
// makes - authoring the script nobody has written yet.
//
// Android has no `ao sim`, so its block teaches `maestro --device` through the
// same runner and says plainly that nothing leases an emulator. Which `ao sim`
// commands the iOS block names is a reviewed decision held by
// cli.TestMobileScriptGuidance_DecidesEverySubcommand, the same way the
// catalog's is.
//
// A project with a verify skill gets a short block instead, holding only what
// AO owns (which devices are yours, the lease, power, CA trust, the health check
// and the store worktree) and pointing at the skill for the rest: two sources
// that both teach how to build and drive the app drift apart, and the skill is
// the one its project maintains.
//
// Both iOS shapes end with simAPIMocks, the human's rule for when a run may use
// a mock, which no verify skill overrides. Android has no `bin/flow --mocks`, so
// its blocks carry none.
func MobileScriptGuidance(ms MobileScripts) string {
	if ms.Skill != "" {
		if ms.IOS {
			return ms.fill(mobileScriptSkillIOS + ms.storeRule() + simAPIMocks(mockToolsScripts))
		}
		return ms.fill(mobileScriptSkillAndroid + ms.storeRule())
	}
	if ms.IOS {
		return ms.fill(mobileScriptIOS + mobileScriptShared + ms.storeRule() + mobileScriptIOSClosing + simAPIMocks(mockToolsScripts))
	}
	return ms.fill(mobileScriptAndroid + mobileScriptShared + ms.storeRule() + mobileScriptAndroidClosing)
}

// storeRule is where an agent writes scripts and how they reach other
// sessions. A task's own worktree is published with `ao scripts publish`; the
// shared checkout (AO could not make a worktree) keeps the old rule of
// committing only your own files by path.
func (ms MobileScripts) storeRule() string {
	if ms.Isolated {
		return mobileScriptStoreIsolated
	}
	return mobileScriptStoreShared
}

// MobileScriptPlay is how qa plays test cases on a script-only project, Testiny
// on or off, and it replaces RecordedFlowLoop there. That loop records a flow by
// driving it with `ao sim tap` and commits it into the app's repository; under
// the rule every device run is a script from the store, so a played case
// becomes a CASE SCRIPT in the store's cases/ layer instead (the human's
// request, 2026-10-06): one file per behaviour, which today is how qa plays the
// case and later is promoted into a CI test in finno-maestro.
//
// A case result needs two checks, and the block names both because a script
// can prove only one. Its assertions prove the data and the behaviour; a screen
// can show the right data and still differ from its Figma frame, so qa compares
// each judged screenshot with the frame and cites it, and leaves the visual
// check to a person when no frame is linked. The script's own screenshots feed
// that comparison, so the block names no screen-reading command.
//
// qa only, like the loop it replaces: dev hands verification over, and a solo
// worker has nobody to play cases for.
//
// On iOS it opens with simQADevBuild: qa plays on its own device, with the
// build dev handed over rather than one it builds itself, so with a verify
// skill qa takes only the skill's health check, not its build step. Android
// qa sets up its device and build the way the skill says.
func MobileScriptPlay(ms MobileScripts) string {
	devBuild, record, device, mocks, oneShot, setup := "", "", " --platform android --device <serial>", "", " "+oneShotOrder, ""
	if ms.IOS {
		devBuild = simQADevBuild
		record = " When nobody knows the route, ask the human to play it ONCE in your Device tab while `ao sim flow record` runs: that one play becomes the script."
		device = ""
		mocks = " Add `--mocks <set>` only where \"Real API or mock\" above allows it."
		oneShot = " A step that cannot be undone follows the one-shot order in \"Real API or mock\" above."
	}
	if ms.Skill != "" {
		setup = "Set up your device and your build the way the verify skill says (Launch, Doctor), then play every case as below. "
		if ms.IOS {
			setup = "Check your device the way the verify skill says (Doctor), with dev's build installed as above, then play every case as below. "
		}
	}
	return ms.fill(devBuild + strings.NewReplacer(
		"{{record}}", record, "{{device}}", device, "{{mocks}}", mocks, "{{oneshot}}", oneShot, "{{setup}}", setup,
	).Replace(mobileScriptPlay))
}

func (ms MobileScripts) fill(s string) string {
	root := ms.Root
	if root == "" {
		root = ms.Store
	}
	return strings.NewReplacer(
		"{{store}}", ms.Store,
		"{{root}}", root,
		"{{product}}", ms.Product,
		"{{base}}", ms.Base,
		"{{skill}}", ms.Skill,
	).Replace(s)
}

const mobileScriptIOS = "\n\n" + `## Driving the iOS Simulator: scripts only (AO)

On this project a simulator is driven ONLY by running a reusable Maestro script from the scripts store at ` + "`{{store}}`" + ` (product ` + "`{{product}}`" + `). To see a screen, verify a change, reproduce a bug or take evidence, run the script that reaches that screen - never tap through the app step by step. On a known route a script is as reliable as an agent driving and many times faster, and it stays that way because every script starts from a fresh app, whatever the device was left on.

` + "```bash\n" + `ao sim list                     # every device, its role (base, or whose clone) and whether it is booted
ao sim boot                     # power yours ON; already booted is a no-op
ao sim run --scheme <name>      # put YOUR build on the device first: a script resets the app it finds installed
ao sim doctor --app <bundle id> # read-only health check: device, lease, installed build, proxy CA
{{store}}/bin/flow list {{product}}
                                # INDEX.md: which script reaches which screen, its params, what it leaves behind
{{store}}/bin/flow run {{product}} reach/<script> --param KEY=VALUE --account <id>
                                # claims $AO_SIM_UDID and runs the script through ` + "`ao sim flow run`" + `
ao sim shot                     # judge the end state: a PNG, plus the BUILD it was of
ao sim ax                       # the same screen as elements
ao sim log                      # what the app printed, when the screen does not explain it
ao sim release                  # when you are done with the device` + "\n```" + `

- **Reading is how you judge; a script is how you move.** ` + "`ao sim shot`" + `, ` + "`ao sim ax`" + ` and ` + "`ao sim log`" + ` are fine at any time. Gestures - ` + "`ao sim tap`" + `, ` + "`ao sim type`" + `, ` + "`ao sim drag`" + ` and the rest - are not, except while authoring a missing script (below).
` + simDevices + `
- **A lease guards the device, not the command.** ` + "`ao sim run`" + ` and ` + "`ao sim install`" + ` take it as they install; a raw ` + "`xcrun simctl`" + ` or ` + "`xcodebuild -destination`" + ` never asks it, and aimed at a device that is not yours it overwrites whoever is on it. A refusal names the holder - wait, or say so.
- **A screenshot says which build it was of.** Compare its ` + "`Build:`" + ` line before the pictures.
- **No script reaches that screen yet: author one, then use it.** This is the only time step-by-step driving is allowed: ` + "`ao sim claim`" + `, ` + "`ao sim flow record start --name <screen>`" + `, drive the route once, ` + "`ao sim flow record stop --out {{store}}/projects/{{product}}/reach/<name>.yaml --entry ../start/<state>.yaml --param NAME=VALUE`" + ` (every typed or tapped VALUE becomes ` + "`${MAESTRO_NAME}`" + `; a password is pasted, never recorded) - or write the YAML yourself.`

const mobileScriptAndroid = "\n\n" + `## Driving the Android emulator: scripts only (AO)

On this project an emulator is driven ONLY by running a reusable Maestro script from the scripts store at ` + "`{{store}}`" + ` (product ` + "`{{product}}`" + `). To see a screen, verify a change, reproduce a bug or take evidence, run the script that reaches that screen - never tap through the app step by step. On a known route a script is as reliable as an agent driving and many times faster, and it stays that way because every script starts from a fresh app, whatever the device was left on. There is no ` + "`ao sim`" + ` for Android: scripts run through ` + "`maestro --device <serial>`" + `, which ` + "`bin/flow`" + ` does for you.

` + "```bash\n" + `adb devices                                   # which emulators are up, by serial
adb -s <serial> install -r <app.apk>          # put YOUR build on it first: a script resets the app it finds installed
{{store}}/bin/flow list {{product}}
                                              # INDEX.md: which script reaches which screen, its params, what it leaves behind
{{store}}/bin/flow run {{product}} reach/<script> --platform android --device <serial> --param KEY=VALUE --account <id>
adb -s <serial> exec-out screencap -p > end.png   # judge the end state
maestro --device <serial> hierarchy           # the same screen as elements
adb -s <serial> logcat -d -t 500              # what the app printed, when the screen does not explain it` + "\n```" + `

- **Reading is how you judge; a script is how you move.** A screenshot, the hierarchy and logcat are fine at any time. Step-by-step input (` + "`adb shell input`" + ` taps, text and swipes) is not, except while authoring a missing script (below).
- **Nothing leases an emulator.** Two sessions on one emulator break each other's runs and AO cannot stop it, so use the serial your brief or the human gives you (` + "`bin/flow`" + ` falls back to ` + "`$ANDROID_SERIAL`" + `), and never wipe or kill an emulator - it may be someone else's.
- **No script reaches that screen yet: author one, then use it.** This is the only time step-by-step driving is allowed: find the selectors with ` + "`maestro --device <serial> hierarchy`" + ` and write the YAML. A product's iOS and Android apps share their scripts; where they differ, branch with ` + "`runFlow: when: platform: Android`" + `.`

// mobileScriptShared finishes the authoring bullet both platforms open, and adds
// the two rules that do not depend on the device.
const mobileScriptShared = ` Follow the store's README ("Rules that keep a script reusable", "Add a script"): start from ` + "`start/`" + `, no value typed into the script, end with an assertion and ` + "`takeScreenshot`" + `, stop before anything irreversible. Then ` + "`bin/flow check {{product}}`" + `, run it twice green from fresh, and add its row to ` + "`projects/{{product}}/INDEX.md`" + `.
- **A script fails: read, fix, re-run - never finish the run by hand.** The run prints Maestro's debug folder, a screenshot and hierarchy for every step. Decide whether the app or the script is wrong, fix the script or report the app bug with that folder as evidence, and run it again.
- **Accounts are referred to by id.** ` + "`bin/flow accounts {{product}}`" + ` lists them and ` + "`--account <id>`" + ` passes one. They are int/uat test accounts, safe to use and to store; production credentials never go anywhere.`

const mobileScriptIOSClosing = "\n\n" + `Everything else - the store's layout and rules, the full ` + "`ao sim`" + ` catalog, running several flows in one Maestro start-up - is in the store's README and the ao skill this prompt already points you at.`

const mobileScriptAndroidClosing = "\n\n" + `Everything else - the store's layout, its rules and how to set up a device - is in the store's README.`

const mobileScriptPlay = "\n\n" + `## Playing test cases with Maestro scripts (AO)

{{setup}}Every test case you play on a device, you play by running ONE case script - never by gestures, and never by running reach scripts one after another by hand. A case script is the case written down so a machine can replay it: today it is how you play the case, later it is how the case becomes an automated UI test. The store's README section "Case scripts (` + "`cases/`" + `)" is the full standard.

1. **Find the case's script** in the Cases table of ` + "`{{store}}/projects/{{product}}/INDEX.md`" + `. On a Testiny project it is listed by its Testiny case id.
2. **No script yet: write one**, then use it. It lives at ` + "`{{store}}/projects/{{product}}/cases/<area>/<behaviour>.yaml`" + `, named after the behaviour the case checks, never after a ticket. Its header carries one ` + "`# testiny: <project_key> TC-<id>`" + ` line per Testiny case it plays. It starts from ` + "`start/`" + `, reaches the screen through ` + "`reach/`" + ` and ` + "`common/`" + ` scripts, then runs the case's own steps and ASSERTS the case's expected result, taking a screenshot at every screen the case judges, named ` + "`{{product}}-case-<behaviour>-<step>`" + `. Verify it like any other script: ` + "`bin/flow check {{product}}`" + ` and two green runs from fresh. Then add its row to the Cases table, and only then trust its result.{{record}}
3. **Play the case:** ` + "`{{store}}/bin/flow run {{product}} cases/<area>/<behaviour>{{device}} --param KEY=VALUE --account <id>`" + `.{{mocks}} The assertions prove the DATA and the BEHAVIOUR: a failed assertion is a failed case, with the Maestro debug folder as the evidence.
4. **Compare the screen with the DESIGN**, which no assertion proves. For every case that shows UI, compare each screenshot the case judges with the case's Figma frame - layout, spacing, copy, colour, components and states - and cite the frame you compared against. Find the frame from the ticket or the case. If neither links one, say so in your handback and leave the visual check for a person rather than guessing. A visual difference fails the case: name what differs and where.
5. **The case PASSES only when both hold:** every assertion, and the screen against the design.
6. **Keep the screenshots as the case's evidence.** On a Testiny project they go in the run's evidence folder: "Playing a run, start to finish" in the Testiny block below says how it goes to Drive and onto each case's result. Otherwise give their path in your handback.

A case whose script you cannot make pass is UNDRIVEABLE: say so in your handback with the reason from your attempt, and a person plays it. Never finish a case by hand.{{oneshot}} The case scripts you write follow the store rule in the device block above.`

const mobileScriptSkillIOS = "\n\n" + `## Driving the iOS Simulator: the project's verify skill (AO)

On this project a simulator is driven ONLY by Maestro scripts from the scripts store, and the project's ` + "`verify`" + ` skill is the one guide for building the app, health-checking the device, driving it, mocks and evidence: use it (` + "`.claude/skills/verify`" + `, or read ` + "`{{skill}}/SKILL.md`" + `). This block holds only what AO owns, and nothing in the skill overrides it.

` + simDevices + `
- **AO makes each device trust the proxy's CA** every time it boots or a session claims it.
- **A lease guards the device, not the command.** ` + "`ao sim run`" + ` and ` + "`ao sim install`" + ` take it as they install; a raw ` + "`xcrun simctl`" + ` or ` + "`xcodebuild -destination`" + ` never asks it, and aimed at a device that is not yours it overwrites whoever is on it. A refusal names the holder - wait, or say so.
- **` + "`ao sim doctor --app <bundle id> --expect <your .app>`" + ` is the health check** of your device: booted, whose lease, whether the installed build is the one you built, and whether the proxy CA is trusted. It only reads: run it before the first drive and after every failed run.`

const mobileScriptSkillAndroid = "\n\n" + `## Driving the Android emulator: the project's verify skill (AO)

On this project an emulator is driven ONLY by Maestro scripts from the scripts store, and the project's ` + "`verify`" + ` skill is the one guide for building the app, checking the device, driving it, mocks and evidence: use it (` + "`.claude/skills/verify`" + `, or read ` + "`{{skill}}/SKILL.md`" + `). This block holds only what AO owns, and nothing in the skill overrides it.

- **Nothing leases an emulator.** Two sessions on one emulator break each other's runs and AO cannot stop it, so use the serial your brief or the human gives you (` + "`bin/flow`" + ` falls back to ` + "`$ANDROID_SERIAL`" + `), and never wipe or kill an emulator - it may be someone else's.`

// mobileScriptStoreIsolated is the store rule when the task has its own
// worktree of the store. Publishing is an explicit command so the agent that
// wrote a script sees a refused merge at once and can fix it in its worktree;
// AO publishes again when the session ends, as the safety net.
const mobileScriptStoreIsolated = "\n" + `- **The store is yours: ` + "`$AO_SCRIPTS_STORE`" + `** is this task's own git worktree of the scripts store, on its own branch. Run ` + "`bin/flow`" + ` from there and commit there, then run ` + "`ao scripts publish`" + ` so other sessions get your scripts. A refused publish names the files: merge ` + "`{{base}}`" + ` into your branch, resolve, commit and publish again. Scripts other sessions published after you started: ` + "`git -C \"$AO_SCRIPTS_STORE\" merge {{base}}`" + `. ` + "`ao scripts status`" + ` shows what is not committed or not published yet. Accounts stay in the main checkout, ` + "`{{root}}/accounts/`" + `, where ` + "`bin/flow`" + ` reads them from any worktree. Nothing in the store goes into your pull request.`

// mobileScriptStoreShared is the store rule when AO could not give the task a
// worktree, so every session writes into the one checkout.
const mobileScriptStoreShared = "\n" + `- **The store is one checkout shared with other sessions** (` + "`{{store}}`" + `): commit only the files you added or changed, by path (` + "`git -C {{store}} commit <paths>`" + `), never another session's uncommitted work, and name them in your report. Nothing in the store goes into your pull request.`
