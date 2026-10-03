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
}

type learnStatusResponse struct {
	Projects []learnProjectStatusDTO `json:"projects"`
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
	}
	_, err := io.WriteString(out, b.String())
	return err
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
