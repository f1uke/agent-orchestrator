package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
	"Everything else still works, including `ao sim ax --format maestro`, which needs no binary."

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

// maestroBinary resolves the tool, or explains that AO will not fetch it.
func (c *commandContext) maestroBinary() (string, error) {
	lookPath := c.deps.LookPath
	if lookPath == nil {
		return "", errors.New(maestroMissing)
	}
	bin, err := lookPath("maestro")
	if err != nil || strings.TrimSpace(bin) == "" {
		return "", errors.New(maestroMissing)
	}
	return bin, nil
}

// runMaestro checks the flow file exists, then runs maestro on it. The file is
// checked here rather than left to maestro because a missing path should not
// cost a JVM start, and because maestro's own message for it is worse.
func (c *commandContext) runMaestro(ctx context.Context, args ...string) (string, error) {
	file := args[len(args)-1]
	if _, err := os.Stat(file); err != nil {
		return "", fmt.Errorf("no flow file at %s", file)
	}
	bin, err := c.maestroBinary()
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
