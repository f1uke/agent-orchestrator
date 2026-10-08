# ao sim: debugging an app

`ao sim pid`, `ao sim lldb`, `ao sim launch --console` / `ao sim run --console`,
`ao sim console`, `ao sim crashes`. They act only on this session's own devices (the primary
one, or `--device <label>`), and pick the app as: the bundle id you pass, else `$AO_SIM_APP`,
else the newest installed app. Rules for every `ao sim` command are in [sim.md](sim.md); flags
are in `ao sim <command> --help`.

## print output

`print` and `debugPrint` go to the app's stdout, which is discarded for an app launched from the
home screen or a plain `simctl launch`. They never reach `ao sim log`.

```bash
ao sim launch --console && ao sim console --follow   # relaunches with stdout+stderr in a file
ao sim console --grep "resp:" --max-lines 50
```

A file never blocks the app, unlike a pipe. Only that launch is captured: a Maestro `launchApp`,
the home screen, or a launch without `--console` sends stdout back to `/dev/null`, and
`ao sim console` then says so above the lines. For output you need across a flow, use an
`NSLog` probe and `ao sim log` ([sim-screen.md](sim-screen.md#ao-sim-log)).

## lldb: bounded, and always detached

Use `ao sim lldb`, not a bare `lldb -p`: an interactive or forgotten lldb keeps the app attached
or stopped, and a stopped app answers no accessibility query and takes no touch.

```bash
# stop at a breakpoint, print, detach
ao sim lldb -- -o "breakpoint set -n ForecastStore.load" -o continue -o "frame variable" -o "bt 8"
# a logpoint for 10 seconds without stopping the app
ao sim lldb --continue-for 10s -- -o "breakpoint set -n VC.tick -C 'frame variable self.count' -G true"
```

It ends its own lldb at `--timeout` (default 2m), by pid, and checks afterwards that the app
runs detached; a second attach is refused, naming who holds the app.

**Wait for lldb to print `Process <pid> resuming` before you drive the app.** Attaching stops it
for several seconds while symbols load; a tap in that window reads an empty screen.

## An app frozen by a debugger

An app at a breakpoint, or left SIGSTOPped by an lldb that timed out, breaks the next check:

- `ao sim pid` prints its state on stderr: running, attached (naming the debugserver and lldb
  pids and how to detach), or stopped by SIGSTOP (with the `kill -CONT <pid>` that resumes it).
- `ao sim doctor`'s `debugger` line FAILs while any app on the device is held.
- `ao sim flow run` refuses to start Maestro, and `ao sim ax` / `ao sim tap --label` refuse at
  once on a SIGSTOPped app.

Fix: end the lldb that holds it, then resume with `kill -CONT <pid>` (or run the next
`ao sim lldb` with `--resume`).

## Crashes

`ao sim crashes` lists this device's crash reports, newest first; `--show 1` prints the newest
readably (exception, crashed thread's frames). Two measured facts: a report appears about 30 s
after the app dies, so wait and list again; and it does not carry a Swift `fatalError` or
`precondition` message, which `ao sim log --grep "Fatal error"` has.
