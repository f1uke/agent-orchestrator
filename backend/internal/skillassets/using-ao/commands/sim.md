# ao sim

Read-only access to the local iOS Simulators on this machine: list them, and capture a booted one's screen as a PNG you can read.

Every subcommand shells out to `xcrun simctl` on demand and is **read-only against the device**. `ao sim` never boots, shuts down, reboots or erases a simulator, and never synthesizes taps, typing or gestures. It runs no background process and polls nothing.

Simulators are shared: another AO session, or a human working in Xcode, may be driving the same device. A captured frame can therefore be mid-interaction and is not proof that you put the app in that state.

Requires macOS with the Xcode command line tools (`xcrun` on PATH).

## Syntax

```
ao sim list [flags]
ao sim shot [flags]
```

---

### ao sim list

List every simulator `xcrun simctl` reports, with its udid, state, runtime and name, and say which one `ao sim shot` would pick.

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
      "default": true
    }
  ],
  "defaultUdid": "00000000-0000-0000-0000-000000000000",
  "defaultReason": "the only booted simulator"
}
```

`defaultUdid` is `null` when there is no unambiguous choice, and `defaultReason` says why.

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
# Machine-readable result (path, bytes, device, capture time)
ao sim shot --json
```
