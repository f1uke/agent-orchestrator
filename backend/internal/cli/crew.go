package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
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
	// From names the SENDING session, the same way `ao send` and `ao smoke set`
	// do. It is the only thing that tells the daemon an AGENT ran this rather
	// than a person: a human's shell has no $AO_SESSION_ID, so the field is empty
	// and the call is never refused, while every AO session identifies itself and
	// is refused on a project that has turned automatic crew formation off.
	From string `json:"from,omitempty"`
}

type crewAddResponse struct {
	Session crewSessionView `json:"session"`
}

type crewListResponse struct {
	Sessions []crewSessionView `json:"sessions"`
}

// newCrewCommand is how a human sees and starts the agents on one task.
//
// A task of `--task-size standard` or `deep` is worked by two sessions on ONE
// worktree - dev, which owns the branch and the PR, and qa, which writes, runs
// and records the tests - and they run AT THE SAME TIME. Neither waits for the
// other and neither can stand the other down; what keeps a shared checkout
// honest is the run bracket (`ao crew run`), which throws away a build or test
// the tree moved under instead of trusting it.
func newCrewCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "crew",
		Short: "See and start the two agents working one task",
		Long: "A `standard` or `deep` task MAY be worked by a CREW of two sessions sharing one\n" +
			"worktree: dev owns the branch and the pull request, qa writes, runs and records\n" +
			"the tests. BOTH run at the same time - starting one never stops the other - and\n" +
			"`ao crew run` is what keeps a result honest when they overlap.\n\n" +
			"Every task starts as dev ALONE, and stays that way until somebody asks for the\n" +
			"second agent. dev asks with `ao crew review`, when it believes the change is\n" +
			"done and wants it checked; a person asks with `ao crew add`, which also works\n" +
			"on a `mechanical` task, where dev may not ask at all.",
	}
	cmd.AddCommand(newCrewReviewCommand(ctx))
	cmd.AddCommand(newCrewAddCommand(ctx))
	cmd.AddCommand(newCrewWakeCommand(ctx))
	cmd.AddCommand(newCrewRunCommand(ctx))
	cmd.AddCommand(newCrewStatusCommand(ctx))
	return cmd
}

// newCrewReviewCommand is DEV asking for the qa that checks its work, and it is
// the ordinary way a task gains one.
//
// It replaced an OBSERVATION: AO used to create a qa the first time dev touched
// the app's runtime - a simulator claim, an `ao preview`. That fires when dev is
// STARTING to drive the app, so the qa it created reached for the device dev was
// still using and the two fought over it. Nothing about dev's tooling can say
// "the work is done"; dev can.
//
// It takes NO argument. The task is the caller's own ($AO_SESSION_ID), which is
// the one id dev always has, and the daemon reads everything else - the task's
// size, the project's policy - from its own records. That is the same division
// `ao crew add` follows: the CLI sends identity, never policy.
func newCrewReviewCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "review",
		Short: "dev: ask for a qa to check this task, now that you think it is done",
		Long: "Puts a **qa** on this task - a second agent, in the same worktree, awake and\n" +
			"working beside you from the moment it is created. Run it when you believe the\n" +
			"change is finished and you want it verified: qa writes and runs what a machine\n" +
			"can assert, records what it found, and hands the account back to you.\n\n" +
			"Nothing else creates one. AO used to add a qa by itself the first time a task\n" +
			"drove the app, and that fired when you were STARTING to drive it - so the qa\n" +
			"turned up wanting the same device you were using. You are the only one who\n" +
			"knows the work is ready, so you are the one who says so.\n\n" +
			"It takes no argument: the task is this session's own. It is refused if the task\n" +
			"already has a qa, if the task is finished (its pull request has merged), if it\n" +
			"was tagged `--task-size mechanical` - one agent by design - or if this project\n" +
			"is set to form no crews automatically. In each of those a PERSON can still add\n" +
			"one with the `+ qa` control on the task in the app.\n\n" +
			"Asking is one-way and once: a task has one qa, it keeps its id, and standing it\n" +
			"down (`ao session kill`) is the undo.",
		Example: `  ao crew review   # the change is done; have it checked`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			self := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
			if self == "" {
				return usageError{errors.New("`ao crew review` is a session asking for its own qa, so it has to be run from inside an AO session; a person adds one with `ao crew add <session-id>`")}
			}
			var out crewAddResponse
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(self)+"/crew/review", nil, &out); err != nil {
				return explainCrewAddRefusal(err)
			}
			_, printErr := fmt.Fprintf(cmd.OutOrStdout(),
				"%s (%s) is on this task and working, in your worktree. It reads the diff against the base branch, not your conversation.\n"+
					"From here the smoke checklist is SHARED: use `ao smoke add` / `edit --case <id>`, never `ao smoke set`, which would delete its cases.\n"+
					"Message it by role: `ao send --crew %s --about <commit-sha> --message \"...\"`.\n",
				out.Session.ID, crewRoleOf(out.Session), crewRoleOf(out.Session))
			return printErr
		},
	}
}

// newCrewAddCommand is the PERSON's door, built as what it
// actually is: a CREATE.
//
// No task has a qa until somebody asks. dev asks with `ao crew review` when it
// thinks the change is done; this is how a PERSON asks, and it is the wider door.
// It works where dev's does not - a `mechanical` task, which is one agent by an
// explicit decision only a person may undo - and it works when dev simply did not
// think to ask.
//
// The member arrives WORKING, and dev keeps running straight through, so the task
// gains an agent without losing a moment of the one it had.
func newCrewAddCommand(ctx *commandContext) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "add <session-id>",
		Short: "Attach a qa to a task that is already running",
		Long: "Adds a second agent to an existing task, sharing its worktree. The new member\n" +
			"STARTS WORKING straight away, beside the agent that is already there - attaching\n" +
			"never interrupts it, and both members then run at the same time.\n\n" +
			"dev asks for its own qa with `ao crew review` once it thinks the change is done.\n" +
			"This is the wider door: it also works on a `mechanical` task, where dev may not\n" +
			"ask, and on a task whose dev never got round to it. dev is TOLD when you use it,\n" +
			"because a dev that was working alone is holding instructions that say the smoke\n" +
			"checklist is its own.\n\n" +
			"Name either member of the task; both resolve to the same crew. It is refused if\n" +
			"the task already has that role, or if the task is finished (its pull request has\n" +
			"merged, or its agent has been torn down).\n\n" +
			"Attaching is one-way. To undo it, stand the member down with `ao session kill` -\n" +
			"which leaves the task's worktree, branch and pull request with dev, exactly where\n" +
			"they were - and `ao session restore` brings the SAME member back.\n\n" +
			"On a project whose settings say `Never form a crew automatically`, this command\n" +
			"is refused when an AO SESSION runs it - the switch turns off the automatic half\n" +
			"and keeps the manual one, and the manual one is a person's. A human adds a qa\n" +
			"there from the task's `+ qa` control in the app, or by running this in their own\n" +
			"shell.",
		Example: `  ao crew add agent-orchestrator-230   # this task should have a qa after all`,
		Args:    oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			var out crewAddResponse
			body := crewAddRequest{Role: strings.TrimSpace(role), From: strings.TrimSpace(os.Getenv("AO_SESSION_ID"))}
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/crew/members", body, &out); err != nil {
				return explainCrewAddRefusal(err)
			}
			_, printErr := fmt.Fprintf(cmd.OutOrStdout(), "%s (%s) is on the task and working, in the same worktree; dev keeps running.\n",
				out.Session.ID, crewRoleOf(out.Session))
			return printErr
		},
	}
	cmd.Flags().StringVar(&role, "role", "qa", "Role the new member fills (only `qa` today: dev is the crew's root, not a seat)")
	return cmd
}

// explainCrewAddRefusal turns the crew-off refusal into a usage error (exit 2),
// the same way `ao smoke set` treats its two: it is not a failure of the command
// but a statement that this caller may not do this here, and the daemon's
// message already names who can. Everything else passes through unchanged, so a
// real error still exits 1 and still looks like one.
func explainCrewAddRefusal(err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) {
		return err
	}
	if apiErr.ErrorBody.Code != "CREW_AUTO_FORMATION_OFF" {
		return err
	}
	if strings.TrimSpace(apiErr.ErrorBody.Message) == "" {
		return err
	}
	return usageError{errors.New(apiErr.ErrorBody.Message)}
}

func newCrewWakeCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "wake <session-id>",
		Short: "Start one crew member, leaving its crewmate exactly as it is",
		Long: "Brings the named member up in the task's worktree. It TOUCHES NOBODY ELSE: the\n" +
			"other member keeps running, keeps its terminal and is not interrupted, because\n" +
			"both members of a crew work at the same time.\n\n" +
			"Waking a member that is already awake does nothing and is not an error.",
		Example: `  ao crew wake agent-orchestrator-231   # start qa; dev carries on
  ao crew status                        # who is up, and who has not started`,
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
		Short: "List the crews on the board and which members are awake",
		Long: "Groups the board's sessions into the tasks they are working and says, for each\n" +
			"member, whether it has a running agent right now. Both members may be awake at\n" +
			"once, and normally are:\n\n" +
			"  awake        this member has a running agent\n" +
			"  asleep       suspended - `ao crew wake` (or opening its card) starts it\n" +
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
