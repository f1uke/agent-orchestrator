package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pngmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbuild"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

// `ao sim` gives a session a cheap way to see an iOS Simulator screen. It
// shells out to `xcrun simctl` on demand and nothing else: no helper process,
// no private frameworks, no HID synthesis and no polling.
//
// One command here changes a device's power state, and exactly one: `ao sim
// boot`, in sim_boot.go, which exists because lazily-created qa deadlocked
// without it. Taking a device DOWN - shutdown, reboot, erase - stays a human
// capability exercised through the desktop app's Device tab, because those are
// the operations that destroy data or take a device away from whoever is on it.
// A human or another AO session may be driving the same simulator, so every
// capture says so rather than pretending the frame is ours alone.
//
// Device leases live alongside these commands in sim_lease.go: `ao sim claim`
// and `ao sim release` are the write half, and both read-only commands here
// REPORT lease state without needing one. A screenshot cannot corrupt anyone's
// gesture, and cheap unblocked screenshots are the whole point of the command,
// so requiring a lease to take one would buy nothing and cost a great deal.

const (
	// simSharedDeviceNote goes out with every capture. The frame may be mid
	// interaction: a human in Xcode or another AO session shares the device.
	simSharedDeviceNote = "This simulator is shared - a human driving Xcode, or another AO session, " +
		"may have been mid-interaction when this frame was captured."
	// simPowerNote is repeated wherever power comes up, because the asymmetry
	// is the part an agent has to be told: it may bring a device UP, and
	// nothing here takes one down.
	simPowerNote = "`ao sim boot` powers a simulator on; no `ao sim` command shuts one down, reboots or erases one - the desktop app's Device tab is where a human does that."
	// simShotStampLayout keeps millisecond precision so two captures from one
	// session cannot collide on a filename.
	simShotStampLayout = "20060102-150405.000"
)

// simDevice is one simulator plus what AO knows about who is driving it.
//
// Discovery and the default-device rule live in internal/simctl, not here: the
// daemon route behind the desktop app's Simulator tab needs the same answers,
// and two implementations of "which simulator did you mean" would eventually
// disagree - which is exactly the guess this command refuses to make.
type simDevice struct {
	simctl.Device
	// Lease is what AO knows about who is driving this device. It is never
	// "free": see simLeaseUnknownReason.
	Lease simLeaseView `json:"lease"`
	// Assigned marks the device AO gave THIS session (AO_SIM_UDID). It is not a
	// lease and grants nothing: it says which device is yours to work on, so an
	// agent looking at a machine with several booted simulators can tell its own
	// from its crewmate's without having to remember anything.
	Assigned bool `json:"assigned"`
}

type simListResult struct {
	Devices       []simDevice `json:"devices"`
	DefaultUDID   *string     `json:"defaultUdid"`
	DefaultReason string      `json:"defaultReason"`
}

type simShotResult struct {
	UDID              string `json:"udid"`
	Name              string `json:"name"`
	Runtime           string `json:"runtime"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	Path              string `json:"path"`
	Bytes             int64  `json:"bytes"`
	CapturedAt        string `json:"capturedAt"`
	Note              string `json:"note"`
	// Lease is additive: slice 1's keys above are a shipped contract.
	Lease simLeaseView `json:"lease"`
	// Build is which app was on the device when the frame was captured, and it
	// is written INTO the PNG as well as reported here (see simBuildTextKey).
	// It exists because `xcodebuild test` installs the app target as part of
	// running tests, so the binary can change under a session that never asked
	// for it - and two captures either side of that look identical.
	Build *simBuildView `json:"build,omitempty"`
	// BuildUnknown is why there is no Build. Never silent: a capture that could
	// not say which build it saw has to say THAT, or a reader assumes the
	// question was not worth asking.
	BuildUnknown string `json:"buildUnknown,omitempty"`
}

// simBuildView is simbuild.Build on the wire.
type simBuildView struct {
	ID          string    `json:"id"`
	BundleID    string    `json:"bundleId"`
	Name        string    `json:"name"`
	Version     string    `json:"version,omitempty"`
	Number      string    `json:"number,omitempty"`
	Digest      string    `json:"digest"`
	InstalledAt time.Time `json:"installedAt"`
	// Inferred marks a build AO chose rather than was told - the newest of
	// several apps on the device - and Of is how many it chose from. Reported so
	// nobody over-trusts a pick, and so the way to pin it is discoverable.
	Inferred bool `json:"inferred,omitempty"`
	Of       int  `json:"of,omitempty"`
}

// ID is what goes in the PNG and on the Build: line. An inferred pick says so:
// the identity is still exact (it names the app it is about), but a reader
// should know AO chose which app rather than being told.
func (b simBuildView) line() string {
	if !b.Inferred {
		return b.ID
	}
	return fmt.Sprintf("%s (newest of %d apps on this device; pin it with --app or $AO_SIM_APP)", b.ID, b.Of)
}

func newSimCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sim",
		Short: "Read, drive and boot local iOS Simulators",
		Long: "Inspect local iOS Simulators, capture their screen, drive them, and boot one.\n\n" +
			"It shells out to `xcrun simctl` on demand. `ao sim boot` is the only " +
			"subcommand that changes a device's power state, and it only ever powers " +
			"one ON: there is no shutdown, reboot or erase here, because those " +
			"destroy data or take a device away from whoever is using it - a human " +
			"does them from the desktop app's Device tab. Simulators are shared with " +
			"other AO sessions and with any human using Xcode, so a captured frame " +
			"may be mid-interaction.",
	}
	cmd.AddCommand(
		newSimListCommand(ctx), newSimShotCommand(ctx), newSimBootCommand(ctx),
		newSimClaimCommand(ctx), newSimReleaseCommand(ctx),
		newSimAXCommand(ctx), newSimLogCommand(ctx),
		newSimTapCommand(ctx), newSimSwipeCommand(ctx), newSimDragCommand(ctx),
		newSimPinchCommand(ctx),
		newSimTypeCommand(ctx), newSimButtonCommand(ctx),
		newSimInstallCommand(ctx), newSimLaunchCommand(ctx),
		newSimFlowCommand(ctx),
		newSimRecordCommand(ctx),
	)
	return cmd
}

func newSimListCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List local iOS Simulators and which one `ao sim shot` would pick",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			devices, err := ctx.listSimDevices(cmd.Context())
			if err != nil {
				return err
			}
			result := simList(devices)
			result.attachLeases(ctx.simLeaseViews(cmd.Context()))
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimList(cmd.OutOrStdout(), result, ctx.deps.Now().UTC())
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output simulators as JSON")
	return cmd
}

func newSimShotCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid   string
		output string
		app    string
		json   bool
	}
	cmd := &cobra.Command{
		Use:   "shot",
		Short: "Capture a booted simulator's screen to a PNG this session can read",
		Long: "Capture the screen of a booted iOS Simulator and print the PNG's path.\n\n" +
			"With no --udid the booted simulator is used, but only when exactly one is " +
			"booted: with none, or with several, the command fails and says so rather " +
			"than guessing; `ao sim boot` is how one is started.\n\n" +
			"The PNG lands under this session's own artifact directory " +
			"(<AO data dir>/sim/<session id>/), outside any repository, so it can never " +
			"be committed by accident. Use --output to write somewhere else.",
		Example: `  ao sim shot
  ao sim shot --udid 00000000-0000-0000-0000-000000000000
  ao sim shot --output /tmp/screen.png --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.captureSimShot(cmd.Context(), opts.udid, opts.output, opts.app)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimShot(cmd.OutOrStdout(), result, strings.TrimSpace(os.Getenv("AO_SESSION_ID")))
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Capture this simulator instead of the booted one")
	f.StringVar(&opts.output, "output", "", "Write the PNG here instead of the session artifact directory")
	f.StringVar(&opts.app, "app", "", "Fingerprint this bundle id instead of the newest installed app ($AO_SIM_APP pins it)")
	f.BoolVar(&opts.json, "json", false, "Output the capture result as JSON")
	return cmd
}

// listSimDevices is the only path into simctl. Every subcommand goes through
// it, so there is a single place that knows how devices are discovered.
func (c *commandContext) listSimDevices(ctx context.Context) ([]simDevice, error) {
	found, err := simctl.List(ctx, c.deps.LookPath, c.deps.CommandOutput)
	if err != nil {
		if errors.Is(err, simctl.ErrUnavailable) {
			return nil, fmt.Errorf("%s not found on PATH: `ao sim` needs the Xcode command line tools on macOS", simctl.Binary)
		}
		return nil, err
	}
	devices := make([]simDevice, 0, len(found))
	for _, d := range found {
		devices = append(devices, simDevice{Device: d})
	}
	return devices, nil
}

// assignedSimUDID is the device this session was given, read from the
// environment AO puts it in at spawn (see session_manager.EnvSimUDID). Empty
// means the machine had no device left to give, and every command falls back to
// the rule it has always used.
//
// It is read here rather than passed down from each command because forgetting
// to consult it is precisely the failure this exists to prevent: an agent with
// one booted simulator in front of it reaches for that one, and that one may be
// its crewmate's.
func assignedSimUDID() string {
	return strings.TrimSpace(os.Getenv("AO_SIM_UDID"))
}

// simUDIDOrAssigned resolves what an unqualified command means: the device the
// caller named, else the device this session owns. An explicit --udid always
// wins - naming a device is a deliberate act, and a session that has been given
// one still has legitimate reasons to look at another (reading a screen takes
// no lease and corrupts nothing).
//
// An assignment that names a device this machine no longer has is DROPPED
// rather than reported: the session was given that udid at spawn and simulators
// can be deleted since, and turning a stale reservation into "no simulator with
// udid ..." would blame the caller for something it never typed.
func simUDIDOrAssigned(udid string, devices []simDevice) string {
	if trimmed := strings.TrimSpace(udid); trimmed != "" {
		return trimmed
	}
	assigned := domain.NormalizeSimUDID(assignedSimUDID())
	for _, d := range devices {
		if domain.NormalizeSimUDID(d.UDID) == assigned {
			return assigned
		}
	}
	return ""
}

// resolveSimDevice applies the shared default-device rule and phrases its
// refusals the way a person reading a terminal needs them - with the command to
// run next. The rule itself lives in internal/simctl so the desktop app's
// device picker cannot drift from it.
//
// An unqualified call resolves to the caller's OWN device when it has one, so
// two members of a crew each driving a simulator no longer make every command
// ambiguous - and, more importantly, no longer silently agree on one device.
func resolveSimDevice(devices []simDevice, udid string) (simDevice, error) {
	udid = simUDIDOrAssigned(udid, devices)
	plain := make([]simctl.Device, 0, len(devices))
	for _, d := range devices {
		plain = append(plain, d.Device)
	}
	chosen, err := simctl.Resolve(plain, udid)
	if err != nil {
		return simDevice{}, explainSimResolve(err, strings.TrimSpace(udid))
	}
	for _, d := range devices {
		if d.UDID == chosen.UDID {
			return d, nil
		}
	}
	return simDevice{Device: chosen}, nil
}

// explainSimResolve turns a resolution outcome into the sentence that says what
// to do about it. Every branch that CAN be unblocked by booting says so with the
// command that does it: "nothing is booted" used to be a dead end an agent had
// to stop at, and naming the way out is the whole point of the change that gave
// this CLI a boot in the first place.
func explainSimResolve(err error, udid string) error {
	var notBooted *simctl.NotBootedError
	var ambiguous *simctl.AmbiguousError
	switch {
	case errors.As(err, &notBooted):
		return fmt.Errorf("simulator %s is not booted (state: %s). Boot it with `ao sim boot --udid %s` and retry",
			notBooted.Device.Label(), notBooted.Device.State, notBooted.Device.UDID)
	case errors.Is(err, simctl.ErrUnknownUDID):
		return fmt.Errorf("no simulator with udid %q; run `ao sim list` to see what this machine has", udid)
	case errors.Is(err, simctl.ErrNoDevices):
		return errors.New("no simulators found on this machine; `ao sim` needs Xcode's simulator runtimes installed")
	case errors.Is(err, simctl.ErrNoBooted):
		return fmt.Errorf("%s. Boot one with `ao sim boot --udid <udid>` and retry",
			strings.TrimPrefix(err.Error(), "simctl: "))
	case errors.As(err, &ambiguous):
		var b strings.Builder
		fmt.Fprintf(&b, "%d simulators are booted, so there is no unambiguous default. Re-run with one of:", len(ambiguous.Booted))
		for _, d := range ambiguous.Booted {
			fmt.Fprintf(&b, "\n  ao sim shot --udid %s   # %s (%s)", d.UDID, d.Name, d.Runtime)
		}
		return errors.New(b.String())
	default:
		return err
	}
}

// simList annotates the devices with the default `ao sim shot` would pick, and
// says why when there is none.
func simList(devices []simDevice) simListResult {
	result := simListResult{Devices: devices}
	assigned := simUDIDOrAssigned("", devices)
	for i := range result.Devices {
		result.Devices[i].Assigned = assigned != "" &&
			domain.NormalizeSimUDID(result.Devices[i].UDID) == domain.NormalizeSimUDID(assigned)
	}
	chosen, err := resolveSimDevice(devices, "")
	if err != nil {
		result.DefaultReason = err.Error()
		return result
	}
	for i := range result.Devices {
		if result.Devices[i].UDID == chosen.UDID {
			result.Devices[i].Default = true
		}
	}
	udid := chosen.UDID
	result.DefaultUDID = &udid
	result.DefaultReason = "the only booted simulator"
	if assigned != "" {
		result.DefaultReason = "the simulator assigned to this session (AO_SIM_UDID)"
	}
	return result
}

// attachLeases records, per device, what AO knows about who is driving it.
func (r *simListResult) attachLeases(views map[string]simLeaseView, daemonReachable bool) {
	for i := range r.Devices {
		r.Devices[i].Lease = simLeaseFor(views, r.Devices[i].UDID, daemonReachable)
	}
}

// unknownReason is why the unleased devices in this listing read as unknown.
// attachLeases gives every unknown device the same reason, so the first one
// speaks for all of them.
func (r simListResult) unknownReason() string {
	for _, d := range r.Devices {
		if d.Lease.Reason != "" {
			return d.Lease.Reason
		}
	}
	return simLeaseUnknownReason
}

func (c *commandContext) captureSimShot(ctx context.Context, udid, output, app string) (simShotResult, error) {
	devices, err := c.listSimDevices(ctx)
	if err != nil {
		return simShotResult{}, err
	}
	device, err := resolveSimDevice(devices, udid)
	if err != nil {
		return simShotResult{}, err
	}

	capturedAt := c.deps.Now().UTC()
	path := strings.TrimSpace(output)
	if path == "" {
		path, err = simSessionShotPath(capturedAt, device.UDID)
		if err != nil {
			return simShotResult{}, err
		}
	} else if path, err = filepath.Abs(path); err != nil {
		return simShotResult{}, fmt.Errorf("resolve --output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return simShotResult{}, fmt.Errorf("create screenshot directory: %w", err)
	}

	out, err := c.deps.CommandOutput(ctx, simctl.Binary, "simctl", "io", device.UDID, "screenshot", "--type=png", path)
	if err != nil {
		return simShotResult{}, fmt.Errorf("`simctl io screenshot` failed for %s: %w: %s", device.Label(), err, simctl.Output(out))
	}
	// simctl can exit 0 without leaving a usable frame. Handing an agent an
	// empty file it would read as a screen is worse than failing here.
	info, err := os.Stat(path)
	if err != nil {
		return simShotResult{}, fmt.Errorf("`simctl io screenshot` reported success but wrote no file at %s: %w", path, err)
	}
	if info.Size() == 0 {
		return simShotResult{}, fmt.Errorf("`simctl io screenshot` wrote an empty file at %s", path)
	}

	// Read-only: the capture never took a lease and never waited for one. It
	// only reports what AO knows, so an agent that reads a frame is told when
	// the device belongs to somebody else rather than assuming it may drive it.
	views, reachable := c.simLeaseViews(ctx)
	result := simShotResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Path:              path,
		Bytes:             info.Size(),
		CapturedAt:        capturedAt.Format(time.RFC3339),
		Note:              simSharedDeviceNote,
		Lease:             simLeaseFor(views, device.UDID, reachable),
	}
	result.Build, result.BuildUnknown = c.readSimBuild(ctx, device, app)
	if result.Build != nil {
		// Into the file, not merely beside it. Evidence gets downloaded, moved
		// and dragged into the Tests tab by a person who was never told there
		// was a second thing to bring, so the build has to travel inside the
		// picture. A failure here does not fail the capture: a screenshot with
		// no build recorded is still a screenshot.
		if err := pngmeta.Set(path, simBuildTextKey, result.Build.ID); err != nil {
			result.BuildUnknown = fmt.Sprintf("the build was read but could not be written into the PNG: %v", err)
		}
	}
	if size, err := os.Stat(path); err == nil {
		result.Bytes = size.Size()
	}
	return result, nil
}

// simBuildTextKey is the PNG tEXt keyword the build id is stored under. It is
// part of the on-disk contract between `ao sim shot`, `ao smoke record` and the
// Tests tab's own upload, so all three must agree on the spelling.
const simBuildTextKey = "ao-build"

// readSimBuild fingerprints the app on a device, or says why it could not.
//
// Which app is a decision, not a guess. The app under test is the single
// user-installed application on the device, with the XCUITest runner that
// `xcodebuild test` installs alongside it discounted. Several candidates are
// refused with the flag that resolves them, in the same house style every other
// ambiguity in this CLI is refused with.
//
// Deliberately NOT the frontmost app. It IS readable - the accessibility bridge
// reports a bundle id with every tree - but the read costs over a second, it
// contends for the exclusive bridge that gestures go through, and it answers
// "SpringBoard" whenever the app happens to be backgrounded. A silently wrong
// build id is worse than none, and it would also cost `ao sim shot` the
// property that makes it useful: that it is cheap and takes no lease.
func (c *commandContext) readSimBuild(ctx context.Context, device simDevice, bundleID string) (*simBuildView, string) {
	build, err := simbuild.Read(ctx, simbuild.Runner(c.deps.CommandOutput), device.DataPath, simAppOrEnv(bundleID))
	if err != nil {
		return nil, explainSimBuild(err, device)
	}
	return &simBuildView{
		ID:          build.ID(),
		BundleID:    build.BundleID,
		Name:        build.Name,
		Version:     build.Version,
		Number:      build.Number,
		Digest:      build.Digest,
		InstalledAt: build.InstalledAt,
		Inferred:    build.Inferred,
		Of:          build.Of,
	}, ""
}

// simAppOrEnv resolves which app a capture is about: the one the caller named,
// else the one this session was configured with.
//
// $AO_SIM_APP exists because a project's sessions test the same app every time,
// and a device that has accumulated nine of them over months should not make
// every one of those sessions pass a flag. Set it once in the project's
// environment and every capture, install and launch is pinned.
func simAppOrEnv(bundleID string) string {
	if trimmed := strings.TrimSpace(bundleID); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(os.Getenv("AO_SIM_APP"))
}

// explainSimBuild says why a capture carries no build, with the way out where
// there is one.
func explainSimBuild(err error, device simDevice) string {
	switch {
	case errors.Is(err, simbuild.ErrNoApp):
		return fmt.Sprintf("no app is installed on %s, so there is no build to record; `ao sim install <path/to/App.app>` puts one there", device.Name)
	case errors.Is(err, simbuild.ErrUnknownApp):
		return err.Error() + " - `ao sim shot` without --app records the single installed app"
	case errors.Is(err, simbuild.ErrNoDataPath):
		return fmt.Sprintf("simctl did not say where %s keeps its data, so its installed app could not be read", device.Name)
	default:
		return fmt.Sprintf("the build on %s could not be read: %v", device.Name, err)
	}
}

// simSessionShotPath puts the capture in this session's own artifact directory
// under the AO data dir. Per-session keeps concurrent workers from clobbering
// each other, and living outside every repository means a stray screenshot can
// never be committed by accident.
//
// Screenshots go in their own subdirectory, apart from recorded flows. They
// used to share one, and a session with a morning of screenshots in it buried
// every flow it had recorded - which matters because the two are used
// differently: a screenshot is looked at once, a flow is found again later and
// handed to somebody. Screenshots already on disk stay exactly where they are;
// nothing reads them back by path.
func simSessionShotPath(capturedAt time.Time, udid string) (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", errors.New("ao sim shot writes into the current session's artifact directory, but AO_SESSION_ID is not set; pass --output <path> to capture outside an AO session")
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	name := capturedAt.Format(simShotStampLayout) + "Z-" + udid + ".png"
	return filepath.Join(simrecord.ShotsDir(cfg.DataDir, sessionID), name), nil
}

func writeSimList(out io.Writer, result simListResult, now time.Time) error {
	if len(result.Devices) == 0 {
		_, err := fmt.Fprintln(out, "No simulators found on this machine.")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "UDID\tSTATE\tRUNTIME\tLEASE\tNAME"); err != nil {
		return err
	}
	for _, d := range result.Devices {
		name := d.Name
		if !d.Available {
			name += " (unavailable)"
		}
		if d.Assigned {
			name += "  <- yours ($AO_SIM_UDID)"
		}
		if d.Default {
			name += "  <- default for `ao sim shot`"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.UDID, d.State, d.Runtime, d.Lease.column(now), name); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// Spell the honest meaning of the column out once, with the reason AO
	// actually has: "nobody holds it" and "nobody could be asked" are both
	// unknown, and printing the wrong one states something AO never checked.
	if _, err := fmt.Fprintf(out, "\nLEASE is only what AO knows: `unknown` means %s.\n", result.unknownReason()); err != nil {
		return err
	}
	if result.DefaultUDID == nil {
		_, err := fmt.Fprintf(out, "`ao sim shot` has no default here: %s\n", result.DefaultReason)
		return err
	}
	return nil
}

func writeSimShot(out io.Writer, result simShotResult, sessionID string) error {
	if _, err := fmt.Fprintf(out, "Captured %s (%s, %s) at %s\n",
		result.Name, result.Runtime, result.UDID, result.CapturedAt); err != nil {
		return err
	}
	// The path gets a line of its own so it can be read straight off the
	// terminal and handed to a file read.
	if _, err := fmt.Fprintln(out, result.Path); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Note: %s\n", result.Note); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Lease: %s\n", result.Lease.captureLine(sessionID)); err != nil {
		return err
	}
	// The build goes last because it is the line a reader comes back to: it is
	// what says whether this frame and the one before it are of the same app.
	if result.Build != nil {
		_, err := fmt.Fprintf(out, "Build: %s\n", result.Build.line())
		return err
	}
	_, err := fmt.Fprintf(out, "Build: unknown - %s\n", result.BuildUnknown)
	return err
}
