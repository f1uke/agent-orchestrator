# Slimming a simulator as it boots

Status: design approved, not implemented
Date: 2026-08-28

## Why

`ao sim boot` caps the machine at two booted simulators
(`backend/internal/cli/sim_boot.go:70`, `simBootMaxBooted`). The number is not
arbitrary: a stock simulator is a multi-GB virtual machine, and three at once
has run this machine out of memory. The cost of that cap is that a crew's dev
and qa cannot comfortably hold a device each.

[simslim](https://github.com/MobAI-App/simslim) writes persistent `launchctl
disable` entries for background daemons into a simulator's own launchd database
and reboots it. A measurement spike on this machine (M5 Pro, 24 GB, iOS 26.3,
iPhone 17 Pro) found:

|                                         | processes | phys_footprint |
| --------------------------------------- | --------- | -------------- |
| stock                                   | 217       | 3,671 MB       |
| slim, keeping what `nter-ios-app` needs | 86        | 1,236 MB       |

That is 2.97x less memory per device, with every behaviour probe (push,
universal links, photo library, keychain, biometrics, CallKit daemon liveness)
matching stock. The full measurement, the per-daemon evidence for the keep
list, and the reasoning behind every daemon dropped are recorded outside this
repo, in the AO knowledge store under
`spike-simslim-ram-measurement--research.md`.

This design covers the FIRST slice only: apply a project's profile when AO
boots a device. The memory cap stays at 2. See "Out of scope".

## The one failure mode this design is shaped around

The spike's most important finding is not a number. It is that
`xcrun simctl push` **returns exit 0 and prints "Notification sent"** when
`apsd` is disabled. The command reports success and the notification never
arrives.

That is the shape of every risk here: a daemon is off, a feature silently does
nothing, qa records a FAIL, and dev chases a ghost. So this design never lets
"the device is slim" be assumed. Every path either states which of four
outcomes happened, or says plainly that the device is stock.

## Configuration

A new opt-in field on `domain.ProjectConfig`:

```go
// SimProfile is the set of background daemons this project's simulators keep
// running. nil means AO does not slim this project's devices at all, which is
// what every project that says nothing gets.
SimProfile *SimProfileConfig `json:"simProfile,omitempty"`

type SimProfileConfig struct {
    // Keep lists the daemons that stay enabled; everything simslim manages and
    // is not named here is disabled. An empty (but present) Keep means a fully
    // slim device.
    Keep []string `json:"keep,omitempty"`
}
```

It is a pointer, not a value, for one reason: `nil` (do not slim) and
`{"keep": []}` (slim everything) are different instructions, and a value type
cannot tell them apart. Opt-in matches `HasWebUI` and `HasIOSSimulator` in the
same file, and for the same reason given there — a project that says nothing
must behave exactly as it does today.

For `nter-ios-app` the profile is:

```json
"simProfile": {
  "keep": [
    "com.apple.apsd",
    "com.apple.swcd",
    "com.apple.assetsd",
    "com.apple.telephonyutilities.callservicesd"
  ]
}
```

`ProjectConfig.Validate()` rejects an entry that is empty, carries whitespace,
or does not start with `com.apple.`. A typo in this list is a daemon that is
silently not kept, which is the failure mode above; simslim also rejects labels
it does not recognise, so a mistake fails loudly twice.

Enabling another daemon later is editing this list. Named add-on groups
(`--with widgets`) are deliberately not in this slice: an add-on is only useful
once the boot guard charges for it, and that guard is slice 2. `{"keep": [...]}`
extends to `{"base": {...}, "addons": {...}}` without a rewrite.

## Components

### `backend/internal/simslim` (new)

Knows simslim's command line. Knows nothing about AO.

```go
const Binary = "simslim"

type Profile struct { Keep []string }

type Outcome string
const (
    Applied Outcome = "applied" // drifted, so `simslim on` ran and rebooted it
    Already Outcome = "already" // `simslim verify` passed; nothing was run
    Skipped Outcome = "skipped" // simslim is not on PATH; the device is stock
    Failed  Outcome = "failed"  // simslim ran and refused; the device is stock
)

type Result struct {
    Outcome Outcome
    Reason  string // the tool's own words, for Skipped and Failed
}

func Apply(ctx context.Context, lookPath simctl.LookPath, run simctl.Runner,
           udid string, p Profile) Result
```

`Apply` runs `simslim verify` first and only runs `simslim on` when verify
reports drift. This is load-bearing, not an optimisation: **`simslim on` reboots
the device every time, even when nothing changes** (measured, 19-27s on this
machine). Calling `on` unconditionally would add a second reboot to every boot
for the life of the feature. `verify` does not reboot.

There is no `Outcome` that means "fine" without saying which case it was.

It takes the same `simctl.LookPath` and `simctl.Runner` the rest of the sim
packages take, so it is testable with the existing `recorder` fake and needs
neither Xcode nor a device.

### `backend/internal/simpower` (changed)

```go
func (p *Power) Start(ctx context.Context, udid string, op Op,
                      prof *simslim.Profile, done func()) error
```

In `execute`, after `simctl bootstatus -b` succeeds and only then, `Apply` runs.
The operation is not settled until it finishes.

Doing the profile step inside the boot — rather than after it, from a separate
component — is the same decision `plan()` already documents for choosing
`bootstatus -b` over `boot`: there must be exactly one definition of "this
device is ready". `simslim on` reboots the device, so a boot that reported
success before the profile step would hand `ao sim claim` a device that is
going down. The alternative (a separate applier running after Power settles)
was rejected for exactly this reason.

`Status` gains:

```go
Phase   string          // "booting" | "slimming"
Profile *simslim.Result
```

`Phase` exists because the profile step takes tens of seconds during which the
Device tab would otherwise look frozen.

A new `State` value, `Warned`, changes what happens on success:

| Outcome              | entry                     | what a human sees                                                                                 |
| -------------------- | ------------------------- | ------------------------------------------------------------------------------------------------- |
| `Applied`, `Already` | deleted, as today         | nothing; it worked                                                                                |
| `Skipped`, `Failed`  | **kept, `State: Warned`** | a row saying this device is stock, which stays until the device shuts down or the daemon restarts |

Today `execute` deletes the entry on success because "the device's own state is
the answer now". That stops being true here: whether the device is slim is a
fact its power state does not carry. `Warned` is not `Failed` — **the boot
succeeded and is reported as succeeding**. It only refuses to let the outcome
be silent.

The stock row has no dismiss control, and that is deliberate rather than an
omission. `ClearPower` drops a stale `Failed` whose goal the machine has since
reached; there is no equivalent for `Warned`, because there is nothing stale
about it - the device really is stock, and it stays stock until it is shut down
or reprofiled. A row a reader can wave away is a row the second crewmate never
sees.

`profileTimeout` is a field alongside `bootTimeout`, so tests do not wait on
real time and slow hosts can be given more.

### `backend/internal/httpd/controllers` (changed)

The power route is already session-scoped
(`/sessions/{sessionId}/sim-devices/{udid}/power`), so the session — and
through it the project and its config — is already in hand.

`SimScreenController` gains one narrow dependency, injected the way `Leases` is:

```go
type SimProfileResolver interface {
    SimProfileFor(ctx context.Context, id domain.SessionID) (*simslim.Profile, error)
}
```

A nil resolver means no slimming, which keeps every existing test and every
existing deployment working unchanged.

The resolver owns the mapping from `domain.SimProfileConfig` to
`simslim.Profile`, so `domain` never imports `simslim` and the tool's vocabulary
does not leak into the config types.

**If the resolver itself returns an error** — the session or its project cannot
be read — the boot proceeds unslimmed and reports outcome `Failed` with that
error as the reason. It does not fail the boot, for the same reason a missing
simslim does not, and it does not quietly boot as if no profile were configured:
"this project asked for a profile and we could not find out which" is exactly
the kind of thing this design refuses to let pass silently.

`Screen.StartPower` and `Power.Start` take the resolved `*simslim.Profile` as a
parameter. Neither `Screen` nor `Power` ever looks a profile up: they are
device-level components, and keeping them ignorant of projects is what keeps
them testable over a bare fake runner.

## Reporting

1. **`ao sim boot`** — `simBootResult` gains `profile` and `profileReason`. The
   text output states plainly that the device is stock when the outcome is
   `Skipped` or `Failed`.
2. **Daemon listing** — `SimDevicePowerView` gains `phase`, `profile` and
   `profileReason`.
3. **Device tab** — renders "Slimming…" during the phase, and afterwards a
   dismissible warning row when the device ended up stock.

## Error handling

If simslim is missing, or slimming fails, **the boot still succeeds** and the
outcome is reported loudly. `ao sim boot` exists because qa could not otherwise
be created at all (#265); failing a boot over a missing optional tool would
regress that deadlock fix to buy nothing.

A boot that fails is unchanged: it fails with the machine's own reason, and the
half-booted device is left where it is, as documented in `reason()`.

## Testing

TDD; tests are written before the code.

**`internal/simslim`**, over the existing `recorder` fake:

- verify passes → `Already`, and **`simslim on` is never invoked** — this is the
  test that guards against a second reboot on every boot
- verify reports drift → `on` runs → `Applied`
- `lookPath` fails → `Skipped`, and nothing is executed at all
- `on` fails → `Failed`, carrying the tool's stderr
- empty `Keep` → the commands carry no `--keep` flag

**`internal/simpower`**:

- `recorder.calls()` shows `bootstatus` before `simslim` — order, not just presence
- **a nil profile runs no simslim command whatsoever** — the regression guard for
  every project that has not opted in
- `Skipped` / `Failed` leave a `Warned` entry; `Applied` / `Already` leave none
- `Phase` reads `slimming` while the profile step runs
- the profile step honours `profileTimeout`, not `bootTimeout`

**controllers**: a session whose project has no profile reaches `StartPower`
with `nil`.

## Known gap in this slice

The power route answers `409 SIM_POWER_ALREADY` for a device that is already
booted. So **a device a human booted by hand is never slimmed, and raises no
warning either.** This slice slims only the boots AO performs. This is a known
limitation, recorded here rather than discovered later; the human's own
`UI Test 17 Pro Max` is exactly this case and can be slimmed by hand with
`simslim on <udid> --keep ...`.

## Out of scope

Deliberately not in this slice, each needing its own design:

- **the memory cap** — `simBootMaxBooted` stays 2. Making the guard charge a
  RAM budget per booted device (rather than counting devices) is slice 2, and
  it is what a named add-on would need in order to mean anything.
- **qa doctor preflight** — `simslim doctor --requires ...` before a qa run.
  Note for whoever picks this up: doctor reports `photos` BROKEN on a device
  where the photo library demonstrably works, because its `photos` feature
  includes `photoanalysisd`, which the picker does not need. A preflight
  without a false-positive allow-list would fail permanently and be ignored.
- **named add-on groups** (`--with widgets`)
- **stamping the profile name into evidence PNGs**, next to the build
  fingerprint from #272
- **re-applying a profile after `erase`**, which resets a device to stock
