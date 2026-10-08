package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

func newClaudeProfileCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude-profile",
		Short: "Inspect the Claude profiles claude-code sessions launch with",
		Long: `A Claude profile is a named Claude Code settings file a claude-code session
launches with (claude --settings <file>), on top of ~/.claude/settings.json.
Subscription (no file) and OmniRoute are built in; add more in the app's
Settings. A project picks its default with ao project set-config
--claude-profile, a spawn overrides it with ao spawn --claude-profile, and a
running session switches with ao session set-claude-profile.`,
	}
	cmd.AddCommand(newClaudeProfileListCommand(ctx))
	return cmd
}

func newClaudeProfileListCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the Claude profiles, built-ins first",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var res controllers.ClaudeProfilesResponse
			if err := ctx.getJSON(cmd.Context(), "settings/claude-profiles", &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(tw, "NAME\tFILE\tBUILTIN"); err != nil {
				return err
			}
			for _, p := range res.Profiles {
				file, builtin := p.SettingsFile, "no"
				if file == "" {
					file = "-"
				}
				if p.Builtin {
					builtin = "yes"
				}
				if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, file, builtin); err != nil {
					return err
				}
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the profiles as JSON")
	return cmd
}

func newSessionSetClaudeProfileCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	var restart bool
	cmd := &cobra.Command{
		Use:   "set-claude-profile <session-id> <profile>",
		Short: "Switch a claude-code session to another Claude profile",
		Long: `Record <profile> (see ao claude-profile ls) as the Claude profile the session
launches with. Only claude-code sessions have one.

Without --restart the running agent keeps its current profile until the session
is next restarted or restored. With --restart an idle agent is restarted onto
the new profile now, resuming its conversation; an agent in the middle of a turn
is restarted once it goes idle.`,
		Example: "  ao session set-claude-profile ao-12 OmniRoute --restart",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(2)(cmd, args); err != nil {
				return usageError{err}
			}
			if _, err := normalizeSessionID(args[0]); err != nil {
				return err
			}
			if strings.TrimSpace(args[1]) == "" {
				return usageError{errors.New("profile is required")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.setSessionClaudeProfile(cmd.Context(), cmd, id, strings.TrimSpace(args[1]), restart, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to scope the lookup")
	cmd.Flags().BoolVar(&restart, "restart", false, "Restart the agent onto the profile: now when it is idle, once it is idle when it is mid-turn")
	return cmd
}

func (c *commandContext) setSessionClaudeProfile(ctx context.Context, cmd *cobra.Command, id, profile string, restart bool, opts sessionOptions) error {
	if opts.project != "" {
		if _, err := c.fetchScopedSession(ctx, id, opts.project); err != nil {
			return err
		}
	}
	var res struct {
		Session struct {
			ClaudeProfile string `json:"claudeProfile"`
		} `json:"session"`
		Restart domain.ClaudeProfileRestart `json:"restart"`
	}
	req := controllers.SetSessionClaudeProfileRequest{Profile: profile, Restart: restart}
	if err := c.putJSON(ctx, "sessions/"+url.PathEscape(id)+"/claude-profile", req, &res); err != nil {
		return err
	}
	name := res.Session.ClaudeProfile
	var line string
	switch res.Restart {
	case domain.ClaudeProfileRestarted:
		line = fmt.Sprintf("restarted %s on Claude profile %s", id, name)
	case domain.ClaudeProfileRestartPending:
		line = fmt.Sprintf("%s restarts on Claude profile %s once its agent is idle", id, name)
	default:
		line = fmt.Sprintf("%s uses Claude profile %s from its next restart", id, name)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), line)
	return err
}
