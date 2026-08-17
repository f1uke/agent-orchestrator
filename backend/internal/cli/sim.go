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
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// `ao sim` gives a session a cheap, strictly read-only way to see an iOS
// Simulator screen. It shells out to `xcrun simctl` on demand and nothing else:
// no helper process, no private frameworks, no HID synthesis, no polling, and
// no command that could boot, shut down, reboot or erase a device. A human or
// another AO session may be driving the same simulator, so every capture says
// so rather than pretending the frame is ours alone.
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
	// simNeverBootsNote is repeated in every refusal so an agent does not go
	// looking for a flag that boots a device. There is none, on purpose.
	simNeverBootsNote = "AO never boots, shuts down or erases a simulator for you."
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
}

func newSimCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sim",
		Short: "Read-only access to local iOS Simulators",
		Long: "Inspect local iOS Simulators and capture their screen.\n\n" +
			"Every subcommand is read-only against the device: it shells out to " +
			"`xcrun simctl` on demand and never boots, shuts down, reboots or erases " +
			"a simulator. Simulators are shared with other AO sessions and with any " +
			"human using Xcode, so a captured frame may be mid-interaction.",
	}
	cmd.AddCommand(
		newSimListCommand(ctx), newSimShotCommand(ctx),
		newSimClaimCommand(ctx), newSimReleaseCommand(ctx),
		newSimAXCommand(ctx), newSimLogCommand(ctx),
		newSimTapCommand(ctx), newSimSwipeCommand(ctx), newSimDragCommand(ctx),
		newSimTypeCommand(ctx), newSimButtonCommand(ctx),
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
		json   bool
	}
	cmd := &cobra.Command{
		Use:   "shot",
		Short: "Capture a booted simulator's screen to a PNG this session can read",
		Long: "Capture the screen of a booted iOS Simulator and print the PNG's path.\n\n" +
			"With no --udid the booted simulator is used, but only when exactly one is " +
			"booted: with none, or with several, the command fails and says so rather " +
			"than guessing. " + simNeverBootsNote + "\n\n" +
			"The PNG lands under this session's own artifact directory " +
			"(<AO data dir>/sim/<session id>/), outside any repository, so it can never " +
			"be committed by accident. Use --output to write somewhere else.",
		Example: `  ao sim shot
  ao sim shot --udid 00000000-0000-0000-0000-000000000000
  ao sim shot --output /tmp/screen.png --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.captureSimShot(cmd.Context(), opts.udid, opts.output)
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

// resolveSimDevice applies the shared default-device rule and phrases its
// refusals the way a person reading a terminal needs them - with the command to
// run next. The rule itself lives in internal/simctl so the desktop app's
// device picker cannot drift from it.
func resolveSimDevice(devices []simDevice, udid string) (simDevice, error) {
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
// to do about it. Every branch repeats that AO does not boot devices, because
// that is the flag an agent goes looking for next.
func explainSimResolve(err error, udid string) error {
	var notBooted *simctl.NotBootedError
	var ambiguous *simctl.AmbiguousError
	switch {
	case errors.As(err, &notBooted):
		return fmt.Errorf("simulator %s is not booted (state: %s). %s Boot it yourself (Xcode, or Simulator.app) and retry",
			notBooted.Device.Label(), notBooted.Device.State, simNeverBootsNote)
	case errors.Is(err, simctl.ErrUnknownUDID):
		return fmt.Errorf("no simulator with udid %q; run `ao sim list` to see what this machine has", udid)
	case errors.Is(err, simctl.ErrNoDevices):
		return fmt.Errorf("no simulators found on this machine. %s", simNeverBootsNote)
	case errors.Is(err, simctl.ErrNoBooted):
		return fmt.Errorf("%s. %s Boot one (Xcode, or Simulator.app) and retry, or run `ao sim list`",
			strings.TrimPrefix(err.Error(), "simctl: "), simNeverBootsNote)
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

func (c *commandContext) captureSimShot(ctx context.Context, udid, output string) (simShotResult, error) {
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
	return simShotResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Path:              path,
		Bytes:             info.Size(),
		CapturedAt:        capturedAt.Format(time.RFC3339),
		Note:              simSharedDeviceNote,
		Lease:             simLeaseFor(views, device.UDID, reachable),
	}, nil
}

// simSessionShotPath puts the capture in this session's own artifact directory
// under the AO data dir. Per-session keeps concurrent workers from clobbering
// each other, and living outside every repository means a stray screenshot can
// never be committed by accident.
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
	return filepath.Join(cfg.DataDir, "sim", sessionID, name), nil
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
	_, err := fmt.Fprintf(out, "Lease: %s\n", result.Lease.captureLine(sessionID))
	return err
}
