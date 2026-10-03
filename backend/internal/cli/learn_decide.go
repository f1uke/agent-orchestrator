package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type learnProposalDTO struct {
	ID           int64     `json:"id"`
	ProjectID    string    `json:"projectId"`
	TaskKey      string    `json:"taskKey"`
	Action       string    `json:"action"`
	TargetPath   string    `json:"targetPath"`
	Scope        string    `json:"scope"`
	Title        string    `json:"title"`
	Rationale    string    `json:"rationale"`
	NewContent   string    `json:"newContent"`
	Diff         string    `json:"diff"`
	Confidence   float64   `json:"confidence"`
	Outcome      string    `json:"outcome"`
	Status       string    `json:"status"`
	DropReason   string    `json:"dropReason,omitempty"`
	EvidenceIDs  []int64   `json:"evidenceIds"`
	CreatedAt    time.Time `json:"createdAt"`
	RuleVerdicts []struct {
		RuleID  string `json:"ruleId"`
		Verdict string `json:"verdict"`
		Note    string `json:"note,omitempty"`
	} `json:"ruleVerdicts"`
	Verifier struct {
		ContradictsRule bool   `json:"contradictsRule"`
		Grounded        bool   `json:"grounded"`
		SensitiveData   bool   `json:"sensitiveData"`
		Notes           string `json:"notes,omitempty"`
	} `json:"verifier"`
}

type learnDecideRunDTO struct {
	Running    bool       `json:"running"`
	Manual     bool       `json:"manual"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Tasks      int        `json:"tasks"`
	Failed     int        `json:"failed"`
	Proposals  int        `json:"proposals"`
	Dropped    int        `json:"dropped"`
	CostUSD    float64    `json:"costUsd"`
	BudgetUSD  float64    `json:"budgetUsd"`
	StopReason string     `json:"stopReason,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
}

type learnProposalsResponse struct {
	Proposals []learnProposalDTO `json:"proposals"`
	Run       learnDecideRunDTO  `json:"run"`
}

type learnProposalResponse struct {
	Proposal learnProposalDTO `json:"proposal"`
	Evidence []learnDraftDTO  `json:"evidence"`
}

func newLearnDecideCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		task    string
		budget  float64
		wait    bool
	)
	cmd := &cobra.Command{
		Use:   "decide",
		Short: "Turn finished tasks' drafts into proposals now, under its own budget",
		Long: "The background loop decides a task once it has finished (its sessions ended, an orchestrator's day is over,\n" +
			"or a long-lived session's newest draft is a day old), within the daily budget. This runs it now; --task decides\n" +
			"one task whether it is ready or not. Nothing is applied: see `ao learn proposals`.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := map[string]any{"project": project, "task": task, "budgetUsd": budget}
			if err := ctx.postJSON(cmd.Context(), "learning/decide", body, nil); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !wait {
				_, err := fmt.Fprintf(out, "Decide started (budget $%.2f). Follow it with `ao learn proposals`.\n", budget)
				return err
			}
			for {
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(5 * time.Second):
				}
				var res learnProposalsResponse
				if err := ctx.getJSON(cmd.Context(), "learning/proposals?all=true", &res); err != nil {
					return err
				}
				r := res.Run
				if !r.Running {
					_, err := fmt.Fprintf(out, "Done: %d tasks, %d proposals, %d dropped, %d failed, $%.3f%s\n", r.Tasks, r.Proposals, r.Dropped, r.Failed, r.CostUSD, stopSuffix(r.StopReason, r.LastError))
					return err
				}
				if _, err := fmt.Fprintf(out, "  %d tasks, %d proposals, $%.3f...\n", r.Tasks, r.Proposals, r.CostUSD); err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project to decide (default: every project that learns from sessions)")
	cmd.Flags().StringVar(&task, "task", "", "Decide one task now, e.g. solo:<session> or crew:<id>")
	cmd.Flags().Float64Var(&budget, "budget", 2, "Most this run may spend, in dollars at API prices (at most 50)")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for the run to finish, printing progress")
	return cmd
}

func stopSuffix(stop, lastErr string) string {
	s := ""
	if stop != "" {
		s += " (" + stop + ")"
	}
	if lastErr != "" {
		s += "\nlast error: " + lastErr
	}
	return s
}

func newLearnProposalsCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		all     bool
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "proposals",
		Short: "List the changes learning proposes, newest first",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if project != "" {
				q.Set("project", project)
			}
			if all {
				q.Set("all", "true")
			}
			var res learnProposalsResponse
			if err := ctx.getJSON(cmd.Context(), "learning/proposals?"+q.Encode(), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeLearnProposals(cmd.OutOrStdout(), res.Proposals)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: every project)")
	cmd.Flags().BoolVar(&all, "all", false, "Include dropped, rejected and settled proposals")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.AddCommand(newLearnProposalShowCommand(ctx))
	cmd.AddCommand(newLearnProposalApproveCommand(ctx))
	cmd.AddCommand(newLearnProposalRejectCommand(ctx))
	cmd.AddCommand(newLearnProposalSnoozeCommand(ctx))
	return cmd
}

func proposalID(arg string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
	if err != nil || id <= 0 {
		return 0, usageError{fmt.Errorf("id must be a positive number, got %q", arg)}
	}
	return id, nil
}

func newLearnProposalApproveCommand(ctx *commandContext) *cobra.Command {
	var file, side, text string
	cmd := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a proposal: write it as proposed, or as edited in --file; a conflict needs --side",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := proposalID(args[0])
			if err != nil {
				return err
			}
			content := text
			if file != "" {
				b, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				content = string(b)
			}
			var res learnProposalDTO
			if err := ctx.postJSON(cmd.Context(), "learning/proposals/"+strconv.FormatInt(id, 10)+"/approve",
				map[string]string{"content": content, "resolution": side}, &res); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d %s %s\n", res.ID, res.Status, res.TargetPath)
			return err
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "Write this file's content instead of the proposed content")
	cmd.Flags().StringVar(&text, "text", "", "For a conflict: the new rule's text, or where each one applies with --side both")
	cmd.Flags().StringVar(&side, "side", "", "For a conflict: keep_rule, words_win or both")
	return cmd
}

func newLearnProposalRejectCommand(ctx *commandContext) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "reject <id>",
		Short: "Reject a proposal; the reason keeps the same thing from being proposed again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := proposalID(args[0])
			if err != nil {
				return err
			}
			var res learnProposalDTO
			if err := ctx.postJSON(cmd.Context(), "learning/proposals/"+strconv.FormatInt(id, 10)+"/reject", map[string]string{"reason": reason}, &res); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d rejected\n", res.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why not")
	return cmd
}

func newLearnProposalSnoozeCommand(ctx *commandContext) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "snooze <id>",
		Short: "Hide a pending proposal for some days",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := proposalID(args[0])
			if err != nil {
				return err
			}
			until := time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC()
			var res learnProposalDTO
			if err := ctx.postJSON(cmd.Context(), "learning/proposals/"+strconv.FormatInt(id, 10)+"/snooze", map[string]string{"until": until.Format(time.RFC3339)}, &res); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d snoozed until %s\n", res.ID, until.Local().Format("2006-01-02 15:04"))
			return err
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, "Days to hide it (at most 90)")
	return cmd
}

func writeLearnProposals(w io.Writer, ps []learnProposalDTO) error {
	if len(ps) == 0 {
		_, err := fmt.Fprintln(w, "No proposals.")
		return err
	}
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "#%d %s  %s  %s  conf %.2f  %s  (%s)\n  %s\n  %s\n", p.ID, p.Status, p.Action, p.Scope, p.Confidence, p.ProjectID, p.Outcome, p.Title, p.TargetPath)
		if p.DropReason != "" {
			fmt.Fprintf(&b, "  dropped: %s\n", p.DropReason)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func newLearnProposalShowCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one proposal: why, the evidence, how it relates to the standing rules, and the diff",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
			if err != nil || id <= 0 {
				return usageError{fmt.Errorf("id must be a positive number, got %q", args[0])}
			}
			var res learnProposalResponse
			if err := ctx.getJSON(cmd.Context(), "learning/proposals/"+strconv.FormatInt(id, 10), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeLearnProposal(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func writeLearnProposal(w io.Writer, res learnProposalResponse) error {
	p := res.Proposal
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s  %s  %s  conf %.2f  task %s (%s)\n%s\n%s\n\n%s\n", p.ID, p.Status, p.Action, p.Scope, p.Confidence, p.TaskKey, p.Outcome, p.Title, p.TargetPath, p.Rationale)
	if p.DropReason != "" {
		fmt.Fprintf(&b, "\ndropped: %s\n", p.DropReason)
	}
	fmt.Fprintf(&b, "\nverifier: grounded %t, contradicts a rule %t, sensitive %t", p.Verifier.Grounded, p.Verifier.ContradictsRule, p.Verifier.SensitiveData)
	if p.Verifier.Notes != "" {
		fmt.Fprintf(&b, "\n  %s", p.Verifier.Notes)
	}
	b.WriteString("\n")
	if len(p.RuleVerdicts) > 0 {
		b.WriteString("\nrules:\n")
		for _, v := range p.RuleVerdicts {
			fmt.Fprintf(&b, "  %s %s %s\n", v.Verdict, v.RuleID, v.Note)
		}
	}
	if len(res.Evidence) > 0 {
		b.WriteString("\nevidence:\n")
		for _, d := range res.Evidence {
			fmt.Fprintf(&b, "  d%d @%s (%s, %s, conf %.2f): %q\n", d.ID, d.SessionID, d.AnchorSourceClass, d.About, d.Confidence, d.Quote)
		}
	}
	if p.Diff != "" {
		b.WriteString("\n" + p.Diff)
	} else if p.NewContent != "" {
		b.WriteString("\n" + p.NewContent + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
