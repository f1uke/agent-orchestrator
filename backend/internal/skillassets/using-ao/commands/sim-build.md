# ao sim: putting your build on a device

`ao sim run`, `ao sim install`, `ao sim launch`. Each takes the device's lease as its first act:
when another session holds the device, it refuses and writes nothing. Rules for every `ao sim`
command are in [sim.md](sim.md); flags are in `ao sim <command> --help`.

## Use these, not simctl or a device-pinned xcodebuild

`xcrun simctl install` and `xcodebuild -destination id=...` consult no lease. A worker once
chained `ao sim claim`, `xcrun simctl install` and `xcrun simctl launch`: the claim was refused
because a crewmate held the device, the install went through anyway, and it invalidated evidence
already captured.

```bash
ao sim run --scheme NterApp --configuration Dev    # build, install, launch: the whole loop
ao sim install ./build/Debug-iphonesimulator/MyApp.app
ao sim launch --terminate-first                    # the screen now runs what you installed
ao sim shot                                        # its Build: line names the build on screen
```

## Make the screen run the new build

Installing replaces the bundle and keeps the app's data, but **an instance already running keeps
the old code**. After `ao sim install`, launch with `--terminate-first`. `ao sim run` always
terminates before it launches. Confirm with the `Build:` line of `ao sim shot`, or
`ao sim doctor --app <bundle-id> --expect <path/to/App.app>`, which compares bytes.

With no bundle id, `ao sim launch` starts the most recently installed app and says when it chose
between several; `AO_SIM_APP` in the project's environment pins it.

## Launch arguments and environment

Everything after `--` goes to the app as its launch arguments, word for word. A `-Key value` pair
lands in the app's UserDefaults argument domain. Each `--env KEY=VALUE` (repeatable) is set in the
app's environment, through simctl's `SIMCTL_CHILD_KEY`. Both apply with `--console` and
`--terminate-first`.

```bash
ao sim launch -- -FeatureFlag YES                              # the default app, with an argument
ao sim launch com.example.MyApp --env API_HOST=https://int.example.com -- -UITestMode
ao sim launch --console --env LOG_LEVEL=debug && ao sim console
```

The output, and `--json` (`args`, `envKeys`), lists the arguments and the env keys, never an env
value: a value may be a token. A malformed `--env` (no `=`, an empty key, a key given twice) is
refused before the device is touched.

## ao sim run: scheme AND configuration

`ao sim run` builds the `.xcworkspace` or `.xcodeproj` at the root of the current directory (a
monorepo runs it from the app's subdirectory). The build targets the Simulator generically and
names no device; the device is claimed before the build starts, so a held device is refused in
a second instead of after minutes of building. A shut-down device is booted on the way.

The scheme says WHAT to build; the configuration says WHICH ENVIRONMENT. On `nter-ios-app` the
schemes are `NterApp` and `FNCore` (app and library) and the configurations are `Dev`,
`Mock-api`, `Mock-local`, `Production`, `Release` and `UAT`: there is no `Debug`, and building a
configuration that does not exist fails minutes later on a path starting at `/` (CocoaPods made
no xcconfig for it). So with several schemes or no `Debug`, it refuses and lists the choices
rather than guessing. Names match case-insensitively: `--configuration uat` builds `UAT`.
