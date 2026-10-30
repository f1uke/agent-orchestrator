package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

func newSessionHostCommand() *cobra.Command {
	var cfg sessionhost.Config
	cmd := &cobra.Command{
		Use:    "session-host --log <events.jsonl> --socket <host.sock> [--prompt-file <file>] -- <agent command>",
		Short:  "Host a headless claude chat session (internal)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.ArgsLenAtDash() != 0 || len(args) == 0 {
				return usageError{errors.New("session-host needs the agent command after --")}
			}
			if cfg.LogPath == "" || cfg.SocketPath == "" {
				return usageError{errors.New("session-host needs --log and --socket")}
			}
			cfg.Command = args
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			code, err := sessionhost.Run(ctx, cfg, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if code != 0 {
				return exitStatusError{code: code, err: fmt.Errorf("claude exited with code %d", code)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfg.LogPath, "log", "", "event log to append to (created if missing)")
	cmd.Flags().StringVar(&cfg.SocketPath, "socket", "", "unix socket to serve the session on")
	cmd.Flags().StringVar(&cfg.PromptFile, "prompt-file", "", "first user message, sent only when the log has no user input yet")
	return cmd
}
