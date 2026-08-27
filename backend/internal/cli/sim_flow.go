package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// `ao sim flow` is the seam between what a session explored and what the team's
// Maestro suite can replay.
//
// AO does not drive a device through Maestro and never will - that was studied
// and rejected. What it does here is narrower and safe: syntax-check a flow
// without a device, and run one against a device this session already holds,
// with `--device` always pinned so Maestro can never wander onto the simulator
// a human is working on.

// maestroEnvNoAnalytics is set on every invocation. Maestro's analytics are on
// by default and its argv sanitiser transmits flag values that do not look like
// paths - a udid among them. Nothing AO runs reports to a third party.
const maestroEnvNoAnalytics = "MAESTRO_CLI_NO_ANALYTICS=1"

// maestroMissing says what is absent, and - just as important - what is not.
const maestroMissing = "`maestro` is not on PATH. `ao sim flow` shells out to it and AO never " +
	"installs, downloads or vendors it: install it yourself if you want this command. " +
	"Everything else still works, including `ao sim ax --format maestro`, which needs no binary"

func newSimFlowCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flow",
		Short: "Check and run Maestro flows against a simulator this session holds",
		Long: "Work with Maestro flow files.\n\n" +
			"`check` parses a flow and needs no device at all. `run` executes one, and " +
			"requires a claim on the target simulator: a flow relaunches the app under " +
			"test and resets its permissions, which is fine on a device set aside for " +
			"testing and destructive on one somebody is using.\n\n" +
			"AO never installs `maestro`.",
	}
	cmd.AddCommand(newSimFlowCheckCommand(ctx))
	cmd.AddCommand(newSimFlowRunCommand(ctx))
	return cmd
}

func newSimFlowCheckCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "check <file>",
		Short: "Parse a Maestro flow file without touching a device",
		Long: "Parse a Maestro flow and report the first syntax error.\n\n" +
			"This is a pure parse: no simulator is read, driven or booted. It catches " +
			"unknown commands and malformed structure. It cannot tell you whether a " +
			"selector matches anything on screen - only `ao sim flow run` answers that.",
		Example: `  ao sim flow check flow.yaml`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := ctx.runMaestro(cmd.Context(), "check-syntax", args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		},
	}
}

func newSimFlowRunCommand(ctx *commandContext) *cobra.Command {
	var udid string
	cmd := &cobra.Command{
		Use:   "run <file>",
		Short: "Run a Maestro flow against a simulator this session holds",
		Long: "Run a Maestro flow on a booted simulator.\n\n" +
			"The device is always pinned explicitly, so Maestro can never fall back to " +
			"picking one - left to choose, it takes the only connected simulator, which " +
			"is whichever one a human is using.\n\n" +
			"A claim is required. A flow relaunches the app under test and resets its " +
			"privacy permissions; that is what a regression test wants on a device set " +
			"aside for it, and damage anywhere else. " + simPowerNote,
		Example: `  ao sim claim --udid <test-device>
  ao sim flow run flow.yaml --udid <test-device>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			device, err := ctx.resolveBootedSimDevice(cmd.Context(), udid)
			if err != nil {
				return err
			}
			if err := ctx.requireSimLeaseForFlow(cmd.Context(), device); err != nil {
				return err
			}
			return ctx.runMaestroStream(cmd.Context(), cmd.OutOrStdout(), "test", "--device", device.UDID, args[0])
		},
	}
	cmd.Flags().StringVar(&udid, "udid", "", "Run against this simulator instead of the booted one")
	return cmd
}

// requireSimLeaseForFlow refuses unless this session already holds the device.
//
// It never claims one. Running a flow relaunches the app and rewrites its
// permissions, and taking a device in order to do that to it is precisely the
// "succeed quietly and carry on" behaviour the lease exists to prevent.
func (c *commandContext) requireSimLeaseForFlow(ctx context.Context, device simDevice) error {
	sessionID, err := simSessionID("`ao sim flow run`")
	if err != nil {
		return err
	}
	views, reachable := c.simLeaseViews(ctx)
	if !reachable {
		return errors.New("the daemon is not reachable, so AO cannot tell who holds this device - refusing to drive it")
	}
	view := simLeaseFor(views, device.UDID, true)
	if view.State != domain.SimLeaseHeld {
		return fmt.Errorf("%s is not claimed by this session; run `ao sim claim --udid %s` first", device.Label(), device.UDID)
	}
	if view.Holder != sessionID {
		return fmt.Errorf("%s is held by @%s, not this session; a flow would relaunch the app under test on their device", device.Label(), view.Holder)
	}
	return nil
}

// maestroBinary resolves the tool, or explains that AO will not fetch it.
func (c *commandContext) maestroBinary() (string, error) {
	bin, err := c.deps.LookPath("maestro")
	if err != nil || strings.TrimSpace(bin) == "" {
		return "", errors.New(maestroMissing)
	}
	return bin, nil
}

// maestroPreflight checks the flow file exists, then resolves the binary. The
// file is checked here rather than left to maestro because a missing path
// should not cost a JVM start, and because maestro's own message for it is
// worse.
//
// The flow file must be the last argument: that is what gets stat-ed before a
// JVM is started. Every caller in this package satisfies that by construction
// (`check-syntax <file>`, `test --device <udid> <file>`); a caller that puts
// something else last will have the wrong path checked.
func (c *commandContext) maestroPreflight(args []string) (string, error) {
	file := args[len(args)-1]
	if _, err := os.Stat(file); err != nil {
		return "", fmt.Errorf("no flow file at %s", file)
	}
	return c.maestroBinary()
}

// runMaestro runs maestro and collects all of its output before returning.
// This is `check`'s command: a syntax check with no device is a fast parse,
// over almost as soon as it starts, so there is nothing worth watching arrive.
func (c *commandContext) runMaestro(ctx context.Context, args ...string) (string, error) {
	bin, err := c.maestroPreflight(args)
	if err != nil {
		return "", err
	}
	out, runErr := c.deps.CommandOutputWithEnv(ctx, []string{maestroEnvNoAnalytics}, bin, args...)
	text := string(out)
	if runErr != nil {
		// Maestro's own diagnostic is the useful part; the exit status is not.
		return "", fmt.Errorf("maestro %s failed:\n%s", args[0], strings.TrimSpace(text))
	}
	return text, nil
}

// runMaestroStream runs maestro and prints its output as it arrives. This is
// `run`'s command: a flow takes tens of seconds to minutes, and a worker
// watching it needs to see progress rather than silence until it ends.
func (c *commandContext) runMaestroStream(ctx context.Context, out io.Writer, args ...string) error {
	bin, err := c.maestroPreflight(args)
	if err != nil {
		return err
	}
	stream, err := c.deps.StartStreamWithEnv(ctx, []string{maestroEnvNoAnalytics}, bin, args...)
	if err != nil {
		return fmt.Errorf("could not start maestro %s: %w", args[0], err)
	}
	defer func() { _ = stream.Close() }()
	// A read parked on a pipe cannot be interrupted by a context; stopping the
	// child is what ends it. Same treatment as `ao sim log --follow` (stream.go),
	// for the same reason.
	reading := make(chan struct{})
	defer close(reading)
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-reading:
		}
	}()

	_, copyErr := io.Copy(out, stream)
	// Being stopped is how an interrupted run ends: by a signal, by a parent
	// that went away, or by a harness that timed it out. None of those is a
	// failure, and the read error it produces ("file already closed") is this
	// command closing its own pipe on the way out.
	if ctx.Err() != nil {
		return nil //nolint:nilerr // intentional: an interrupted flow run was stopped on purpose
	}
	if copyErr != nil {
		return fmt.Errorf("maestro %s failed: %w", args[0], copyErr)
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("maestro %s failed:\n%s", args[0], strings.TrimSpace(err.Error()))
	}
	return nil
}
