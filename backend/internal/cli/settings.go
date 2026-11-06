package cli

import (
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
	cmd.AddCommand(newSettingsOrchestratorTransportCommand(ctx))
	return cmd
}

func newSettingsWorkerTransportCommand(ctx *commandContext) *cobra.Command {
	return newSettingsTransportCommand(ctx, settingsTransport[controllers.WorkerTransportSettingsResponse]{
		label: "worker",
		short: "Show or set how new claude-code workers are driven, for every project",
		long: `Without an argument, print the global default transport for new claude-code
workers. With one, set it.

A new claude-code worker is driven by, in order: the --transport of its spawn,
its project's setting (ao project set-config <id> --worker-transport), this
global setting, and then chat. Other agents are always terminal. A session
keeps the transport it was spawned with, so changing this never touches a
running one.

To go back to the agent's own terminal for every project:

  ao settings worker-transport terminal`,
		request: func(t domain.Transport) any { return controllers.SetWorkerTransportSettingsRequest{WorkerTransport: t} },
		value:   func(r controllers.WorkerTransportSettingsResponse) domain.Transport { return r.WorkerTransport },
	})
}

func newSettingsOrchestratorTransportCommand(ctx *commandContext) *cobra.Command {
	return newSettingsTransportCommand(ctx, settingsTransport[controllers.OrchestratorTransportSettingsResponse]{
		label: "orchestrator",
		short: "Show or set how new claude-code orchestrators are driven, for every project",
		long: `Without an argument, print the global default transport for new claude-code
orchestrators. With one, set it.

A new claude-code orchestrator is driven by, in order: its project's setting
(ao project set-config <id> --orchestrator-transport), this global setting, and
then chat. Other agents are always terminal. Only an orchestrator spawned after
the change picks it up: a session keeps the transport it was spawned with
through restore and restart. To move a project's orchestrator, restart it as a
new session from the project menu.

To go back to the agent's own terminal for every project:

  ao settings orchestrator-transport terminal`,
		request: func(t domain.Transport) any {
			return controllers.SetOrchestratorTransportSettingsRequest{OrchestratorTransport: t}
		},
		value: func(r controllers.OrchestratorTransportSettingsResponse) domain.Transport {
			return r.OrchestratorTransport
		},
	})
}

// settingsTransport describes one global transport setting: its label, its help, and how its wire types carry the value.
// The route is settings/<label>-transport.
type settingsTransport[R any] struct {
	label, short, long string
	request            func(domain.Transport) any
	value              func(R) domain.Transport
}

func newSettingsTransportCommand[R any](ctx *commandContext, st settingsTransport[R]) *cobra.Command {
	use := st.label + "-transport"
	route := "settings/" + use
	var asJSON bool
	cmd := &cobra.Command{
		Use:     use + " [chat|terminal]",
		Short:   st.short,
		Long:    st.long,
		Example: fmt.Sprintf("  ao settings %[1]s\n  ao settings %[1]s terminal", use),
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return usageError{err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var res R
			if len(args) == 1 {
				transport, err := parseSettingsTransport(st.label, args[0])
				if err != nil {
					return err
				}
				if err := ctx.putJSON(cmd.Context(), route, st.request(transport), &res); err != nil {
					return err
				}
			} else if err := ctx.getJSON(cmd.Context(), route, &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s transport: %s\n", st.label, st.value(res))
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the setting as JSON")
	return cmd
}

func parseSettingsTransport(label, raw string) (domain.Transport, error) {
	transport, err := domain.ParseTransport(strings.TrimSpace(raw))
	if err != nil {
		return "", usageError{fmt.Errorf("%s transport must be terminal or chat, not %q", label, raw)}
	}
	if transport == "" {
		return "", usageError{fmt.Errorf("%s transport is required: terminal or chat", label)}
	}
	return transport, nil
}
