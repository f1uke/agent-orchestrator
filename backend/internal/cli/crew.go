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
	// The three facts domain.SessionRecord.Awake() is defined on. All three are
	// mirrored, not just isSuspended: a member that is TERMINATED or still a TODO
	// has no running agent either, and reading only the suspend flag made a
	// finished crew print two awake members forever.
	IsTerminated bool   `json:"isTerminated"`
	IsSuspended  bool   `json:"isSuspended"`
	IsTodo       bool   `json:"isTodo"`
	TaskSize     string `json:"taskSize"`
	Crew         *struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	} `json:"crew"`
	// CrewRun is the bracketed build/test run this member has OPEN right now, and
	// it is what lets this listing say "running a build" instead of the far
	// weaker "awake". Nothing else on the wire can answer that: the activity state
	// comes from the agent's own hooks and cannot tell a build from an agent
	// reading a file. Absent for every member that is not running one.
	CrewRun *struct {
		Kind      string `json:"kind"`
		Label     string `json:"label"`
		StartedAt string `json:"startedAt"`
	} `json:"crewRun"`
	// CrewRunDiscards is the current streak of runs thrown away because the tree
	// moved under them. At the cap the task is at NEEDS YOU.
	CrewRunDiscards int `json:"crewRunDiscards"`
}

type crewWakeResponse struct {
	Session crewSessionView `json:"session"`
}

type crewAddRequest struct {
	Role string `json:"role,omitempty"`
}

type crewAddResponse struct {
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
			"A `mechanical` task has no crew: it is dev alone - and if that turns out to be\n" +
			"the wrong call, `ao crew add` attaches a qa to it without disturbing the agent\n" +
			"that is already working.",
	}
	cmd.AddCommand(newCrewAddCommand(ctx))
	cmd.AddCommand(newCrewWakeCommand(ctx))
	cmd.AddCommand(newCrewRunCommand(ctx))
	cmd.AddCommand(newCrewStatusCommand(ctx))
	return cmd
}

// newCrewAddCommand is the escape hatch the design asks for, built as what it
// actually is: a CREATE.
//
// The crew is decided at spawn - `standard` and `deep` get a qa, `mechanical`
// does not - and the design offers "add qa to a solo task" as a manual way out.
// It describes that as a WAKE, "the session exists, suspended", which is true
// only for a task whose qa was formed at spawn. A mechanical task's qa was never
// created, and neither was one for any task older than the crew, so there is
// nothing to wake: this makes the member.
//
// It changes nothing about the task it joins. The new member is born asleep - a
// row and an id, no terminal - and dev keeps running straight through, so no
// task gains a qa unless somebody asks for one.
func newCrewAddCommand(ctx *commandContext) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "add <session-id>",
		Short: "Attach a qa to a task that is already running",
		Long: "Adds a second agent to an existing task, sharing its worktree. The new member is\n" +
			"born ASLEEP - it has an id and a card from the moment this returns, and `ao send`\n" +
			"to it is held until somebody gives it the turn with `ao crew wake` - so attaching\n" +
			"never interrupts the agent that is working.\n\n" +
			"Name either member of the task; both resolve to the same crew. It is refused if\n" +
			"the task already has that role, or if the task is finished (its pull request has\n" +
			"merged, or its agent has been torn down).\n\n" +
			"Attaching is one-way. To undo it, stand the member down with `ao session kill` -\n" +
			"which leaves the task's worktree, branch and pull request with dev, exactly where\n" +
			"they were - and `ao session restore` brings the SAME member back.",
		Example: `  ao crew add agent-orchestrator-230   # this task should have a qa after all
  ao crew wake agent-orchestrator-231  # now give it the turn`,
		Args: oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			var out crewAddResponse
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/crew/members", crewAddRequest{Role: strings.TrimSpace(role)}, &out); err != nil {
				return err
			}
			_, printErr := fmt.Fprintf(cmd.OutOrStdout(), "%s (%s) is on the task, asleep. Give it the turn with `ao crew wake %s`.\n",
				out.Session.ID, crewRoleOf(out.Session), out.Session.ID)
			return printErr
		},
	}
	cmd.Flags().StringVar(&role, "role", "qa", "Role the new member fills (only `qa` today: dev is the crew's root, not a seat)")
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
		Long: "Groups the board's sessions into the tasks they are working and says, for each\n" +
			"member, whether it has a running agent right now:\n\n" +
			"  awake        this member holds the task's turn\n" +
			"  asleep       suspended, waiting for a turn - `ao crew wake` gives it one\n" +
			"  finished     torn down; `ao session restore` is what brings it back\n" +
			"  not started  a prepared TODO that has never been started\n\n" +
			"A solo task is not a crew and is not listed here; use `ao session ls` for those.",
		Args: noArgs,
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
			if _, err := fmt.Fprintf(out, "  %-4s %-28s %s\n", crewRoleOf(member), member.ID, crewMemberState(member)); err != nil {
				return err
			}
			for _, note := range crewRunNotes(member) {
				if _, err := fmt.Fprintf(out, "       %s\n", note); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// crewMemberState is this listing's word for "does this member have a running
// agent right now?", and it is the CLI's mirror of domain.Awake() - the same
// three facts, in the same order of precedence, so the two can never disagree
// again.
//
// A terminated member gets its OWN word rather than sharing "asleep" with a
// suspended one. Both are true statements about a stopped agent, but they are
// answers to different questions: an asleep member is waiting for its turn and
// `ao crew wake` gives it one, while a finished member is gone and only
// `ao session restore` brings it back. Collapsing them would leave a completed
// task looking like one that is merely between turns.
func crewMemberState(s crewSessionView) string {
	switch {
	case s.IsTerminated:
		return "finished"
	case s.IsTodo:
		return "not started"
	case s.IsSuspended:
		return "asleep"
	}
	return "awake"
}

// crewRunNotes says what this member is DOING, when the bracket knows. "awake"
// is far weaker advice than "running a build": a member deciding whether now is
// a good moment to start its own run needs the second, and the bracket is the
// only place it exists.
//
// The streak line is the other half. Three discards in a row means the member
// cannot get a quiet window, and that has to be readable here rather than only
// on the board.
func crewRunNotes(s crewSessionView) []string {
	notes := []string{}
	if s.CrewRun != nil {
		line := "running a " + s.CrewRun.Kind
		if s.CrewRun.Label != "" {
			line += " - " + s.CrewRun.Label
		}
		if since := relativeStart(s.CrewRun.StartedAt); since != "" {
			line += " (started " + since + ")"
		}
		notes = append(notes, line)
	}
	if s.CrewRunDiscards > 0 {
		notes = append(notes, fmt.Sprintf("%d run(s) in a row discarded - the tree moved under each", s.CrewRunDiscards))
	}
	return notes
}

func crewRoleOf(s crewSessionView) string {
	if s.Crew == nil || s.Crew.Role == "" {
		return "solo"
	}
	return s.Crew.Role
}
