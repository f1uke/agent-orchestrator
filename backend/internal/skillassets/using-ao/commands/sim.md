# ao sim

Local iOS Simulators: your own devices, reading a screen, driving it, building onto it,
debugging the app on it, and Maestro flows. Requires macOS and Xcode's command line tools;
the gesture commands also need Node.js 20+. Flags and arguments: `ao sim <command> --help`.

## Rules for every command

- **You may power a simulator on, nothing else.** `ao sim boot` is the only command that
  changes power state. Shutdown, reboot and erase are the human's, from the desktop app's
  Device tab.
- **Your devices are clones AO made for this session, and `$AO_SIM_UDID` is the primary one.**
  `ao sim` uses it with no `--udid`. Other tools must be told:
  `xcodebuild -destination "$AO_SIM_DESTINATION"`, `maestro --device "$AO_SIM_UDID"`. Unset
  means AO could not make one: run `ao sim claim`, which makes it or says what is missing.
  Never fall back to whichever device happens to be booted.
- **Claim before you drive; reading needs no claim.** A simulator has one finger, so two
  sessions driving at once merge into one touch and can wedge its input. Gesture commands
  refuse without your claim. `list`, `shot`, `ax`, `log` and `record` never need one, and a
  frame you capture may show someone else mid-gesture.
- **Read the screen, then act on what you read.** Tap the name or point `ao sim ax` gives
  you, never a point estimated from a screenshot. A command that reports success has not
  necessarily changed anything: read again.
- **Never attach a pipe to an app's stdout** (`xcrun simctl launch --console-pipe`). When
  nothing drains it, the app blocks on its main thread: `ao sim ax` comes back empty, taps
  "succeed" and change nothing. Use `ao sim log`, or `ao sim launch --console` with
  `ao sim console`.
- **On a script-only project, every check is a script run and you read what it left.** A
  project with `mobileScripts` on
  (`ao project set-config <id> --mobile-scripts <product> --mobile-platform ios|android`)
  verifies a change and takes evidence by running the reusable Maestro script from the
  scripts store (`<store>/bin/flow run <product> reach/<script>`), then judging the end
  state with `ao sim shot`, `ao sim ax` and `ao sim log`. dev and a solo worker may drive by
  hand while finding a cause and debugging, then re-test the fix with a script.
  qa uses them only to author a script nobody has written yet, with `ao sim flow record`.
  A screen reached by hand is never evidence. A failed script is read, fixed or reported,
  and re-run, never finished by hand.

## The loop

```bash
ao sim claim                  # once, before you drive anything
ao sim ax                     # read the screen
ao sim tap --label "Continue" # act on what you read, by name (or by the point ax printed)
ao sim ax                     # read again to see what actually changed
ao sim release                # when you are done
```

## Which page

| Page | Commands | Read it when |
|---|---|---|
| [sim-devices.md](sim-devices.md) | `ao sim list`, `ao sim boot`, `ao sim claim`, `ao sim release`, `ao sim udid`, `ao sim doctor` | Choosing, claiming or adding a device (another model, a second phone); a lease refusal; an app stuck on its splash screen |
| [sim-screen.md](sim-screen.md) | `ao sim ax`, `ao sim shot`, `ao sim log`, `ao sim record` | Checking what a screen shows, what an app logged, or filming it |
| [sim-drive.md](sim-drive.md) | `ao sim tap`, `ao sim swipe`, `ao sim drag`, `ao sim pinch`, `ao sim type`, `ao sim key`, `ao sim button` | Driving a screen, or a gesture or typed text that did not do what you expected |
| [sim-build.md](sim-build.md) | `ao sim run`, `ao sim install`, `ao sim launch` | Putting your build on a device and making sure the screen runs it |
| [sim-debug.md](sim-debug.md) | `ao sim pid`, `ao sim lldb`, `ao sim console`, `ao sim crashes` | Reading `print` output, breakpoints, crashes, an app frozen by a debugger |
| [sim-maestro.md](sim-maestro.md) | `ao sim flow check`, `ao sim flow run`, `ao sim flow record` | Running or recording Maestro flows |

`--device <label>` on any command acts on one of your extra devices instead of the primary
one. `ao sim record` films the SCREEN; `ao sim flow record` records the GESTURES you drive as
a Maestro flow (the old `ao sim record` spelling for that fails).
