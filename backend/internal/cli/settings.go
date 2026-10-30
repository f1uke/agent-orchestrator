package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

func newSettingsCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Read and change AO-wide settings",
	}
	cmd.AddCommand(newSettingsWorkerTransportCommand(ctx))
	return cmd
}

func newSettingsWorkerTransportCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "worker-transport [chat|terminal]",
		Short: "Show or set how new claude-code workers are driven, for every project",
		Long: `Without an argument, print the global default transport for new claude-code
workers. With one, set it.

A new claude-code worker is driven by, in order: the --transport of its spawn,
its project's setting (ao project set-config <id> --worker-transport), this
global setting, and then chat. Other agents and orchestrators are always
terminal. A session keeps the transport it was spawned with, so changing this
never touches a running one.

To go back to the agent's own terminal for every project:

  ao settings worker-transport terminal`,
		Example: "  ao settings worker-transport\n  ao settings worker-transport terminal",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return usageError{err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var res controllers.WorkerTransportSettingsResponse
			if len(args) == 1 {
				transport, err := parseSettingsTransport(args[0])
				if err != nil {
					return err
				}
				req := controllers.SetWorkerTransportSettingsRequest{WorkerTransport: transport}
				if err := ctx.putJSON(cmd.Context(), "settings/worker-transport", req, &res); err != nil {
					return err
				}
			} else if err := ctx.getJSON(cmd.Context(), "settings/worker-transport", &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "worker transport: %s\n", res.WorkerTransport)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the setting as JSON")
	return cmd
}

func parseSettingsTransport(raw string) (domain.Transport, error) {
	transport, err := domain.ParseTransport(strings.TrimSpace(raw))
	if err != nil {
		return "", usageError{fmt.Errorf("worker transport must be terminal or chat, not %q", raw)}
	}
	if transport == "" {
		return "", usageError{errors.New("worker transport is required: terminal or chat")}
	}
	return transport, nil
}
