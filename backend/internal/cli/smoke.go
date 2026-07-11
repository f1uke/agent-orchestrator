package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// smokeAuthoredCaseInput mirrors controllers.SmokeAuthoredCaseInput.
type smokeAuthoredCaseInput struct {
	ID       string   `json:"id,omitempty"`
	Tag      string   `json:"tag,omitempty"`
	Name     string   `json:"name"`
	Why      string   `json:"why,omitempty"`
	Steps    []string `json:"steps,omitempty"`
	Expected string   `json:"expected,omitempty"`
	PRNum    int      `json:"prNum,omitempty"`
	FileRef  string   `json:"fileRef,omitempty"`
}

// authorSmokeChecksRequest mirrors controllers.AuthorSmokeChecksInput.
type authorSmokeChecksRequest struct {
	Cases []smokeAuthoredCaseInput `json:"cases"`
}

// smokeEvidenceClient mirrors domain.SmokeEvidence (display subset).
type smokeEvidenceClient struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// smokeCheckClient mirrors domain.SmokeCheck (display subset).
type smokeCheckClient struct {
	ID        string                `json:"id"`
	Seq       int                   `json:"seq"`
	Name      string                `json:"name"`
	Verdict   string                `json:"verdict"`
	Note      string                `json:"note"`
	PRNum     int                   `json:"prNum"`
	FileRef   string                `json:"fileRef"`
	Evidence  []smokeEvidenceClient `json:"evidence"`
	DecidedAt *time.Time            `json:"decidedAt,omitempty"`
}

// listSmokeChecksResponse mirrors controllers.ListSmokeChecksResponse.
type listSmokeChecksResponse struct {
	Worker     string             `json:"worker"`
	ReportedAt *time.Time         `json:"reportedAt,omitempty"`
	Checks     []smokeCheckClient `json:"checks"`
}

const smokeSetLong = `Register or replace a session's whole smoke-test checklist (typically 3–6 cases).

The checklist is stored AO-private under ~/.ao, keyed to the session — it is never
written into your checkout. Pass the JSON on stdin (--from-file -) so nothing lands
on your branch. Re-running set is a keyed upsert: a case whose "id" matches an
existing one keeps the user's verdict/note/evidence; new ids are added; ids absent
from the payload are removed.

The JSON is { "cases": [ ... ] } (a bare [ ... ] array is also accepted). Each case:

  {
    "id":       "gitlab-mr-appears",   // optional; derived from name when omitted.
                                       //   Supply it to keep results across a re-author.
    "name":     "A fresh MR shows up in Reviews on its own",   // required
    "why":      "Confirms re-polling surfaces a new MR without a manual refresh.",
    "steps":    ["Open the Reviews tab.", "Open a new MR.", "Wait ~60s."],
    "expected": "The new MR appears automatically with CI + review status.",
    "prNum":    36,                    // PR/MR number the change belongs to (0 if none)
    "fileRef":  "scmobserver.go:936"   // file:line the change touched
  }

Example:

  cat <<'JSON' | ao smoke set "$AO_SESSION_ID" --from-file -
  { "cases": [ { "name": "…", "why": "…", "steps": ["…"], "expected": "…", "prNum": 36, "fileRef": "f.go:1" } ] }
  JSON`

func newSmokeCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smoke",
		Short: "Author and read a session's manual smoke-test checklist",
	}
	cmd.AddCommand(newSmokeSetCommand(ctx))
	cmd.AddCommand(newSmokeListCommand(ctx))
	return cmd
}

func newSmokeSetCommand(ctx *commandContext) *cobra.Command {
	var session, fromFile string
	cmd := &cobra.Command{
		Use:   "set [session]",
		Short: "Author/replace a session's smoke-test checklist from JSON",
		Long:  smokeSetLong,
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.setSmokeChecklist(cmd, args, session, fromFile)
		},
	}
	// Agents routinely spell flags with underscores (--from_file); normalize both.
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
	})
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "Path to the checklist JSON, or - to read from stdin (required)")
	return cmd
}

func (c *commandContext) setSmokeChecklist(cmd *cobra.Command, args []string, session, fromFile string) error {
	session = resolveSmokeSession(args, session)
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	fromFile = strings.TrimSpace(fromFile)
	if fromFile == "" {
		return usageError{errors.New("usage: --from-file <path|-> is required")}
	}
	cases, err := readSmokeCases(cmd, fromFile)
	if err != nil {
		return err
	}
	path := "sessions/" + url.PathEscape(session) + "/smoke-checks"
	var res listSmokeChecksResponse
	if err := c.putJSON(cmd.Context(), path, authorSmokeChecksRequest{Cases: cases}, &res); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "authored %d smoke check(s) for %s\n", len(res.Checks), session)
	return err
}

// readSmokeCases reads the checklist JSON from a file or stdin ("-"). It accepts
// either the wrapper object { "cases": [ … ] } or a bare [ … ] array, choosing
// by the first non-space byte so nothing is written into the worker's checkout
// (mirroring how `ao review submit --reviews -` reads from stdin).
func readSmokeCases(cmd *cobra.Command, fromFile string) ([]smokeAuthoredCaseInput, error) {
	var raw []byte
	var err error
	if fromFile == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(fromFile)
	}
	if err != nil {
		return nil, usageError{fmt.Errorf("read checklist: %w", err)}
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, usageError{errors.New("usage: the checklist JSON is empty")}
	}
	var cases []smokeAuthoredCaseInput
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &cases); err != nil {
			return nil, usageError{fmt.Errorf("decode checklist JSON: %w", err)}
		}
	} else {
		var req authorSmokeChecksRequest
		if err := json.Unmarshal(trimmed, &req); err != nil {
			return nil, usageError{fmt.Errorf("decode checklist JSON: %w", err)}
		}
		cases = req.Cases
	}
	if len(cases) == 0 {
		return nil, usageError{errors.New("usage: the checklist must contain at least one case")}
	}
	return cases, nil
}

func newSmokeListCommand(ctx *commandContext) *cobra.Command {
	var session string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list [session]",
		Short: "Print a session's smoke-test checklist with its play results",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.listSmokeChecklist(cmd, args, session, asJSON)
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the raw JSON response")
	return cmd
}

func (c *commandContext) listSmokeChecklist(cmd *cobra.Command, args []string, session string, asJSON bool) error {
	session = resolveSmokeSession(args, session)
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	path := "sessions/" + url.PathEscape(session) + "/smoke-checks"
	var res listSmokeChecksResponse
	if err := c.getJSON(cmd.Context(), path, &res); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	if len(res.Checks) == 0 {
		_, err := fmt.Fprintf(out, "no smoke checks for %s\n", session)
		return err
	}
	lines := []string{fmt.Sprintf("smoke checklist for %s (worker: %s)", session, res.Worker)}
	for _, check := range res.Checks {
		lines = append(lines, fmt.Sprintf("  CHECK %d [%s] %s", check.Seq, smokeVerdictLabel(check.Verdict), check.Name))
		if ref := smokeCaseRef(check); ref != "" {
			lines = append(lines, "        "+ref)
		}
		if note := strings.TrimSpace(check.Note); note != "" {
			lines = append(lines, "        note: "+note)
		}
		if n := len(check.Evidence); n > 0 {
			lines = append(lines, fmt.Sprintf("        evidence: %d attached", n))
		}
	}
	if res.ReportedAt != nil {
		lines = append(lines, "reported: "+res.ReportedAt.Format(time.RFC3339))
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

func resolveSmokeSession(args []string, session string) string {
	session = strings.TrimSpace(session)
	if len(args) == 1 {
		session = strings.TrimSpace(args[0])
	}
	return session
}

func smokeVerdictLabel(v string) string {
	switch v {
	case "pass":
		return "PASS"
	case "fail":
		return "FAIL"
	case "skip":
		return "SKIP"
	default:
		return "to check"
	}
}

func smokeCaseRef(check smokeCheckClient) string {
	parts := make([]string, 0, 2)
	if check.PRNum > 0 {
		parts = append(parts, fmt.Sprintf("PR #%d", check.PRNum))
	}
	if check.FileRef != "" {
		parts = append(parts, check.FileRef)
	}
	return strings.Join(parts, " · ")
}
