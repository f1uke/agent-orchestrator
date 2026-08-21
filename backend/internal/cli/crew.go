package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// crewSessionView is the slice of the daemon's SessionView this command reads.
// Hand-mirrored, like every other CLI DTO in this package, so the CLI stays a
// thin HTTP client with no import of the controllers.
type crewSessionView struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Status      string `json:"status"`
	IsSuspended bool   `json:"isSuspended"`
	TaskSize    string `json:"taskSize"`
	Crew        *struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	} `json:"crew"`
}

type crewWakeResponse struct {
	Session crewSessionView `json:"session"`
}

type crewListResponse struct {
	Sessions []crewSessionView `json:"sessions"`
}

// newCrewCommand exposes the ONE crew action AO deliberately leaves to a human:
// deciding whose turn it is.
//
// A task of `--task-size standard` or `deep` is worked by two sessions on one
// worktree - dev, which owns the branch and the PR, and qa, which writes, runs
// and records the tests - and exactly one of them may be awake at a time. AO
// enforces that rule but takes NO position on when the baton should move: the
// handover policy is meant to be decided after watching real tasks, not guessed
// at. So `ao crew wake` is the affordance, and there is no scheduler behind it.
func newCrewCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "crew",
		Short: "See and move the baton between the two agents working one task",
		Long: "A `standard` or `deep` task is worked by a CREW of two sessions sharing one\n" +
			"worktree: dev owns the branch and the pull request, qa writes, runs and records\n" +
			"the tests. Only one of them may be awake at a time, and AO does not decide when\n" +
			"that changes - you do, with `ao crew wake`.\n\n" +
			"A `mechanical` task has no crew: it is dev alone.",
	}
	cmd.AddCommand(newCrewWakeCommand(ctx))
	cmd.AddCommand(newCrewStatusCommand(ctx))
	return cmd
}

func newCrewWakeCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "wake <session-id>",
		Short: "Give this task's turn to one crew member, putting the current holder to sleep",
		Long: "Hands the task's one awake slot to the named member. Whoever holds it is stood\n" +
			"down first - suspended, with its terminal reaped, its card kept and its worktree\n" +
			"untouched - and the named member is resumed in its place, so the two never run\n" +
			"in the shared checkout at the same time.\n\n" +
			"Waking the member that already holds the slot does nothing and is not an error.",
		Example: `  ao crew wake agent-orchestrator-231   # qa's turn now
  ao crew status                        # who is up, and who is asleep`,
		Args: oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			var out crewWakeResponse
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/crew/wake", nil, &out); err != nil {
				return err
			}
			_, printErr := fmt.Fprintf(cmd.OutOrStdout(), "%s (%s) is awake\n", out.Session.ID, crewRoleOf(out.Session))
			return printErr
		},
	}
}

func newCrewStatusCommand(ctx *commandContext) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "List the crews on the board and which member is awake",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.printCrewStatus(cmd.Context(), cmd.OutOrStdout(), project)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Only crews in this project")
	return cmd
}

func (c *commandContext) printCrewStatus(ctx context.Context, out io.Writer, project string) error {
	path := "sessions"
	if p := strings.TrimSpace(project); p != "" {
		path += "?project=" + url.QueryEscape(p)
	}
	var resp crewListResponse
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return err
	}
	// Group by crew id, which IS dev's session id - so the crews come out keyed by
	// the same string `ao send` takes.
	byCrew := map[string][]crewSessionView{}
	order := []string{}
	for _, s := range resp.Sessions {
		if s.Crew == nil || s.Crew.ID == "" {
			continue
		}
		if _, seen := byCrew[s.Crew.ID]; !seen {
			order = append(order, s.Crew.ID)
		}
		byCrew[s.Crew.ID] = append(byCrew[s.Crew.ID], s)
	}
	if len(order) == 0 {
		_, err := fmt.Fprintln(out, "No crews: every task on the board is one agent working alone.")
		return err
	}
	for _, crewID := range order {
		if _, err := fmt.Fprintf(out, "%s\n", crewID); err != nil {
			return err
		}
		for _, member := range byCrew[crewID] {
			state := "awake"
			if member.IsSuspended {
				state = "asleep"
			}
			if _, err := fmt.Fprintf(out, "  %-4s %-28s %s\n", crewRoleOf(member), member.ID, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func crewRoleOf(s crewSessionView) string {
	if s.Crew == nil || s.Crew.Role == "" {
		return "solo"
	}
	return s.Crew.Role
}
