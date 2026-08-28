package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbuild"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// Putting a build on a device, and starting it.
//
// These exist because there was no lease-aware way to do either, so reaching
// for `xcrun simctl install` directly was the reasonable thing for an agent to
// do - and simctl consults no lease. That is how a refused claim and an install
// ended up in one shell call with no dependency between them: the claim exited
// non-zero, reported the device was somebody else's, and the install overwrote
// their binary anyway, mid-verification.
//
// So the lease is not a precondition these commands CHECK, which an agent could
// forget to satisfy in a separate command. It is part of the operation: the
// device is taken, and only then written to. A wrapper built that way cannot be
// defeated by forgetting to chain commands, which is the only failure mode that
// actually occurred.

const (
	// simInstallNote is what an install did NOT do. Installing leaves whatever
	// is running running and touches no data container, so an agent that
	// expects a fresh app has to be told to start it.
	simInstallNote = "Installing replaces the app bundle and leaves everything else alone: a running " +
		"instance keeps running the old code until it is relaunched, and the app's data is untouched. " +
		"`ao sim launch` starts it."
	// simAppLeaseNote says what taking the device bought, in the same terms
	// `ao sim claim` uses, because it is the same lease.
	simAppLeaseNote = "This session now holds the device, so no other AO session can write to it. " +
		"Run `ao sim release` when you are done with it."
)

// simInstallResult is the `ao sim install --json` payload.
type simInstallResult struct {
	UDID              string `json:"udid"`
	Name              string `json:"name"`
	Runtime           string `json:"runtime"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	// Source is the .app that was installed, as an absolute path.
	Source string `json:"source"`
	// Build is what is on the device now, read back from the device rather than
	// from the bundle that was sent - so the answer is about the install that
	// happened, not the one that was asked for.
	Build        *simBuildView `json:"build,omitempty"`
	BuildUnknown string        `json:"buildUnknown,omitempty"`
	Lease        simLeaseView  `json:"lease"`
	Note         string        `json:"note"`
}

// simLaunchResult is the `ao sim launch --json` payload.
type simLaunchResult struct {
	UDID              string `json:"udid"`
	Name              string `json:"name"`
	Runtime           string `json:"runtime"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	BundleID          string `json:"bundleId"`
	// Chosen marks a bundle id AO picked rather than was given - the newest of
	// several apps on the device - and Of is how many it chose from. Said out
	// loud so a launch that started the wrong app is obvious in its own output.
	Chosen bool `json:"chosen,omitempty"`
	Of     int  `json:"of,omitempty"`
	// PID is what simctl reported the app started as, when it said.
	PID          string        `json:"pid,omitempty"`
	Terminated   bool          `json:"terminated"`
	Build        *simBuildView `json:"build,omitempty"`
	BuildUnknown string        `json:"buildUnknown,omitempty"`
	Lease        simLeaseView  `json:"lease"`
	Note         string        `json:"note"`
}

func newSimInstallCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid string
		ttl  string
		json bool
	}
	cmd := &cobra.Command{
		Use:   "install <path/to/App.app>",
		Short: "Install an app bundle on a simulator this session holds",
		Long: "Install a built .app on a simulator, taking the device's lease as part of doing it.\n\n" +
			"The lease is not a separate step to remember: this command claims the device " +
			"and refuses outright when another AO session holds it, so nothing is written " +
			"to a simulator somebody else is working on. That is the difference between " +
			"this and `xcrun simctl install`, which consults no lease at all and will " +
			"happily overwrite the binary a crewmate is verifying.\n\n" +
			"With no --udid it installs on the simulator assigned to this session " +
			"($AO_SIM_UDID), falling back to the one booted device. " + simPowerNote,
		Example: `  ao sim install ./build/Debug-iphonesimulator/MyApp.app
  ao sim install --ttl 30m ./MyApp.app
  ao sim install --udid 00000000-0000-0000-0000-000000000000 ./MyApp.app --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := ctx.installSimApp(cmd.Context(), opts.udid, args[0], opts.ttl)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimInstall(cmd.OutOrStdout(), result)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Install on this simulator instead of this session's own")
	f.StringVar(&opts.ttl, "ttl", "", "How long to hold the device afterwards (e.g. 30s, 10m, 1h). Default 10m")
	f.BoolVar(&opts.json, "json", false, "Output the result as JSON")
	return cmd
}

func newSimLaunchCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid      string
		ttl       string
		terminate bool
		json      bool
	}
	cmd := &cobra.Command{
		Use:   "launch [bundle-id]",
		Short: "Launch an installed app on a simulator this session holds",
		Long: "Start an app on a simulator, taking the device's lease as part of doing it.\n\n" +
			"With no bundle id it launches the most recently installed app - which after " +
			"an `ao sim install` is the one you just put there - and says so when it chose " +
			"between several; $AO_SIM_APP pins it for good. Like `ao sim install`, the " +
			"lease is part of the operation rather than a step to remember: another AO " +
			"session holding the device refuses the launch.\n\n" +
			"With no --udid it uses the simulator assigned to this session ($AO_SIM_UDID), " +
			"falling back to the one booted device. " + simPowerNote,
		Example: `  ao sim launch
  ao sim launch com.example.MyApp --terminate-first
  ao sim launch --udid 00000000-0000-0000-0000-000000000000 --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bundleID := ""
			if len(args) == 1 {
				bundleID = args[0]
			}
			result, err := ctx.launchSimApp(cmd.Context(), opts.udid, bundleID, opts.ttl, opts.terminate)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimLaunch(cmd.OutOrStdout(), result)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Launch on this simulator instead of this session's own")
	f.StringVar(&opts.ttl, "ttl", "", "How long to hold the device afterwards (e.g. 30s, 10m, 1h). Default 10m")
	f.BoolVar(&opts.terminate, "terminate-first", false, "Terminate the app if it is already running, so the launch runs the code that is installed now")
	f.BoolVar(&opts.json, "json", false, "Output the result as JSON")
	return cmd
}

func (c *commandContext) installSimApp(ctx context.Context, udid, source, rawTTL string) (simInstallResult, error) {
	bundle, err := resolveAppBundle(source)
	if err != nil {
		return simInstallResult{}, err
	}
	device, lease, err := c.takeSimDeviceFor(ctx, "`ao sim install`", udid, rawTTL)
	if err != nil {
		return simInstallResult{}, err
	}
	out, err := c.deps.CommandOutput(ctx, simctl.Binary, "simctl", "install", device.UDID, bundle)
	if err != nil {
		return simInstallResult{}, fmt.Errorf("`simctl install` failed on %s: %w: %s",
			device.Label(), err, simctl.Output(out))
	}
	result := simInstallResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Source:            bundle,
		Lease:             lease,
		Note:              simInstallNote,
	}
	// Read the identity back off the DEVICE rather than off the bundle that was
	// sent, so what is reported is the install that happened.
	result.Build, result.BuildUnknown = c.readSimBuild(ctx, device, bundleIDOf(ctx, c.deps.CommandOutput, bundle))
	return result, nil
}

func (c *commandContext) launchSimApp(ctx context.Context, udid, bundleID, rawTTL string, terminate bool) (simLaunchResult, error) {
	device, lease, err := c.takeSimDeviceFor(ctx, "`ao sim launch`", udid, rawTTL)
	if err != nil {
		return simLaunchResult{}, err
	}
	apps, err := simbuild.ListApps(ctx, simbuild.Runner(c.deps.CommandOutput), device.DataPath)
	if err != nil {
		return simLaunchResult{}, fmt.Errorf("%s", explainSimBuild(err, device))
	}
	app, chosen, err := simbuild.UnderTest(apps, simAppOrEnv(bundleID))
	if err != nil {
		return simLaunchResult{}, explainSimLaunchTarget(err, device)
	}
	result := simLaunchResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		BundleID:          app.BundleID,
		Chosen:            chosen,
		Lease:             lease,
		Note:              simSharedDeviceNote,
	}
	if chosen {
		result.Of = len(apps)
	}
	if terminate {
		// A terminate that finds nothing running is a success, not a failure:
		// the state asked for - this app is not running the old code - is
		// already true. simctl says so with a non-zero exit, which is why the
		// error is deliberately dropped here.
		_, _ = c.deps.CommandOutput(ctx, simctl.Binary, "simctl", "terminate", device.UDID, app.BundleID)
		result.Terminated = true
	}
	out, err := c.deps.CommandOutput(ctx, simctl.Binary, "simctl", "launch", device.UDID, app.BundleID)
	if err != nil {
		return simLaunchResult{}, fmt.Errorf("`simctl launch` failed for %s on %s: %w: %s",
			app.BundleID, device.Label(), err, simctl.Output(out))
	}
	result.PID = launchedPID(out)
	result.Build, result.BuildUnknown = c.readSimBuild(ctx, device, app.BundleID)
	return result, nil
}

// takeSimDeviceFor resolves the device a write is about and takes its lease.
//
// The two happen together on purpose. Every previous way to put a build on a
// device left "which device" and "may I have it" as separate commands, and a
// shell running both with no dependency between them is exactly how a refused
// claim was followed by an install that went through.
func (c *commandContext) takeSimDeviceFor(
	ctx context.Context, command, udid, rawTTL string,
) (simDevice, simLeaseView, error) {
	sessionID, err := simSessionID(command)
	if err != nil {
		return simDevice{}, simLeaseView{}, err
	}
	ttl, err := parseSimTTL(rawTTL)
	if err != nil {
		return simDevice{}, simLeaseView{}, err
	}
	device, err := c.resolveBootedSimDevice(ctx, udid)
	if err != nil {
		return simDevice{}, simLeaseView{}, err
	}
	var res simLeaseResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-leases"
	body := acquireSimLeaseRequest{UDID: device.UDID, TTLSeconds: int(ttl.Seconds())}
	if err := c.postJSON(ctx, path, body, &res); err != nil {
		return simDevice{}, simLeaseView{}, c.explainSimWriteContention(command, device, err)
	}
	acquired, expires := res.Lease.AcquiredAt.UTC(), res.Lease.ExpiresAt.UTC()
	return device, simLeaseView{
		State:      "held",
		Holder:     res.Lease.SessionID,
		AcquiredAt: &acquired,
		ExpiresAt:  &expires,
	}, nil
}

// explainSimWriteContention is explainSimContention for a command that WRITES
// to the device. The extra sentence matters: a read that is refused costs
// nothing, and a write that is refused is the moment somebody else's work was
// about to be overwritten, so the refusal has to say that nothing happened.
func (c *commandContext) explainSimWriteContention(command string, device simDevice, err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "SIM_DEVICE_LEASED" {
		return err
	}
	return fmt.Errorf("%w\nNothing was written to the device: %s takes the lease before it touches the simulator, "+
		"so a refusal here means the app on %s is exactly as its holder left it",
		c.explainSimContention(device, err), command, device.Name)
}

// explainSimLaunchTarget phrases "which app did you mean" for a launch.
func explainSimLaunchTarget(err error, device simDevice) error {
	switch {
	case errors.Is(err, simbuild.ErrUnknownApp):
		return fmt.Errorf("%w on %s; `ao sim install <path/to/App.app>` puts one there", err, device.Name)
	default:
		return errors.New(explainSimBuild(err, device))
	}
}

// resolveAppBundle checks that what the caller named is a .app directory before
// a device is taken for it. Taking somebody's simulator and then discovering the
// path was wrong would hold a device for nothing.
func resolveAppBundle(source string) (string, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return "", usageError{errors.New("name the app bundle to install, e.g. `ao sim install ./build/Debug-iphonesimulator/MyApp.app`")}
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", trimmed, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("no app bundle at %s: %w", abs, err)
	}
	if !info.IsDir() || !strings.HasSuffix(abs, ".app") {
		return "", usageError{fmt.Errorf("%s is not a .app bundle; `ao sim install` takes the built app directory, "+
			"e.g. ./build/Debug-iphonesimulator/MyApp.app", abs)}
	}
	return abs, nil
}

// bundleIDOf reads the identifier out of a bundle about to be installed, so the
// build read back afterwards is of the app that was just sent rather than of
// whichever app happens to be the only one on the device. An unreadable bundle
// yields "", which falls back to that single-app rule.
func bundleIDOf(ctx context.Context, run simctl.Runner, bundle string) string {
	apps, err := simbuild.ReadBundle(ctx, simbuild.Runner(run), bundle)
	if err != nil {
		return ""
	}
	return apps.BundleID
}

// launchedPID pulls the pid out of what `simctl launch` printed
// ("com.example.App: 51234"). Absent is fine: the launch succeeded either way,
// and inventing a pid would be worse than reporting none.
func launchedPID(out []byte) string {
	fields := strings.Fields(simctl.Output(out))
	if len(fields) == 0 {
		return ""
	}
	last := fields[len(fields)-1]
	for _, r := range last {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return last
}

func writeSimInstall(out io.Writer, result simInstallResult) error {
	if _, err := fmt.Fprintf(out, "Installed %s on %s (%s, %s).\n",
		filepath.Base(result.Source), result.Name, result.Runtime, result.UDID); err != nil {
		return err
	}
	if err := writeSimBuildLine(out, result.Build, result.BuildUnknown); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Lease: held by @%s until %s. %s\n",
		result.Lease.Holder, expiryOf(result.Lease), simAppLeaseNote); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Note: %s\n", result.Note)
	return err
}

func writeSimLaunch(out io.Writer, result simLaunchResult) error {
	launched := "Launched " + result.BundleID
	if result.Terminated {
		launched = "Terminated and relaunched " + result.BundleID
	}
	if result.PID != "" {
		launched += " (pid " + result.PID + ")"
	}
	if result.Chosen {
		launched += fmt.Sprintf(", the newest of %d apps on this device - pin it with `ao sim launch <bundle-id>` or $AO_SIM_APP", result.Of)
	}
	if _, err := fmt.Fprintf(out, "%s on %s (%s, %s).\n",
		launched, result.Name, result.Runtime, result.UDID); err != nil {
		return err
	}
	if err := writeSimBuildLine(out, result.Build, result.BuildUnknown); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Lease: held by @%s until %s. %s\n",
		result.Lease.Holder, expiryOf(result.Lease), simAppLeaseNote); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Note: %s\n", result.Note)
	return err
}

// writeSimBuildLine reports what is on the device now - or says, out loud, that
// it could not be read. Silence would let a reader assume the question was
// never worth asking.
func writeSimBuildLine(out io.Writer, build *simBuildView, unknown string) error {
	if build != nil {
		_, err := fmt.Fprintf(out, "Build: %s\n", build.line())
		return err
	}
	_, err := fmt.Fprintf(out, "Build: unknown - %s\n", unknown)
	return err
}

func expiryOf(lease simLeaseView) string {
	if lease.ExpiresAt == nil {
		return "an unreported time"
	}
	return lease.ExpiresAt.Format(time.RFC3339)
}
