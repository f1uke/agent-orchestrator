package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type learnRuleDTO struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Quote       string   `json:"quote"`
	Tags        []string `json:"tags"`
	Heading     string   `json:"heading,omitempty"`
	SourceKey   string   `json:"sourceKey"`
	SourceLabel string   `json:"sourceLabel"`
	SourceKind  string   `json:"sourceKind"`
	Scope       string   `json:"scope"`
	ProjectID   string   `json:"projectId,omitempty"`
	Score       float64  `json:"score,omitempty"`
}

type learnRulesResponse struct {
	Rules []learnRuleDTO `json:"rules"`
}

type learnRuleSourceDTO struct {
	Key         string    `json:"key"`
	Scope       string    `json:"scope"`
	ProjectID   string    `json:"projectId,omitempty"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	Chunks      int       `json:"chunks"`
	Rules       int       `json:"rules"`
	RefreshedAt time.Time `json:"refreshedAt"`
	Error       string    `json:"error,omitempty"`
}

type learnRulesRefreshDTO struct {
	Running    bool       `json:"running"`
	Manual     bool       `json:"manual"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Sources    int        `json:"sources"`
	Atomized   int        `json:"atomized"`
	Failed     int        `json:"failed"`
	CostUSD    float64    `json:"costUsd"`
	BudgetUSD  float64    `json:"budgetUsd"`
	StopReason string     `json:"stopReason,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
}

type learnRuleSourcesResponse struct {
	Sources []learnRuleSourceDTO `json:"sources"`
	Refresh learnRulesRefreshDTO `json:"refresh"`
}

type learnProtectedRuleDTO struct {
	ID        int64     `json:"id"`
	ProjectID string    `json:"projectId,omitempty"`
	Text      string    `json:"text"`
	Patterns  []string  `json:"patterns"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type learnProtectedRulesResponse struct {
	Rules []learnProtectedRuleDTO `json:"rules"`
}

type learnProtectRequest struct {
	Project  string   `json:"project,omitempty"`
	Text     string   `json:"text,omitempty"`
	From     string   `json:"from,omitempty"`
	Patterns []string `json:"patterns,omitempty"`
	Note     string   `json:"note,omitempty"`
}

type learnCheckRequest struct {
	Project string `json:"project,omitempty"`
	Text    string `json:"text"`
}

type learnForbiddenHitDTO struct {
	RuleID  int64  `json:"ruleId"`
	Rule    string `json:"rule"`
	Pattern string `json:"pattern"`
	Match   string `json:"match"`
}

type learnCheckResponse struct {
	Hits []learnForbiddenHitDTO `json:"hits"`
}

// sessionProject is --project, or the current session's project.
func sessionProject(project string) string {
	if project == "" {
		return strings.TrimSpace(os.Getenv("AO_PROJECT_ID"))
	}
	return project
}

func newLearnRulesCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		search  string
		limit   int
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List or search the standing rules agents are already told",
		Long: "The standing-rules corpus is what learning checks a lesson against: the human's CLAUDE.md and skills, each\n" +
			"learning project's repo CLAUDE.md, AGENTS.md and skills, AO's own standing prompt and the knowledge INDEX,\n" +
			"split into statements. Without --project only the global rules are listed.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if p := sessionProject(project); p != "" {
				q.Set("project", p)
			}
			if search != "" {
				q.Set("q", search)
			}
			q.Set("limit", strconv.Itoa(limit))
			var res learnRulesResponse
			if err := ctx.getJSON(cmd.Context(), "learning/rules?"+q.Encode(), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeLearnRules(cmd.OutOrStdout(), res.Rules, search != "")
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: the current session's project; none lists global rules only)")
	cmd.Flags().StringVar(&search, "search", "", "Search the rules instead of listing them")
	cmd.Flags().IntVar(&limit, "limit", 20, "Most rules to show")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.AddCommand(newLearnRulesSourcesCommand(ctx))
	cmd.AddCommand(newLearnRulesRefreshCommand(ctx))
	cmd.AddCommand(newLearnRulesProtectCommand(ctx))
	cmd.AddCommand(newLearnRulesProtectedCommand(ctx))
	cmd.AddCommand(newLearnRulesUnprotectCommand(ctx))
	cmd.AddCommand(newLearnRulesCheckCommand(ctx))
	return cmd
}

func writeLearnRules(w io.Writer, rules []learnRuleDTO, scored bool) error {
	if len(rules) == 0 {
		_, err := fmt.Fprintln(w, "No rules. The corpus fills in once a project learns from sessions; see `ao learn rules sources`.")
		return err
	}
	var b strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&b, "%s  %s", r.ID, r.SourceLabel)
		if r.Heading != "" {
			fmt.Fprintf(&b, " > %s", r.Heading)
		}
		if scored {
			fmt.Fprintf(&b, "  score %.2f", r.Score)
		}
		fmt.Fprintf(&b, "\n  %s\n", r.Text)
		if len(r.Tags) > 0 {
			fmt.Fprintf(&b, "  tags: %s\n", strings.Join(r.Tags, ", "))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func newLearnRulesSourcesCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "List the files and prompts the rules corpus is built from",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if project != "" {
				q.Set("project", project)
			}
			var res learnRuleSourcesResponse
			if err := ctx.getJSON(cmd.Context(), "learning/rules/sources?"+q.Encode(), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeLearnRuleSources(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: every source)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func writeLearnRuleSources(w io.Writer, res learnRuleSourcesResponse) error {
	var table strings.Builder
	table.WriteString("SOURCE\tKIND\tSCOPE\tCHUNKS\tRULES\tERROR\n")
	for _, s := range res.Sources {
		scope := s.Scope
		if s.ProjectID != "" {
			scope = s.ProjectID
		}
		fmt.Fprintf(&table, "%s\t%s\t%s\t%d\t%d\t%s\n", s.Label, s.Kind, scope, s.Chunks, s.Rules, s.Error)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := io.WriteString(tw, table.String()); err != nil {
		return err
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	r := res.Refresh
	state := "idle"
	if r.Running {
		state = "running"
	}
	line := fmt.Sprintf("\nrefresh %s: %d sources, %d chunks atomized, %d failed, $%.3f of $%.2f", state, r.Sources, r.Atomized, r.Failed, r.CostUSD, r.BudgetUSD)
	if r.FinishedAt != nil {
		line += ", finished " + r.FinishedAt.Local().Format("2006-01-02 15:04")
	}
	if r.StopReason != "" {
		line += " (" + r.StopReason + ")"
	}
	if r.LastError != "" {
		line += "\nlast error: " + r.LastError
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

func newLearnRulesRefreshCommand(ctx *commandContext) *cobra.Command {
	var budget float64
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the rules corpus now, under its own budget",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ctx.postJSON(cmd.Context(), "learning/rules/refresh", map[string]float64{"budgetUsd": budget}, nil); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Refresh started (budget $%.2f). Follow it with `ao learn rules sources`.\n", budget)
			return err
		},
	}
	cmd.Flags().Float64Var(&budget, "budget", 1, "Most this refresh may spend on model runs, in dollars at API prices")
	return cmd
}

func newLearnRulesProtectCommand(ctx *commandContext) *cobra.Command {
	var (
		project  string
		text     string
		from     string
		patterns []string
		note     string
	)
	cmd := &cobra.Command{
		Use:   "protect",
		Short: "Pin a rule as protected, optionally with patterns a learned skill must never contain",
		Long: "A protected rule is always checked against every proposal, and a learned skill that matches one of its\n" +
			"patterns (RE2, case-insensitive) is blocked. Without --project it applies in every project.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (text == "") == (from == "") {
				return usageError{fmt.Errorf("give exactly one of --text or --from")}
			}
			var res learnProtectedRuleDTO
			if err := ctx.postJSON(cmd.Context(), "learning/protected-rules", learnProtectRequest{
				Project: project, Text: text, From: from, Patterns: patterns, Note: note,
			}, &res); err != nil {
				return err
			}
			return writeLearnProtected(cmd.OutOrStdout(), []learnProtectedRuleDTO{res})
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project the rule applies in (default: every project)")
	cmd.Flags().StringVar(&text, "text", "", "The rule")
	cmd.Flags().StringVar(&from, "from", "", "Copy the text of a corpus rule, by id (see `ao learn rules`)")
	cmd.Flags().StringArrayVar(&patterns, "pattern", nil, "Forbidden pattern (repeatable)")
	cmd.Flags().StringVar(&note, "note", "", "Why the rule is protected")
	return cmd
}

func newLearnRulesProtectedCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "protected",
		Short: "List the protected rules",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if project != "" {
				q.Set("project", project)
			}
			var res learnProtectedRulesResponse
			if err := ctx.getJSON(cmd.Context(), "learning/protected-rules?"+q.Encode(), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeLearnProtected(cmd.OutOrStdout(), res.Rules)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func writeLearnProtected(w io.Writer, rules []learnProtectedRuleDTO) error {
	if len(rules) == 0 {
		_, err := fmt.Fprintln(w, "No protected rules.")
		return err
	}
	var b strings.Builder
	for _, r := range rules {
		scope := "every project"
		if r.ProjectID != "" {
			scope = r.ProjectID
		}
		fmt.Fprintf(&b, "#%d  %s\n  %s\n", r.ID, scope, r.Text)
		for _, p := range r.Patterns {
			fmt.Fprintf(&b, "  forbids: %s\n", p)
		}
		if r.Note != "" {
			fmt.Fprintf(&b, "  note: %s\n", r.Note)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func newLearnRulesUnprotectCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "unprotect <id>",
		Short: "Unpin a protected rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
			if err != nil || id <= 0 {
				return usageError{fmt.Errorf("id must be a positive number, got %q", args[0])}
			}
			if err := ctx.deleteJSON(cmd.Context(), "learning/protected-rules/"+strconv.FormatInt(id, 10), nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Unprotected #%d.\n", id)
			return err
		},
	}
}

func newLearnRulesCheckCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		text    string
		file    string
	)
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Report every forbidden pattern a text or file matches; exits 1 when one does",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (text == "") == (file == "") {
				return usageError{fmt.Errorf("give exactly one of --text or --file")}
			}
			if file != "" {
				b, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				text = string(b)
			}
			var res learnCheckResponse
			if err := ctx.postJSON(cmd.Context(), "learning/protected-rules/check", learnCheckRequest{Project: sessionProject(project), Text: text}, &res); err != nil {
				return err
			}
			if len(res.Hits) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No forbidden pattern matches.")
				return err
			}
			var b strings.Builder
			for _, h := range res.Hits {
				fmt.Fprintf(&b, "#%d %s\n  pattern %s matched %q\n", h.RuleID, h.Rule, h.Pattern, h.Match)
			}
			if _, err := io.WriteString(cmd.OutOrStdout(), b.String()); err != nil {
				return err
			}
			return fmt.Errorf("%d forbidden pattern match(es)", len(res.Hits))
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project whose rules apply besides the global ones (default: the current session's project)")
	cmd.Flags().StringVar(&text, "text", "", "Text to check")
	cmd.Flags().StringVar(&file, "file", "", "File to check")
	return cmd
}
