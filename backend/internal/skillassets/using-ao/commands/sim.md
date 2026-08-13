# ao sim

Local iOS Simulators on this machine: list them, capture a booted one's screen as a PNG you can read, and claim one so other AO sessions keep off it while you work.

`ao sim` never boots, shuts down, reboots or erases a simulator, and never synthesizes taps, typing or gestures. It runs no background process and polls nothing. Claiming a device changes nothing about the device itself - a lease is bookkeeping the AO daemon holds, not an operation on the simulator.

Simulators are shared: another AO session, or a human working in Xcode, may be driving the same device. A captured frame can therefore be mid-interaction and is not proof that you put the app in that state.

**Claim a device before you drive it.** A simulator has one finger and no per-caller state, so two sessions interacting at once merge into a single teleporting touch, and one session's release lifts the other's finger. A lost release wedges the device's input until somebody reboots it - which breaks whoever else is mid-test. Reading (`ao sim list`, `ao sim shot`) never needs a claim.

Requires macOS with the Xcode command line tools (`xcrun` on PATH).

## Syntax

```
ao sim list    [flags]
ao sim shot    [flags]
ao sim claim   [flags]
ao sim release [flags]
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
