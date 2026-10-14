package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func newTrainRulesCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List the release-train rules with their on/off state",
		Long:  "Lists every rule from the Board's catalogue. A * marks a rule set away from its default; reset returns it.",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, _, err := ctx.trainBoard(cmd)
			if err != nil {
				return err
			}
			if body.Board == nil {
				return writeTrainNoBoard(cmd.OutOrStdout(), body)
			}
			return writeTrainRules(cmd.OutOrStdout(), body.Board.Catalogue)
		},
	}
	for _, verb := range []struct {
		use, short string
		enabled    *bool
	}{
		{"enable", "Turn a rule on", boolPtr(true)},
		{"disable", "Turn a rule off; it still runs, but its hits never reach the badge", boolPtr(false)},
		{"reset", "Return a rule to its default on/off", nil},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   verb.use + " <RULE>",
			Short: verb.short,
			Args:  exactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				var res controllers.ReleaseTrainRuleSettingResponse
				req := controllers.ReleaseTrainRuleSettingRequest{Enabled: verb.enabled}
				if err := ctx.putJSON(cmd.Context(), "release-trains/rules/"+url.PathEscape(strings.ToUpper(args[0])), req, &res); err != nil {
					return trainNotFoundIsUsage(err)
				}
				return writeTrainRules(cmd.OutOrStdout(), []trainRule{{
					ID: string(res.Rule.ID), Group: res.Rule.Group, Severity: res.Rule.Severity.String(),
					DefaultOn: res.Rule.DefaultOn, On: res.Rule.On, Title: res.Rule.Title,
				}})
			},
		})
	}
	return cmd
}

func boolPtr(b bool) *bool { return &b }

func writeTrainRules(w io.Writer, rules []trainRule) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RULE\tGROUP\tSEV\tON\tTITLE")
	for _, r := range rules {
		state := "off"
		if r.On {
			state = "on"
		}
		if r.On != r.DefaultOn {
			state += "*"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Group, r.Severity, state, r.Title)
	}
	return tw.Flush()
}

func newTrainHideCommand(ctx *commandContext) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "hide <HIT>",
		Short: "Hide a hit; it stays hidden while its evidence changes and comes back if its severity rises",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var res controllers.ReleaseTrainHideResponse
			if err := ctx.putJSON(cmd.Context(), "release-trains/hides/"+url.PathEscape(args[0]), controllers.ReleaseTrainHideRequest{Reason: reason}, &res); err != nil {
				return trainNotFoundIsUsage(err)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "hid %s at %s\n", res.HitID, res.Severity)
			return err
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why it is hidden; shown in the hidden list")
	return cmd
}

func newTrainUnhideCommand(ctx *commandContext) *cobra.Command {
	var stale bool
	cmd := &cobra.Command{
		Use:   "unhide <HIT> | --stale",
		Short: "Show a hidden hit again, or with --stale clear every hide whose hit no longer matches",
		Args:  rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch {
			case stale && len(args) == 0:
				var res controllers.ReleaseTrainUnhideStaleResponse
				if err := ctx.doJSON(cmd.Context(), "DELETE", "release-trains/hides?stale=true", nil, &res); err != nil {
					return err
				}
				if len(res.Removed) == 0 {
					_, err := io.WriteString(out, "no stale hides\n")
					return err
				}
				for _, id := range res.Removed {
					if _, err := fmt.Fprintf(out, "unhid %s\n", id); err != nil {
						return err
					}
				}
				return nil
			case !stale && len(args) == 1:
				if err := ctx.deleteJSON(cmd.Context(), "release-trains/hides/"+url.PathEscape(args[0]), nil); err != nil {
					return trainNotFoundIsUsage(err)
				}
				_, err := fmt.Fprintf(out, "unhid %s\n", args[0])
				return err
			}
			return usageError{errors.New("give one hit id, or --stale and no id")}
		},
	}
	cmd.Flags().BoolVar(&stale, "stale", false, "Clear every hide the Board lists as no longer matching")
	return cmd
}

func newTrainPlanCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Short: "Long-lived-branch plans: where a branch group lands"}
	var asJSON bool
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List the long-lived-branch plans",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res controllers.ReleaseTrainLlbPlansResponse
			if err := ctx.getJSON(cmd.Context(), "release-trains/llb-plans", &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeTrainPlans(cmd.OutOrStdout(), res.Plans)
		},
	}
	ls.Flags().BoolVar(&asJSON, "json", false, "Print the daemon's JSON")
	cmd.AddCommand(ls, newTrainPlanSetCommand(ctx))
	return cmd
}

func newTrainPlanSetCommand(ctx *commandContext) *cobra.Command {
	var target, name, note string
	var branches []string
	var ifVersion int
	cmd := &cobra.Command{
		Use:   "set <PLAN> --target <N|unplanned|dropped>",
		Short: "Create or replace a plan; --if-version is the version you edited (0 creates)",
		Long: "Replaces the whole plan: its target, note and every branch. " +
			"A stale --if-version or a branch already in another plan is refused.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := controllers.ReleaseTrainLlbPlanInput{Name: name, Note: note, IfVersion: ifVersion}
			if in.Name == "" {
				in.Name = args[0]
			}
			in.Via = "cli"
			in.Session = strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
			switch target {
			case "":
				return usageError{errors.New("--target is required: a train number such as 22, unplanned or dropped")}
			case string(rt.TargetUnplanned), string(rt.TargetDropped):
				in.TargetKind = rt.TargetKind(target)
			default:
				key, err := trainKeyArg(target, ctx.deps.Now().Year())
				if err != nil {
					return err
				}
				in.TargetKind, in.TargetTrain = rt.TargetTrain, key
			}
			for _, b := range branches {
				app, branch, ok := strings.Cut(b, ":")
				if !ok || app == "" || branch == "" {
					return usageError{fmt.Errorf("--branch %q is not app:branch", b)}
				}
				in.Branches = append(in.Branches, rt.PlanBranch{App: rt.AppID(app), Branch: branch})
			}
			var res controllers.ReleaseTrainLlbPlanResponse
			err := ctx.putJSON(cmd.Context(), "release-trains/llb-plans/"+url.PathEscape(args[0]), in, &res)
			var apiErr apiResponseError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
				return usageError{err}
			}
			if err != nil {
				return err
			}
			return writeTrainPlans(cmd.OutOrStdout(), []rt.LlbPlan{res.Plan})
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "A train number (22, 20-1, or a key 22-2026), unplanned or dropped")
	cmd.Flags().StringVar(&name, "name", "", "A display name; the plan id when absent")
	cmd.Flags().StringVar(&note, "note", "", "A note for the plan")
	cmd.Flags().StringArrayVar(&branches, "branch", nil, "app:branch, repeatable")
	cmd.Flags().IntVar(&ifVersion, "if-version", 0, "The plan version this edit replaces; 0 creates a new plan")
	return cmd
}

var trainNumberRE = regexp.MustCompile(`^#?(\d+)(-\d+)?$`)

func trainKeyArg(s string, year int) (string, error) {
	if _, ok := rt.ParseTrainKey(s); ok {
		return s, nil
	}
	m := trainNumberRE.FindStringSubmatch(s)
	if m == nil {
		return "", usageError{fmt.Errorf("--target %q is not a train number (22, 20-1), unplanned or dropped", s)}
	}
	return fmt.Sprintf("%s%s-%d", m[1], m[2], year), nil
}

func writeTrainPlans(w io.Writer, plans []rt.LlbPlan) error {
	if len(plans) == 0 {
		_, err := io.WriteString(w, "no long-lived branch plans\n")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PLAN\tVERSION\tTARGET\tBRANCHES\tUPDATED BY\tNOTE")
	for _, p := range plans {
		target := string(p.TargetKind)
		if p.TargetTrain != nil {
			target = p.TargetTrain.String()
		}
		bs := make([]string, len(p.Branches))
		for i, b := range p.Branches {
			bs[i] = string(b.App) + ":" + b.Branch
		}
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n", p.ID, p.Version, target, orDash(strings.Join(bs, ", ")), p.UpdatedBy, orDash(p.Note))
	}
	return tw.Flush()
}

func trainNotFoundIsUsage(err error) error {
	var apiErr apiResponseError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusBadRequest) {
		return usageError{err}
	}
	return err
}
