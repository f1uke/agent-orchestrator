package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemon"
	"github.com/aoagents/agent-orchestrator/backend/internal/releaseweb"
)

func newReleaseWebCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release-web",
		Short: "Run the read-only team web of the release trains",
		Long: "The team web shows the Release Train board to your team behind one shared key. " +
			"It runs as its own program, listens on 127.0.0.1 only, and never writes the board. " +
			"Putting it on the internet (Tailscale Funnel) is your step; ao never runs tailscale. " +
			"The key is never printed: copy-key puts it on the clipboard, and key.json holds it.",
	}
	cmd.AddCommand(newReleaseWebServeCommand(ctx), newReleaseWebStartCommand(ctx), newReleaseWebStopCommand(ctx),
		newReleaseWebStatusCommand(ctx), newReleaseWebRotateKeyCommand(ctx), newReleaseWebCopyKeyCommand(ctx))
	return cmd
}

// releaseWebControl resolves the data dir the way the daemon does, so the CLI
// and the daemon's routes act on the same release web.
func (c *commandContext) releaseWebControl() (cfg config.Config, control *daemon.ReleaseWebControl, err error) {
	cfg, err = config.Load()
	if err != nil {
		return config.Config{}, nil, err
	}
	control = daemon.NewReleaseWebControl(cfg.DataDir)
	control.Executable = c.deps.Executable
	return cfg, control, nil
}

func newReleaseWebServeCommand(ctx *commandContext) *cobra.Command {
	var port int
	var assets string
	var hosts []string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the team web in the foreground on 127.0.0.1",
		Long: "Serves the team web until interrupted. It needs a key: run rotate-key first. " +
			"Use start to run it in the background instead.",
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, control, err := ctx.releaseWebControl()
			if err != nil {
				return err
			}
			if port < 1 || port > 65535 {
				return usageError{fmt.Errorf("--port %d is not a port", port)}
			}
			for _, h := range hosts {
				if _, err := releaseweb.NormalizeHost(h); err != nil {
					return usageError{fmt.Errorf("--host: %w", err)}
				}
			}
			dir, err := daemon.ResolveReleaseWebAssets(assets, ctx.deps.Executable)
			if err != nil {
				return err
			}
			logOut := releaseweb.WebLogWriter(control.Paths, cmd.ErrOrStderr())
			run, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "release web: serving %s on 127.0.0.1:%d\n", dir, port)
			return daemon.RunReleaseWeb(run, daemon.ReleaseWebOptions{
				DataDir:      cfg.DataDir,
				Port:         port,
				AssetsDir:    dir,
				AllowedHosts: hosts,
				Logger:       slog.New(slog.NewTextHandler(logOut, nil)),
			})
		},
	}
	cmd.Flags().IntVar(&port, "port", releaseweb.DefaultPort, "Port to listen on, on 127.0.0.1")
	cmd.Flags().StringArrayVar(&hosts, "host", nil, "A public host name to answer besides 127.0.0.1 and localhost, on top of the hosts file; repeatable")
	cmd.Flags().StringVar(&assets, "assets", "", "Directory of the web bundle (default $"+daemon.ReleaseWebAssetsEnv+", else release-web/ beside the ao binary)")
	return cmd
}

func newReleaseWebStartCommand(ctx *commandContext) *cobra.Command {
	var hosts []string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the team web in the background, creating a key if there is none",
		Long: "Starts the team web in the background. --host replaces the public names it answers, " +
			"such as your Funnel name <machine>.<tailnet>.ts.net, and keeps them in the hosts file for later starts.",
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, control, err := ctx.releaseWebControl()
			if err != nil {
				return err
			}
			if len(hosts) > 0 {
				if err := releaseweb.WriteHosts(control.Paths, hosts); err != nil {
					return usageError{fmt.Errorf("--host: %w", err)}
				}
			}
			st, created, err := control.StartReportingKey(cmd.Context())
			if created {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "created a key at %s; copy-key puts it on the clipboard\n", control.Paths.KeyFile())
			}
			if err != nil {
				return err
			}
			if err := writeReleaseWebStatus(cmd.OutOrStdout(), control.Paths, st); err != nil {
				return err
			}
			if configured, _ := releaseweb.ReadHosts(control.Paths); !slices.Equal(configured, st.Hosts) {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "the running web still answers its old hosts; stop and start it to answer %s\n", hostList(configured))
			}
			return err
		},
	}
	cmd.Flags().StringArrayVar(&hosts, "host", nil, "A public host name to answer besides 127.0.0.1 and localhost, such as <machine>.<tailnet>.ts.net; repeatable, replaces the hosts file")
	return cmd
}

func newReleaseWebStopCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the team web",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, control, err := ctx.releaseWebControl()
			if err != nil {
				return err
			}
			if _, err := control.Stop(cmd.Context()); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "release web: stopped")
			return err
		},
	}
}

func newReleaseWebStatusCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the team web runs, its address, signed-in sessions and key generation",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, control, err := ctx.releaseWebControl()
			if err != nil {
				return err
			}
			st, err := control.Status(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), st)
			}
			return writeReleaseWebStatus(cmd.OutOrStdout(), control.Paths, st)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the status as JSON")
	return cmd
}

func newReleaseWebRotateKeyCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "rotate-key",
		Short: "Replace the shared key and sign everyone out",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, control, err := ctx.releaseWebControl()
			if err != nil {
				return err
			}
			st, err := control.RotateKey(cmd.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "rotated the key: generation %d\nkey file: %s\neveryone is signed out; share the new key with copy-key\n",
				st.KeyGeneration, control.Paths.KeyFile())
			return err
		},
	}
}

func newReleaseWebCopyKeyCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "copy-key",
		Short: "Put the shared key on the clipboard without printing it",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, control, err := ctx.releaseWebControl()
			if err != nil {
				return err
			}
			if err := control.CopyKey(cmd.Context()); err != nil {
				if errors.Is(err, releaseweb.ErrNoClipboard) {
					return fmt.Errorf("%w: %s", err, control.Paths.KeyFile())
				}
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "copied the key to the clipboard")
			return err
		},
	}
}

func writeReleaseWebStatus(w io.Writer, p releaseweb.Paths, st releaseweb.Status) error {
	state := "stopped"
	if st.Running {
		state = "running"
	}
	_, err := fmt.Fprintf(w, "release web: %s\n", state)
	if err != nil {
		return err
	}
	if st.Running {
		if _, err = fmt.Fprintf(w, "url: %s\npid: %d\nsessions: %d\n", st.URL, st.PID, st.Sessions); err != nil {
			return err
		}
	}
	if _, err = fmt.Fprintf(w, "hosts: %s\n", hostList(st.Hosts)); err != nil {
		return err
	}
	if st.KeyPresent {
		_, err = fmt.Fprintf(w, "key: generation %d (%s)\n", st.KeyGeneration, p.KeyFile())
	} else {
		_, err = fmt.Fprintf(w, "key: none yet; start or rotate-key creates one (%s)\n", p.KeyFile())
	}
	return err
}

func hostList(hosts []string) string {
	if len(hosts) == 0 {
		return "none; only 127.0.0.1 and localhost answer (add yours with start --host)"
	}
	return strings.Join(hosts, ", ")
}
