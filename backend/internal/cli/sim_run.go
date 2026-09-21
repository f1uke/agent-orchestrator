package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
	"github.com/aoagents/agent-orchestrator/backend/internal/xcodeproj"
)

// Building an app from source and putting it on a simulator.
//
// This is the one `ao sim` command that starts from a checkout rather than from
// a device, and it exists because every other way to do it left the lease
// behind. An agent - or a Run button - that wants to see its change on a screen
// reaches for `xcodebuild -destination id=<udid>` followed by `xcrun simctl
// install`, and neither of those tools has ever heard of AO: the first aims a
// build at a device somebody else may be driving, and the second overwrites
// their binary. Both were reachable from two sessions at once the moment the
// run bar shipped.
//
// So the two halves are answered separately, and neither is a rule an agent has
// to remember:
//
//   - the BUILD never names a device at all. It goes to
//     xcodeproj.GenericSimulatorDestination, which produces the same .app for
//     every simulator, so the tool that cannot honour a lease is never pointed
//     at a leased device.
//   - the DEVICE is taken before the build starts, not after it. A crewmate who
//     presses Run on the same simulator is refused in about a second, with
//     `ao sim install`'s wording about nothing having been written - rather than
//     after three minutes of building something it turns out it may not install.

const (
	// simRunFallbackConfiguration is Debug - the configuration Xcode's own Run
	// button produces, and the one this command used to pass unconditionally.
	//
	// ⚠ It is a FALLBACK for one case only: the project could not be asked what
	// its configurations are. It is never a default over a list that was read.
	// nter-ios-app's configurations are Dev, Mock-api, Mock-local, Production,
	// Release and UAT - there is no Debug, and building it there dies after
	// minutes with an xcfilelist path that starts at `/`, because CocoaPods
	// generated no xcconfig for a configuration that does not exist.
	simRunFallbackConfiguration = "Debug"
	// simRunTTL is how long the device is held by default. Longer than
	// install/launch's 10 minutes because a cold build of a real app spends
	// most of it: a lease that lapses mid-build hands the device away in the
	// window this command exists to defend.
	simRunTTL = "30m"
	// simRunNote is what a finished run leaves behind, said out loud because
	// the device stays held afterwards on purpose - the app is on screen and
	// the next thing anybody does is drive it.
	simRunNote = "The app is running and this session still holds the device, so `ao sim ax`, `ao sim tap` and " +
		"`ao sim shot` act on it. Run `ao sim release` when you are done with it."
)

// simRunResult is the `ao sim run --json` payload.
type simRunResult struct {
	Scheme        string `json:"scheme"`
	Configuration string `json:"configuration"`
	// Project is the container that was built, as a human names it.
	Project string `json:"project"`
	// App is the bundle the build produced, as an absolute path.
	App string `json:"app"`

	UDID              string `json:"udid"`
	Name              string `json:"name"`
	Runtime           string `json:"runtime"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	// Booted marks a device this command powered on on its way through.
	Booted bool `json:"booted"`

	BundleID     string        `json:"bundleId"`
	PID          string        `json:"pid,omitempty"`
	Build        *simBuildView `json:"build,omitempty"`
	BuildUnknown string        `json:"buildUnknown,omitempty"`
	// Warning is what is wrong with the app this run built, when anything is.
	// Empty is the ordinary case, and after the CODE_SIGNING_ALLOWED=NO fix it
	// is the only one this command produces on its own - it is kept because a
	// project can disable signing in its OWN settings, and because a run that
	// installs an app with no entitlements must never again be reported as a
	// plain success. See simUnentitledWarning.
	Warning string       `json:"warning,omitempty"`
	Lease   simLeaseView `json:"lease"`
	Note    string       `json:"note"`
}

func newSimRunCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		scheme        string
		configuration string
		udid          string
		ttl           string
		json          bool
	}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Build this project from source and run it on a simulator",
		Long: "Build an Xcode scheme, install what it produced on a simulator, and launch it.\n\n" +
			"It is the whole loop in one command: `xcodebuild` for the scheme, then the " +
			"same lease-aware install and launch `ao sim install` and `ao sim launch` do. " +
			"The project is whatever .xcworkspace or .xcodeproj sits at the root of the " +
			"current directory; a workspace wins over a project when both are there.\n\n" +
			"The device is taken BEFORE the build starts, so another AO session running " +
			"this at the same time is refused in a second rather than after its build. " +
			"The build itself names no device - it targets the Simulator generically - so " +
			"nothing `xcodebuild` does can reach a simulator somebody else is driving. A " +
			"device that is shut down is booted on the way through, under the same " +
			"two-simulator cap as `ao sim boot`.\n\n" +
			"With no --scheme it builds the project's only scheme, and lists them rather " +
			"than choosing when there are several. --configuration works the same way, with " +
			"one addition: a project that HAS a Debug configuration gets it by default, " +
			"because that is what Xcode's Run button builds. A project without one - schemes " +
			"for the app and its library, environments in the configurations - is asked " +
			"about rather than guessed at. With no --udid it uses the simulator assigned to " +
			"this session ($AO_SIM_UDID).",
		Example: `  ao sim run
  ao sim run --scheme NterApp --configuration Dev
  ao sim run --scheme NterApp --configuration UAT
  ao sim run --scheme NterApp --configuration Dev --udid 00000000-0000-0000-0000-000000000000 --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The build's output goes to the terminal as it arrives, even under
			// --json: thousands of lines of xcodebuild are the thing somebody
			// reads when a build fails, and holding them back until the end
			// would make a three-minute build look like a hang. --json governs
			// the RESULT, which is written last.
			result, err := ctx.runSimApp(cmd.Context(), cmd.ErrOrStderr(), opts.scheme, opts.configuration, opts.udid, opts.ttl)
			// Report the verdict before returning either way. The run bar
			// started this command in a pane nothing waits on, so this file is
			// the ONLY way the outcome gets back to it - and a failed run that
			// reported nothing would leave the bar saying "running" for ever.
			reportSimRunResult(result, err)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimRun(cmd.OutOrStdout(), result)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.scheme, "scheme", "", "Xcode scheme to build. Defaults to the project's only scheme")
	f.StringVar(&opts.configuration, "configuration", "", "Build configuration. Defaults to Debug when the project has one, and asks when it does not")
	f.StringVar(&opts.udid, "udid", "", "Run on this simulator instead of this session's own")
	f.StringVar(&opts.ttl, "ttl", "", "How long to hold the device afterwards (e.g. 30s, 10m, 1h). Default 30m")
	f.BoolVar(&opts.json, "json", false, "Output the result as JSON")
	return cmd
}

// runSimApp is the whole command: find the project, decide the scheme, take the
// device, build, install, launch.
//
// The ORDER is the design. Everything that can be refused cheaply is refused
// before anything expensive happens: a missing project, an ambiguous scheme and
// a device somebody else holds all fail in about a second, because each of them
// after a full build is a wasted build.
func (c *commandContext) runSimApp(
	ctx context.Context, progress io.Writer, scheme, configuration, udid, rawTTL string,
) (simRunResult, error) {
	dir, err := os.Getwd()
	if err != nil {
		return simRunResult{}, fmt.Errorf("could not read the current directory: %w", err)
	}
	project, err := xcodeproj.Find(dir)
	if err != nil {
		return simRunResult{}, explainNoXcodeProject(err, dir)
	}
	scheme, err = c.resolveSimRunScheme(ctx, dir, project, scheme)
	if err != nil {
		return simRunResult{}, err
	}
	configuration, err = c.resolveSimRunConfiguration(ctx, progress, dir, project, configuration)
	if err != nil {
		return simRunResult{}, err
	}

	// Boot first when the device is down: a lease on a shut-down simulator
	// grants the right to write to something that cannot be written to.
	booted, err := c.bootSimRunDevice(ctx, progress, udid)
	if err != nil {
		return simRunResult{}, err
	}
	if rawTTL == "" {
		rawTTL = simRunTTL
	}
	device, lease, err := c.takeSimDeviceFor(ctx, "`ao sim run`", udid, rawTTL)
	if err != nil {
		return simRunResult{}, err
	}

	noteProgress(progress, "Building %s (%s) from %s…\n", scheme, configuration, project.Name)
	watch := &signingWatch{out: progress}
	if err := c.streamBuild(ctx, watch, xcodeproj.Binary, xcodeproj.BuildArgs(project, scheme, configuration)...); err != nil {
		return simRunResult{}, explainSimBuildFailure(err, scheme, device, watch.saw)
	}
	app, err := xcodeproj.ProductPath(ctx, c.deps.CommandOutputInDir, dir, project, scheme, configuration)
	if err != nil {
		return simRunResult{}, err
	}

	result := simRunResult{
		Scheme:            scheme,
		Configuration:     configuration,
		Project:           project.Name,
		App:               app,
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Booted:            booted,
		Warning:           c.unentitledWarning(ctx, app),
		Lease:             lease,
		Note:              simRunNote,
	}

	noteProgress(progress, "Installing %s on %s…\n", filepath.Base(app), device.Label())
	out, err := c.deps.CommandOutput(ctx, "xcrun", "simctl", "install", device.UDID, app)
	if err != nil {
		return simRunResult{}, fmt.Errorf("`simctl install` failed on %s: %w: %s", device.Label(), err, strings.TrimSpace(string(out)))
	}

	bundleID := bundleIDOf(ctx, c.deps.CommandOutput, app)
	if bundleID == "" {
		return simRunResult{}, fmt.Errorf("%s was installed on %s, but its bundle identifier could not be read, "+
			"so there is nothing to launch; `ao sim launch <bundle-id>` starts it", filepath.Base(app), device.Name)
	}
	result.BundleID = bundleID

	// Terminate before launching, always. The whole point of Run is to see the
	// code that was just built, and `simctl launch` against an app that is
	// already running brings the OLD process to the front - a build that
	// succeeded and a screen that did not change is the single most confusing
	// outcome this command could have. A terminate that finds nothing running
	// exits non-zero, which is why its error is dropped.
	_, _ = c.deps.CommandOutput(ctx, "xcrun", "simctl", "terminate", device.UDID, bundleID)
	launched, err := c.deps.CommandOutput(ctx, "xcrun", "simctl", "launch", device.UDID, bundleID)
	if err != nil {
		return simRunResult{}, fmt.Errorf("`simctl launch` failed for %s on %s: %w: %s",
			bundleID, device.Label(), err, strings.TrimSpace(string(launched)))
	}
	result.PID = launchedPID(launched)
	result.Build, result.BuildUnknown = c.readSimBuild(ctx, device, bundleID)
	return result, nil
}

// resolveSimRunScheme decides which scheme to build, in the house style: the
// only candidate is used without asking, and several with none named is a
// refusal that prints the command to run next rather than a guess.
func (c *commandContext) resolveSimRunScheme(ctx context.Context, dir string, project xcodeproj.Project, scheme string) (string, error) {
	schemes, err := xcodeproj.Schemes(ctx, c.deps.CommandOutputInDir, dir, project)
	if err != nil {
		if errors.Is(err, xcodeproj.ErrNoSchemes) {
			return "", fmt.Errorf("%s has no schemes, so there is nothing to build; open it in Xcode and add one", project.Name)
		}
		return "", err
	}
	if named := strings.TrimSpace(scheme); named != "" {
		for _, s := range schemes {
			if s == named {
				return s, nil
			}
		}
		sorted := append([]string(nil), schemes...)
		sort.Strings(sorted)
		var b strings.Builder
		fmt.Fprintf(&b, "%s has no scheme called %q.\nIt has:", project.Name, named)
		for _, s := range sorted {
			fmt.Fprintf(&b, "\n  ao sim run --scheme %s", s)
		}
		return "", usageError{errors.New(b.String())}
	}
	if len(schemes) == 1 {
		return schemes[0], nil
	}
	sorted := append([]string(nil), schemes...)
	sort.Strings(sorted)
	var b strings.Builder
	fmt.Fprintf(&b, "%s has %d schemes, so there is no unambiguous default - a build of the wrong one installs "+
		"the wrong app.\nRe-run with one of:", project.Name, len(schemes))
	for _, s := range sorted {
		fmt.Fprintf(&b, "\n  ao sim run --scheme %s", s)
	}
	return "", usageError{errors.New(b.String())}
}

// resolveSimRunConfiguration decides which configuration to build.
//
// 🗝 Debug is a candidate here, never an assumption. The rule, in the order it
// is applied:
//
//  1. a named configuration is matched against the project's own list, case
//     insensitively, and the project's spelling is what gets passed on -
//     `--configuration uat` builds `UAT`;
//  2. one configuration in the project is used without asking, exactly as one
//     scheme is;
//  3. a project that HAS Debug gets Debug, because that is what Xcode's Run
//     button builds;
//  4. several, none of them Debug - refuse and list them. This is the
//     nter-ios-app case, and choosing for somebody there is choosing which
//     backend their app talks to.
//
// The one fallback: a project whose configurations cannot be READ at all builds
// Debug and says so, which is what this command did before configurations were
// listed. The run bar blocks instead of falling back, because a human waiting
// on a doomed build has no error to read until it is over - a terminal has this
// line and xcodebuild's own.
func (c *commandContext) resolveSimRunConfiguration(
	ctx context.Context, progress io.Writer, dir string, project xcodeproj.Project, configuration string,
) (string, error) {
	named := strings.TrimSpace(configuration)
	configurations, err := xcodeproj.Configurations(ctx, c.deps.CommandOutputInDir, dir, project)
	if err != nil {
		if named != "" {
			return named, nil
		}
		noteProgress(progress, "Could not read %s's build configurations (%v), so building %s.\n",
			project.Name, firstLineOf(err), simRunFallbackConfiguration)
		return simRunFallbackConfiguration, nil
	}
	if named != "" {
		for _, candidate := range configurations {
			if strings.EqualFold(candidate, named) {
				return candidate, nil
			}
		}
		return "", usageError{errors.New(listSimRunConfigurations(
			fmt.Sprintf("%s has no build configuration called %q.\nIt has:", project.Name, named), configurations))}
	}
	if len(configurations) == 1 {
		return configurations[0], nil
	}
	for _, candidate := range configurations {
		if strings.EqualFold(candidate, simRunFallbackConfiguration) {
			return candidate, nil
		}
	}
	return "", usageError{errors.New(listSimRunConfigurations(
		fmt.Sprintf("%s has %d build configurations and no Debug, so there is no unambiguous default - "+
			"a build of the wrong one installs an app pointed at the wrong environment.\nRe-run with one of:",
			project.Name, len(configurations)), configurations))}
}

// listSimRunConfigurations prints a refusal with one runnable command per
// configuration, in the project's own order - which is how Xcode shows them,
// and sorting it would separate Dev from UAT for no reader's benefit.
func listSimRunConfigurations(lead string, configurations []string) string {
	var b strings.Builder
	b.WriteString(lead)
	for _, candidate := range configurations {
		fmt.Fprintf(&b, "\n  ao sim run --configuration %s", candidate)
	}
	return b.String()
}

// firstLineOf keeps a failure to the sentence that fits on a progress line, or
// in a strip above a terminal; the rest of it - xcodebuild's complaint, or the
// list of commands to run instead - runs to pages and is on screen anyway.
//
// A trailing colon is dropped because the line it introduced is not coming
// along: "It has:" with nothing after it reads as truncation rather than as a
// sentence.
func firstLineOf(err error) string {
	return firstLine(err.Error())
}

// firstLine is firstLineOf for text that is not an error - the app warning the
// bar shows beside a run that succeeded.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return strings.TrimSuffix(strings.TrimSpace(line), ":")
}

// bootSimRunDevice powers the target on when it is down, and reports whether it
// did. It is the same path as `ao sim boot`, which is what keeps one
// two-simulator cap rather than two that can drift apart.
func (c *commandContext) bootSimRunDevice(ctx context.Context, progress io.Writer, udid string) (bool, error) {
	devices, err := c.listSimDevices(ctx)
	if err != nil {
		return false, err
	}
	device, err := resolveSimDevice(devices, udid)
	// resolveSimDevice answers "which BOOTED device did you mean", so a machine
	// with nothing booted fails here rather than returning a device to boot.
	// That is the case to fall through to boot's own resolution, which is the
	// one that can answer it.
	if err == nil && device.Booted() {
		return false, nil
	}
	target, bootErr := resolveSimBootTarget(devices, udid, "ao sim run")
	if bootErr != nil {
		// The booted-device error is the better one whenever there was a real
		// ambiguity among booted devices; boot's is better when nothing is up.
		if err != nil && len(bootedSimDevices(devices)) > 1 {
			return false, err
		}
		return false, bootErr
	}
	if target.Booted() {
		return false, nil
	}
	noteProgress(progress, "%s is shut down. Booting it…\n", target.Label())
	timeout, err := parseSimBootTimeout("")
	if err != nil {
		return false, err
	}
	if _, err := c.bootSimDevice(ctx, target.UDID, timeout); err != nil {
		return false, err
	}
	return true, nil
}

// bootedSimDevices is the booted subset, for deciding which of two resolution
// errors is the one worth showing.
func bootedSimDevices(devices []simDevice) []simDevice {
	var booted []simDevice
	for _, d := range devices {
		if d.Booted() {
			booted = append(booted, d)
		}
	}
	return booted
}

// streamBuild runs a child process and copies its output to out as it arrives.
// A build is minutes long and its output IS the feedback; buffering it until
// the end would make a working build indistinguishable from a hang.
//
// It needs no working directory: every xcodebuild invocation here names the
// project by absolute path, so the answer does not depend on where the command
// was run from.
func (c *commandContext) streamBuild(ctx context.Context, out io.Writer, name string, args ...string) error {
	stream, err := c.deps.StartStream(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("could not start %s: %w", name, err)
	}
	defer func() { _ = stream.Close() }()
	reading := make(chan struct{})
	defer close(reading)
	// A read parked on a pipe cannot be interrupted by a context; stopping the
	// child is what ends it. Same treatment as `ao sim flow run`.
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-reading:
		}
	}()
	if _, err := io.Copy(out, stream); err != nil && ctx.Err() == nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return stream.Err()
}

// signingWatch passes a build's output straight through and remembers whether
// xcodebuild complained about code signing on the way past.
//
// It exists because explainSimBuildFailure has nothing else to go on: the build
// is STREAMED to the terminal rather than captured, precisely so a three-minute
// build does not look like a hang, and by the time it fails its output is gone.
// Re-running the build to find out why it failed would cost another three
// minutes, and scrolling back is what the human is already doing.
//
// It keeps no log - only a bool and at most one partial line - so watching a
// build of any size costs the same.
type signingWatch struct {
	out io.Writer
	// partial is the tail of a write that did not end in a newline, carried
	// into the next one so a marker split across two reads is still seen. It is
	// capped: a build that emits a megabyte without a newline is not a build
	// whose signing error is in that megabyte.
	partial []byte
	// saw is whether any line so far was an error about code signing.
	saw bool
}

// maxSigningWatchPartial bounds the carried tail. Long enough for any single
// line xcodebuild writes, short enough that it is not a buffer.
const maxSigningWatchPartial = 8192

// signingErrorMarkers are what a code signing failure says. Matched only on a
// line that is also an error, so the ordinary signing CHATTER of a successful
// build - "Signing Identity: -", the CodeSign step itself - cannot trip it.
var signingErrorMarkers = []string{
	"code sign",     // "Code Signing Error", "Ad Hoc code signing is not allowed with SDK ..."
	"codesign",      // codesign's own diagnostics, relayed by xcodebuild
	"entitlement",   // "Build input file cannot be found: '.../App.entitlements'"
	"provisioning",  // "requires a provisioning profile"
	"signing certi", // "No signing certificate ... found"
}

func (w *signingWatch) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	if !w.saw && n > 0 {
		w.scan(p[:n])
	}
	return n, err
}

func (w *signingWatch) scan(p []byte) {
	// Appended onto partial itself rather than into a new slice, so a build
	// that writes a thousand chunks does not allocate a thousand times.
	w.partial = append(w.partial, p...)
	rest := w.partial
	for {
		end := bytes.IndexByte(rest, '\n')
		if end < 0 {
			break
		}
		if isSigningError(string(rest[:end])) {
			w.saw = true
			w.partial = nil
			return
		}
		rest = rest[end+1:]
	}
	if len(rest) > maxSigningWatchPartial {
		rest = rest[len(rest)-maxSigningWatchPartial:]
	}
	// rest is a tail of partial's own buffer; copying it to the front is what
	// keeps the carried remainder from growing by one build's worth of output.
	w.partial = append(w.partial[:0], rest...)
}

// isSigningError is whether one line of build output is an error about signing.
func isSigningError(line string) bool {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "error") {
		return false
	}
	for _, marker := range signingErrorMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// noteProgress narrates one step to the terminal. The error is dropped on
// purpose: these lines are PROGRESS, not results, and a run that could not
// print "Building…" is still a run worth finishing - failing it would let the
// terminal's write error decide what happens to a simulator.
func noteProgress(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// explainNoXcodeProject says what was looked for and where, because the most
// likely cause is being in the wrong directory rather than the project being
// absent.
func explainNoXcodeProject(err error, dir string) error {
	if !errors.Is(err, xcodeproj.ErrNoProject) {
		return err
	}
	return usageError{fmt.Errorf("no .xcworkspace or .xcodeproj at the root of %s, so there is nothing to build. "+
		"`ao sim run` builds the project in the current directory; a monorepo keeping its app in a subdirectory "+
		"has to be run from there", dir)}
}

// explainSimBuildFailure phrases a failed build.
//
// It deliberately does NOT repeat what xcodebuild said. The compiler errors
// have already been streamed to this terminal line by line, and appending the
// tail of stderr on top of them buries the one sentence the error adds that the
// output does not: the device was not touched. Measured on a real failure - the
// repeated tail was xcodebuild's own `IDERunDestination` warning, not the
// compiler error anybody needed.
func explainSimBuildFailure(err error, scheme string, device simDevice, signing bool) error {
	failure := fmt.Errorf("building %s failed (%v). The compiler's output is above.\n"+
		"Nothing was installed on %s, so the app on it is whatever was there before; "+
		"this session still holds the device", scheme, buildExit(err), device.Name)
	if !signing {
		return failure
	}
	return fmt.Errorf("%w\n%s", failure, simSigningFailureHint)
}

// simSigningFailureHint is the one thing a reader cannot get from the build
// output: why AO is letting a signing failure through at all.
//
// 🗝 `ao sim run` used to pass CODE_SIGNING_ALLOWED=NO, which made every
// signing failure disappear - along with the app's entitlements, silently. A
// project that really cannot sign for the Simulator is now a failed build
// rather than a working-looking app that cannot reach the Keychain, and
// somebody who remembers the old behaviour deserves to be told that the change
// was deliberate rather than reaching for the flag again.
const simSigningFailureHint = "This build failed while code signing. AO deliberately no longer passes " +
	"CODE_SIGNING_ALLOWED=NO: that flag skips ProcessProductPackaging, which is what puts a simulator " +
	"app's entitlements into it, so it turned every failure here into an app that launched and then " +
	"failed every Keychain call with -34018 and said nothing.\n" +
	"A Simulator build signs itself ad hoc and needs no team and no certificate, so what failed is this " +
	"project's own signing settings - most often a CODE_SIGN_ENTITLEMENTS file that is not where the " +
	"target says it is. Fix it in the project; do not disable signing for the build."

// buildExit is how the build ended, in as few words as carry the fact.
//
// A ProcessStream wraps the child's exit with its whole stderr attached, which
// for xcodebuild is its own warnings rather than the compiler errors - those
// went to stdout and are already on screen. Unwrapping to the exit status drops
// the lot, so the sentence that follows is the first thing read.
func buildExit(err error) string {
	if err == nil {
		return "no output"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.String()
	}
	return err.Error()
}

func writeSimRun(out io.Writer, result simRunResult) error {
	if result.Booted {
		if _, err := fmt.Fprintf(out, "Booted %s (%s).\n", result.Name, result.Runtime); err != nil {
			return err
		}
	}
	launched := "Built " + result.Scheme + " (" + result.Configuration + ") and launched " + result.BundleID
	if result.PID != "" {
		launched += " (pid " + result.PID + ")"
	}
	if _, err := fmt.Fprintf(out, "%s on %s (%s, %s).\n",
		launched, result.Name, result.Runtime, result.UDID); err != nil {
		return err
	}
	if err := writeSimBuildLine(out, result.Build, result.BuildUnknown); err != nil {
		return err
	}
	if err := writeSimWarning(out, result.Warning); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "App: %s\n", result.App); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Lease: held by @%s until %s.\n", result.Lease.Holder, expiryOf(result.Lease)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Note: %s\n", result.Note)
	return err
}

// reportSimRunResult writes how this run ended, when the run bar asked to be
// told (iosrun.EnvResultFile). A human or an agent typing `ao sim run` has the
// variable unset and nothing is written.
//
// 🗝 The command reports rather than the daemon inferring, because only the
// command knows WHICH step failed: a compile error, a lease another session
// holds, and a device that would not boot are three different sentences, and
// the exit status nobody watched is the same for all three.
//
// It is one sentence, never the build log. The output is already in the pane
// the bar points at, and a bar that restated compiler errors would be a worse
// copy of the terminal underneath it.
func reportSimRunResult(result simRunResult, runErr error) {
	path := strings.TrimSpace(os.Getenv(iosrun.EnvResultFile))
	if path == "" {
		return
	}
	finished := time.Now().UTC()
	verdict := iosrun.Result{State: iosrun.RunSucceeded, FinishedAt: &finished}
	switch {
	case runErr != nil:
		verdict.State = iosrun.RunFailed
		verdict.Summary = firstLineOf(runErr)
	default:
		verdict.Summary = fmt.Sprintf("Built %s (%s) and launched %s on %s.",
			result.Scheme, result.Configuration, result.BundleID, result.Name)
		// A run that WORKED and installed a broken app is still a run that
		// worked - the pane's output is not an error and calling the run failed
		// would send the reader looking for a compiler error that is not there.
		// The warning rides beside the verdict instead, so the bar can say both.
		verdict.Warning = firstLine(result.Warning)
	}
	body, err := json.Marshal(verdict)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return
	}
	// A verdict that cannot be written changes nothing about the run itself,
	// which has already happened; the bar falls back to "stopped", which is
	// what it says whenever a run ends without reporting.
	_ = os.WriteFile(path, body, 0o600)
}
