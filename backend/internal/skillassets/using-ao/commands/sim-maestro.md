# ao sim: Maestro flows

`ao sim flow check`, `ao sim flow run`, `ao sim flow record`, and `ao sim ax --format maestro`.
On a script-only project, the scripts store's `bin/flow run` wraps `ao sim flow run`: see the
script-only rule in [sim.md](sim.md). Flags are in `ao sim flow <command> --help`.

## Running flows

```bash
ao sim flow check flow.yaml                 # a pure parse: no device, no selector matching
ao sim claim
ao sim flow run flow.yaml
ao sim flow run a.yaml b.yaml               # several flows, ONE Maestro start-up (saves ~20 s each)
```

- **A claim is required, and the device is always pinned.** Left to choose, Maestro takes the
  only connected simulator, which may be the one a human is using.
- **A flow is destructive.** Its `launchApp` terminates the app and resets its privacy
  permissions. Run flows only on a device set aside for it: your own clone, never a human's.
- Values reach a flow as `MAESTRO_<KEY>` environment variables. Every file is parsed before
  Maestro starts. AO never installs `maestro`.

## Recording what you drive

`ao sim flow record` captures the gestures this session drives (and a human's clicks in this
session's Device tab) as a Maestro flow. It needs your claim and never takes one.

```bash
ao sim claim
ao sim flow record start --name "sign up"
ao sim tap --label "Continue"               # every tap/swipe/drag/type/button becomes a step
ao sim flow record stop --entry ../flows/sign-in.yaml
```

- **No `launchApp` is ever invented.** A recording starts wherever the app already was. `--entry`
  prepends a shared entry-point flow as `runFlow`, so the flow runs standalone.
- Only gestures that reached the device become steps. Maestro has no pinch, so a recording with
  one cannot be exported.
- A script carries no data: `stop --param NAME=VALUE` writes typed text equal to VALUE as
  `${MAESTRO_NAME}`. Text typed into a secure field is never recorded.
- The flow lands in this session's artifact directory, outside every repository; `--out` writes
  it elsewhere.

## Selectors from a screen

`ao sim ax --format maestro` prints a selector per element. These are selectors, not a flow:
you choose the steps, their order and their waits.

- A bare `- tapOn: "Some label"` means the label is unique in the tree.
- `text:` plus `index:` means several elements share it: check you indexed the one you meant.
- `id:` means it matched the accessibility id; `point:` means nothing else existed and breaks
  on any layout change.
- `scrollUntilVisible` replaces a tap for an element off screen.

The ambiguity count is a lower bound: Maestro walks a fuller hierarchy than `ao sim ax`, so a
selector reported unique can still match several nodes. Only a run proves it.
