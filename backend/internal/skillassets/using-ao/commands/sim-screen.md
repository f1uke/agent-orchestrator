# ao sim: reading the screen and the log

`ao sim ax`, `ao sim shot`, `ao sim log`, `ao sim record`. None of them needs a claim, and each
reports who holds the device. Rules for every `ao sim` command are in [sim.md](sim.md); flags
are in `ao sim <command> --help`.

## ao sim ax

The primary way to check a screen: what is on it, whether it is enabled, and where to touch it.
Each line is one element, indented by nesting:

```
Button "Continue" (disabled)  tap 0.500 0.863  box 0.045,0.837->0.955,0.889  [0.1]
Button "See all"  off screen  box 0.802,1.010->0.964,1.040  [0.2]
Button "Next"  covered by Toolbar "Toolbar", no part of it left to touch  box ...  [0.3]
StaticText "Top story"  tap 0.695 0.898 (the part still showing - its centre is under TabBar "Tab Bar")  ...
```

- **`tap x y` is the point to pass to `ao sim tap`.** No `tap` means there is nowhere to touch
  it now.
- **`off screen`**: scroll it into view with `ao sim drag` or `ao sim swipe`, then read again.
  `box` is not clipped to the screen, so a `y1` of 1.36 says how far down it is.
- **`covered by ...`**: something is drawn over it (the tab bar, the keyboard's `^ v Done` bar,
  a sheet). Scroll it clear or close the keyboard, read again, then tap.
- **`(disabled)`**: a tap does nothing. Check it before blaming a tap that "did not work".
- **`Reader:` line.** On a device some session holds, an XCTest runner reads every process on
  screen: a web sign-in sheet (SafariViewService), system alerts, the Paste menu, the keyboard,
  and which field has focus. With no claim, the accessibility bridge reads the frontmost app
  only, and those are missing. Claim the device when you need them.
- **`Keyboard: ... NOT Latin letters`** means a non-English input mode is up; `ao sim type`
  still delivers the right characters (see [sim-drive.md](sim-drive.md)).
- **An empty tree is a diagnosis, not "no elements".** The error names the frontmost app
  (`com.apple.springboard` is the home screen). When it says the app's **main thread is
  blocked**, a tap will also report success and change nothing: the usual cause is an
  undrained stdout pipe (see [sim.md](sim.md)), or an app stopped by a debugger
  (see [sim-debug.md](sim-debug.md)).
- **Only the status bar** for about a second after an app comes forward means it has not drawn
  yet; `--settle` reads until two reads agree.
- `--format maestro` prints Maestro selectors: see [sim-maestro.md](sim-maestro.md).

## ao sim shot

Captures a PNG into this session's artifact directory (outside every repository) and prints
its path; read that path to look at it.

**Every capture names the build it saw**, on a `Build:` line and inside the PNG:

```
Build: com.example.MyApp 6.5.18 (708) cdhash:d114bed635ed2eb91f5c
```

`xcodebuild test` installs the app as part of testing, so two screenshots that look the same
can be of different software: captures whose `Build:` lines differ are not evidence of the
same thing. With several apps installed it fingerprints the newest one and says so; pin it with
`--app <bundle-id>` or `AO_SIM_APP` in the project's environment.

## ao sim log

The device's unified log: what an app says, as opposed to what its screen shows. `--process`
takes the executable's name (`Nimbus`), not the bundle id; when nothing matches, the output
lists which processes did log.

| What the app uses      | `ao sim log` | `ao sim console` (after `--console` launch) |
| ---------------------- | ------------ | ------------------------------------------- |
| `NSLog(...)`           | yes          | yes (stderr)                                |
| `os_log` / `Logger`    | yes          | no                                          |
| `print` / `debugPrint` | **no**       | yes, until something else relaunches the app |

So an empty `ao sim log` for a `print` is correct. To read `print`, see
[sim-debug.md](sim-debug.md). For a payload you need across a Maestro flow (whose `launchApp`
drops the console capture), add a temporary `NSLog("resp: \(body)")` probe, run the flow, read
it with `ao sim log`, and take the probe out again.

## ao sim record

Films the screen to a `.mov` (`start`, `status`, `stop`), for what a still frame cannot show:
an animation, a transition, a cold launch. No lease is needed, so you can film a device a human
is driving. Only the session that started a recording can stop it. It stops itself after
`--max-duration` (default 10m) and when the session ends. A screen that does not change records
almost nothing, so the video can be shorter than the wall-clock time. Videos land in the
session's `videos/` directory, pruned to the newest 10.
