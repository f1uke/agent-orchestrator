# ao sim: your devices

`ao sim list`, `ao sim boot`, `ao sim claim`, `ao sim release`, `ao sim udid`, `ao sim doctor`.
Rules that apply to every `ao sim` command are in [sim.md](sim.md); flags are in
`ao sim <command> --help`.

## Another model, or a second device

```bash
ao sim claim --model "iPhone SE"      # clones that base for you and claims it; label: iphone-se
ao sim shot --device iphone-se        # --device <label> works on ANY ao sim command
ao sim release --device iphone-se     # DELETES it now (plain `ao sim release` only drops your lease)
ao sim claim --device advisor         # a second device of the default model: the other side of a chat
xcodebuild -destination "id=$(ao sim udid --device advisor)" ...   # tools that need a udid
```

Models: `"iPhone SE"` (label `iphone-se`), `"iPad Pro 11-inch"` (`ipad-pro-11`). Every extra
device is deleted when the session ends. Run one Maestro run per device; several devices can
run at once.

**Bases are templates, never work devices.** "iPhone 17 Pro Max", "iPhone SE (3rd generation)"
and "iPad Pro 11-inch (M5)" are what AO clones from. `ao sim list` marks them `base` and AO
refuses to claim or boot one, because anything run on a base is in every clone made after it.
A missing base is reported with the `xcrun simctl create` command that makes it.

## Leases

- **Claim again to renew.** A claim lapses after its TTL (default 10m, max 1h) and is released
  when the session ends. Re-claiming before a long stretch of work is the intended way to keep
  a device.
- **Another holder means exit 1, never a wait.** It names the holder and the time left. Do
  something else and retry, or ask that session to release it.
- **Leases are machine-wide.** A lease taken through any AO daemon on this Mac (a sandbox
  daemon with its own `AO_DATA_DIR` too) is refused by all of them. A holder on another daemon
  is named with that daemon; its session id can equal yours and still be someone else.
- **A lease does not cover a human in Xcode.** `ao sim list` shows lease state `held` or
  `unknown`, never `free`. Treat `unknown` as "probably yours to claim", not "proven idle".

## Booting

`ao sim boot` returns when the device can actually be driven (SpringBoard up), not when simctl
first says Booted. Booting a booted device is a no-op, so retrying is safe. With several
devices and none booted it refuses and lists them rather than guessing.

It stays under a machine-wide cap of booted simulators (4 unless the human changed it). At the
cap it shuts down AO's least recently booted clone that nobody leases, and says which; with
none idle it refuses and names who holds each slot: release a device you are done with
(`ao sim release --device <label>`), or ask the human. Booting claims nothing.

## An app stuck on its splash screen: root CAs

This Mac sends traffic through a TLS-intercepting debugging proxy, and a simulator does not
inherit the Mac's trust store. A device that does not trust the proxy's root CA fails every
HTTPS call (`-1200` / `-9802`, `Trust evaluate failure` in `ao sim log`), and the app sits on
its splash screen. That looks like an app or backend bug and is neither.

Every `ao sim claim` (renewals too) and `ao sim boot` makes the device trust the configured CAs
and prints `Trusted root CA: <file>`; a `Warning:` there means HTTPS through the proxy will fail
on that device. Claiming again fixes a device that missed it. Which CAs: the project's
`ao project set-config <id> --sim-trust-ca <file>`, else the global setting (Proxyman's CA).

## Is the device ready?

`ao sim doctor` only reads, and prints one `OK`/`WARN`/`FAIL` line per check (device, lease,
app, debugger, proxy CA); each FAIL names the command that fixes it. It exits 1 only on FAIL.
With `--app <bundle-id> --expect <path/to/App.app>` it compares the installed bytes (cdhash),
so a stale install with the right version number still fails.
