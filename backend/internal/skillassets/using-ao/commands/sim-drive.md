# ao sim: driving a screen

`ao sim tap`, `ao sim swipe`, `ao sim drag`, `ao sim pinch`, `ao sim type`, `ao sim key`,
`ao sim button`. Every one needs this session's claim (`ao sim claim`) and never takes one for
you. Coordinates are 0..1 of the screen, the values `ao sim ax` prints. Rules for every
`ao sim` command are in [sim.md](sim.md); flags are in `ao sim <command> --help`.

## Gestures

```bash
ao sim tap --label "Continue"           # or --id sign-in-button, or the point: ao sim tap 0.5 0.934
ao sim swipe 0.5 0.8 0.5 0.2            # scroll down, dismiss a sheet
ao sim drag 0.5 0.8 0.5 0.5 0.2 0.5     # one finger through a route, never lifting
ao sim pinch 0.5 0.5 0.2 0.6            # two fingers spread from 0.2 to 0.6 of the width: x3
ao sim key enter                        # enter, backspace, tab, arrow-*; --times 20 repeats
ao sim button home                      # home or app-switcher: the way back to a known screen
ao sim ax                               # always read again: success is not proof
```

- **`drag`, not several `swipe`s, for one continuous gesture.** Separate swipes lift the finger
  between them, which an app reads as several flicks.
- **A pinch lands a little short** (x3 asked arrived as about x2.7); a slower `--duration` lands
  closer, and an app at its zoom limit ignores it. Read the result.
- **`button` offers only buttons proven to work**, because the mechanism reports success for
  ones that do nothing.

## Tapping by name

`--label` matches the name `ao sim ax` shows (its label, or its value when it has none); `--id`
matches the accessibility identifier, which is stable where a label is copy. It reads the screen
first, which replaces the `ao sim ax` you would have run anyway. Matching ignores case and falls
back to a name that CONTAINS the text, saying so. It refuses rather than guesses, exit 1, when:

- two different elements answer to the name: it lists the `ao sim tap <x> <y>` for each;
- nothing answers: it lists what can be tapped now;
- the element is off screen or covered: scroll it clear or close the keyboard, read, tap;
- the element is disabled: it gives the point to override with if the app is wrong;
- the app's main thread is blocked: the same diagnosis `ao sim ax` gives.

A partly covered element is tapped in the part still showing, and the output says so.

## Typing

Tap the field first: `ao sim type` types into whatever has keyboard focus, and fails without
typing when nothing has. It types characters through AO's XCTest runner, so Thai, English, mixed
text and emoji arrive as asked whatever the keyboard language, in a secure field or a web
sign-in sheet too. The first type after a claim can wait a few seconds for the runner.

- **It is proven, not assumed.** The output names the field and quotes what it reads now (a
  secure field: the dot count). It fails rather than claim characters it did not deliver.
- **`could not prove it` means read it back, do not send it again.** Part of the text may have
  arrived; sending it again types that part twice. Read the field with `ao sim ax` and fix from
  what it holds.
- **Newline or tab in the text?** Type the text, then press the key: `ao sim key enter`.
- **Hardware key presses minimize the on-screen keyboard** for every field after them: iOS then
  thinks a hardware keyboard is attached. `ao sim key`, `--raw-keys` and Command-V paste do
  this, and AO shows the keyboard again, reporting it on a `Keyboard:` line. A keyboard left
  minimized anyway (`Keyboard off screen` in `ao sim ax`): focus a field and run
  `ao sim key arrow-right`.
- When the runner is not up, it falls back to the pasteboard or key presses and says so on a
  `Fallback:` line. `--paste` and `--raw-keys` force those routes. A paste puts the text on the
  simulator's pasteboard briefly, where any app on it could read it.

## When a command fails

| Output | Meaning |
|---|---|
| `... is leased by @other-session` | Another session holds the device. Nothing was sent. Reads still work; wait or ask. |
| `this session has not claimed ...` | Run `ao sim claim`. AO never takes a device for you. |
| `... is mid-gesture` | Another command holds the finger now. Nothing was sent. Retry in a moment. |
| `node was not found on PATH` | Gesture commands need Node.js 20+. `shot` and `list` still work. |
| `the simulator bridge could not load` | An Xcode or macOS update broke the private-framework bridge. Report it with the Xcode version. |
| `... is not booted` | `ao sim boot --udid <udid>`. |
| exit 2 | Bad arguments (a coordinate outside 0..1, an unknown key). Nothing reached the device. |

A gesture that dies in flight still sends the release, and says so.
