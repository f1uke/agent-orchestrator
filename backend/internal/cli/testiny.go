package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// linkTestinyRunRequest mirrors controllers.LinkTestinyRunInput.
type linkTestinyRunRequest struct {
	Ref  string `json:"ref"`
	From string `json:"from,omitempty"`
}

// testinyRunsResponse mirrors controllers.TestinyRunsResponse.
type testinyRunsResponse struct {
	Project string                  `json:"project"`
	Runs    []domain.TestinyRunView `json:"runs"`
}

// testinyUsageCodes are the daemon's answers that mean the command was asked
// for something it can never do as typed: exit 2, like any other misuse.
var testinyUsageCodes = map[string]bool{"TESTINY_BAD_RUN_REF": true, "TESTINY_OFF": true}

func newTestinyCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "testiny",
		Short: "Link a task's Testiny test runs and read them back",
		Long: "A task's Testiny tab lists the Testiny test runs its cases were played in. " +
			"AO keeps only the links; titles, cases and results are read live from Testiny, " +
			"and AO never writes to Testiny.",
	}
	cmd.AddCommand(newTestinyLinkCommand(ctx), newTestinyUnlinkCommand(ctx), newTestinyRunsCommand(ctx))
	return cmd
}

func newTestinyLinkCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "link <task> <run-id|url>",
		Short: "Link a Testiny run to a task",
		Long: "Links a run to the task, after Testiny confirms the run exists in the project's " +
			"Testiny project. The run is a run id (632), TR-632, or the run's URL. Run from a " +
			"crew's qa, it lands on the task all the same. Linking a run twice is a no-op.",
		Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := strings.TrimSpace(args[0])
			req := linkTestinyRunRequest{Ref: strings.TrimSpace(args[1]), From: strings.TrimSpace(os.Getenv("AO_SESSION_ID"))}
			var view domain.TestinyRunView
			if err := ctx.postJSON(cmd.Context(), testinyRunsPath(task), req, &view); err != nil {
				return testinyError(err)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "linked %s\n", testinyHeadline(view))
			return err
		},
	}
}

func newTestinyUnlinkCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <task> <run-id>",
		Short: "Unlink a Testiny run from a task",
		Args:  exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			task, run := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			if err := ctx.deleteJSON(cmd.Context(), testinyRunsPath(task)+"/"+url.PathEscape(run), nil); err != nil {
				return testinyError(err)
			}
			label := run
			if id, _, err := domain.ParseTestinyRunRef(run); err == nil {
				label = id.String()
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "unlinked %s from %s\n", label, task)
			return err
		},
	}
}

func newTestinyRunsCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "runs <task>",
		Short: "Show the Testiny runs linked to a task",
		Long: "Shows each linked run: its title and counts, every case that did not pass, and " +
			"its evidence folder. Every run is read from Testiny now.",
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := strings.TrimSpace(args[0])
			var res testinyRunsResponse
			if err := ctx.getJSON(cmd.Context(), testinyRunsPath(task)+"?refresh=1", &res); err != nil {
				return testinyError(err)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeTestinyRuns(cmd.OutOrStdout(), task, res.Runs)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the daemon's JSON")
	return cmd
}

func testinyRunsPath(task string) string {
	return "sessions/" + url.PathEscape(task) + "/testiny/runs"
}

func testinyError(err error) error {
	var apiErr apiResponseError
	if errors.As(err, &apiErr) && testinyUsageCodes[apiErr.ErrorBody.Code] {
		return usageError{err}
	}
	return err
}

func writeTestinyRuns(w io.Writer, task string, runs []domain.TestinyRunView) error {
	if len(runs) == 0 {
		_, err := fmt.Fprintf(w, "no Testiny runs linked to %s\n", task)
		return err
	}
	var b strings.Builder
	for i, v := range runs {
		if i > 0 {
			b.WriteString("\n")
		}
		if v.FetchedAt == nil {
			fmt.Fprintf(&b, "%s (no data)\n", v.Link.RunID)
		} else {
			fmt.Fprintf(&b, "%s\n", testinyHeadline(v))
			if v.URL != "" {
				fmt.Fprintf(&b, "  %s\n", v.URL)
			}
			var belongs []string
			if v.Plan != nil {
				belongs = append(belongs, "plan: "+v.Plan.Title)
			}
			if v.Milestone != nil {
				belongs = append(belongs, "milestone: "+v.Milestone.Title)
			}
			if len(belongs) > 0 {
				fmt.Fprintf(&b, "  %s\n", strings.Join(belongs, ", "))
			}
			for _, c := range v.Cases {
				if c.Status != domain.TestinyPassed {
					fmt.Fprintf(&b, "  %-7s TC-%d %s\n", c.Status, c.ID, c.Title)
				}
			}
			if v.EvidenceDir != "" {
				fmt.Fprintf(&b, "  evidence: %s\n", v.EvidenceDir)
			}
		}
		if fe := v.FetchError; fe != nil {
			fmt.Fprintf(&b, "  could not read it now (%s): %s", fe.Kind, strings.TrimSuffix(fe.Message, "."))
			if v.FetchedAt != nil {
				fmt.Fprintf(&b, ". Showing the read from %s", v.FetchedAt.UTC().Format(time.RFC3339))
			}
			b.WriteString(".\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// testinyHeadline is `TR-632 "<title>" (6 cases: 5 passed, 1 failed)`.
func testinyHeadline(v domain.TestinyRunView) string {
	return fmt.Sprintf("%s %q (%s)", v.Link.RunID, v.Title, testinyCounts(v.Counts))
}

// testinyStatusOrder puts the statuses a person acts on first; a status AO
// does not know follows, alphabetically.
var testinyStatusOrder = map[domain.TestinyCaseStatus]int{
	domain.TestinyPassed: 0, domain.TestinyFailed: 1, domain.TestinyBlocked: 2, domain.TestinySkipped: 3, domain.TestinyNotRun: 4,
}

func testinyCounts(counts map[domain.TestinyCaseStatus]int) string {
	statuses := make([]domain.TestinyCaseStatus, 0, len(counts))
	total := 0
	for s, n := range counts {
		statuses = append(statuses, s)
		total += n
	}
	sort.Slice(statuses, func(i, j int) bool {
		oi, iKnown := testinyStatusOrder[statuses[i]]
		oj, jKnown := testinyStatusOrder[statuses[j]]
		if iKnown != jKnown {
			return iKnown
		}
		if iKnown {
			return oi < oj
		}
		return statuses[i] < statuses[j]
	})
	parts := make([]string, len(statuses))
	for i, s := range statuses {
		name := strings.ToLower(string(s))
		if s == domain.TestinyNotRun {
			name = "not run"
		}
		parts[i] = fmt.Sprintf("%d %s", counts[s], name)
	}
	noun := "cases"
	if total == 1 {
		noun = "case"
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d %s", total, noun)
	}
	return fmt.Sprintf("%d %s: %s", total, noun, strings.Join(parts, ", "))
}
