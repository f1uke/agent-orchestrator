package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// crewRunView mirrors the daemon's domain.CrewRun on the wire. Hand-mirrored,
// like every other CLI DTO here, so the CLI stays a thin HTTP client.
type crewRunView struct {
	ID             string   `json:"id"`
	SessionID      string   `json:"sessionId"`
	Role           string   `json:"role"`
	Kind           string   `json:"kind"`
	Label          string   `json:"label"`
	Attempt        int      `json:"attempt"`
	Detector       string   `json:"detector"`
	DetectorReason string   `json:"detectorReason"`
	GenAtStart     uint64   `json:"genAtStart"`
	GenAtEnd       uint64   `json:"genAtEnd"`
	StartedAt      string   `json:"startedAt"`
	EndedAt        *string  `json:"endedAt"`
	Outcome        string   `json:"outcome"`
	Result         string   `json:"result"`
	ChangedPaths   []string `json:"changedPaths"`
	HeadSHA        string   `json:"headSha"`
}

type crewRunStartRequest struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

type crewRunStartResponse struct {
	Run             crewRunView  `json:"run"`
	Certified       bool         `json:"certified"`
	SupersededRunID string       `json:"supersededRunId"`
	CrewmateRun     *crewRunView `json:"crewmateRun,omitempty"`
}

type crewRunEndRequest struct {
	RunID  string `json:"runId,omitempty"`
	Result string `json:"result,omitempty"`
}

type crewRunEndResponse struct {
	Run         crewRunView `json:"run"`
	Retry       bool        `json:"retry"`
	Attempt     int         `json:"attempt"`
	MaxAttempts int         `json:"maxAttempts"`
	Escalated   bool        `json:"escalated"`
}

type crewRunListResponse struct {
	Runs []crewRunView `json:"runs"`
}

// newCrewRunCommand is the BRACKET a crew member puts around a build, a test
// suite or a device pass.
//
// Two members of a task share one worktree, so a run can read a tree the other
// member is halfway through writing. That is not data loss - it is an
// untrustworthy result, and the dangerous part is that it looks fine. The race
// cannot be prevented (nothing can stop an agent saving a file), so AO detects
// it: a filesystem watcher counts writes to non-ignored paths, `--start` reads
// the counter and `--end` reads it again, and equal readings are what make the
// result trustworthy.
//
// The same bracket is the only way the board can say "qa is running a build"
// rather than the far weaker "qa is awake" - one mechanism, two consumers.
func newCrewRunCommand(ctx *commandContext) *cobra.Command {
	var (
		start  bool
		end    bool
		list   bool
		kind   string
		label  string
		result string
		runID  string
	)
	cmd := &cobra.Command{
		Use:   "run (--start | --end | --list)",
		Short: "Bracket a build or test run so its result can be trusted",
		Long: "Wrap every build, test suite or device pass in a bracket:\n\n" +
			"  ao crew run --start --kind test --label 'go test ./...'\n" +
			"  go test ./...\n" +
			"  ao crew run --end --result pass\n\n" +
			"AO counts writes to the worktree between the two calls. If nothing wrote to a\n" +
			"non-ignored path the result is TRUSTED. If something did, the run is DISCARDED -\n" +
			"which is neither a pass nor a fail: the result read a mixture of two states, so\n" +
			"failing it would blame the code and passing it would launder a mixed result as a\n" +
			"clean one. Discards are re-run automatically, and after three in a row the task\n" +
			"goes to NEEDS YOU for a human to decide.\n\n" +
			"If AO cannot watch the worktree at all it says so and marks the run UNCERTIFIED.\n" +
			"It never quietly falls back to a cheaper check: an absent detector has to be as\n" +
			"visible as one that misses.\n\n" +
			"An open bracket is also how the board and `ao crew status` know this member is\n" +
			"running something rather than merely awake.",
		Example: `  ao crew run --start --kind build          # opens the bracket
  ao crew run --end --result fail          # closes it and records what the build said
  ao crew run --list                       # this session's recent runs`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			chosen := 0
			for _, on := range []bool{start, end, list} {
				if on {
					chosen++
				}
			}
			if chosen != 1 {
				return usageError{errors.New("choose exactly one of --start, --end or --list")}
			}
			path, err := crewRunPath()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case start:
				return ctx.startCrewRun(cmd, path, out, kind, label)
			case end:
				return ctx.endCrewRun(cmd, path, out, runID, result)
			default:
				return ctx.listCrewRuns(cmd, path, out)
			}
		},
	}
	cmd.Flags().BoolVar(&start, "start", false, "Open a bracket: read the worktree's write counter and record that a run has begun")
	cmd.Flags().BoolVar(&end, "end", false, "Close the bracket: read the counter again and decide whether the result can be trusted")
	cmd.Flags().BoolVar(&list, "list", false, "List this session's recent bracketed runs")
	cmd.Flags().StringVar(&kind, "kind", "test", "What is about to run: build, test or device")
	cmd.Flags().StringVar(&label, "label", "", "Free-text label for the run, e.g. the command you are about to type")
	cmd.Flags().StringVar(&result, "result", "", "What the build or test said: pass or fail. Omit to record a run that did not judge itself")
	cmd.Flags().StringVar(&runID, "run", "", "Run to close. Defaults to this session's open run")
	return cmd
}

func (c *commandContext) startCrewRun(cmd *cobra.Command, path string, out io.Writer, kind, label string) error {
	var resp crewRunStartResponse
	body := crewRunStartRequest{Kind: strings.TrimSpace(kind), Label: strings.TrimSpace(label)}
	if err := c.postJSON(cmd.Context(), path, body, &resp); err != nil {
		return err
	}
	if resp.SupersededRunID != "" {
		if _, err := fmt.Fprintf(out, "A previous run was never ended; it was closed as uncertified (%s).\n", resp.SupersededRunID); err != nil {
			return err
		}
	}
	if !resp.Certified {
		if _, err := fmt.Fprintf(out,
			"WARNING: no tree-write detector on this worktree - %s\nThis run's result will be marked UNCERTIFIED, not verified.\n",
			resp.Run.DetectorReason); err != nil {
			return err
		}
	}
	// Advisory, and phrased as advice. The other member is not asked to stop and
	// this run is not held: what it is for is the one case nothing here verifies -
	// two `xcodebuild` runs against the SHARED DerivedData that is the whole
	// reason this task has one worktree.
	if peer := resp.CrewmateRun; peer != nil {
		label := peer.Label
		if label == "" {
			label = peer.Kind
		}
		who := peer.Role
		if who == "" {
			who = peer.SessionID
		}
		if _, err := fmt.Fprintf(out,
			"NOTE: %s is already running a %s in this worktree (%s). Nothing stops you sharing it,\nbut two builds against one cache is the case AO does not verify - consider waiting.\n",
			who, peer.Kind, label); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "Run %s open (%s). Close it with `ao crew run --end --result pass|fail`.\n", resp.Run.ID, resp.Run.Kind)
	return err
}

func (c *commandContext) endCrewRun(cmd *cobra.Command, path string, out io.Writer, runID, result string) error {
	var resp crewRunEndResponse
	body := crewRunEndRequest{RunID: strings.TrimSpace(runID), Result: strings.TrimSpace(result)}
	if err := c.postJSON(cmd.Context(), path+"/end", body, &resp); err != nil {
		return err
	}
	for _, line := range crewRunEndLines(resp) {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

// crewRunEndLines is what the member is told, and it is deliberately blunt in
// the two cases that matter: a discarded run must never read as the result it
// reported, and an uncertified one must not read as verified.
func crewRunEndLines(resp crewRunEndResponse) []string {
	run := resp.Run
	switch run.Outcome {
	case "discarded":
		lines := []string{
			fmt.Sprintf("DISCARDED - the tree changed under this run, so its result is not trustworthy (attempt %d of %d).", resp.Attempt, resp.MaxAttempts),
		}
		if len(run.ChangedPaths) > 0 {
			lines = append(lines, "  What moved: "+strings.Join(run.ChangedPaths, ", "))
		}
		switch {
		case resp.Escalated:
			lines = append(lines,
				fmt.Sprintf("%d runs discarded in a row. Stop re-running: this task is now NEEDS YOU, and a human decides", resp.MaxAttempts),
				"whether to pause the other member or accept an uncertified result.")
		case resp.Retry:
			lines = append(lines, "Run it again - do not record this result.")
		}
		return lines
	case "uncertified":
		return []string{
			"UNCERTIFIED - nothing was watching the tree, so this result cannot be vouched for.",
			"  Reason: " + run.DetectorReason,
		}
	default:
		verdict := "recorded"
		if run.Result != "" {
			verdict = strings.ToUpper(run.Result)
		}
		return []string{fmt.Sprintf("TRUSTED - the tree did not move; %s stands.", verdict)}
	}
}

func (c *commandContext) listCrewRuns(cmd *cobra.Command, path string, out io.Writer) error {
	var resp crewRunListResponse
	if err := c.getJSON(cmd.Context(), path, &resp); err != nil {
		return err
	}
	if len(resp.Runs) == 0 {
		_, err := fmt.Fprintln(out, "No bracketed runs yet.")
		return err
	}
	for _, run := range resp.Runs {
		if _, err := fmt.Fprintf(out, "%-12s %-7s %-9s %s\n", crewRunStateOf(run), run.Kind, relativeStart(run.StartedAt), run.Label); err != nil {
			return err
		}
	}
	return nil
}

// crewRunStateOf is the CLI's mirror of domain.CrewRun.State: a discarded or
// uncertified run reads as itself, never as the pass/fail it reported.
func crewRunStateOf(run crewRunView) string {
	if run.EndedAt == nil {
		return "running"
	}
	switch run.Outcome {
	case "discarded":
		return "discarded"
	case "uncertified":
		return "uncertified"
	}
	switch run.Result {
	case "pass":
		return "passed"
	case "fail":
		return "failed"
	}
	return "finished"
}

func relativeStart(stamp string) string {
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return ""
	}
	return time.Since(at).Round(time.Second).String() + " ago"
}

func crewRunPath() (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", usageError{errors.New("ao crew run must run inside an AO session (AO_SESSION_ID is not set): a run bracket belongs to the member that made it")}
	}
	return "sessions/" + url.PathEscape(sessionID) + "/crew/runs", nil
}
