package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
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
	// From names the SENDING session, the same way `ao send` does. The daemon
	// needs it because both crew members author against the same target - the
	// checklist belongs to the task, and $AO_CREW_ID is dev's id - so the path
	// cannot say which of them is calling.
	From string `json:"from,omitempty"`
}

// smokeEvidenceClient mirrors domain.SmokeEvidence (display subset). RunID says
// which machine run captured it; empty means it belongs to no run - a capture
// from before AO kept run history, whose result is gone.
type smokeEvidenceClient struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	RunID string `json:"runId"`
}

// smokeRunClient mirrors domain.SmokeRun: one round of a machine running a case.
// A case accumulates these instead of overwriting one result, so `ao smoke list`
// can show that a case used to fail and now passes.
type smokeRunClient struct {
	ID         string     `json:"id"`
	Seq        int        `json:"seq"`
	Verdict    string     `json:"verdict"`
	Note       string     `json:"note"`
	SHA        string     `json:"sha"`
	RecordedAt *time.Time `json:"recordedAt,omitempty"`
}

// smokeCheckClient mirrors domain.SmokeCheck (display subset). It must carry
// every author-supplied field: a field missing here is dropped by
// json.Unmarshal without an error, so the command reports success while losing
// the part of the case a worker needs in order to play it.
type smokeCheckClient struct {
	ID        string                `json:"id"`
	Seq       int                   `json:"seq"`
	Name      string                `json:"name"`
	Why       string                `json:"why"`
	Steps     []string              `json:"steps"`
	Expected  string                `json:"expected"`
	Verdict   string                `json:"verdict"`
	Note      string                `json:"note"`
	PRNum     int                   `json:"prNum"`
	FileRef   string                `json:"fileRef"`
	Evidence  []smokeEvidenceClient `json:"evidence"`
	DecidedAt *time.Time            `json:"decidedAt,omitempty"`
	// The machine's run history, oldest first, and the latest recorded run's
	// result surfaced beside the user's - never merged into it.
	Runs          []smokeRunClient      `json:"runs"`
	AgentVerdict  string                `json:"agentVerdict"`
	AgentNote     string                `json:"agentNote"`
	AgentEvidence []smokeEvidenceClient `json:"agentEvidence"`
	AgentRanAt    *time.Time            `json:"agentRanAt,omitempty"`
	AgentSHA      string                `json:"agentSha"`
	RetiredAt     *time.Time            `json:"retiredAt,omitempty"`
	RetiredReason string                `json:"retiredReason"`
	// Who last wrote this case's authored fields, and when. Empty when AO could
	// not identify the caller.
	AuthoredBy     string     `json:"authoredBy"`
	AuthoredByRole string     `json:"authoredByRole"`
	AuthoredAt     *time.Time `json:"authoredAt,omitempty"`
}

// smokeStandDownClient mirrors domain.SmokeStandDown: a member's recorded
// conclusion that this change needs no human verification. Its absence and its
// presence are the two things an empty checklist used to say at once.
type smokeStandDownClient struct {
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	ByRole string    `json:"byRole"`
	Reason string    `json:"reason"`
}

// smokeCheckResponse mirrors controllers.SmokeCheckResponse.
type smokeCheckResponse struct {
	Check smokeCheckClient `json:"check"`
}

// recordSmokeAgentResultRequest mirrors controllers.RecordSmokeAgentResultInput.
type recordSmokeAgentResultRequest struct {
	Verdict string `json:"verdict,omitempty"`
	Note    string `json:"note,omitempty"`
	SHA     string `json:"sha,omitempty"`
}

// retireSmokeCheckRequest mirrors controllers.RetireSmokeCheckInput.
type retireSmokeCheckRequest struct {
	Reason string `json:"reason"`
}

// listSmokeChecksResponse mirrors controllers.ListSmokeChecksResponse.
type listSmokeChecksResponse struct {
	Worker     string                `json:"worker"`
	ReportedAt *time.Time            `json:"reportedAt,omitempty"`
	Checks     []smokeCheckClient    `json:"checks"`
	StandDown  *smokeStandDownClient `json:"standDown,omitempty"`
}

const smokeSetLong = `Register or replace a session's whole smoke-test checklist (typically 3–6 cases).

The checklist is stored AO-private under ~/.ao, keyed to the session — it is never
written into your checkout. Pass the JSON on stdin (--from-file -) so nothing lands
on your branch. Re-running set is a keyed upsert: a case whose "id" matches an
existing one keeps the user's verdict/note/evidence; new ids are added; ids absent
from the payload are removed.

An id is DERIVED FROM THE NAME when you omit it, so rewording a name produces a
different id and the old case falls out of the payload. AO refuses that call when
the case in question already carries the user's verdict, note or evidence, naming
the cases it would have destroyed: re-send them under their existing ids (add
"id": "<existing-id>" to the case that replaces a reworded one), or ask the user
to Reset the case in the Tests tab first if it should really go, or retire it
with "ao smoke retire" - which keeps its results and the reason it went, and then
lets it fall out of the checklist. Cases nobody has played are still yours to
revise or drop freely.

Naming a RETIRED case's id is refused rather than reviving it. If a retired case
must come back, add it under a new id.

On a crew, BOTH members own this checklist - dev knows what the change actually
touched, qa sees it the way a user will - which is exactly why this command is
the wrong one for two authors: it sets the WHOLE list, so whoever runs it second
takes the other member's cases with it. Use it to author an initial checklist,
then reach for ` + "`ao smoke add`" + ` / ` + "`edit`" + ` / ` + "`remove`" + `, which touch one case each.
Every write records who made it and when, and the Tests tab shows it.

The JSON is { "cases": [ ... ] } (a bare [ ... ] array is also accepted). Each case:

  {
    "id":       "gitlab-mr-appears",   // optional; derived from name when omitted.
                                       //   Supply it to keep results across a rename.
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
	cmd.AddCommand(newSmokeAddCommand(ctx))
	cmd.AddCommand(newSmokeEditCommand(ctx))
	cmd.AddCommand(newSmokeRemoveCommand(ctx))
	cmd.AddCommand(newSmokeStandDownCommand(ctx))
	cmd.AddCommand(newSmokeListCommand(ctx))
	cmd.AddCommand(newSmokeRecordCommand(ctx))
	cmd.AddCommand(newSmokeRetireCommand(ctx))
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
	cmd.Flags().SetNormalizeFunc(underscoreFlagNames)
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
	body := authorSmokeChecksRequest{Cases: cases, From: callingSessionID()}
	if err := c.putJSON(cmd.Context(), path, body, &res); err != nil {
		return explainSmokeRefusal(err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "authored %d smoke check(s) for %s\n", len(res.Checks), session)
	return err
}

// explainSmokeRefusal turns the daemon's authoring refusals into a usage error
// (exit 2), because neither is a failure of the command: SMOKE_RESULTS_AT_RISK
// means the write would destroy results the user recorded and already names
// which cases and the way out, and SMOKE_CASE_RETIRED means the case is frozen
// and says when it went and why. Anything else passes through unchanged.
func explainSmokeRefusal(err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.ErrorBody.Code != "SMOKE_RESULTS_AT_RISK" && apiErr.ErrorBody.Code != "SMOKE_CASE_RETIRED" {
		return err
	}
	if strings.TrimSpace(apiErr.ErrorBody.Message) == "" {
		return err
	}
	return usageError{errors.New(apiErr.ErrorBody.Message)}
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

const smokeListLong = `Print a session's smoke-test checklist with its play results.

Each case is printed in full — why it matters, its numbered steps and the
expected result — so it can be played straight from this output without opening
the Tests tab or calling the API. Pass --brief for one line per case (plus
ref/note/evidence) when you only want to scan verdicts.

A case can carry TWO results and they are printed separately: the user's verdict
(the "[…]" on the CHECK line) and, indented under it, a machine's result from
` + "`ao smoke record`" + `. Only the user's decides whether the case is confirmed - a
machine answers "did the steps run", not "does this work for a person".

Verdicts, notes and evidence are the user's to record while playing the case
live in the app; there is no CLI command that sets them.

Retired cases are listed last, with the reason they went and the results they
kept. They are no longer part of what the user is asked to play.`

func newSmokeListCommand(ctx *commandContext) *cobra.Command {
	var session string
	var asJSON, brief bool
	cmd := &cobra.Command{
		Use:   "list [session]",
		Short: "Print a session's smoke-test checklist with its play results",
		Long:  smokeListLong,
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.listSmokeChecklist(cmd, args, session, asJSON, brief)
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the raw JSON response")
	cmd.Flags().BoolVar(&brief, "brief", false, "Condense each case to one line, omitting why/steps/expected")
	return cmd
}

func (c *commandContext) listSmokeChecklist(cmd *cobra.Command, args []string, session string, asJSON, brief bool) error {
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
		if res.StandDown != nil {
			_, err := fmt.Fprintf(out, "%s stood down on %s: %s\n", standDownActor(*res.StandDown), session, res.StandDown.Reason)
			return err
		}
		_, err := fmt.Fprintf(out, "no smoke checks for %s - nobody has decided what a person should look at yet\n", session)
		return err
	}
	lines := []string{fmt.Sprintf("smoke checklist for %s (worker: %s)", session, res.Worker)}
	retired := 0
	for _, check := range res.Checks {
		if check.RetiredAt != nil {
			retired++
			lines = append(lines,
				fmt.Sprintf("  RETIRED  id %s  %s", check.ID, check.Name),
				"        reason: "+check.RetiredReason,
				fmt.Sprintf("        kept: user verdict %s%s", smokeVerdictLabel(check.Verdict), evidenceSuffix(len(check.Evidence))))
			continue
		}
		lines = append(lines,
			fmt.Sprintf("  CHECK %d [%s] %s", check.Seq, smokeVerdictLabel(check.Verdict), check.Name),
			"        id: "+check.ID)
		if by := smokeAuthorLine(check); by != "" {
			lines = append(lines, "        "+by)
		}
		if ref := smokeCaseRef(check); ref != "" {
			lines = append(lines, "        "+ref)
		}
		if !brief {
			lines = append(lines, smokeCaseBody(check)...)
		}
		if note := strings.TrimSpace(check.Note); note != "" {
			lines = append(lines, "        note: "+note)
		}
		if n := len(check.Evidence); n > 0 {
			lines = append(lines, fmt.Sprintf("        evidence: %d attached", n))
		}
		lines = append(lines, smokeAgentLines(check)...)
	}
	if retired > 0 {
		lines = append(lines, fmt.Sprintf("retired: %d case(s), kept with their results", retired))
	}
	if res.ReportedAt != nil {
		lines = append(lines, "reported: "+res.ReportedAt.Format(time.RFC3339))
	}
	if res.StandDown != nil {
		lines = append(lines, fmt.Sprintf("stood down by %s: %s", standDownActor(*res.StandDown), res.StandDown.Reason))
	}
	_, err := fmt.Fprintln(out, strings.Join(lines, "\n"))
	return err
}

// smokeAgentLines renders the MACHINE's result for a case, always as its own
// lines under the user's. Nothing here is folded into the user's verdict: a
// reader must be able to see at a glance that a machine ran the steps and that a
// person still has not confirmed the case works.
func smokeAgentLines(check smokeCheckClient) []string {
	if check.AgentVerdict == "" && check.AgentRanAt == nil && len(check.AgentEvidence) == 0 && len(check.Runs) == 0 {
		return nil
	}
	head := "        agent: "
	if check.AgentVerdict == "" {
		head += "ran, did not judge"
	} else {
		head += smokeVerdictLabel(check.AgentVerdict)
	}
	if check.AgentSHA != "" {
		head += " at " + shortSHA(check.AgentSHA)
	}
	if check.AgentRanAt != nil {
		head += " on " + check.AgentRanAt.Format(time.RFC3339)
	}
	// The CURRENT result's round, not the newest row: a round the machine opened
	// and never concluded is not the result being printed above it.
	if current, ok := latestRecordedRun(check); ok && len(check.Runs) > 1 {
		head += fmt.Sprintf(" (run %d of %d)", current.Seq, len(check.Runs))
	}
	lines := []string{head}
	if note := strings.TrimSpace(check.AgentNote); note != "" {
		lines = append(lines, "        agent note: "+note)
	}
	if n := len(check.AgentEvidence); n > 0 {
		line := fmt.Sprintf("        agent evidence: %d captured", n)
		// Captures from before AO kept run history belong to no run, and the
		// result they were taken for is gone. Saying so is the point: reading
		// them as evidence for the current verdict is the mistake this avoids.
		if unknown := smokeUnknownRunEvidence(check); unknown > 0 {
			line += fmt.Sprintf(" (%d from an unknown run)", unknown)
		}
		lines = append(lines, line)
	}
	return append(lines, smokeEarlierRunLines(check)...)
}

// latestRecordedRun is the case's current machine result: the last round that
// actually concluded. A round still open carries no result, and treating it as
// one would let a crashed run stand in for a real verdict.
func latestRecordedRun(check smokeCheckClient) (smokeRunClient, bool) {
	for i := len(check.Runs) - 1; i >= 0; i-- {
		if check.Runs[i].RecordedAt != nil {
			return check.Runs[i], true
		}
	}
	return smokeRunClient{}, false
}

// smokeEarlierRunLines prints every round EXCEPT the one whose result is printed
// above, newest first. They are what a single overwritten result could never
// show: that a case used to fail at one commit and passes at another.
func smokeEarlierRunLines(check smokeCheckClient) []string {
	if len(check.Runs) < 2 {
		return nil
	}
	current, _ := latestRecordedRun(check)
	var lines []string
	for i := len(check.Runs) - 1; i >= 0; i-- {
		run := check.Runs[i]
		if run.ID == current.ID {
			continue
		}
		line := fmt.Sprintf("        run %d: ", run.Seq)
		switch {
		case run.RecordedAt == nil:
			line += "never concluded"
		case run.Verdict == "":
			line += "ran, did not judge"
		default:
			line += smokeVerdictLabel(run.Verdict)
		}
		if run.SHA != "" {
			line += " at " + shortSHA(run.SHA)
		}
		if run.RecordedAt != nil {
			line += " on " + run.RecordedAt.Format(time.RFC3339)
		}
		if n := smokeRunEvidence(check, run.ID); n > 0 {
			line += fmt.Sprintf(", %d captured", n)
		}
		if note := strings.TrimSpace(run.Note); note != "" {
			line += " - " + note
		}
		lines = append(lines, line)
	}
	return lines
}

func smokeRunEvidence(check smokeCheckClient, runID string) int {
	n := 0
	for _, ev := range check.AgentEvidence {
		if ev.RunID == runID {
			n++
		}
	}
	return n
}

func smokeUnknownRunEvidence(check smokeCheckClient) int { return smokeRunEvidence(check, "") }

// smokeAuthorLine names who last wrote a case's authored fields. Both crew
// members write this list, so which of them a case came from is part of reading
// it: dev writes from the call sites, qa from what a user would do. A case AO
// could not attribute - written by the desktop app, a direct API call or an
// older `ao` - prints no author rather than a guessed one.
func smokeAuthorLine(check smokeCheckClient) string {
	who := strings.TrimSpace(check.AuthoredBy)
	if who == "" {
		return ""
	}
	if role := strings.TrimSpace(check.AuthoredByRole); role != "" {
		who = role + " @" + who
	} else {
		who = "@" + who
	}
	if check.AuthoredAt != nil {
		return "by: " + who + " on " + check.AuthoredAt.Format(time.RFC3339)
	}
	return "by: " + who
}

// standDownActor names who concluded nothing here needs a person, falling back
// to the neutral "the worker" when AO could not identify the caller.
func standDownActor(sd smokeStandDownClient) string {
	if role := strings.TrimSpace(sd.ByRole); role != "" {
		return role
	}
	if by := strings.TrimSpace(sd.By); by != "" {
		return "@" + by
	}
	return "the worker"
}

func evidenceSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d evidence file(s)", n)
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

// smokeCaseBody renders the author-supplied part of a case — why it matters,
// the numbered steps and the expected result — so the reader can play it
// directly. Each field is omitted when the case does not carry it, so an older
// checklist prints no empty scaffolding.
func smokeCaseBody(check smokeCheckClient) []string {
	var lines []string
	if why := strings.TrimSpace(check.Why); why != "" {
		lines = append(lines, "        why: "+why)
	}
	steps := make([]string, 0, len(check.Steps))
	for _, step := range check.Steps {
		if step = strings.TrimSpace(step); step != "" {
			steps = append(steps, step)
		}
	}
	if len(steps) > 0 {
		lines = append(lines, "        steps:")
		for i, step := range steps {
			lines = append(lines, fmt.Sprintf("          %d. %s", i+1, step))
		}
	}
	if expected := strings.TrimSpace(check.Expected); expected != "" {
		lines = append(lines, "        expected: "+expected)
	}
	return lines
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

const smokeRecordLong = `Record a MACHINE's result for one smoke-test case, beside the user's.

A case carries two results, and they are never merged. This one answers "did the
steps run" - the user's answers "does this actually work for a person". Every
regression a person has caught by hand (recording latency, dead drag-scroll,
keystrokes never arriving, a tab pausing when unfocused, control lost after a
lease lapse) lives in the gap between those two questions, so a recorded pass
NEVER stands in for the user's verdict and never opens the merge gate. It moves
a label; the person still plays the case.

The command is additive by construction: it cannot rewrite a case's authored
content (name/why/steps/expected), cannot touch the user's verdict, note or
evidence, and cannot remove a case. Running it again re-records this machine's
result, which is what re-running a case means.

  ao smoke record "$AO_SESSION_ID" --case mr-appears --verdict pass \
      --note "3 runs, MR listed within 40s each time"

--sha is the commit the case was run against, so a reader can tell a fresh
result from one that predates the current head. It defaults to the HEAD of the
git repository in the current directory, which inside a session's worktree is
exactly right; pass it explicitly to override.

Attach what the machine saw with --evidence (repeatable). Those files land in
the case's OWN agent-evidence list, never in the user's - evidence is what you go
back to when you distrust a verdict, so it must always be obvious who produced
it. Attaching evidence without --verdict is a legitimate record: it says "I ran
it and captured this, I am not the one who can judge it", which is the permanent
state of a paint/focus/timing/feel case.

  ao smoke record "$AO_SESSION_ID" --case tab-stays-live --evidence /tmp/shot.png

--verdict skip is the third answer, and the only one that says nothing about the
app: "this machine could not run this case". It REQUIRES --note, because a
reasonless skip is indistinguishable from the case nobody got to - and the reason
has to come from an ATTEMPT. "The agent cannot press and hold" is a finding after
you have tried it and a guess before it, and the note is where a person can tell
which one they are reading. It is not a way out of judging a case you DID drive:
that one is --evidence with no --verdict.

  ao smoke record "$AO_CREW_ID" --case press-hold --verdict skip \
      --note "tried ao sim drag with a 1.2s hold; the menu never opened, so nothing was exercised"`

func newSmokeRecordCommand(ctx *commandContext) *cobra.Command {
	var session, caseID, verdict, note, sha string
	var evidence []string
	cmd := &cobra.Command{
		Use:   "record [session]",
		Short: "Record a machine's result for a smoke-test case (never the user's)",
		Long:  smokeRecordLong,
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.recordSmokeAgentResult(cmd, args, smokeRecordOptions{
				session:  session,
				caseID:   caseID,
				verdict:  verdict,
				note:     note,
				sha:      sha,
				evidence: evidence,
			})
		},
	}
	cmd.Flags().SetNormalizeFunc(underscoreFlagNames)
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&caseID, "case", "", "Case id to record against (required; see `ao smoke list`)")
	cmd.Flags().StringVar(&verdict, "verdict", "", "pass | fail | skip. Omit for an evidence-only record. `skip` means THIS MACHINE could not run the case and needs --note saying why.")
	cmd.Flags().StringVar(&note, "note", "", "What the machine saw")
	cmd.Flags().StringVar(&sha, "sha", "", "Commit the case was run against (default: HEAD of the repo in the current directory)")
	cmd.Flags().StringArrayVar(&evidence, "evidence", nil, "Path to a screenshot/clip the machine captured (repeatable)")
	return cmd
}

// smokeRecordOptions groups the record command's flags so the runner stays
// readable as the flag list grows.
type smokeRecordOptions struct {
	session  string
	caseID   string
	verdict  string
	note     string
	sha      string
	evidence []string
}

func (c *commandContext) recordSmokeAgentResult(cmd *cobra.Command, args []string, opts smokeRecordOptions) error {
	session := resolveSmokeSession(args, opts.session)
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	caseID := strings.TrimSpace(opts.caseID)
	if caseID == "" {
		return usageError{errors.New("usage: --case <id> is required (run `ao smoke list` to see the ids)")}
	}
	verdict := strings.TrimSpace(opts.verdict)
	switch verdict {
	case "", "pass", "fail", "skip":
	default:
		return usageError{fmt.Errorf("usage: --verdict must be pass, fail, or skip (got %q); omit it for an evidence-only record", verdict)}
	}
	if verdict == "" && len(opts.evidence) == 0 {
		return usageError{errors.New("usage: give --verdict pass|fail|skip, or --evidence <path> to record what the machine saw without judging it")}
	}
	// skip is the only verdict that answers nothing about the app, so it is the
	// only one that has to say why - and the reason has to come from an ATTEMPT.
	if verdict == "skip" && strings.TrimSpace(opts.note) == "" {
		return usageError{errors.New("usage: --verdict skip needs --note saying WHY this machine could not run the case, and what you tried; a reasonless skip is indistinguishable from a case nobody got to")}
	}
	base := "sessions/" + url.PathEscape(session) + "/smoke-checks/" + url.PathEscape(caseID)
	// Evidence first: an evidence-only record is only accepted once the case
	// actually carries some, and a failed upload should stop before a verdict
	// claims a run that produced nothing.
	for _, path := range opts.evidence {
		if err := c.uploadAgentEvidence(cmd, base, path); err != nil {
			return err
		}
	}
	sha := strings.TrimSpace(opts.sha)
	if sha == "" {
		sha = c.headSHA(cmd.Context())
	}
	var res smokeCheckResponse
	body := recordSmokeAgentResultRequest{Verdict: verdict, Note: strings.TrimSpace(opts.note), SHA: sha}
	if err := c.postJSON(cmd.Context(), base+"/agent-result", body, &res); err != nil {
		return err
	}
	summary := "no verdict (evidence only)"
	if res.Check.AgentVerdict != "" {
		summary = "agent verdict " + res.Check.AgentVerdict
	}
	if res.Check.AgentSHA != "" {
		summary += " at " + shortSHA(res.Check.AgentSHA)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "recorded %s on %q - the user's verdict is unchanged (%s)\n",
		summary, res.Check.Name, smokeVerdictLabel(res.Check.Verdict))
	return err
}

// uploadAgentEvidence streams one captured file to the case's evidence endpoint
// tagged as the machine's, so it can never be mistaken for something a person
// attached.
func (c *commandContext) uploadAgentEvidence(cmd *cobra.Command, base, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return usageError{errors.New("usage: --evidence needs a file path")}
	}
	contentType, ok := evidenceContentType(path)
	if !ok {
		return usageError{fmt.Errorf("usage: %s is not an accepted evidence type (png, jpg, gif, webp, mp4, webm, mov)", filepath.Base(path))}
	}
	file, err := os.Open(path) // #nosec G304 -- the operator names the file to attach.
	if err != nil {
		return usageError{fmt.Errorf("read evidence: %w", err)}
	}
	defer func() { _ = file.Close() }()
	return c.postBytes(cmd.Context(), base+"/evidence?source=agent", contentType, filepath.Base(path), file, nil)
}

// evidenceContentTypes mirrors the daemon's upload allow-list so a wrong file
// fails here, before its bytes travel.
var evidenceContentTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
}

func evidenceContentType(path string) (string, bool) {
	ct, ok := evidenceContentTypes[strings.ToLower(filepath.Ext(path))]
	return ct, ok
}

// headSHA reads the current commit of the repository in the working directory.
// Best-effort: outside a repo, or with git unavailable, a recorded result simply
// carries no commit rather than failing the command. The output is checked to
// LOOK like a commit before it is used - it comes from a combined stdout+stderr
// read, and a stale-looking sha would be read as a real staleness signal, which
// is worse than recording none.
func (c *commandContext) headSHA(ctx context.Context) string {
	if c.deps.CommandOutput == nil {
		return ""
	}
	out, err := c.deps.CommandOutput(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(out))
	if !commitSHA.MatchString(sha) {
		return ""
	}
	return sha
}

// commitSHA matches a bare git object name, and nothing that came along with it.
var commitSHA = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

const smokeRetireLong = `Retire a smoke-test case out of a session's checklist.

Retire is NOT delete. The case stops being something the user is asked to play,
and everything about it stays: its name, its steps, the verdict, note and
evidence the user recorded, plus the reason it went and when. That trace is the
whole point - "retired 3, now covered by tests" is worth far more than three
cases quietly disappearing, and results the user recorded are the one part of a
checklist AO cannot regenerate.

This is the sanctioned way to remove a case the user has already played. ` + "`ao smoke set`" + `
refuses to drop one (SMOKE_RESULTS_AT_RISK) precisely because dropping it would
destroy those results; retire keeps them and then lets the case fall out of the
checklist. Cases nobody has played are still yours to revise or drop freely with
` + "`ao smoke set`" + ` - retiring one is for a case that mattered.

  ao smoke retire "$AO_SESSION_ID" --case drag-scroll --reason "now covered by TestDragScroll"

A retired case is frozen: it takes no verdict, no reset and no machine result,
and naming its id in a later ` + "`ao smoke set`" + ` payload is refused rather than
reviving it. If the case genuinely needs to come back, add it under a NEW id -
it comes back unplayed, which is right, because the old results were recorded
against the old steps.`

func newSmokeRetireCommand(ctx *commandContext) *cobra.Command {
	var session, caseID, reason string
	cmd := &cobra.Command{
		Use:   "retire [session]",
		Short: "Retire a case out of the checklist, keeping its results and the reason it went",
		Long:  smokeRetireLong,
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.retireSmokeCheck(cmd, args, session, caseID, reason)
		},
	}
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&caseID, "case", "", "Case id to retire (required; see `ao smoke list`)")
	cmd.Flags().StringVar(&reason, "reason", "", "Why it is no longer worth playing (required)")
	return cmd
}

func (c *commandContext) retireSmokeCheck(cmd *cobra.Command, args []string, session, caseID, reason string) error {
	session = resolveSmokeSession(args, session)
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	caseID = strings.TrimSpace(caseID)
	if caseID == "" {
		return usageError{errors.New("usage: --case <id> is required (run `ao smoke list` to see the ids)")}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return usageError{errors.New("usage: --reason is required - it is the trace retiring leaves behind, e.g. --reason \"now covered by TestFoo\"")}
	}
	path := "sessions/" + url.PathEscape(session) + "/smoke-checks/" + url.PathEscape(caseID) + "/retire"
	var res smokeCheckResponse
	if err := c.postJSON(cmd.Context(), path, retireSmokeCheckRequest{Reason: reason}, &res); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "retired %q - %s. Its results are kept; it is no longer part of the checklist.\n",
		res.Check.Name, res.Check.RetiredReason)
	return err
}
