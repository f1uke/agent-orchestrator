package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpower"
)

// `ao sim boot` is the one command in this CLI that changes a device's power
// state, and it exists because leaving it out deadlocked something else.
//
// qa is created lazily, the first time a task takes a simulator lease
// (service/sim -> Manager.NoteRuntimeTouch, reason CrewJoinSim). A lease needs
// a BOOTED device. While booting was human-only, an iOS project on which nobody
// had happened to leave a simulator running could never grow a qa at all: dev
// found four shut-down devices and had to stop and ask. Boot is the link that
// was missing from that chain, so the human reversed the earlier decision.
//
// What did NOT change, and why the reversal is safe: shutdown, reboot and erase
// stay human-only, in the desktop app's Device tab. Erase wipes a device's data,
// and a shutdown takes a device out from under whoever is on it - neither is
// additive, and neither is what the deadlock needed. Boot only ever brings
// something up.
//
// It goes through the daemon rather than shelling out to simctl here, unlike
// the read-only half of `ao sim`. Three reasons, in order of weight:
//
//   - one implementation of "booted". internal/simpower runs
//     `simctl bootstatus -b`, which is the only form that waits for the device
//     to be DRIVABLE; `simctl list` flips to Booted seconds before SpringBoard
//     is up, so a CLI that watched the state would hand `ao sim claim` a device
//     that cannot yet be touched.
//   - the boot becomes visible. The Device tab renders the in-flight operation
//     from the same device listing it already polls, so an agent's boot shows up
//     to the human exactly like their own.
//   - two sessions booting at once is arbitrated. simpower.Power keeps one
//     operation per device; two CLI processes shelling out on their own would
//     each have their own idea of what is in flight.
//
// The cost is that boot, alone among the read-only commands, needs a running
// daemon and an AO session. `ao sim claim` - the very next command in the chain
// this exists to unblock - already needs both.

const (
	// simBootMaxBooted is the memory guard, and the number is not a round one:
	// three booted simulators have caused a true OOM on the machine this was
	// built for, which is the evidence the original human-only decision rested
	// on. Each device is a virtual machine of several GB that outlives the
	// session that started it, and a lease serialises DRIVING one device - it
	// does nothing whatever about how many exist.
	//
	// Two is what the ordinary case needs: the human's working device up, and
	// a scratch device for the agent to install onto. The third is where the
	// Device tab's escalating confirmation earns its keep, and an agent has no
	// dialog to escalate to - so this is where it stops and says so.
	//
	// It is a guard rail in the CLI, not an enforcement boundary: the daemon
	// route is shared with the Device tab, which must be able to go past it
	// with a human behind it.
	simBootMaxBooted = 2

	// simBootPollInterval is how often the device listing is asked whether the
	// boot has landed - the same second the Device tab polls at while an
	// operation is in flight.
	simBootPollInterval = time.Second

	// simBootGrace is how much longer the CLI waits than the daemon does, so a
	// boot that runs out of time fails with the DAEMON's reason (which knows
	// what the machine said) rather than with our own bare timeout.
	simBootGrace = 30 * time.Second

	// simBootedDeviceNote is what a freshly booted device is: shared, and not
	// yours. Booting takes nothing, so the device the command just started is
	// as open to the next session as any other.
	simBootedDeviceNote = "Booting claims nothing - this simulator is shared with other AO sessions and with " +
		"any human in Xcode, so claim it before you drive it."
)

// simBootResult is the `ao sim boot --json` payload.
type simBootResult struct {
	UDID              string `json:"udid"`
	Name              string `json:"name"`
	Runtime           string `json:"runtime"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	// State is simctl's own vocabulary, so it reads the same as `ao sim list`.
	State string `json:"state"`
	// AlreadyBooted marks the no-op: the device was up before the command ran
	// and nothing was started. A retry is a success, not a conflict.
	AlreadyBooted bool   `json:"alreadyBooted"`
	Note          string `json:"note"`
}

// simPowerRequest mirrors controllers.SimPowerInput. Only the state is sent:
// confirmHolder belongs to shutdown, which this command does not have.
type simPowerRequest struct {
	State string `json:"state"`
}

// simDevicePowerListing mirrors controllers.SimDevicePowerView.
type simDevicePowerListing struct {
	Op        string    `json:"op"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"startedAt"`
	Reason    string    `json:"reason,omitempty"`
}

// simDeviceListing mirrors controllers.SimDeviceView - the daemon's own view of
// a device, which is the only one that carries an in-flight power operation.
type simDeviceListing struct {
	UDID              string                 `json:"udid"`
	Name              string                 `json:"name"`
	Runtime           string                 `json:"runtime"`
	RuntimeIdentifier string                 `json:"runtimeIdentifier"`
	State             string                 `json:"state"`
	Available         bool                   `json:"available"`
	Power             *simDevicePowerListing `json:"power,omitempty"`
}

// listSimDevicesResponse mirrors controllers.ListSimDevicesResponse.
type listSimDevicesResponse struct {
	Devices []simDeviceListing `json:"devices"`
}

func newSimBootCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid    string
		timeout string
		json    bool
	}
	cmd := &cobra.Command{
		Use:   "boot",
		Short: "Power a simulator on and wait until it can actually be driven",
		Long: "Boot an iOS Simulator and wait until it is up, so the next command can claim and drive it.\n\n" +
			"With no --udid it boots the only simulator this machine has; with several " +
			"installed and none booted it fails and lists them rather than choosing which " +
			"multi-gigabyte device to start. A device that is already booted is a no-op, " +
			"not an error, so retrying is safe.\n\n" +
			"It stops at " + fmt.Sprint(simBootMaxBooted) + " booted simulators. Each is a virtual machine of " +
			"several GB and three at once has run this kind of machine out of memory, so " +
			"past that the answer is to drive one that is already up - or to ask a human, " +
			"who can boot another from the desktop app's Device tab.\n\n" +
			simPowerNote,
		Example: `  ao sim boot
  ao sim boot --udid 00000000-0000-0000-0000-000000000000
  ao sim boot --udid 00000000-0000-0000-0000-000000000000 --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			timeout, err := parseSimBootTimeout(opts.timeout)
			if err != nil {
				return err
			}
			result, err := ctx.bootSimDevice(cmd.Context(), opts.udid, timeout)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimBoot(cmd.OutOrStdout(), result)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Boot this simulator instead of the machine's only one")
	f.StringVar(&opts.timeout, "timeout", "", "How long to wait for the device to come up (e.g. 90s, 3m)")
	f.BoolVar(&opts.json, "json", false, "Output the result as JSON")
	return cmd
}

func parseSimBootTimeout(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return simpower.BootTimeout + simBootGrace, nil
	}
	timeout, err := time.ParseDuration(trimmed)
	if err != nil || timeout <= 0 {
		return 0, usageError{fmt.Errorf("--timeout %q is not a positive duration (try 90s, 3m)", raw)}
	}
	return timeout, nil
}

// bootSimDevice is the whole command: resolve which device, refuse the ones
// that must be refused, ask the daemon, then wait for the device to be up.
func (c *commandContext) bootSimDevice(ctx context.Context, udid string, timeout time.Duration) (simBootResult, error) {
	sessionID, err := simSessionID("ao sim boot")
	if err != nil {
		return simBootResult{}, err
	}
	devices, err := c.listSimDevices(ctx)
	if err != nil {
		return simBootResult{}, err
	}
	device, err := resolveSimBootTarget(devices, udid)
	if err != nil {
		return simBootResult{}, err
	}
	if device.Booted() {
		return simBootedResult(device, true), nil
	}
	if err := checkSimBootBudget(devices, device); err != nil {
		return simBootResult{}, err
	}

	path := "sessions/" + url.PathEscape(sessionID) + "/sim-devices/" + url.PathEscape(device.UDID) + "/power"
	switch err := c.postJSON(ctx, path, simPowerRequest{State: "booted"}, nil); {
	case err == nil:
	case simPowerCode(err) == "SIM_POWER_ALREADY":
		// The device came up between our listing and the request - somebody
		// else's boot, or a human in Xcode. That is the state we asked for.
		return simBootedResult(device, true), nil
	case simPowerCode(err) == "SIM_POWER_BUSY":
		// Somebody is already powering this device. If it is a boot we want
		// the same outcome, so we join the wait rather than treating the race
		// as a failure; the wait itself is what refuses a shutdown.
	default:
		return simBootResult{}, err
	}

	if err := c.waitForSimBoot(ctx, device, timeout); err != nil {
		return simBootResult{}, err
	}
	return simBootedResult(device, false), nil
}

// resolveSimBootTarget decides which device an unqualified `ao sim boot` means.
//
// It is deliberately NOT resolveSimDevice: that rule answers "which booted
// device did you mean", and boot's question is the opposite one. What is kept
// is the house style the rest of `ao sim` is built on - with more than one
// candidate it refuses and prints the command to run next, rather than
// guessing. Guessing costs more here than anywhere else in this CLI: the wrong
// guess starts a multi-gigabyte virtual machine nobody asked for.
func resolveSimBootTarget(devices []simDevice, udid string) (simDevice, error) {
	// A session that owns a device means that device, even when others are
	// booted or several are installed: booting is exactly where guessing costs
	// the most, and the assignment is the one answer that is not a guess.
	if key := domain.NormalizeSimUDID(simUDIDOrAssigned(udid, devices)); key != "" {
		for _, d := range devices {
			if domain.NormalizeSimUDID(d.UDID) != key {
				continue
			}
			if !d.Available {
				return simDevice{}, fmt.Errorf(
					"simulator %s is unavailable on this machine, so it cannot be booted; `ao sim list` shows which can",
					d.Label())
			}
			return d, nil
		}
		return simDevice{}, fmt.Errorf("no simulator with udid %q; run `ao sim list` to see what this machine has",
			strings.TrimSpace(udid))
	}

	var booted, available []simDevice
	for _, d := range devices {
		if !d.Available {
			continue
		}
		available = append(available, d)
		if d.Booted() {
			booted = append(booted, d)
		}
	}
	switch {
	case len(booted) == 1:
		// The state boot asks for is already true of exactly one device, so
		// that device is what an unqualified request means. The caller reports
		// it as a no-op.
		return booted[0], nil
	case len(booted) > 1:
		var b strings.Builder
		fmt.Fprintf(&b, "%d simulators are already booted, so `ao sim boot` has no unambiguous default:", len(booted))
		for _, d := range booted {
			fmt.Fprintf(&b, "\n  %s   # %s (%s)", d.UDID, d.Name, d.Runtime)
		}
		b.WriteString("\nDrive one of those, or name the device you mean with `ao sim boot --udid <udid>`.")
		return simDevice{}, errors.New(b.String())
	case len(available) == 0:
		return simDevice{}, errors.New("no simulators found on this machine; `ao sim` needs Xcode's simulator runtimes installed")
	case len(available) == 1:
		return available[0], nil
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%d simulators are installed and none is booted, so there is no unambiguous default - "+
			"booting one starts a virtual machine of several GB, which is not a choice to make for you. Re-run with one of:", len(available))
		for _, d := range available {
			fmt.Fprintf(&b, "\n  ao sim boot --udid %s   # %s (%s)", d.UDID, d.Name, d.Runtime)
		}
		return simDevice{}, errors.New(b.String())
	}
}

// checkSimBootBudget is the memory guard. See simBootMaxBooted for the number
// and the reasoning; this is only where it is applied.
func checkSimBootBudget(devices []simDevice, target simDevice) error {
	var booted []simDevice
	for _, d := range devices {
		if d.Booted() && d.UDID != target.UDID {
			booted = append(booted, d)
		}
	}
	if len(booted) < simBootMaxBooted {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d simulators are already booted and each is a virtual machine of several GB - "+
		"three at once has run this machine out of memory, so `ao sim boot` stops at %d. Already booted:",
		len(booted), simBootMaxBooted)
	for _, d := range booted {
		fmt.Fprintf(&b, "\n  %s (%s, %s)", d.Name, d.Runtime, d.UDID)
	}
	fmt.Fprintf(&b, "\nDrive one of those instead, or ask the human to boot %s from the desktop app's Device tab, "+
		"where booting past this point is a button they press.", target.Name)
	return errors.New(b.String())
}

// waitForSimBoot blocks until the device is up, the daemon says the boot
// failed, or we run out of patience.
//
// It waits on the DAEMON's device listing rather than on simctl, because that
// listing is the only one that carries the in-flight operation: simctl flips a
// device to Booted seconds before it can be driven, and reporting success there
// would hand `ao sim claim` a device that answers nothing.
func (c *commandContext) waitForSimBoot(ctx context.Context, device simDevice, timeout time.Duration) error {
	attempts := int(timeout / simBootPollInterval)
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		view, err := c.fetchSimDeviceListing(ctx, device.UDID)
		if err != nil {
			return err
		}
		switch power := view.Power; {
		case power == nil:
			if strings.EqualFold(view.State, simctl.BootedState) {
				return nil
			}
		case power.Op == string(simpower.Shutdown) && power.State == string(simpower.Running):
			// Not a race to wait out: somebody deliberately asked for this
			// device to go down, and booting it back up under them is the
			// shutdown-shaped harm `ao sim boot` is not allowed to do.
			return fmt.Errorf("%s is being shut down right now, so it was not booted. "+
				"Wait for that to finish and run `ao sim boot --udid %s` again, or boot a different device",
				device.Label(), device.UDID)
		case power.Op == string(simpower.Boot) && power.State == string(simpower.Failed):
			return fmt.Errorf("booting %s failed: %s", device.Label(), power.Reason)
		}
		c.deps.Sleep(simBootPollInterval)
	}
	return fmt.Errorf("%s did not finish booting within %s. It may still be coming up - "+
		"run `ao sim list` to see where it got to", device.Label(), timeout)
}

// fetchSimDeviceListing reads one device from the daemon's listing.
func (c *commandContext) fetchSimDeviceListing(ctx context.Context, udid string) (simDeviceListing, error) {
	var res listSimDevicesResponse
	if err := c.getJSON(ctx, "sim/devices", &res); err != nil {
		return simDeviceListing{}, err
	}
	key := domain.NormalizeSimUDID(udid)
	for _, d := range res.Devices {
		if domain.NormalizeSimUDID(d.UDID) == key {
			return d, nil
		}
	}
	return simDeviceListing{}, fmt.Errorf("the daemon no longer lists a simulator with udid %s; run `ao sim list`", udid)
}

// simPowerCode is the daemon's error code for a refused power request, or "".
func simPowerCode(err error) string {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) {
		return ""
	}
	return apiErr.ErrorBody.Code
}

func simBootedResult(device simDevice, already bool) simBootResult {
	return simBootResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		State:             simctl.BootedState,
		AlreadyBooted:     already,
		// Not simSharedDeviceNote: that one is about a captured FRAME, and a
		// boot has no frame. What a caller needs to know here is the thing
		// booting does not do - it takes nothing.
		Note: simBootedDeviceNote,
	}
}

func writeSimBoot(out io.Writer, result simBootResult) error {
	verb := "Booted"
	if result.AlreadyBooted {
		verb = "Already booted:"
	}
	if _, err := fmt.Fprintf(out, "%s %s (%s, %s)\n", verb, result.Name, result.Runtime, result.UDID); err != nil {
		return err
	}
	// The next command in the chain, spelled out: a booted device is not yours
	// until you claim it, and the claim is what a shared machine needs.
	if _, err := fmt.Fprintf(out, "Claim it before you drive it: ao sim claim --udid %s\n", result.UDID); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Note: %s\n", result.Note)
	return err
}
