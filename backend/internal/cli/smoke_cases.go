package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// addSmokeCasesRequest mirrors controllers.AddSmokeCasesInput.
type addSmokeCasesRequest struct {
	Cases []smokeAuthoredCaseInput `json:"cases"`
	From  string                   `json:"from,omitempty"`
}

// editSmokeCaseRequest mirrors controllers.EditSmokeCaseInput. Every field is a
// POINTER so "not named" and "set to empty" travel as different JSON: a nil
// field is omitted and the stored value survives, which is the whole contract of
// a partial edit.
type editSmokeCaseRequest struct {
	Name     *string   `json:"name,omitempty"`
	Why      *string   `json:"why,omitempty"`
	Steps    *[]string `json:"steps,omitempty"`
	Expected *string   `json:"expected,omitempty"`
	PRNum    *int      `json:"prNum,omitempty"`
	FileRef  *string   `json:"fileRef,omitempty"`
	From     string    `json:"from,omitempty"`
}

// removeSmokeCaseRequest mirrors controllers.RemoveSmokeCaseInput.
type removeSmokeCaseRequest struct {
	From string `json:"from,omitempty"`
}

// standDownSmokeRequest mirrors controllers.StandDownSmokeChecklistInput.
type standDownSmokeRequest struct {
	Reason string `json:"reason"`
	From   string `json:"from,omitempty"`
}

const smokeAddLong = `Add cases to a session's checklist without touching the ones already on it.

This is the write to use when a task has TWO agents on it. ` + "`ao smoke set`" + ` sets the
WHOLE list, so whoever calls it second decides what the list contains - and takes
the other member's cases (and the human's verdicts, notes and screenshots) with
it. ` + "`add`" + ` only ever reaches the cases you name, so dev and qa can both write
without arranging turns: dev knows what the change actually touched, qa sees it
the way a user will, and both belong on one list.

A case whose id is ALREADY on the checklist is edited in place and keeps the
user's verdict, note and evidence. A new id is appended after the last case.
Naming a RETIRED case's id is refused rather than reviving it.

The JSON is exactly what ` + "`ao smoke set`" + ` takes (see ` + "`ao smoke set --help`" + ` for the
per-field schema), and giving each case an EXPLICIT "id" is what makes a later
edit land on it rather than on a new copy of it.

  cat <<'JSON' | ao smoke add "$AO_CREW_ID" --from-file -
  { "cases": [ { "id": "drag-scroll", "name": "…", "why": "…", "steps": ["…"], "expected": "…", "prNum": 0, "fileRef": "f.go:1" } ] }
  JSON

To change ONE field of a case that already exists, use ` + "`ao smoke edit`" + ` instead -
re-sending a whole case here would overwrite whatever your crewmate improved
about the rest of it in the meantime.`

const smokeEditLong = `Edit one case's authored fields. A flag you do not pass is a field left alone.

That is the point of a separate verb. Re-sending a whole case just to fix its
fileRef silently overwrites the wording your crewmate sharpened while you were
typing, and the loss looks exactly like nothing having happened.

  ao smoke edit "$AO_CREW_ID" --case drag-scroll --pr 264
  ao smoke edit "$AO_CREW_ID" --case drag-scroll --step "Open the Tests tab." --step "Drag the list."

--step is repeatable and REPLACES the whole step list (pass every step you want
the case to end up with). Passing --step once with an empty value clears them.

The user's verdict, note and evidence are not reachable from this command, and a
retired case is frozen: edit it and you are told when it went and why.`

const smokeRemoveLong = `Remove one case from a session's checklist.

A case NOBODY has played comes straight off - that is the ordinary way a
checklist gets it wrong and is corrected.

A case the user HAS played is refused. Their verdict, note and evidence are the
one part of a checklist AO cannot regenerate, and a judgement that disappears
with no trace is worse than a list that stays one case too long. Retire that one
instead - it keeps everything and records why the case went:

  ao smoke retire "$AO_CREW_ID" --case drag-scroll --reason "now covered by TestDragScroll"`

const smokeStandDownLong = `Record that this change needs NO human verification, and why.

An empty Tests tab says two opposite things at once: nobody has decided yet, or
somebody looked and there is nothing here worth your eyes. Those rendered
identically, so the screen could not be read - this is how you say the second one
out loud.

  ao smoke stand-down "$AO_CREW_ID" --reason "pure refactor; behavior is covered by TestReplaceSmokeChecks"

The reason IS the command: "nothing to check" with no account of what you looked
at is not an answer. It is refused while the checklist still has cases to play,
because the claim cannot stand beside them, and it is retracted automatically the
moment anyone adds a case.`

func newSmokeAddCommand(ctx *commandContext) *cobra.Command {
	var session, fromFile string
	cmd := &cobra.Command{
		Use:   "add [session]",
		Short: "Add or edit cases without replacing the rest of the checklist",
		Long:  smokeAddLong,
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.addSmokeCases(cmd, args, session, fromFile)
		},
	}
	cmd.Flags().SetNormalizeFunc(underscoreFlagNames)
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&fromFile, "from-file", "", "Path to the cases JSON, or - to read from stdin (required)")
	return cmd
}

func (c *commandContext) addSmokeCases(cmd *cobra.Command, args []string, session, fromFile string) error {
	session = resolveSmokeSession(args, session)
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	if strings.TrimSpace(fromFile) == "" {
		return usageError{errors.New("usage: --from-file <path|-> is required")}
	}
	cases, err := readSmokeCases(cmd, strings.TrimSpace(fromFile))
	if err != nil {
		return err
	}
	path := "sessions/" + url.PathEscape(session) + "/smoke-checks"
	var res listSmokeChecksResponse
	body := addSmokeCasesRequest{Cases: cases, From: callingSessionID()}
	if err := c.patchJSON(cmd.Context(), path, body, &res); err != nil {
		return explainSmokeRefusal(err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "added/edited %d case(s); the checklist for %s now has %d\n",
		len(cases), session, len(res.Checks))
	return err
}

func newSmokeEditCommand(ctx *commandContext) *cobra.Command {
	var session, caseID, name, why, expected, fileRef string
	var steps []string
	var prNum int
	cmd := &cobra.Command{
		Use:   "edit [session]",
		Short: "Edit one case's authored fields, leaving the ones you don't name alone",
		Long:  smokeEditLong,
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			patch := editSmokeCaseRequest{From: callingSessionID()}
			flags := cmd.Flags()
			if flags.Changed("name") {
				patch.Name = &name
			}
			if flags.Changed("why") {
				patch.Why = &why
			}
			if flags.Changed("expected") {
				patch.Expected = &expected
			}
			if flags.Changed("file-ref") {
				patch.FileRef = &fileRef
			}
			if flags.Changed("pr") {
				patch.PRNum = &prNum
			}
			if flags.Changed("step") {
				patch.Steps = &steps
			}
			return ctx.editSmokeCase(cmd, args, session, caseID, patch)
		},
	}
	cmd.Flags().SetNormalizeFunc(underscoreFlagNames)
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&caseID, "case", "", "Case id to edit (required; see `ao smoke list`)")
	cmd.Flags().StringVar(&name, "name", "", "One-line 'what to verify'")
	cmd.Flags().StringVar(&why, "why", "", "Why the case matters")
	cmd.Flags().StringArrayVar(&steps, "step", nil, "One play step; repeat for each, REPLACING the stored list")
	cmd.Flags().StringVar(&expected, "expected", "", "The expected result")
	cmd.Flags().IntVar(&prNum, "pr", 0, "PR/MR number the case belongs to")
	cmd.Flags().StringVar(&fileRef, "file-ref", "", "file:line the change touched")
	return cmd
}

func (c *commandContext) editSmokeCase(cmd *cobra.Command, args []string, session, caseID string, patch editSmokeCaseRequest) error {
	session = resolveSmokeSession(args, session)
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	caseID = strings.TrimSpace(caseID)
	if caseID == "" {
		return usageError{errors.New("usage: --case <id> is required (run `ao smoke list` to see the ids)")}
	}
	if patch.Name == nil && patch.Why == nil && patch.Steps == nil && patch.Expected == nil && patch.PRNum == nil && patch.FileRef == nil {
		return usageError{errors.New("usage: name at least one field to change (--name, --why, --step, --expected, --pr, --file-ref)")}
	}
	path := "sessions/" + url.PathEscape(session) + "/smoke-checks/" + url.PathEscape(caseID)
	var res smokeCheckResponse
	if err := c.patchJSON(cmd.Context(), path, patch, &res); err != nil {
		return explainSmokeRefusal(err)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "edited %q (id %s) - the user's verdict is unchanged (%s)\n",
		res.Check.Name, res.Check.ID, smokeVerdictLabel(res.Check.Verdict))
	return err
}

func newSmokeRemoveCommand(ctx *commandContext) *cobra.Command {
	var session, caseID string
	cmd := &cobra.Command{
		Use:   "remove [session]",
		Short: "Remove one unplayed case (a played case is retired instead)",
		Long:  smokeRemoveLong,
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.removeSmokeCase(cmd, args, session, caseID)
		},
	}
	cmd.Flags().SetNormalizeFunc(underscoreFlagNames)
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&caseID, "case", "", "Case id to remove (required; see `ao smoke list`)")
	return cmd
}

func (c *commandContext) removeSmokeCase(cmd *cobra.Command, args []string, session, caseID string) error {
	session = resolveSmokeSession(args, session)
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	caseID = strings.TrimSpace(caseID)
	if caseID == "" {
		return usageError{errors.New("usage: --case <id> is required (run `ao smoke list` to see the ids)")}
	}
	path := "sessions/" + url.PathEscape(session) + "/smoke-checks/" + url.PathEscape(caseID)
	var res listSmokeChecksResponse
	body := removeSmokeCaseRequest{From: callingSessionID()}
	if err := c.doJSON(cmd.Context(), http.MethodDelete, path, body, &res); err != nil {
		return explainSmokeRefusal(err)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "removed %s; the checklist for %s now has %d case(s)\n",
		caseID, session, len(res.Checks))
	return err
}

func newSmokeStandDownCommand(ctx *commandContext) *cobra.Command {
	var session, reason string
	cmd := &cobra.Command{
		Use:   "stand-down [session]",
		Short: "Record that this change needs no human verification, and why",
		Long:  smokeStandDownLong,
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.standDownSmokeChecklist(cmd, args, session, reason)
		},
	}
	cmd.Flags().SetNormalizeFunc(underscoreFlagNames)
	cmd.Flags().StringVar(&session, "session", "", "Session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&reason, "reason", "", "What you looked at, and why none of it needs a person (required)")
	return cmd
}

func (c *commandContext) standDownSmokeChecklist(cmd *cobra.Command, args []string, session, reason string) error {
	session = resolveSmokeSession(args, session)
	if session == "" {
		return usageError{errors.New("usage: session id is required (positional or --session)")}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return usageError{errors.New("usage: --reason is required - \"nothing to check\" with no account of what you looked at is not an answer, e.g. --reason \"pure refactor, behavior covered by TestFoo\"")}
	}
	path := "sessions/" + url.PathEscape(session) + "/smoke-checks/stand-down"
	var res listSmokeChecksResponse
	body := standDownSmokeRequest{Reason: reason, From: callingSessionID()}
	if err := c.postJSON(cmd.Context(), path, body, &res); err != nil {
		return explainSmokeRefusal(err)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "stood down on %s: %s. The Tests tab now says so instead of showing an empty list.\n", session, reason)
	return err
}

// callingSessionID is the writer's OWN session id, which is what every smoke
// write is attributed to. It is deliberately not the target: both crew members
// write to the same target, because the checklist belongs to the task and
// $AO_CREW_ID is dev's id, so the target cannot say which of them is calling.
func callingSessionID() string {
	return strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
}

// underscoreFlagNames accepts --from_file for --from-file. Agents routinely
// spell flags with underscores.
func underscoreFlagNames(_ *pflag.FlagSet, name string) pflag.NormalizedName {
	return pflag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
}
