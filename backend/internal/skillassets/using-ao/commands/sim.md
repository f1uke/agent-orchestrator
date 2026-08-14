# ao sim

Local iOS Simulators on this machine: list them, read what is on a booted one's screen (as an accessibility tree or a PNG), drive it with taps, swipes, typing and hardware buttons, and claim one so other AO sessions keep off it while you work.

`ao sim` never boots, shuts down, reboots or erases a simulator. It runs no background process, opens no port and polls nothing: each command runs, does its one job, and exits. Claiming a device changes nothing about the device itself - a lease is bookkeeping the AO daemon holds, not an operation on the simulator.

Simulators are shared: another AO session, or a human working in Xcode, may be driving the same device. A captured frame can therefore be mid-interaction and is not proof that you put the app in that state.

**Claim a device before you drive it.** A simulator has one finger and no per-caller state, so two sessions interacting at once merge into a single teleporting touch, and one session's release lifts the other's finger. A lost release wedges the device's input until somebody reboots it - which breaks whoever else is mid-test. The commands that touch the screen refuse to run unless this session holds the device, and refuse again while another gesture is in flight. Reading (`ao sim list`, `ao sim shot`, `ao sim ax`) never needs a claim.

**Read the screen, then act on what you read - never on what you expect.** `ao sim ax` gives every element a `tap` point in the same 0..1 coordinates `ao sim tap` takes, so acting on a screen is copy-the-number, not estimate-from-a-picture. After any interaction, read again: a tap that reports success has not necessarily changed anything.

Requires macOS with the Xcode command line tools (`xcrun` on PATH). The interaction commands additionally need Node.js 20+ on PATH; `ao sim list` and `ao sim shot` do not.

## Syntax

```
ao sim list    [flags]
ao sim shot    [flags]
ao sim ax      [flags]
ao sim claim   [flags]
ao sim release [flags]
ao sim tap    <x> <y>          [flags]
ao sim swipe  <x1> <y1> <x2> <y2> [flags]
ao sim type   <text>           [flags]
ao sim button <name>           [flags]
```

## The loop that works

```bash
ao sim claim                 # once, before you drive anything
ao sim ax                    # read the screen; every element carries its tap point
ao sim tap 0.5 0.934         # act on a point the tree gave you
ao sim ax                    # read again to confirm what actually changed
ao sim release               # when you are done
```

---

### ao sim list

List every simulator `xcrun simctl` reports, with its udid, state, runtime, name and lease, and say which one `ao sim shot` would pick.

**Flags:**

| Flag | Description |
|---|---|
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

| Flag | Description |
|---|---|
| `--udid <udid>` | Capture this simulator instead of the booted one |
| `--output <path>` | Write the PNG here instead of the session artifact directory |
| `--json` | Output the capture result as JSON |

**Which device gets captured**

| Situation | Result |
|---|---|
| `--udid` given, device booted | that device |
| `--udid` given, device not found or not booted | fails, exit 1 |
| no `--udid`, exactly one booted | that device |
| no `--udid`, none booted | fails, exit 1 - boot one yourself in Xcode or Simulator.app |
| no `--udid`, several booted | fails, exit 1, listing a `--udid` line per device; it never guesses |

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

| Flag | Description |
|---|---|
| `--udid <udid>` | Read this simulator instead of the booted one |
| `--max-nodes <n>` | Stop after this many elements (default 500) |
| `--json` | Output the tree as JSON |

The device is resolved exactly like `ao sim shot`. Reading takes no lease and is never blocked by one, but the output always reports who holds the device.

Text output is one line per element, indented by nesting:

```
iPhone 17 Pro Max - 440x956 points, 22 elements
Foreground app: com.example.app (pid 42)
Device: 00000000-0000-0000-0000-000000000000
Lease: You hold this device until 2026-08-13T07:51:02Z. ...

Application  tap 0.500 0.500  [0]
  TextField "Search"  tap 0.500 0.126  [0.0]
  Button "Continue" (disabled)  tap 0.500 0.863  [0.1]
```

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
      "children": []
    }
  ],
  "nodeCount": 22,
  "totalNodeCount": 22,
  "truncated": false,
  "udid": "00000000-0000-0000-0000-000000000000",
  "name": "iPhone 17 Pro Max",
  "lease": { "state": "held", "holder": "your-project-12" }
}
```

- **`tap` is the whole point.** Feed `tap.x` and `tap.y` straight into `ao sim tap`. Never estimate a coordinate from a screenshot.
- **`path`** (`0.1.2`) is the index path in this tree. It always exists; `id` (the app's own accessibility identifier) often does not.
- **`enabled: false`** means tapping it does nothing. Check it before blaming a tap that "did not work".
- **`truncated: true`** means the cap cut the tree; `totalNodeCount` is the real size and `--max-nodes` raises the cap. Nothing is ever dropped silently.
- **An empty tree fails** rather than reporting "no elements": the error names the frontmost bundle, which is usually the explanation (`com.apple.springboard` means you are looking at the home screen, not your app).

---

### ao sim claim

Claim a booted simulator for this session, or renew a claim it already holds. Do this before any interaction that changes the device; skip it when you are only reading.

**Flags:**

| Flag | Description |
|---|---|
| `--udid <udid>` | Claim this simulator instead of the booted one |
| `--ttl <duration>` | How long to hold it (`30s`, `10m`, `1h`). Default 10m, max 1h |
| `--json` | Output the claim as JSON |

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

| Flag | Description |
|---|---|
| `--udid <udid>` | Release this simulator instead of the one this session holds |
| `--json` | Output the release as JSON |

With no `--udid` it releases the one device you hold, and fails if you hold none or hold several. You cannot release someone else's lease. Releasing does not touch the simulator, so it works even if the device was shut down in the meantime.

```bash
# Done driving the device
ao sim release
```

---

### ao sim tap / swipe / type / button

The four ways to drive a screen. **All four require this session to hold the device** (`ao sim claim`); they never claim it for you, because AO cannot see whether a human is driving the same simulator from Xcode.

All of them take a `--udid` and a `--json` flag, resolve the device exactly like `ao sim shot`, and all coordinates are **normalized 0..1 of the screen** - the values `ao sim ax` prints.

| Command | What it does |
|---|---|
| `ao sim tap <x> <y>` | Press and release one point |
| `ao sim swipe <x1> <y1> <x2> <y2> [--duration 300ms]` | Drag between two points - how you scroll a list or dismiss a sheet |
| `ao sim type <text>` | Send text to whatever has keyboard focus (tap the field first) |
| `ao sim button <name>` | `home` (the swipe-up home gesture - your way back to a known screen) or `app-switcher`. The list is short on purpose: only buttons observably verified to change a real device are offered, because the mechanism reports success for ones that do nothing. |

```bash
ao sim claim
ao sim ax                              # find the field's tap point
ao sim tap 0.5 0.126                   # focus it
ao sim type "hello@example.com"
ao sim swipe 0.5 0.8 0.5 0.2           # scroll down
ao sim button home                     # back to a known screen
ao sim ax                              # confirm what actually happened
```

**How these fail, and what each failure means**

| Failure | What it means |
|---|---|
| `... is leased by @other-session` | Another AO session holds the device. Nothing was sent. Read-only commands still work; wait, or ask them to release. |
| `this session has not claimed ...` | Run `ao sim claim` first. AO never takes a device on your behalf. |
| `... is mid-gesture` | Another command holds the finger right now. Nothing was sent. Retry in a moment - it never queues, because two overlapping gestures merge into one touch. |
| `node was not found on PATH` | The interaction commands need Node.js 20+. `ao sim shot` and `ao sim list` still work. |
| `the simulator bridge could not load` | The native bridge calls private Apple frameworks and an Xcode/macOS upgrade broke it. Report it with your Xcode version; screenshots still work. |
| `... is not booted` | Boot it yourself in Xcode or Simulator.app. AO never boots a device. |
| exit 2 | Bad arguments (a coordinate outside 0..1, an unknown button, a character a US keyboard cannot type). Nothing reached the device. |

- **Typing sends US-keyboard key presses, and the SIMULATOR decides what they produce.** Characters a US keyboard cannot send are refused rather than silently dropped, but if the simulator's own active input source is not US English (a Thai or Japanese keyboard, say), the letters that appear will not be the ones you typed. This is a real, observed outcome - always re-read the field with `ao sim ax` and check the value, never assume.
- **A failed gesture always releases the touch.** If a gesture dies in flight, the command sends the release anyway and says so; only if that release also fails does it warn that the device may need attention.
- **Success is not proof.** A tap can land on a disabled control or the wrong element and still report success. Always re-read with `ao sim ax`.
