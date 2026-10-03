package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// The `ao learn` commands are a window onto learning capture: what AO has kept
// from projects that learn from sessions, whether capture is healthy, and the
// one way to delete what was kept. Turning learning on or off is `ao project
// set-config --learn-from-sessions`.

type learnWindowDTO struct {
	AgentText string   `json:"agentText,omitempty"`
	Actions   []string `json:"actions"`
}

type learnExcerptDTO struct {
	ID          int64          `json:"id"`
	ProjectID   string         `json:"projectId"`
	SessionID   string         `json:"sessionId"`
	TurnAt      time.Time      `json:"turnAt"`
	SourceClass string         `json:"sourceClass"`
	GitBranch   string         `json:"gitBranch,omitempty"`
	Before      learnWindowDTO `json:"before"`
	HumanText   string         `json:"humanText"`
	After       learnWindowDTO `json:"after"`
	Redactions  map[string]int `json:"redactions"`
}

type learnExcerptsResponse struct {
	Excerpts []learnExcerptDTO `json:"excerpts"`
}

type learnFailingFileDTO struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type learnProjectStatusDTO struct {
	ProjectID         string                `json:"projectId"`
	Enabled           bool                  `json:"enabled"`
	Transcripts       int                   `json:"transcripts"`
	Excerpts          int                   `json:"excerpts"`
	BySourceClass     map[string]int        `json:"bySourceClass"`
	HumanTurns        int                   `json:"humanTurns"`
	MachineTurns      int                   `json:"machineTurns"`
	Prompts           int                   `json:"prompts"`
	UnmatchedPrompts  int                   `json:"unmatchedPrompts"`
	LastCaptureAt     *time.Time            `json:"lastCaptureAt,omitempty"`
	FailingFiles      []learnFailingFileDTO `json:"failingFiles"`
	AtRiskTranscripts int                   `json:"atRiskTranscripts"`
	Uncollected       int                   `json:"uncollected"`
	Drafts            map[string]int        `json:"drafts"`
	LastCollectAt     *time.Time            `json:"lastCollectAt,omitempty"`
	LastCollectError  string                `json:"lastCollectError,omitempty"`
	LastCollectStderr string                `json:"lastCollectStderr,omitempty"`
}

type learnCollectRunDTO struct {
	Running    bool       `json:"running"`
	Manual     bool       `json:"manual"`
	Project    string     `json:"project,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Jobs       int        `json:"jobs"`
	Failed     int        `json:"failed"`
	Drafts     int        `json:"drafts"`
	CostUSD    float64    `json:"costUsd"`
	BudgetUSD  float64    `json:"budgetUsd"`
	StopReason string     `json:"stopReason,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
}

type learnCollectStatusDTO struct {
	Enabled        bool               `json:"enabled"`
	TodaySpendUSD  float64            `json:"todaySpendUsd"`
	DailyBudgetUSD float64            `json:"dailyBudgetUsd"`
	Model          string             `json:"model"`
	Effort         string             `json:"effort"`
	Run            learnCollectRunDTO `json:"run"`
}

type learnStatusResponse struct {
	Projects []learnProjectStatusDTO `json:"projects"`
	Collect  learnCollectStatusDTO   `json:"collect"`
}

func newLearnCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "learn",
		Short: "See what AO kept from projects that learn from sessions",
		Long: "Learning capture keeps redacted excerpts of what you type to the sessions of projects " +
			"with learnFromSessions on (`ao project set-config <id> --learn-from-sessions`), so AO can " +
			"later propose skills from what you taught. `status` and `excerpts` read what was kept; `forget` deletes it.",
	}
	cmd.AddCommand(newLearnStatusCommand(ctx))
	cmd.AddCommand(newLearnExcerptsCommand(ctx))
	cmd.AddCommand(newLearnForgetCommand(ctx))
	cmd.AddCommand(newLearnCollectCommand(ctx))
	cmd.AddCommand(newLearnDraftsCommand(ctx))
	cmd.AddCommand(newLearnSettingsCommand(ctx))
	return cmd
}

func newLearnStatusCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show, per project, what capture kept and whether it is healthy",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res learnStatusResponse
			if err := ctx.getJSON(cmd.Context(), "learning/status", &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeLearnStatus(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func writeLearnStatus(out io.Writer, res learnStatusResponse) error {
	if len(res.Projects) == 0 {
		_, err := fmt.Fprintln(out, "No project learns from sessions. Turn it on with `ao project set-config <id> --learn-from-sessions`.")
		return err
	}
	var b strings.Builder
	for i, p := range res.Projects {
		if i > 0 {
			b.WriteString("\n")
		}
		state := "on"
		if !p.Enabled {
			state = "off (keeping what was captured)"
		}
		fmt.Fprintf(&b, "%s: learning %s\n", p.ProjectID, state)
		fmt.Fprintf(&b, "  transcripts tracked  %d\n", p.Transcripts)
		fmt.Fprintf(&b, "  your turns kept      %d%s\n", p.Excerpts, sourceBreakdown(p.BySourceClass))
		fmt.Fprintf(&b, "  turns read           %d yours, %d not yours (AO notices, other sessions, briefs)\n", p.HumanTurns, p.MachineTurns)
		last := "never"
		if p.LastCaptureAt != nil {
			last = p.LastCaptureAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&b, "  last capture         %s\n", last)
		fmt.Fprintf(&b, "  prompts seen         %d (%d not found in a transcript)\n", p.Prompts, p.UnmatchedPrompts)
		if p.UnmatchedPrompts > 0 {
			b.WriteString("  ! some prompts the hooks saw never turned up in a transcript - Claude Code's transcript format may have changed\n")
		}
		if p.AtRiskTranscripts > 0 {
			fmt.Fprintf(&b, "  ! %d transcript(s) with unread turns are 25+ days old; Claude Code deletes them at 30\n", p.AtRiskTranscripts)
		}
		for _, f := range p.FailingFiles {
			fmt.Fprintf(&b, "  ! failed: %s: %s\n", f.Path, f.Error)
		}
		if res.Collect.Enabled {
			fmt.Fprintf(&b, "  waiting for a model   %d turn(s)\n", p.Uncollected)
			fmt.Fprintf(&b, "  drafts               %d open%s\n", p.Drafts["open"], otherDrafts(p.Drafts))
			if p.LastCollectError != "" {
				fmt.Fprintf(&b, "  ! last model run failed: %s\n", p.LastCollectError)
				if p.LastCollectStderr != "" {
					fmt.Fprintf(&b, "    stderr: %s\n", strings.ReplaceAll(p.LastCollectStderr, "\n", " | "))
				}
			}
		}
	}
	if c := res.Collect; c.Enabled {
		fmt.Fprintf(&b, "\ncollect: %s (%s), today $%.2f of $%.2f\n", c.Model, c.Effort, c.TodaySpendUSD, c.DailyBudgetUSD)
		r := c.Run
		switch {
		case r.Running:
			fmt.Fprintf(&b, "  running %s: %d run(s), %d draft(s), $%.2f of $%.2f\n", runKind(r), r.Jobs, r.Drafts, r.CostUSD, r.BudgetUSD)
		case r.FinishedAt != nil:
			fmt.Fprintf(&b, "  last %s run %s: %d run(s), %d failed, %d draft(s), $%.2f - %s\n", runKind(r),
				r.FinishedAt.Local().Format("2006-01-02 15:04"), r.Jobs, r.Failed, r.Drafts, r.CostUSD, r.StopReason)
		}
		if r.LastError != "" {
			fmt.Fprintf(&b, "  ! %s\n", r.LastError)
		}
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func runKind(r learnCollectRunDTO) string {
	if r.Manual {
		return "manual"
	}
	return "background"
}

func otherDrafts(by map[string]int) string {
	var parts []string
	for _, k := range []string{"reversed", "consumed", "dropped"} {
		if by[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", by[k], k))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func sourceBreakdown(by map[string]int) string {
	if len(by) == 0 {
		return ""
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, by[k]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func newLearnExcerptsCommand(ctx *commandContext) *cobra.Command {
	var (
		project     string
		limit       int
		asJSON      bool
		withContext bool
	)
	cmd := &cobra.Command{
		Use:   "excerpts",
		Short: "List the turns of yours AO kept for a project, newest first",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.listLearnExcerpts(cmd.Context(), cmd.OutOrStdout(), project, limit, asJSON, withContext)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: the current session's project)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Most turns to show")
	cmd.Flags().BoolVar(&withContext, "context", false, "Also show what the agent said and did around each turn")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) listLearnExcerpts(ctx context.Context, out io.Writer, project string, limit int, asJSON, withContext bool) error {
	if project == "" {
		project = strings.TrimSpace(os.Getenv("AO_PROJECT_ID"))
	}
	if project == "" {
		return usageError{fmt.Errorf("--project is required outside a session")}
	}
	q := url.Values{}
	q.Set("project", project)
	q.Set("limit", strconv.Itoa(limit))
	var res learnExcerptsResponse
	if err := c.getJSON(ctx, "learning/excerpts?"+q.Encode(), &res); err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, res)
	}
	if len(res.Excerpts) == 0 {
		_, err := fmt.Fprintf(out, "Nothing kept for %s yet.\n", project)
		return err
	}
	var b strings.Builder
	for _, e := range res.Excerpts {
		fmt.Fprintf(&b, "%s  @%s  %s\n", e.TurnAt.Local().Format("2006-01-02 15:04"), e.SessionID, e.SourceClass)
		for _, line := range strings.Split(e.HumanText, "\n") {
			fmt.Fprintf(&b, "  > %s\n", line)
		}
		if withContext {
			writeLearnWindow(&b, "before", e.Before)
			writeLearnWindow(&b, "after", e.After)
		}
		if n := redactionTotal(e.Redactions); n > 0 {
			fmt.Fprintf(&b, "  (%d value(s) redacted)\n", n)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func writeLearnWindow(b *strings.Builder, label string, w learnWindowDTO) {
	if len(w.Actions) > 0 {
		fmt.Fprintf(b, "  %s: %s\n", label, strings.Join(w.Actions, " · "))
	}
	if w.AgentText != "" {
		text := strings.ReplaceAll(w.AgentText, "\n", " ")
		fmt.Fprintf(b, "  %s said: %s\n", label, text)
	}
}

func redactionTotal(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

type forgetLearningResponse struct {
	DeletedTurns int `json:"deletedTurns"`
}

func newLearnForgetCommand(ctx *commandContext) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "forget",
		Short: "Delete everything AO kept for a project (turn learning off first)",
		Long: "Deletes every captured turn, capture cursor and fingerprint AO kept for the project. " +
			"The project must have learnFromSessions off, or capture would read every transcript again on its next pass.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(project) == "" {
				return usageError{fmt.Errorf("--project is required")}
			}
			var res forgetLearningResponse
			if err := ctx.deleteJSON(cmd.Context(), "learning/projects/"+url.PathEscape(project), &res); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Forgot %d kept turn(s) for %s.\n", res.DeletedTurns, project)
			return err
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id")
	return cmd
}

type startCollectRequest struct {
	Project   string  `json:"project,omitempty"`
	BudgetUSD float64 `json:"budgetUsd"`
}

func newLearnCollectCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		budget  float64
		wait    bool
	)
	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Ask the model now about every turn no model has read yet (the backlog)",
		Long: "Starts a collect run over every captured turn no model has read yet, for one project or all " +
			"that learn from sessions, spending at most --budget (API prices, as the CLI reports them). " +
			"The background loop only collects within the daily budget; this is how a backlog is done at once.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ctx.postJSON(cmd.Context(), "learning/collect", startCollectRequest{Project: project, BudgetUSD: budget}, nil); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !wait {
				_, err := fmt.Fprintln(out, "Collect run started. Follow it with `ao learn status`.")
				return err
			}
			for {
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(3 * time.Second):
				}
				var res learnStatusResponse
				if err := ctx.getJSON(cmd.Context(), "learning/status", &res); err != nil {
					return err
				}
				r := res.Collect.Run
				if !r.Running {
					_, err := fmt.Fprintf(out, "Done: %d run(s), %d failed, %d draft(s), $%.2f - %s\n", r.Jobs, r.Failed, r.Drafts, r.CostUSD, r.StopReason)
					return err
				}
				if _, err := fmt.Fprintf(out, "  %d run(s), %d draft(s), $%.2f of $%.2f\n", r.Jobs, r.Drafts, r.CostUSD, r.BudgetUSD); err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: every project that learns from sessions)")
	cmd.Flags().Float64Var(&budget, "budget", 1, "Most this run may spend, in dollars at API prices (at most 50)")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the run to finish, printing progress")
	return cmd
}

type learnDraftDTO struct {
	ID                int64      `json:"id"`
	SessionID         string     `json:"sessionId"`
	Kind              string     `json:"kind"`
	Statement         string     `json:"statement"`
	AppliesWhen       string     `json:"appliesWhen,omitempty"`
	ScopeHint         string     `json:"scopeHint,omitempty"`
	Confidence        float64    `json:"confidence"`
	About             string     `json:"about,omitempty"`
	Quote             string     `json:"quote"`
	AnchorSourceClass string     `json:"anchorSourceClass,omitempty"`
	AnchorTurnAt      *time.Time `json:"anchorTurnAt,omitempty"`
	AgentBefore       string     `json:"agentBefore,omitempty"`
	Weak              bool       `json:"weak"`
	SupersedesID      int64      `json:"supersedesId,omitempty"`
	Status            string     `json:"status"`
}

type learnDraftsResponse struct {
	Drafts []learnDraftDTO `json:"drafts"`
}

func newLearnDraftsCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		limit   int
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "drafts",
		Short: "List the candidate lessons the model found for a project, newest first",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if project == "" {
				project = strings.TrimSpace(os.Getenv("AO_PROJECT_ID"))
			}
			if project == "" {
				return usageError{fmt.Errorf("--project is required outside a session")}
			}
			q := url.Values{}
			q.Set("project", project)
			q.Set("limit", strconv.Itoa(limit))
			var res learnDraftsResponse
			if err := ctx.getJSON(cmd.Context(), "learning/drafts?"+q.Encode(), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeLearnDrafts(cmd.OutOrStdout(), project, res.Drafts)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: the current session's project)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Most drafts to show")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func writeLearnDrafts(out io.Writer, project string, drafts []learnDraftDTO) error {
	if len(drafts) == 0 {
		_, err := fmt.Fprintf(out, "No drafts for %s yet.\n", project)
		return err
	}
	var b strings.Builder
	for _, d := range drafts {
		flags := ""
		if d.Weak {
			flags += " weak"
		}
		if d.Status != "open" {
			flags += " " + d.Status
		}
		fmt.Fprintf(&b, "#%d %s%s  @%s  conf %.2f  %s  %s\n", d.ID, d.Kind, flags, d.SessionID, d.Confidence, d.ScopeHint, d.About)
		fmt.Fprintf(&b, "  %s\n", d.Statement)
		if d.AppliesWhen != "" {
			fmt.Fprintf(&b, "  when: %s\n", d.AppliesWhen)
		}
		fmt.Fprintf(&b, "  > %s\n\n", strings.ReplaceAll(d.Quote, "\n", " "))
	}
	_, err := io.WriteString(out, b.String())
	return err
}

type learnSettingsDTO struct {
	CollectModel   string  `json:"collectModel"`
	CollectEffort  string  `json:"collectEffort"`
	DailyBudgetUSD float64 `json:"dailyBudgetUsd"`
}

func newLearnSettingsCommand(ctx *commandContext) *cobra.Command {
	var (
		model  string
		effort string
		budget float64
	)
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Show or change the collect model, effort and daily budget",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var cur learnSettingsDTO
			if err := ctx.getJSON(cmd.Context(), "learning/settings", &cur); err != nil {
				return err
			}
			flags := cmd.Flags()
			if flags.Changed("model") || flags.Changed("effort") || flags.Changed("daily-budget") {
				if flags.Changed("model") {
					cur.CollectModel = model
				}
				if flags.Changed("effort") {
					cur.CollectEffort = effort
				}
				if flags.Changed("daily-budget") {
					cur.DailyBudgetUSD = budget
				}
				if err := ctx.putJSON(cmd.Context(), "learning/settings", cur, &cur); err != nil {
					return err
				}
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "collect model   %s\ncollect effort  %s\ndaily budget    $%.2f\n", cur.CollectModel, cur.CollectEffort, cur.DailyBudgetUSD)
			return err
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Collect model id")
	cmd.Flags().StringVar(&effort, "effort", "", "Collect effort: low, medium, high, xhigh or max")
	cmd.Flags().Float64Var(&budget, "daily-budget", 0, "Daily budget for the background collect, in dollars at API prices (0 pauses it)")
	return cmd
}
