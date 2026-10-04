package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
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
	SnoozedUntil *time.Time `json:"snoozedUntil,omitempty"`
	RejectReason string     `json:"rejectReason,omitempty"`
	DecidedAt    *time.Time `json:"decidedAt,omitempty"`
	Resolution   string     `json:"resolution,omitempty"`
}

// snoozed reports whether a pending proposal is hidden until a later time.
func (p learnProposalDTO) snoozed(now time.Time) bool {
	return p.Status == "pending" && p.SnoozedUntil != nil && p.SnoozedUntil.After(now)
}

// state is the status as the person reads it: a snoozed proposal is pending
// in the store, but it is not waiting for a decision until its date.
func (p learnProposalDTO) state(now time.Time) string {
	if p.snoozed(now) {
		return "snoozed until " + p.SnoozedUntil.Local().Format("2006-01-02")
	}
	return p.Status
}

type learnWrittenDTO struct {
	Path             string `json:"path"`
	Exists           bool   `json:"exists"`
	Content          string `json:"content"`
	IndexPath        string `json:"indexPath,omitempty"`
	IndexLine        string `json:"indexLine,omitempty"`
	IndexLinePresent bool   `json:"indexLinePresent"`
	Changed          bool   `json:"changed"`
	Diff             string `json:"diff,omitempty"`
	Token            string `json:"token"`
}

type learnProposalEventDTO struct {
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	Note         string     `json:"note,omitempty"`
	SnoozedUntil *time.Time `json:"snoozedUntil,omitempty"`
	Via          string     `json:"via,omitempty"`
	Session      string     `json:"session,omitempty"`
	At           time.Time  `json:"at"`
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
	Proposal learnProposalDTO        `json:"proposal"`
	Evidence []learnDraftDTO         `json:"evidence"`
	Written  *learnWrittenDTO        `json:"written,omitempty"`
	History  []learnProposalEventDTO `json:"history"`
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

// learnProposalStatuses are what `ao learn proposals --status` filters by:
// waiting and snoozed split pending by its snooze date.
var learnProposalStatuses = []string{"waiting", "snoozed", "pending", "applied", "rejected", "stale", "superseded", "dropped", "all"}

func newLearnProposalsCommand(ctx *commandContext) *cobra.Command {
	var (
		project string
		all     bool
		status  string
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "proposals",
		Short: "List the changes learning proposes, newest first",
		Long: "Pending proposals by default, a snoozed one marked with the date it comes back. --status narrows the list:\n" +
			"waiting (pending, not snoozed), snoozed, pending (both), applied, rejected, stale, superseded, dropped or all.\n" +
			"Decide with `ao learn approve|reject|snooze|unsnooze|reopen|undo|edit <id>`.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if status != "" && !slices.Contains(learnProposalStatuses, status) {
				return usageError{fmt.Errorf("--status must be one of %s, got %q", strings.Join(learnProposalStatuses, ", "), status)}
			}
			q := url.Values{}
			if project != "" {
				q.Set("project", project)
			}
			if all || (status != "" && status != "waiting" && status != "snoozed" && status != "pending") {
				q.Set("all", "true")
			}
			var res learnProposalsResponse
			if err := ctx.getJSON(cmd.Context(), "learning/proposals?"+q.Encode(), &res); err != nil {
				return err
			}
			res.Proposals = filterLearnProposals(res.Proposals, status, time.Now())
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeLearnProposals(cmd.OutOrStdout(), res.Proposals, time.Now())
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: every project)")
	cmd.Flags().BoolVar(&all, "all", false, "Include dropped, rejected and settled proposals")
	cmd.Flags().StringVar(&status, "status", "", "Only these: "+strings.Join(learnProposalStatuses, ", "))
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.AddCommand(newLearnProposalShowCommand(ctx))
	// The decisions live on `ao learn` itself; these keep scripts written
	// against the first spelling working.
	for _, c := range []*cobra.Command{newLearnApproveCommand(ctx), newLearnRejectCommand(ctx), newLearnSnoozeCommand(ctx)} {
		c.Hidden = true
		cmd.AddCommand(c)
	}
	return cmd
}

func filterLearnProposals(ps []learnProposalDTO, status string, now time.Time) []learnProposalDTO {
	if status == "" || status == "all" {
		return ps
	}
	out := ps[:0:0]
	for _, p := range ps {
		keep := p.Status == status
		switch status {
		case "waiting":
			keep = p.Status == "pending" && !p.snoozed(now)
		case "snoozed":
			keep = p.snoozed(now)
		}
		if keep {
			out = append(out, p)
		}
	}
	return out
}

func proposalID(arg string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
	if err != nil || id <= 0 {
		return 0, usageError{fmt.Errorf("id must be a positive number, got %q", arg)}
	}
	return id, nil
}

// decide posts one decision on a proposal, saying it came from the CLI and,
// inside an AO session, which one - the proposal's history keeps both. A file
// that changed since AO wrote it is answered with its diff on stderr and how to
// confirm, never overwritten.
func (c *commandContext) decide(cmd *cobra.Command, arg, verb string, body map[string]any) (learnProposalDTO, error) {
	var res learnProposalDTO
	id, err := proposalID(arg)
	if err != nil {
		return res, err
	}
	body["via"] = "cli"
	if s := strings.TrimSpace(os.Getenv("AO_SESSION_ID")); s != "" {
		body["session"] = s
	}
	err = c.postJSON(cmd.Context(), "learning/proposals/"+strconv.FormatInt(id, 10)+"/"+verb, body, &res)
	var apiErr apiResponseError
	if errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "PROPOSAL_CHANGED" {
		diff, _ := apiErr.ErrorBody.Details["diff"].(string)
		token, _ := apiErr.ErrorBody.Details["token"].(string)
		path, _ := apiErr.ErrorBody.Details["path"].(string)
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s changed after AO wrote it:\n\n%s\nRun again with --confirm %s to go ahead anyway; what is there now is backed up first.\n", path, diff, token)
	}
	return res, err
}

func newLearnApproveCommand(ctx *commandContext) *cobra.Command {
	var file, side, text string
	cmd := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a proposal: write it as proposed, or as edited in --file; a conflict needs --side",
		Long: "Writes the proposal (a snoozed one too). --file writes your edit instead, after the same checks as the proposal\n" +
			"(the file's format, your forbidden patterns, sensitive values). A conflict card needs --side: keep_rule, words_win or\n" +
			"both (with --text saying where each one applies).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			content := text
			if file != "" {
				b, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				content = string(b)
			}
			res, err := ctx.decide(cmd, args[0], "approve", map[string]any{"content": content, "resolution": side})
			if err != nil {
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

func newLearnRejectCommand(ctx *commandContext) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "reject <id> --reason <why>",
		Short: "Reject a proposal (a snoozed one too); the reason keeps the same thing from being proposed again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return usageError{errors.New("--reason is required: learning reads it so it does not propose the same thing again")}
			}
			res, err := ctx.decide(cmd, args[0], "reject", map[string]any{"reason": reason})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d rejected: %s\n", res.ID, res.RejectReason)
			return err
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why not (required)")
	return cmd
}

func newLearnSnoozeCommand(ctx *commandContext) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "snooze <id>",
		Short: "Hide a pending proposal for some days",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			until := time.Now().Add(time.Duration(days) * 24 * time.Hour).UTC()
			res, err := ctx.decide(cmd, args[0], "snooze", map[string]any{"until": until.Format(time.RFC3339)})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d snoozed until %s\n", res.ID, until.Local().Format("2006-01-02 15:04"))
			return err
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, "Days to hide it (at most 90)")
	return cmd
}

func newLearnUnsnoozeCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "unsnooze <id>",
		Short: "Bring a snoozed proposal back to the queue now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ctx.decide(cmd, args[0], "unsnooze", map[string]any{})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d is back in the queue\n", res.ID)
			return err
		},
	}
}

func newLearnReopenCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <id>",
		Short: "Put a rejected proposal back in the queue, to decide again",
		Long: "Refused while another proposal for the same file is waiting (decide that one first), and for a new memory\n" +
			"whose file exists already - reopening it would write it twice.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ctx.decide(cmd, args[0], "reopen", map[string]any{})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d reopened: back in the queue\n", res.ID)
			return err
		},
	}
}

func newLearnUndoCommand(ctx *commandContext) *cobra.Command {
	var confirm string
	cmd := &cobra.Command{
		Use:   "undo <id>",
		Short: "Take back what an approved proposal wrote and put it back in the queue",
		Long: "A new memory's file and the MEMORY.md line it added are removed; a changed memory, skill or CLAUDE.md gets the\n" +
			"version from before the approve; a conflict's pinned rule gets its earlier text (a conflict whose rule was kept\n" +
			"is simply reopened). If what AO wrote changed since - by hand, by an agent, by another proposal - nothing is\n" +
			"touched: the diff is printed with a token, and --confirm <token> goes ahead. What it replaces is backed up.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ctx.decide(cmd, args[0], "undo", map[string]any{"confirmToken": confirm})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d undone: back in the queue\n", res.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&confirm, "confirm", "", "The token printed when the file changed since AO wrote it")
	return cmd
}

func newLearnEditCommand(ctx *commandContext) *cobra.Command {
	var file, confirm string
	cmd := &cobra.Command{
		Use:   "edit <id> --file <path>",
		Short: "Replace what an approved proposal wrote with the content of --file, after the same checks",
		Long: "The whole file as it should be (`ao learn proposals show <id>` prints it as it is now). If the file changed since\n" +
			"AO wrote it, nothing is written: the diff is printed with a token, and --confirm <token> goes ahead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return usageError{errors.New("--file is required: the file as it should be")}
			}
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			res, err := ctx.decide(cmd, args[0], "edit", map[string]any{"content": string(b), "confirmToken": confirm})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "#%d edited %s\n", res.ID, res.TargetPath)
			return err
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "The edited file (required)")
	cmd.Flags().StringVar(&confirm, "confirm", "", "The token printed when the file changed since AO wrote it")
	return cmd
}

func writeLearnProposals(w io.Writer, ps []learnProposalDTO, now time.Time) error {
	if len(ps) == 0 {
		_, err := fmt.Fprintln(w, "No proposals.")
		return err
	}
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "#%d %s  %s  %s  conf %.2f  %s  (%s)\n  %s\n  %s\n", p.ID, p.state(now), p.Action, p.Scope, p.Confidence, p.ProjectID, p.Outcome, p.Title, p.TargetPath)
		writeLearnDecision(&b, p)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeLearnDecision(b *strings.Builder, p learnProposalDTO) {
	when := ""
	if p.DecidedAt != nil {
		when = " " + p.DecidedAt.Local().Format("2006-01-02 15:04")
	}
	switch {
	case p.DropReason != "":
		fmt.Fprintf(b, "  dropped: %s\n", p.DropReason)
	case p.Status == "rejected":
		fmt.Fprintf(b, "  rejected%s: %s\n", when, p.RejectReason)
	case p.Status == "applied" && p.Resolution != "":
		fmt.Fprintf(b, "  decided%s: %s\n", when, p.Resolution)
	case p.Status == "applied":
		fmt.Fprintf(b, "  written%s\n", when)
	}
}

func newLearnProposalShowCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one proposal: why, the evidence, how it relates to the standing rules, and the diff",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := proposalID(args[0])
			if err != nil {
				return err
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
	fmt.Fprintf(&b, "#%d %s  %s  %s  conf %.2f  task %s (%s)\n%s\n%s\n\n%s\n", p.ID, p.state(time.Now()), p.Action, p.Scope, p.Confidence, p.TaskKey, p.Outcome, p.Title, p.TargetPath, p.Rationale)
	if p.DropReason != "" || p.Status == "rejected" || p.Status == "applied" {
		b.WriteString("\n")
		writeLearnDecision(&b, p)
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
	if len(res.History) > 0 {
		b.WriteString("\nhistory:\n")
		for _, e := range res.History {
			fmt.Fprintf(&b, "  %s  %s", e.At.Local().Format("2006-01-02 15:04"), e.Kind)
			if e.SnoozedUntil != nil {
				fmt.Fprintf(&b, " until %s", e.SnoozedUntil.Local().Format("2006-01-02"))
			}
			if e.Note != "" {
				fmt.Fprintf(&b, ": %s", e.Note)
			}
			if by := strings.TrimSpace(e.Via + " " + e.Session); by != "" {
				fmt.Fprintf(&b, "  (%s)", by)
			}
			b.WriteString("\n")
		}
	}
	if wr := res.Written; wr != nil {
		switch {
		case !wr.Exists:
			fmt.Fprintf(&b, "\nwritten: %s is gone since AO wrote it\n", wr.Path)
		case wr.Changed:
			fmt.Fprintf(&b, "\nwritten: %s changed since AO wrote it (undo or edit needs --confirm %s):\n%s", wr.Path, wr.Token, wr.Diff)
		default:
			fmt.Fprintf(&b, "\nwritten: %s, unchanged since\n", wr.Path)
		}
		if wr.Exists {
			b.WriteString("\n" + wr.Content)
			if !strings.HasSuffix(wr.Content, "\n") {
				b.WriteString("\n")
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	}
	if p.Diff != "" {
		b.WriteString("\n" + p.Diff)
	} else if p.NewContent != "" {
		b.WriteString("\n" + p.NewContent + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
