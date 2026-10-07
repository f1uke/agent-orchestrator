package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbuild"
	"github.com/aoagents/agent-orchestrator/backend/internal/simproc"
)

// Debugging an app on this session's own simulator.
//
// These are thin wrappers over tools an agent could run by hand - ps, lldb,
// the crash reports on disk - and each exists for something AO knows or guards
// that the raw tool cannot. AO knows which device and which app are this
// session's, so `lldb -p` gets the right pid without a `ps | grep` that matches
// a crewmate's copy of the same app. And AO can bound an lldb's life: an lldb
// left waiting on `process continue` keeps the app attached for as long as it
// lives, and a breakpoint hit inside it freezes the app with it. Every command
// here acts ONLY on this session's own devices - its primary one, or one it
// claimed with --device - never on a base or on somebody else's.

const (
	// simLLDBTimeout bounds an `ao sim lldb` by default. Two minutes covers a
	// batch of breakpoints that are hit, and is still short enough that a
	// forgotten `continue` cannot hold an app frozen through a whole task.
	simLLDBTimeout = 2 * time.Minute
	// simSettleTries and simSettleWait are how long the app is re-read after
	// lldb ends: debugserver exits with it and the kernel resumes the app, but
	// not in the same instant (measured: within 3 s).
	simSettleTries = 15
	simSettleWait  = 300 * time.Millisecond
)

// simAppState is what the process table says about an app.
type simAppState string

const (
	simAppRunning    simAppState = "running"
	simAppNotRunning simAppState = "not-running"
	simAppDebugger   simAppState = "debugger"
	simAppSIGSTOP    simAppState = "sigstop"
)

// simPIDResult is the `ao sim pid --json` payload.
type simPIDResult struct {
	UDID     string `json:"udid"`
	Name     string `json:"name"`
	BundleID string `json:"bundleId"`
	// Chosen marks a bundle id AO picked, the newest of Of apps on the device.
	Chosen bool          `json:"chosen,omitempty"`
	Of     int           `json:"of,omitempty"`
	PID    int           `json:"pid,omitempty"`
	State  simAppState   `json:"state"`
	Hold   *simproc.Hold `json:"hold,omitempty"`
	// Message is the state as a sentence, with the fix when something holds it.
	Message string `json:"message"`
}

// simOwnApp is an app on one of this session's own devices.
type simOwnApp struct {
	device simDevice
	app    simbuild.App
	chosen bool
	of     int
}

// ownSimDevice resolves the device a debugging command acts on: --device, else
// this session's primary one. It refuses a base or another session's device by
// name, even when $AO_SIM_UDID points at one (an environment inherited from
// somewhere else is exactly how that happens).
func (c *commandContext) ownSimDevice(ctx context.Context, cmd *cobra.Command, command string) (simDevice, error) {
	self, err := simSessionID(command)
	if err != nil {
		return simDevice{}, err
	}
	label, _ := cmd.Flags().GetString(simLabelFlag)
	var udid string
	switch {
	case strings.TrimSpace(label) != "":
		udid, err = c.simLabelUDID(ctx, label)
	case assignedSimUDID() != "":
		udid = assignedSimUDID()
	default:
		udid, err = c.simLabelUDID(ctx, domain.SimPrimaryLabel)
	}
	if err != nil {
		return simDevice{}, err
	}
	key := domain.NormalizeSimUDID(udid)
	clones, err := c.fetchSimClones(ctx)
	if err != nil {
		return simDevice{}, fmt.Errorf("%s acts only on this session's own simulators, and the daemon could not be asked which those are: %w", command, err)
	}
	for _, base := range clones.Bases {
		if domain.NormalizeSimUDID(base.UDID) == key {
			return simDevice{}, fmt.Errorf("%s is the base %s AO clones from, never a device to debug on; %s acts only on this session's own simulators (`ao sim list` marks them `yours`)",
				key, base.Name, command)
		}
	}
	for _, clone := range clones.Clones {
		if domain.NormalizeSimUDID(clone.UDID) == key && clone.SessionID != self {
			return simDevice{}, fmt.Errorf("%s is @%s's device (%s), not this session's; %s acts only on this session's own simulators",
				key, clone.SessionID, clone.Label, command)
		}
	}
	devices, err := c.listSimDevices(ctx)
	if err != nil {
		return simDevice{}, err
	}
	for _, d := range devices {
		if domain.NormalizeSimUDID(d.UDID) == key {
			return d, nil
		}
	}
	return simDevice{}, fmt.Errorf("this session's simulator %s is not on this machine any more; `ao sim list` shows what is", key)
}

// ownSimApp resolves the device and the app a debugging command is about. The
// app is the one named, else $AO_SIM_APP, else the newest installed.
func (c *commandContext) ownSimApp(ctx context.Context, cmd *cobra.Command, command, bundleID string) (simOwnApp, error) {
	device, err := c.ownSimDevice(ctx, cmd, command)
	if err != nil {
		return simOwnApp{}, err
	}
	apps, err := simbuild.ListApps(ctx, simbuild.Runner(c.deps.CommandOutput), device.DataPath)
	if err != nil {
		return simOwnApp{}, errors.New(explainSimBuild(err, device))
	}
	app, chosen, err := simbuild.UnderTest(apps, simAppOrEnv(bundleID))
	if err != nil {
		return simOwnApp{}, explainSimLaunchTarget(err, device)
	}
	target := simOwnApp{device: device, app: app, chosen: chosen}
	if chosen {
		target.of = len(apps)
	}
	return target, nil
}

// choiceNote says which app AO picked, when it picked one.
func (t simOwnApp) choiceNote(command string) string {
	if !t.chosen {
		return ""
	}
	return fmt.Sprintf("Chose %s, the newest of %d apps on this device; pin it with `%s <bundle-id>` or $AO_SIM_APP.",
		t.app.BundleID, t.of, command)
}

// simAppStatus reads what the process table says about the app.
func (c *commandContext) simAppStatus(ctx context.Context, target simOwnApp) (simPIDResult, error) {
	if !target.device.Booted() {
		result := target.result()
		result.State = simAppNotRunning
		result.Message = fmt.Sprintf("%s is %s, so %s is not running; `ao sim boot` starts it, then `ao sim launch %s`",
			target.device.Label(), target.device.State, target.app.BundleID, target.app.BundleID)
		return result, nil
	}
	table, err := c.readSimProcesses(ctx)
	if err != nil {
		return simPIDResult{}, err
	}
	return describeSimApp(table, target), nil
}

// result is the part of a status that does not depend on the process table.
func (t simOwnApp) result() simPIDResult {
	return simPIDResult{UDID: t.device.UDID, Name: t.device.Name, BundleID: t.app.BundleID, Chosen: t.chosen, Of: t.of}
}

// describeSimApp is the state of the app's main process in a table.
func describeSimApp(table simproc.Table, target simOwnApp) simPIDResult {
	result := target.result()
	proc, running := table.Main(target.app.Path)
	if !running {
		result.State = simAppNotRunning
		result.Message = fmt.Sprintf("%s is not running on %s; start it with `ao sim launch %s` (add --console to keep its stdout)",
			target.app.BundleID, target.device.Label(), target.app.BundleID)
		return result
	}
	result.PID = proc.PID
	hold, held := table.HoldOf(proc)
	if !held {
		result.State = simAppRunning
		result.Message = fmt.Sprintf("%s is running as pid %d on %s", target.app.BundleID, proc.PID, target.device.Label())
		return result
	}
	result.Hold = &hold
	result.State = simAppDebugger
	if hold.Kind == simproc.HeldBySIGSTOP {
		result.State = simAppSIGSTOP
	}
	result.Message = fmt.Sprintf("%s on %s: %s", target.app.BundleID, target.device.Label(), hold.Describe())
	return result
}

func newSimPIDCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "pid [bundle-id]",
		Short: "Print the pid of an app on this session's own simulator",
		Long: "Print the pid of an app running on this session's own simulator - its primary one " +
			"($AO_SIM_UDID), or with --device one it claimed - and nothing else on stdout, so " +
			"`lldb -p $(ao sim pid)` works. A base or another session's device is refused.\n\n" +
			"The app is the bundle id given, else $AO_SIM_APP, else the newest installed app (and " +
			"it says so when it chose). One line on stderr says its state: running, attached by a " +
			"debugger (naming the debugserver and lldb pids, and how to detach), or stopped by " +
			"SIGSTOP (and the `kill -CONT` that resumes it). An app that is not running exits 1 " +
			"and names `ao sim launch`.",
		Example: `  ao sim pid
  ao sim pid com.example.MyApp --device dbg
  lldb -p "$(ao sim pid)"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := ctx.ownSimApp(cmd.Context(), cmd, "`ao sim pid`", firstArg(args))
			if err != nil {
				return err
			}
			result, err := ctx.simAppStatus(cmd.Context(), target)
			if err != nil {
				return err
			}
			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), result); err != nil {
					return err
				}
			} else if note := target.choiceNote("ao sim pid"); note != "" {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(), note); err != nil {
					return err
				}
			}
			if result.State == simAppNotRunning {
				return errors.New(result.Message)
			}
			if asJSON {
				return nil
			}
			if _, err := fmt.Fprintln(cmd.ErrOrStderr(), result.Message); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), result.PID)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the whole result (pid, state, what holds it) as JSON on stdout")
	return cmd
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

type simLLDBOptions struct {
	timeout     time.Duration
	continueFor time.Duration
	resume      bool
}

func newSimLLDBCommand(ctx *commandContext) *cobra.Command {
	opts := simLLDBOptions{}
	cmd := &cobra.Command{
		Use:   "lldb [bundle-id] [flags] -- <lldb args>...",
		Short: "Run a bounded lldb batch against an app on this session's own simulator",
		Long: "Attach `lldb --batch` to an app on this session's own simulator, run the commands " +
			"after `--`, and make sure the app is running and detached afterwards.\n\n" +
			"The pid is found as `ao sim pid` finds it. An app that already has a debugger is " +
			"refused, naming it - a second attach fails anyway. lldb's output streams as it comes.\n\n" +
			"lldb is bounded by --timeout (default 2m): at the deadline, or on Ctrl-C, the lldb this " +
			"command started is ended by its pid, which takes its debugserver with it and the " +
			"kernel resumes the app. Without that bound, `process continue` with no breakpoint that " +
			"stops waits for ever and keeps the app attached, and a breakpoint hit inside it freezes " +
			"the app for as long as that lldb lives.\n\n" +
			"--continue-for D logs and continues for a window instead of stopping: it appends " +
			"`process continue`, a D-second wait, `process interrupt` and `process detach` after " +
			"your commands, so breakpoints set with `-G true` (auto-continue) and `-C <command>` " +
			"print what they see for D and then let go. It must be shorter than --timeout.\n\n" +
			"After lldb exits the app is read again. Running and detached is the normal end; still " +
			"attached, or stopped by SIGSTOP (an lldb expression that timed out can leave it so), is " +
			"a loud failure naming the fix, and --resume sends a stopped app SIGCONT. The exit code " +
			"is lldb's own, or non-zero on a timeout or an app still held.",
		Example: `  ao sim lldb -- -o "bt all"
  ao sim lldb com.example.MyApp -- -o "breakpoint set -n ForecastStore.load" -o continue -o "frame variable" -o "bt 8"
  ao sim lldb --continue-for 10s -- -o "breakpoint set -n VC.tick -C 'frame variable self.count' -G true"`,
		Args: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 0 || dash == len(args) {
				return usageError{errors.New("give lldb something to do after `--`, e.g. `ao sim lldb -- -o \"bt all\"`")}
			}
			if dash > 1 {
				return usageError{fmt.Errorf("name at most one app before `--`, got %s", strings.Join(args[:dash], " "))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.timeout <= 0 {
				return usageError{fmt.Errorf("--timeout must be positive, got %s", opts.timeout)}
			}
			if opts.continueFor < 0 || (opts.continueFor > 0 && opts.continueFor >= opts.timeout) {
				return usageError{fmt.Errorf("--continue-for %s must be shorter than --timeout %s, or lldb is ended before it detaches",
					opts.continueFor, opts.timeout)}
			}
			dash := cmd.ArgsLenAtDash()
			return ctx.runSimLLDB(cmd, firstArg(args[:dash]), args[dash:], opts)
		},
	}
	f := cmd.Flags()
	f.DurationVar(&opts.timeout, "timeout", simLLDBTimeout, "End lldb after this long if it has not finished (it is killed by pid, and the app resumes)")
	f.DurationVar(&opts.continueFor, "continue-for", 0, "Log and continue for this long, then interrupt and detach (for breakpoints with -G true)")
	f.BoolVar(&opts.resume, "resume", false, "If lldb leaves the app stopped by SIGSTOP, send it SIGCONT")
	return cmd
}

// simLLDBArgs is the lldb command line: batch mode, attached by pid, the
// caller's commands, and with continueFor the measured logpoint recipe after
// them.
func simLLDBArgs(pid int, callerArgs []string, continueFor time.Duration) []string {
	args := append([]string{"--batch", "-p", strconv.Itoa(pid)}, callerArgs...)
	if continueFor > 0 {
		args = append(args,
			"-o", "script lldb.debugger.SetAsync(True)",
			"-o", "process continue",
			"-o", "script import time; time.sleep("+strconv.FormatFloat(continueFor.Seconds(), 'f', -1, 64)+")",
			"-o", "process interrupt",
			"-o", "process detach",
		)
	}
	return args
}

func (c *commandContext) runSimLLDB(cmd *cobra.Command, bundleID string, lldbArgs []string, opts simLLDBOptions) error {
	ctx := cmd.Context()
	errOut := cmd.ErrOrStderr()
	target, err := c.ownSimApp(ctx, cmd, "`ao sim lldb`", bundleID)
	if err != nil {
		return err
	}
	before, err := c.simAppStatus(ctx, target)
	if err != nil {
		return err
	}
	switch before.State {
	case simAppNotRunning:
		return errors.New(before.Message)
	case simAppDebugger, simAppSIGSTOP:
		return fmt.Errorf("not attaching: %s", before.Message)
	}
	if note := target.choiceNote("ao sim lldb"); note != "" {
		noteProgress(errOut, "%s\n", note)
	}
	noteProgress(errOut, "Attaching lldb to %s (pid %d) on %s; it is ended after %s if it has not finished.\n",
		target.app.BundleID, before.PID, target.device.Label(), opts.timeout)

	runCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	runCtx, cancel := context.WithTimeout(runCtx, opts.timeout)
	defer cancel()
	stream, err := c.deps.StartStream(runCtx, "lldb", simLLDBArgs(before.PID, lldbArgs, opts.continueFor)...)
	if err != nil {
		return fmt.Errorf("could not start lldb: %w", err)
	}
	// Closing the stream kills the child this command started, by its pid -
	// never anything found by name, which could be another session's lldb.
	reading := make(chan struct{})
	go func() {
		select {
		case <-runCtx.Done():
			_ = stream.Close()
		case <-reading:
		}
	}()
	_, copyErr := io.Copy(cmd.OutOrStdout(), stream)
	close(reading)
	ended := runCtx.Err()
	lldbErr := stream.Err()
	_ = stream.Close()

	after, settleErr := c.settleSimApp(ctx, target, opts.resume, errOut)
	if settleErr != nil {
		return settleErr
	}
	noteProgress(errOut, "%s\n", simLLDBVerdict(after))

	switch {
	case after.State == simAppDebugger || after.State == simAppSIGSTOP:
		msg := "lldb has ended but the app is still held: " + after.Message
		if after.State == simAppSIGSTOP {
			msg += " (or re-run with --resume)"
		}
		return errors.New(msg)
	case errors.Is(ended, context.DeadlineExceeded):
		return fmt.Errorf("lldb was still running after --timeout %s, so it was ended; raise --timeout, or check that a "+
			"`continue` has a breakpoint that stops it", opts.timeout)
	case ended != nil:
		return errors.New("interrupted: lldb was ended")
	case copyErr != nil:
		return fmt.Errorf("reading lldb's output failed: %w", copyErr)
	case lldbErr != nil:
		var exit *exec.ExitError
		if errors.As(lldbErr, &exit) {
			return exitStatusError{code: exit.ExitCode(), err: fmt.Errorf("lldb failed: %w", lldbErr)}
		}
		return fmt.Errorf("lldb failed: %w", lldbErr)
	}
	return nil
}

// settleSimApp re-reads the app until nothing holds it, or until it has had
// the time a debugserver takes to exit. With resume, an app left stopped by
// SIGSTOP is sent SIGCONT once and read again.
func (c *commandContext) settleSimApp(
	ctx context.Context, target simOwnApp, resume bool, errOut io.Writer,
) (simPIDResult, error) {
	var after simPIDResult
	resumed := false
	for try := 0; try < simSettleTries; try++ {
		if try > 0 {
			c.deps.Sleep(simSettleWait)
		}
		table, err := c.readSimProcesses(ctx)
		if err != nil {
			return simPIDResult{}, fmt.Errorf("lldb has ended, but whether the app is free could not be read: %w", err)
		}
		after = describeSimApp(table, target)
		if after.State == simAppSIGSTOP && resume && !resumed {
			resumed = true
			pid := strconv.Itoa(after.PID)
			if out, err := c.deps.CommandOutput(ctx, "kill", "-CONT", pid); err != nil {
				return simPIDResult{}, fmt.Errorf("`kill -CONT %s` failed: %w: %s", pid, err, strings.TrimSpace(string(out)))
			}
			noteProgress(errOut, "The app was left stopped by SIGSTOP; sent `kill -CONT %s`.\n", pid)
			continue
		}
		if after.State == simAppRunning || after.State == simAppNotRunning {
			break
		}
	}
	return after, nil
}

// simLLDBVerdict is the line that says how lldb left the app.
func simLLDBVerdict(after simPIDResult) string {
	switch after.State {
	case simAppRunning:
		return fmt.Sprintf("%s (pid %d) is running and detached.", after.BundleID, after.PID)
	case simAppNotRunning:
		return fmt.Sprintf("%s is no longer running; if it crashed, `ao sim crashes --show 1` shows the report.", after.BundleID)
	default:
		return "STILL HELD: " + after.Message
	}
}
