package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func cliDecisionBy() controllers.LearningDecisionBy {
	return controllers.LearningDecisionBy{Via: "cli", Session: strings.TrimSpace(os.Getenv("AO_SESSION_ID"))}
}

func newTrainFixCommand(ctx *commandContext) *cobra.Command {
	var choice string
	var confirm bool
	cmd := &cobra.Command{
		Use:   "fix <HIT> [--choice ..] [--confirm]",
		Short: "Show a hit's fix and the exact request each step would send; --confirm records it",
		Long: "Without --confirm this only plans: it shows each step and its request and writes nothing. " +
			"--confirm records the fix. L3 applies to AO's own long-lived plan; every other fix is a dry run " +
			"that is recorded and never sent to Jira, GitLab or Confluence.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res controllers.ReleaseTrainFixPlanResponse
			req := controllers.ReleaseTrainFixPlanRequest{HitID: rt.HitID(args[0]), Choice: choice}
			if err := ctx.postJSON(cmd.Context(), "release-trains/fixes/plan", req, &res); err != nil {
				return trainNotFoundIsUsage(err)
			}
			out := cmd.OutOrStdout()
			if err := writeFixPlan(out, res.Plan); err != nil || !confirm {
				return err
			}
			if !res.Plan.Recordable() {
				return usageError{fmt.Errorf("a %s plan has nothing to confirm%s", res.Plan.Mode, chooseHint(res.Plan))}
			}
			var fix controllers.ReleaseTrainFixResponse
			body := controllers.ReleaseTrainFixConfirmRequest{LearningDecisionBy: cliDecisionBy(), HitID: req.HitID, Choice: choice, PlanFingerprint: res.Plan.Fingerprint}
			err := ctx.postJSON(cmd.Context(), "release-trains/fixes", body, &fix)
			var apiErr apiResponseError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
				return fmt.Errorf("the data changed while planning; run the command again to review the new plan: %w", err)
			}
			if err != nil {
				return trainNotFoundIsUsage(err)
			}
			_, err = fmt.Fprintf(out, "\n%s %s: %s\n", fix.Fix.ID, fixStateText(fix.Fix.State), fix.Fix.Plan.Title)
			return err
		},
	}
	cmd.Flags().StringVar(&choice, "choice", "", "For A5: which side is right, jira or gitlab")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Record the fix (L3 applies; every other fix is a dry run)")
	cmd.AddCommand(newTrainFixUndoCommand(ctx))
	return cmd
}

func chooseHint(p rt.FixPlan) string {
	if p.Mode != rt.ModeChoose {
		return ""
	}
	ids := make([]string, len(p.Choices))
	for i, c := range p.Choices {
		ids[i] = c.ID
	}
	return "; pass --choice " + strings.Join(ids, " or ")
}

func newTrainFixUndoCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "undo <FIX>",
		Short: "Withdraw a dry-run fix, or undo an applied L3 fix",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res controllers.ReleaseTrainFixResponse
			body := controllers.ReleaseTrainFixUndoRequest{LearningDecisionBy: cliDecisionBy()}
			if err := ctx.postJSON(cmd.Context(), "release-trains/fixes/"+url.PathEscape(args[0])+"/undo", body, &res); err != nil {
				return trainNotFoundIsUsage(err)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", res.Fix.ID, fixStateText(res.Fix.State), res.Fix.Plan.Title)
			return err
		},
	}
}

func newTrainFixesCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "fixes",
		Short: "List the recorded fixes and their states",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res controllers.ReleaseTrainFixesResponse
			if err := ctx.getJSON(cmd.Context(), "release-trains/fixes", &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeFixes(cmd.OutOrStdout(), res.Fixes)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the daemon's JSON")
	return cmd
}

func fixStateText(s rt.FixState) string {
	if s == rt.FixDryRun {
		return "recorded (dry run, nothing sent)"
	}
	return string(s)
}

func writeFixPlan(w io.Writer, p rt.FixPlan) error {
	_, _ = fmt.Fprintf(w, "%s  %s  [%s]\n", p.Rule, p.Title, p.Mode)
	if p.Note != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", p.Note)
	}
	if p.Need != nil {
		_, _ = fmt.Fprintf(w, "  %s: ao train plan set %s --target <N>\n", p.Need.Label, p.Need.LLB)
	}
	for _, c := range p.Choices {
		mark := " "
		if c.ID == p.Choice {
			mark = "*"
		}
		_, _ = fmt.Fprintf(w, " %s --choice %s  %s\n", mark, c.ID, c.Label)
	}
	for _, s := range p.Steps {
		_, _ = fmt.Fprintf(w, "\n  %s\n    %s -> %s\n    %s %s\n", s.Label, s.Before, s.After, s.Request.Method, s.Request.URL)
		if len(s.Request.Body) > 0 {
			_, _ = fmt.Fprintf(w, "    %s\n", s.Request.Body)
		}
	}
	if p.Recordable() && p.Mode != rt.ModeApply {
		_, _ = io.WriteString(w, "\n  dry run: nothing is sent; confirming records this request.\n")
	}
	return nil
}

func writeFixes(w io.Writer, fixes []rt.Fix) error {
	if len(fixes) == 0 {
		_, err := io.WriteString(w, "no fixes\n")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "FIX\tRULE\tSTATE\tBY\tUPDATED\tTITLE")
	for _, f := range fixes {
		by := ""
		if len(f.Events) > 0 {
			by = f.Events[0].Actor
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", f.ID, f.Rule, f.State, orDash(by), f.UpdatedAt.Format("2006-01-02 15:04"), f.Plan.Title)
	}
	return tw.Flush()
}
