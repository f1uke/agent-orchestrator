# ao sim

Local iOS Simulators on this machine: list them, read what is on a booted one's screen (as an accessibility tree or a PNG), read what an app on it SAYS (its unified log), drive it with taps, swipes, typing and hardware buttons, and claim one so other AO sessions keep off it while you work.

`ao sim` never boots, shuts down, reboots or erases a simulator. It runs no background process, opens no port and polls nothing: each command runs, does its one job, and exits. Claiming a device changes nothing about the device itself - a lease is bookkeeping the AO daemon holds, not an operation on the simulator.

Simulators are shared: another AO session, or a human working in Xcode, may be driving the same device. A captured frame can therefore be mid-interaction and is not proof that you put the app in that state.

**Claim a device before you drive it.** A simulator has one finger and no per-caller state, so two sessions interacting at once merge into a single teleporting touch, and one session's release lifts the other's finger. A lost release wedges the device's input until somebody reboots it - which breaks whoever else is mid-test. The commands that touch the screen refuse to run unless this session holds the device, and refuse again while another gesture is in flight. Reading (`ao sim list`, `ao sim shot`, `ao sim ax`) never needs a claim.

**Never attach a pipe to an app's stdout.** `xcrun simctl launch --console-pipe` looks like the way to read an app's output. It is a trap: as soon as anything stops draining that pipe the 64 KB buffer fills and the app blocks in `write()` **on its main thread**. The app is then wedged - `ao sim ax` returns nothing, `ao sim tap` reports success and changes nothing, the screen looks frozen - and none of those symptoms points back at your capture. Use `ao sim log`, which reads the unified log and cannot block the app.

**Read the screen, then act on what you read - never on what you expect.** `ao sim ax` gives every element that is actually on the screen a `tap` point in the same 0..1 coordinates `ao sim tap` takes, so acting on a screen is copy-the-number, not estimate-from-a-picture. An element that has scrolled out of view is still listed, marked `off screen`, and carries **no** tap point - scroll it into view with `ao sim drag` and read again. After any interaction, read again: a tap that reports success has not necessarily changed anything.

Requires macOS with the Xcode command line tools (`xcrun` on PATH). The interaction commands additionally need Node.js 20+ on PATH; `ao sim list` and `ao sim shot` do not.

## Syntax

```
ao sim list    [flags]
ao sim shot    [flags]
ao sim ax      [flags]
ao sim log     [flags]
ao sim claim   [flags]
ao sim release [flags]
ao sim tap    <x> <y> | --label <name> | --id <identifier>  [flags]
ao sim swipe  <x1> <y1> <x2> <y2> [flags]
ao sim drag   <x1> <y1> <x2> <y2> [<x3> <y3> ...] [flags]
ao sim type   <text>           [flags]
ao sim button <name>           [flags]
```

## The loop that works

```bash
ao sim claim                 # once, before you drive anything
ao sim ax                    # read the screen; what is on it carries a tap point
ao sim tap --label "Continue" # act on what you read, by name
ao sim tap 0.5 0.934         # or by the point the tree gave you
ao sim ax                    # read again to confirm what actually changed
ao sim release               # when you are done
```

---

### ao sim list

List every simulator `xcrun simctl` reports, with its udid, state, runtime, name and lease, and say which one `ao sim shot` would pick.

**Flags:**

| Flag     | Description               |
| -------- | ------------------------- |
| `--json` | Output simulators as JSON |

**Examples:**

```bash
# See what exists and what is booted
ao sim list
```

```bash
# Machine-readable, including the default `ao sim shot` would use
ao sim list --json
```

JSON shape:

```json
{
	"devices": [
		{
			"udid": "00000000-0000-0000-0000-000000000000",
			"name": "iPhone 17 Pro Max",
			"runtime": "iOS 26.3",
			"runtimeIdentifier": "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
			"state": "Booted",
			"available": true,
			"default": true,
			"lease": {
				"state": "held",
				"holder": "your-project-12",
				"acquiredAt": "2026-08-13T07:41:02Z",
				"expiresAt": "2026-08-13T07:51:02Z"
			}
		}
	],
	"defaultUdid": "00000000-0000-0000-0000-000000000000",
	"defaultReason": "the only booted simulator"
}
```

`defaultUdid` is `null` when there is no unambiguous choice, and `defaultReason` says why.

**Lease state is only ever `held` or `unknown` - never `free`.** `held` means an AO session holds it and names who. `unknown` means no AO session holds it AND AO cannot tell whether a human is driving it from Xcode: Xcode takes its own exclusive lock that AO has no way to see. `unknown` also covers "the daemon is not running so nobody could be asked" - the `reason` field says which. Treat `unknown` as "probably yours to claim", never as "proven idle".

---

### ao sim shot

Capture a booted simulator's screen to a PNG and print its path. Read that path to actually look at the screen.

**Flags:**

| Flag              | Description                                                  |
| ----------------- | ------------------------------------------------------------ |
| `--udid <udid>`   | Capture this simulator instead of the booted one             |
| `--output <path>` | Write the PNG here instead of the session artifact directory |
| `--json`          | Output the capture result as JSON                            |

**Which device gets captured**

| Situation                                      | Result                                                              |
| ---------------------------------------------- | ------------------------------------------------------------------- |
| `--udid` given, device booted                  | that device                                                         |
| `--udid` given, device not found or not booted | fails, exit 1                                                       |
| no `--udid`, exactly one booted                | that device                                                         |
| no `--udid`, none booted                       | fails, exit 1 - boot one yourself in Xcode or Simulator.app         |
| no `--udid`, several booted                    | fails, exit 1, listing a `--udid` line per device; it never guesses |

**Where the PNG goes**

By default `<AO data dir>/sim/<session id>/<timestamp>-<udid>.png` - your own session's artifact directory, outside any repository, so a screenshot can never be committed by accident. Two sessions capturing at once never collide. `--output` writes anywhere you like instead, and is also how you run this outside an AO session.

**Examples:**

```bash
# Capture the booted simulator, then read the printed path
ao sim shot
```

```bash
# Pick a device explicitly when several are booted
ao sim shot --udid 00000000-0000-0000-0000-000000000000
```

```bash
# Machine-readable result (path, bytes, device, capture time, lease)
ao sim shot --json
```

A capture never takes or waits for a lease, but it always reports one: if another session holds the device, the output says so and you must not drive it.

---

### ao sim ax

Read what is on a booted simulator's screen as a structured accessibility tree. **This, not the screenshot, is the primary way to check a screen**: it says what is there, whether it is enabled, and exactly where to touch it.

**Flags:**

| Flag              | Description                                   |
| ----------------- | --------------------------------------------- |
| `--udid <udid>`   | Read this simulator instead of the booted one |
| `--max-nodes <n>` | Stop after this many elements (default 500)   |
| `--json`          | Output the tree as JSON                       |

The device is resolved exactly like `ao sim shot`. Reading takes no lease and is never blocked by one, but the output always reports who holds the device.

Text output is one line per element, indented by nesting:

```
iPhone 17 Pro Max - 440x956 points, 24 elements (18 on screen, 6 off screen)
Foreground app: com.example.app (pid 42)
Device: 00000000-0000-0000-0000-000000000000
Lease: You hold this device until 2026-08-13T07:51:02Z. ...

Application  tap 0.500 0.500  box 0.000,0.000->1.000,1.000  [0]
  TextField "Search"  tap 0.500 0.126  box 0.045,0.105->0.955,0.146  [0.0]
  Button "Continue" (disabled)  tap 0.500 0.863  box 0.045,0.837->0.955,0.889  [0.1]
  Button "See all"  off screen  box 0.802,1.010->0.964,1.040  [0.2]
```

The header splits the count: how much of the screen you can touch now, and how much is only reachable after scrolling. On a real app screen most of the tree is often the second kind.

JSON shape (`--json`):

```json
{
	"screen": { "width": 440, "height": 956 },
	"frontmost": { "bundleId": "com.example.app", "pid": 42 },
	"elements": [
		{
			"path": "0.0",
			"id": "search-field",
			"role": "text field",
			"type": "TextField",
			"label": "Search",
			"value": "",
			"enabled": true,
			"frame": { "x": 20, "y": 100, "width": 400, "height": 40 },
			"tap": { "x": 0.5, "y": 0.1255 },
			"box": { "x1": 0.0454, "y1": 0.1046, "x2": 0.9545, "y2": 0.1464 },
			"children": []
		}
	],
	"nodeCount": 24,
	"totalNodeCount": 24,
	"truncated": false,
	"onScreenCount": 18,
	"offScreenCount": 6,
	"udid": "00000000-0000-0000-0000-000000000000",
	"name": "iPhone 17 Pro Max",
	"lease": { "state": "held", "holder": "your-project-12" }
}
```

- **`tap` is the whole point.** Feed `tap.x` and `tap.y` straight into `ao sim tap`. Never estimate a coordinate from a screenshot.
- **No `tap` means there is nowhere to touch it.** The element is on the page but off the screen (`"offScreen": true`). It used to report the nearest edge instead, which put a finger on whatever really is at that edge - most often the tab bar. Scroll to it and read again.
- **`box` is the element's four edges** (left, top, right, bottom) in the same 0..1 units, and is **not clipped to the screen**: a `y1` of 1.36 means "a third of a screen further down", which is how far to scroll. It also tells you the size and shape of a target - whether a row is a whole card or the chevron at the end of one.
- **`path`** (`0.1.2`) is the index path in this tree. It always exists; `id` (the app's own accessibility identifier) often does not.
- **`enabled: false`** means tapping it does nothing. Check it before blaming a tap that "did not work".
- **`truncated: true`** means the cap cut the tree; `totalNodeCount` is the real size and `--max-nodes` raises the cap. Nothing is ever dropped silently.
- **An empty tree fails** rather than reporting "no elements": the error names the frontmost bundle, which is usually the explanation (`com.apple.springboard` means you are looking at the home screen, not your app).
- **An empty tree is also checked against the app itself.** Before reporting one, `ao sim ax` samples the foreground app's main thread. If that thread never moves and is not in its run loop's own wait, the error says the app has a **blocked main thread**, names the frames it is stuck in, and says a tap will report success and change nothing too - because the same block eats touches. Accessibility is not the problem in that case. The check costs nothing on a read that worked, and falls back to the ordinary message whenever it cannot tell.
- **A tree that is only the status bar** (the clock and the battery) is what a read returns for about a second after an app comes to the front. It is read once more automatically before being reported, and says so if it stays that way - so treat it as "the app has not drawn yet", not "the app is blank".

---

### ao sim log

Read the device's unified log: what an app **says**, as opposed to what its screen shows. This is how you check a payload, an error, or a request that the UI does not put on screen.

**Flags:**

| Flag                 | Description                                                                              |
| -------------------- | ---------------------------------------------------------------------------------------- |
| `--udid <udid>`      | Read this simulator instead of the booted one                                            |
| `--process <name>`   | Only entries from this process - the **executable's** name (`Nimbus`), not the bundle id |
| `--grep <regex>`     | Only entries matching this regular expression, applied to the whole entry                |
| `--since <duration>` | How far back to read (`30s`, `2m`, `1h`). Default 2m. Not valid with `--follow`          |
| `--follow`, `-f`     | Stream entries as they happen instead of reading history                                 |
| `--max-lines <n>`    | Keep at most this many of the most recent entries (default 200)                          |
| `--json`             | Output entries as JSON - one object per line with `--follow`                             |

The device is resolved exactly like `ao sim shot`. Reading a log takes no lease and is never blocked by one, and the output reports who holds the device.

```bash
# What the app said in the last two minutes
ao sim log --process Nimbus
```

```bash
# Watch it live while you drive the screen from another command
ao sim log --follow --process Nimbus --grep "checkout|payment"
```

```bash
# Machine-readable, over a longer window
ao sim log --since 10m --process Nimbus --json
```

**⚠ `print` and `debugPrint` are NOT in this log, and never will be.** They write to the app's stdout, and an app launched by SpringBoard (tapped on the home screen, or started with `simctl launch`) has its stdout **discarded**. The output does not exist anywhere on the device. If you can see it in Xcode's console that is because Xcode drains the pipe itself - nothing else does.

So an empty `ao sim log` for a `print` you expected is the command working correctly. To read a payload:

| What the app uses      | Reaches `ao sim log`                   |
| ---------------------- | -------------------------------------- |
| `NSLog(...)`           | yes                                    |
| `os_log` / `Logger`    | yes                                    |
| `print` / `debugPrint` | **no** - goes to a stdout nobody keeps |

**Add a temporary `NSLog("resp: \(body)")` probe, run the flow, read it here, and take the probe out again.** That is the supported way to see a body, and it costs one line.

**There is no `--stdout` mode, on purpose.** The only way to capture stdout is to launch the app with a pipe attached, and a pipe nobody drains wedges the app's main thread (see the warning at the top of this page). AO will not ship a mode whose failure mode is a hung app under test.

**Nothing matched?** The command says so and lists which processes _did_ log in that window with their entry counts - a `--process` that matches nothing is nearly always the bundle id instead of the executable name. It exits 0: an empty log is an answer, not a failure.

---

### ao sim claim

Claim a booted simulator for this session, or renew a claim it already holds. Do this before any interaction that changes the device; skip it when you are only reading.

**Flags:**

| Flag               | Description                                                   |
| ------------------ | ------------------------------------------------------------- |
| `--udid <udid>`    | Claim this simulator instead of the booted one                |
| `--ttl <duration>` | How long to hold it (`30s`, `10m`, `1h`). Default 10m, max 1h |
| `--json`           | Output the claim as JSON                                      |

The device is resolved exactly like `ao sim shot` (see the table above): with several booted simulators it never guesses.

- **Renewal:** claiming a device you already hold extends it, so calling `ao sim claim` again before a long stretch of work is safe and is the intended way to keep a device.
- **Automatic release:** the lease lapses on its own after the TTL, and is released the moment this session ends. You can never permanently poison a device by crashing.
- **Contention:** if another session holds it, the command **fails (exit 1)** and names the holder and the time left. It never waits and never silently proceeds. Do something else and try later, or ask that session to release it.
- **What it does not cover:** a human driving the same simulator from Xcode. A lease excludes other AO sessions only.

```bash
# Claim the booted simulator for the default 10 minutes
ao sim claim
```

```bash
# Hold it just for one gesture
ao sim claim --ttl 30s
```

---

### ao sim release

Hand back the simulator this session holds, immediately.

**Flags:**

| Flag            | Description                                                  |
| --------------- | ------------------------------------------------------------ |
| `--udid <udid>` | Release this simulator instead of the one this session holds |
| `--json`        | Output the release as JSON                                   |

With no `--udid` it releases the one device you hold, and fails if you hold none or hold several. You cannot release someone else's lease. Releasing does not touch the simulator, so it works even if the device was shut down in the meantime.

```bash
# Done driving the device
ao sim release
```

---

### ao sim tap / swipe / type / button

The four ways to drive a screen. **All four require this session to hold the device** (`ao sim claim`); they never claim it for you, because AO cannot see whether a human is driving the same simulator from Xcode.

All of them take a `--udid` and a `--json` flag, resolve the device exactly like `ao sim shot`, and all coordinates are **normalized 0..1 of the screen** - the values `ao sim ax` prints.

| Command                                                              | What it does                                                                                                                                                                                                                                                                                        |
| -------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ao sim tap <x> <y>`                                                 | Press and release one point                                                                                                                                                                                                                                                                         |
| `ao sim tap --label <name>` / `--id <identifier>`                    | Press and release the element with that name. See below.                                                                                                                                                                                                                                            |
| `ao sim swipe <x1> <y1> <x2> <y2> [--duration 300ms]`                | Drag between two points - how you scroll a list or dismiss a sheet                                                                                                                                                                                                                                  |
| `ao sim drag <x1> <y1> <x2> <y2> [<x3> <y3> ...] [--duration 600ms]` | Hold one finger through a route of points without lifting. Two points is exactly a swipe; more is a path an app can tell apart from a flick - a scroll that changes direction, a drag onto a target. Sending the same route as separate swipes lifts between them, which reads as several gestures. |
| `ao sim type <text>`                                                 | Put text into whatever has keyboard focus (tap the field first). Uses key presses when the simulator will deliver them faithfully and the pasteboard when it would not, says which, and fails rather than claiming characters it did not deliver - see below.                                       |
| `ao sim button <name>`                                               | `home` (the swipe-up home gesture - your way back to a known screen) or `app-switcher`. The list is short on purpose: only buttons observably verified to change a real device are offered, because the mechanism reports success for ones that do nothing.                                         |

```bash
ao sim claim
ao sim ax                              # find the field's tap point
ao sim tap 0.5 0.126                   # focus it
ao sim type "hello@example.com"
ao sim swipe 0.5 0.8 0.5 0.2           # scroll down
ao sim drag 0.5 0.8 0.5 0.5 0.2 0.5    # one finger, three points, never lifting
ao sim button home                     # back to a known screen
ao sim ax                              # confirm what actually happened
```

**How these fail, and what each failure means**

| Failure                               | What it means                                                                                                                                             |
| ------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `... is leased by @other-session`     | Another AO session holds the device. Nothing was sent. Read-only commands still work; wait, or ask them to release.                                       |
| `this session has not claimed ...`    | Run `ao sim claim` first. AO never takes a device on your behalf.                                                                                         |
| `... is mid-gesture`                  | Another command holds the finger right now. Nothing was sent. Retry in a moment - it never queues, because two overlapping gestures merge into one touch. |
| `node was not found on PATH`          | The interaction commands need Node.js 20+. `ao sim shot` and `ao sim list` still work.                                                                    |
| `the simulator bridge could not load` | The native bridge calls private Apple frameworks and an Xcode/macOS upgrade broke it. Report it with your Xcode version; screenshots still work.          |
| `... is not booted`                   | Boot it yourself in Xcode or Simulator.app. AO never boots a device.                                                                                      |
| exit 2                                | Bad arguments (a coordinate outside 0..1, an unknown button, `--paste` and `--raw-keys` together). Nothing reached the device.                            |

- **`ao sim tap` can take the NAME of the element instead of its point.** `--label` matches the name `ao sim ax` prints for an element (its label, or its value when it has none); `--id` matches its accessibility identifier. This reads the screen first, so it costs one accessibility read - and it replaces the `ao sim ax` you would have run anyway, so the loop is shorter, not longer. The coordinate form reads nothing and is unchanged.

  ```bash
  ao sim tap --label "Continue"
  ao sim tap --id sign-in-button
  ```

  | Situation                                               | What happens                                                                                                                                                       |
  | ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
  | One element has that name                               | Tapped, and the output says it matched exactly, with the point it used                                                                                             |
  | No element has it exactly, one CONTAINS it              | Tapped, and the output says it was a contains-match, not the name you gave                                                                                         |
  | Two different elements answer to it                     | **Fails, exit 1**, listing each with the `ao sim tap <x> <y>` that picks it. It never guesses.                                                                     |
  | The name matches a control and the text drawn inside it | One target, not an ambiguity - the outer control is tapped                                                                                                         |
  | Nothing answers to it                                   | **Fails, exit 1**, listing what CAN be tapped right now, so you can fix the name in one round                                                                      |
  | It is below the fold                                    | **Fails, exit 1**, with how far down it is - scroll with `ao sim drag`, read again, then tap                                                                       |
  | It is disabled                                          | **Fails, exit 1** - tapping it would report success and change nothing. The message gives you the coordinate to override with if the app is wrong about the state. |
  | The app cannot answer at all                            | **Fails, exit 1**, saying its main thread is blocked - the same diagnosis `ao sim ax` gives                                                                        |

  Matching ignores case and surrounding spaces. `--label` and `--id` are separate namespaces: an identifier is set for automation and is stable, a label is copy that changes.

- **`ao sim type` puts the characters you asked for into the field - by whichever route can actually do that, and it tells you which.** The keys it can send are US-keyboard key presses and the SIMULATOR decides what each one produces. Simulator.app ships with _I/O > Keyboard > "Use the Same Keyboard Language as macOS"_ ticked, so a Mac set to Thai gives the simulator a Thai keyboard and `ao sim type "fa12345"` would arrive as `ดฟๅ/_ภถ`. So:

  | Situation                                       | What happens                                                                                  | Output says             |
  | ----------------------------------------------- | --------------------------------------------------------------------------------------------- | ----------------------- |
  | The simulator's keyboard sends US ASCII         | Key presses - the truer simulation, since an app sees each keystroke                          | `Typed …`               |
  | It would remap them, or will not say what it is | The text goes through the simulator's **pasteboard**, and is **checked on screen afterwards** | `Pasted …` + the reason |
  | The text is non-ASCII (Thai, emoji, accents)    | Pasteboard - no US keyboard key can send those at all                                         | `Pasted …`              |
  | Neither route can deliver                       | **Fails, exit 1** - nothing is claimed that did not happen                                    | -                       |

  Two things this protects you from, and they are why the command is fussy: the failure is **selective** (fields that force an ASCII keyboard - email, URL - came out right while ordinary and secure fields did not, so it looked like bad test data rather than a broken tool), and in a **secure field the characters are hidden behind dots**, so you cannot see the damage or read it back. A worker already lost time concluding a perfectly good QA account was invalid.

  **The paste is proven, never assumed.** The screen is read before and after, and the field must have grown by exactly the number of characters sent - which works even in a secure field, because it shows one dot per character. If an app refuses paste or the field never had focus, you get a loud failure, not a false success.

  **Two caveats worth knowing.** A pasted field receives one paste, not N keystrokes, so an app with live validation or a character counter behaves differently - use `--raw-keys` when you need real key presses. And while the paste happens, your text sits briefly on the **guest's** pasteboard, where any app on that simulator could read it; it is put back afterwards, and if it cannot be, the command says so loudly.

  | Flag         | What it promises                                                                                                                        |
  | ------------ | --------------------------------------------------------------------------------------------------------------------------------------- |
  | _(none)_     | The characters arrive. Route chosen per device, and reported.                                                                           |
  | `--paste`    | Always the pasteboard.                                                                                                                  |
  | `--raw-keys` | Key presses, and only key presses - whatever the simulator makes of them. This is how you deliberately enter Thai text on a Thai guest. |

  A field that reformats what it receives (a phone or card mask) can make the check fail on a paste that actually worked - the message says so and tells you to read it back with `ao sim ax`.

- **A failed gesture always releases the touch.** If a gesture dies in flight, the command sends the release anyway and says so; only if that release also fails does it warn that the device may need attention.
- **Success is not proof.** A tap can land on a disabled control or the wrong element and still report success. Always re-read with `ao sim ax`.
- **If a tap seems to do nothing AND `ao sim ax` comes back empty, the app itself may be stuck.** `ao sim ax` samples the foreground app's main thread before it reports an empty tree, and says so when that thread is blocked - with the frames that name what it is stuck in. A blocked main thread answers no accessibility query and processes no touch, so both symptoms have one cause and it is not accessibility. The usual cause is a pipe on the app's stdout that nobody is draining.
