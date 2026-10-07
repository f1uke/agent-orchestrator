package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// linkTestinyRunRequest mirrors controllers.LinkTestinyRunInput.
type linkTestinyRunRequest struct {
	Ref     string `json:"ref"`
	Project string `json:"project,omitempty"`
	From    string `json:"from,omitempty"`
}

// testinyRunsResponse mirrors controllers.TestinyRunsResponse.
type testinyRunsResponse struct {
	Runs []domain.TestinyRunView `json:"runs"`
}

// recordTestinyResultsRequest mirrors controllers.RecordTestinyResultsInput.
type recordTestinyResultsRequest struct {
	Results []domain.TestinyResult `json:"results"`
	From    string                 `json:"from,omitempty"`
	SHA     string                 `json:"sha,omitempty"`
}

// uploadTestinyEvidenceRequest mirrors controllers.UploadTestinyEvidenceInput.
type uploadTestinyEvidenceRequest struct {
	From string `json:"from,omitempty"`
}

// testinyEvidenceTimeout bounds an evidence upload. The daemon gives rclone 30
// minutes to send the recordings, and this leaves it time to say it ran out.
const testinyEvidenceTimeout = 35 * time.Minute

// testinyEvidenceUsageCodes are the evidence upload's answers that the agent
// fixes itself, on top of testinyUsageCodes: exit 2.
var testinyEvidenceUsageCodes = map[string]bool{
	"TESTINY_EVIDENCE_INVALID": true,
	"TESTINY_RUN_CLOSED":       true,
	"TESTINY_RUN_NOT_LINKED":   true,
}

// testinyUsageCodes are the daemon's answers that mean the command was asked
// for something it can never do as typed, or that the caller must not retry:
// exit 2, like any other misuse.
var testinyUsageCodes = map[string]bool{
	"TESTINY_BAD_CASE_REF":         true,
	"TESTINY_BAD_RUN_REF":          true,
	"TESTINY_OFF":                  true,
	"TESTINY_RESULT_INVALID":       true,
	"TESTINY_RESULT_SET_BY_PERSON": true,
	"TESTINY_WRITE_NOT_YOURS":      true,
}

func newTestinyCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "testiny",
		Short: "Link a task's Testiny test runs, read them and their cases back, record case results, and upload QA evidence",
		Long: "A task's Testiny tab lists the Testiny test runs its cases were played in. " +
			"AO keeps the links and a log of the results and evidence it recorded; titles, cases and results " +
			"are read live from Testiny. Recording a result and commenting evidence links on a result are " +
			"the only writes AO makes to Testiny.",
	}
	cmd.AddCommand(newTestinyLinkCommand(ctx), newTestinyUnlinkCommand(ctx), newTestinyRunsCommand(ctx),
		newTestinyCaseCommand(ctx), newTestinyResultCommand(ctx), newTestinyEvidenceCommand(ctx))
	return cmd
}

func newTestinyLinkCommand(ctx *commandContext) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "link <task> <run-id|url> [--project <key>]",
		Short: "Link a Testiny run to a task",
		Long: "Links a run to the task, after Testiny confirms the run exists. The run is a run id " +
			"(632), TR-632, or the run's URL. Run ids are global in Testiny, so the run names its own " +
			"Testiny project, and one task may hold runs from several. Give the run's URL, or its id " +
			"with --project: a run that is not in the project the URL or --project names is refused. " +
			"Run from a crew's qa, it lands on the task all the same. Linking a run twice is a no-op.",
		Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := strings.TrimSpace(args[0])
			req := linkTestinyRunRequest{
				Ref:     strings.TrimSpace(args[1]),
				Project: strings.TrimSpace(project),
				From:    strings.TrimSpace(os.Getenv("AO_SESSION_ID")),
			}
			var view domain.TestinyRunView
			if err := ctx.postJSON(cmd.Context(), testinyRunsPath(task), req, &view); err != nil {
				return testinyError(err)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "linked %s\n", testinyHeadline(view))
			return err
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "The Testiny project the run is in: its key (project_key in testiny project ls, e.g. MOB), name or id")
	return cmd
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

func newTestinyCaseCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "case <task> <case-id|TC-id>",
		Short: "Show a case in a task's Testiny runs in full",
		Long: "Shows what a case asks for, read from Testiny: its test data, precondition, steps " +
			"with their expected results, description and remark. The case must be in a run " +
			"linked to the task.",
		Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			task, ref := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			var c domain.TestinyCaseDetail
			if err := ctx.getJSON(cmd.Context(), "sessions/"+url.PathEscape(task)+"/testiny/cases/"+url.PathEscape(ref), &c); err != nil {
				return testinyError(err)
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), c)
			}
			_, err := io.WriteString(cmd.OutOrStdout(), testinyCaseText(c))
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the daemon's JSON")
	return cmd
}

// testinyCaseText is a case as a block an agent reads before playing it: the
// title, one meta line, then each section the case fills in.
func testinyCaseText(c domain.TestinyCaseDetail) string {
	var meta []string
	add := func(label, value string) {
		if value != "" {
			meta = append(meta, label+": "+value)
		}
	}
	if c.Priority != nil {
		add("priority", c.Priority.Label)
	}
	add("type", c.Type)
	add("platforms", strings.Join(c.Platforms, ", "))
	add("jira", c.Jira)
	var features []string
	for _, f := range []string{c.Features, c.SubFeatures} {
		if f != "" {
			features = append(features, f)
		}
	}
	add("features", strings.Join(features, " > "))
	add("section", c.Section)

	var b strings.Builder
	fmt.Fprintf(&b, "TC-%d %s\n", c.ID, c.Title)
	if len(meta) > 0 {
		b.WriteString(strings.Join(meta, " | ") + "\n")
	}
	section := func(heading, body string) {
		if body != "" {
			fmt.Fprintf(&b, "\n%s\n%s\n", heading, indentLines(body, "  "))
		}
	}
	section("Test data", c.TestData)
	section("Precondition", c.Precondition)
	section("Steps", testinyStepsText(c.Steps))
	section("Steps", c.StepsText)
	section("Expected result", c.ExpectedText)
	section("Scenarios", c.BDD)
	section("Description", c.Description)
	section("Remark", c.Remark)
	return b.String()
}

// testinyStepsText numbers each step, with its expected result under it.
func testinyStepsText(steps []domain.TestinyCaseStep) string {
	var lines []string
	for _, s := range steps {
		marker := strconv.Itoa(s.N) + ". "
		pad := strings.Repeat(" ", len(marker))
		lines = append(lines, marker+indentLines(s.Action, pad)[len(pad):])
		if s.Expected != "" {
			lines = append(lines, pad+"-> "+indentLines(s.Expected, pad+"   ")[len(pad)+3:])
		}
	}
	return strings.Join(lines, "\n")
}

// indentLines puts prefix before every line of s.
func indentLines(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

func newTestinyResultCommand(ctx *commandContext) *cobra.Command {
	var status, comment, fromFile string
	var steps []string
	cmd := &cobra.Command{
		Use:   "result <task> <run> [case] (--status <status> [--comment <text>] [--step <n>=<status>]... | --from-file <path|->)",
		Short: "Record case and step results in a Testiny run linked to a task",
		Long: "Records one case's result (--status, with --comment for FAILED, BLOCKED and SKIPPED) and " +
			"the result of any of its steps (--step 2=FAILED, counting from 1; steps alone keep the case's " +
			"status), or a batch read from --from-file as a JSON array of {caseId, status, comment, " +
			"steps: [{n, status}]}. The run must be linked to the task. When the task has a qa, only qa " +
			"may record, and an agent never overwrites a case or step status a person set: that is " +
			"refused (exit 2), so report it in the handback instead. Sends $AO_SESSION_ID and the " +
			"checkout's HEAD commit with the results.",
		Args: rangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			task, run := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			results, err := testinyResultsFromArgs(cmd, args[2:], status, comment, steps, fromFile)
			if err != nil {
				return err
			}
			req := recordTestinyResultsRequest{
				Results: results,
				From:    strings.TrimSpace(os.Getenv("AO_SESSION_ID")),
				SHA:     ctx.headCommit(cmd.Context()),
			}
			var view domain.TestinyRunView
			if err := ctx.postJSON(cmd.Context(), testinyRunsPath(task)+"/"+url.PathEscape(run)+"/results", req, &view); err != nil {
				return testinyError(err)
			}
			return writeTestinyRecorded(cmd.OutOrStdout(), view, results)
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "PASSED, FAILED, BLOCKED, SKIPPED or NOTRUN")
	cmd.Flags().StringVar(&comment, "comment", "", "What happened, at most 300 characters (FAILED, BLOCKED and SKIPPED need one)")
	cmd.Flags().StringArrayVar(&steps, "step", nil, "A step's result as <n>=<status>, counting from 1 (repeatable)")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "A JSON array of {caseId, status, comment, steps}; - reads stdin")
	return cmd
}

// testinyResultsFromArgs is the one case named on the command line, or the
// batch in --from-file. The daemon checks statuses, comments and steps.
func testinyResultsFromArgs(cmd *cobra.Command, caseArg []string, status, comment string, stepArgs []string, fromFile string) ([]domain.TestinyResult, error) {
	if fromFile != "" {
		if len(caseArg) > 0 || status != "" || comment != "" || len(stepArgs) > 0 {
			return nil, usageError{errors.New("usage: --from-file takes no case, --status, --comment or --step")}
		}
		var raw []byte
		var err error
		if fromFile == "-" {
			raw, err = io.ReadAll(cmd.InOrStdin())
		} else {
			raw, err = os.ReadFile(fromFile)
		}
		if err != nil {
			return nil, usageError{fmt.Errorf("read results: %w", err)}
		}
		var results []domain.TestinyResult
		if err := json.Unmarshal(raw, &results); err != nil {
			return nil, usageError{fmt.Errorf("results must be a JSON array of {caseId, status, comment, steps}: %w", err)}
		}
		return results, nil
	}
	if len(caseArg) != 1 || (status == "" && len(stepArgs) == 0) {
		return nil, usageError{errors.New("usage: give a case with --status, --step or both, or --from-file")}
	}
	id, err := domain.ParseTestinyCaseRef(caseArg[0])
	if err != nil {
		return nil, usageError{fmt.Errorf("usage: %q is %w", caseArg[0], err)}
	}
	steps := make([]domain.TestinyStepResult, len(stepArgs))
	for i, arg := range stepArgs {
		n, s, ok := strings.Cut(arg, "=")
		number, err := strconv.Atoi(strings.TrimSpace(n))
		if !ok || err != nil {
			return nil, usageError{fmt.Errorf("usage: --step %q is not <n>=<status>, e.g. --step 2=FAILED", arg)}
		}
		steps[i] = domain.TestinyStepResult{N: number, Status: domain.TestinyCaseStatus(strings.TrimSpace(s))}
	}
	return []domain.TestinyResult{{CaseID: id, Status: domain.TestinyCaseStatus(status), Comment: comment, Steps: steps}}, nil
}

func newTestinyEvidenceCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "evidence <task> <run>",
		Short: "Upload a linked run's QA Evidence folder to Google Drive and link each file on its case's result",
		Long: "Uploads the run's folder under ~/Desktop/QA Evidence/ to the Google Drive folder set in AO " +
			"(Settings > QA evidence, an rclone path such as finnomena:QA) at the same path, then posts each " +
			"evidence file's Drive link as a comment on its case's result. A result's status is never touched. " +
			"The folder must be <Project>/<YYYY>/<milestone>/TP-<n> - <plan>/TR-<n> - <run> with every name as " +
			"Testiny gives it, hold README.md, and name each file \"TC-<id> pass[ - <device>].<ext>\" or " +
			"\"TC-<id> FAIL <JIRA-KEY>[ - <device>].<ext>\" for a case in the run; anything else is refused " +
			"with every problem listed (exit 2). Running it again sends only what Drive lacks and links only " +
			"what no comment on the result links yet. The run must be linked to the task and open. When the " +
			"task has a qa, only qa may run it. Sends $AO_SESSION_ID.",
		Args: exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			task, run := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			req := uploadTestinyEvidenceRequest{From: strings.TrimSpace(os.Getenv("AO_SESSION_ID"))}
			callCtx, cancel := context.WithTimeout(cmd.Context(), testinyEvidenceTimeout)
			defer cancel()
			var report domain.TestinyEvidenceReport
			if err := ctx.postJSON(callCtx, testinyRunsPath(task)+"/"+url.PathEscape(run)+"/evidence", req, &report); err != nil {
				var apiErr apiResponseError
				if errors.As(err, &apiErr) && testinyEvidenceUsageCodes[apiErr.ErrorBody.Code] {
					return usageError{err}
				}
				return testinyError(err)
			}
			return writeTestinyEvidence(cmd.OutOrStdout(), report)
		},
	}
}

// writeTestinyEvidence prints where the evidence went, what was sent this
// time, and per case which files were linked now and which already were.
func writeTestinyEvidence(w io.Writer, r domain.TestinyEvidenceReport) error {
	var b strings.Builder
	fmt.Fprintf(&b, "uploaded the evidence of %s %q\n", r.Run.Link.RunID, r.Run.Title)
	fmt.Fprintf(&b, "  folder: %s\n", r.Folder)
	fmt.Fprintf(&b, "  drive:  %s\n", r.Drive)
	if len(r.Uploaded) == 0 {
		b.WriteString("  sent:   nothing new, Drive had every file\n")
	} else {
		fmt.Fprintf(&b, "  sent:   %s\n", strings.Join(r.Uploaded, ", "))
	}
	if len(r.Cases) == 0 {
		b.WriteString("  no evidence files to link, only README.md\n")
	}
	for _, c := range r.Cases {
		var parts []string
		if len(c.Linked) > 0 {
			parts = append(parts, "linked "+strings.Join(c.Linked, ", "))
		}
		if len(c.AlreadyLinked) > 0 {
			parts = append(parts, "already linked "+strings.Join(c.AlreadyLinked, ", "))
		}
		fmt.Fprintf(&b, "  TC-%d %s\n", c.CaseID, strings.Join(parts, "; "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// headCommit is the short HEAD commit of the checkout the command runs in, or
// "" outside one: it only labels the results.
func (c *commandContext) headCommit(ctx context.Context) string {
	out, err := c.deps.CommandOutput(ctx, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// writeTestinyRecorded prints each case recorded, and each step recorded under
// it, as Testiny now has them, and the run's new counts.
func writeTestinyRecorded(w io.Writer, v domain.TestinyRunView, results []domain.TestinyResult) error {
	cases := make(map[int64]domain.TestinyCaseResult, len(v.Cases))
	for _, c := range v.Cases {
		cases[c.ID] = c
	}
	var b strings.Builder
	fmt.Fprintf(&b, "recorded in %s %q\n", v.Link.RunID, v.Title)
	for _, r := range results {
		c, ok := cases[r.CaseID]
		if !ok {
			c = domain.TestinyCaseResult{ID: r.CaseID, Status: r.Status}
		}
		fmt.Fprintf(&b, "  %-7s TC-%d %s\n", c.Status, c.ID, c.Title)
		if len(r.Steps) > 0 {
			steps := make([]string, len(r.Steps))
			for i, asked := range r.Steps {
				status := asked.Status
				if j := slices.IndexFunc(c.Steps, func(s domain.TestinyRunStep) bool { return s.N == asked.N }); j >= 0 {
					status = c.Steps[j].Status
				}
				steps[i] = fmt.Sprintf("step %d %s", asked.N, status)
			}
			fmt.Fprintf(&b, "          %s\n", strings.Join(steps, ", "))
		}
	}
	fmt.Fprintf(&b, "%s now has %s\n", v.Link.RunID, testinyCounts(v.Counts))
	_, err := io.WriteString(w, b.String())
	return err
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
			fmt.Fprintf(&b, "%s (no data)\n", testinyRunName(v.Link))
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

// testinyHeadline is `MOB TR-632 "<title>" (6 cases: 5 passed, 1 failed)`.
func testinyHeadline(v domain.TestinyRunView) string {
	return fmt.Sprintf("%s %q (%s)", testinyRunName(v.Link), v.Title, testinyCounts(v.Counts))
}

// testinyRunName is the run's id after its Testiny project's key (or name):
// `MOB TR-632`. One task may hold runs from several projects. A link whose
// project AO has not read yet is just `TR-632`.
func testinyRunName(l domain.TestinyRunLink) string {
	if p := l.Project.Label(); p != "" {
		return p + " " + l.RunID.String()
	}
	return l.RunID.String()
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
