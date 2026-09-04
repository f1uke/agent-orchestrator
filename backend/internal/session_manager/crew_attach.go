package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
)

// PUTTING A QA ON A TASK THAT ALREADY EXISTS - through either of its two doors.
//
// Nothing creates a qa by observation any more (crew_join.go). Both doors are
// somebody asking, and they are kept apart because they are asked by different
// people, for different reasons, and answer to different policy:
//
//   - `ao crew review` is DEV asking, once it believes the change is done and
//     wants it checked. This is the ordinary way a task gains a qa, and it is
//     gated by the same crewEligible test that decides whether dev's prompt was
//     ever told the verb - so a mechanical task and a crew-off project refuse it.
//   - `ao crew add` (and the card's `+ qa`) is a PERSON asking, and it is the
//     override: it ignores task size, so a human can put a qa on a mechanical
//     task, and it stays open on a crew-off project because that switch is about
//     what AGENTS may do.
//
// Everything under them is one write: same worktree, same branch, same start.
// `crew_join_reason` is what keeps them apart in the data ('review' vs 'manual'),
// because it is what the board's join line and the joining member's own first
// turn are written from.
//
// Two properties matter and are the same on both paths:
//
//   - The member is created and then STARTED. It arrives working, because there
//     is nothing left for it to wait for: both members run at once, and a member
//     created asleep with no control to start it is the stall this replaced.
//   - dev is not touched at all. No suspend, no reap, no relaunch: dev keeps
//     working straight through the attach, which is the whole point of being
//     able to do this to a task in flight.
//
// Attaching is ONE-WAY, and that is deliberate. The two shapes a detach could
// take are both the id trap one level up (#226/#228): deleting the row orphans
// the smoke_check rows, evidence directories, review_run rows and transcript
// that already name it, and DEMOTING it (clearing the crew columns) is worse -
// an ex-member is still sitting in dev's worktree on dev's branch, but
// OwnsCrewWorkspace() flips to true, so #224's refcount would let its teardown
// destroy a live dev's tree. The undo AO already has is STAND DOWN: `ao kill`
// on the member terminates it locally, leaves dev's tree alone, and keeps the
// crew columns so the refcount and the history stay correct. Its seat stays
// its own, and `ao session restore` is how it comes back - the same id
// returning, rather than a second one inheriting the first's artefacts.

// AttachCrewMember adds a member in `role` to the task dev works on, and starts
// it.
//
// `requestedBy` is the CALLER's own session id when an AO session made the call
// (`ao crew add` sends $AO_SESSION_ID), and empty when a human did - the desktop
// app's `+ qa`, or the CLI typed in an ordinary shell, neither of which has that
// variable set. It is the only thing that can tell the two apart: both arrive on
// the same route, from the same loopback address, and the daemon cannot see who
// is at the keyboard. It exists for exactly one refusal, below, and an
// unidentified caller is never refused.
//
// The "is this task finished?" refusal is NOT here: it needs the session's
// derived status, which is assembled from PR facts at service read time. See
// Service.AttachCrewMember.
//
// Starting is BEST EFFORT and deliberately not fatal: the member is on the task
// either way, and a human who asked for a qa would rather have one they can open
// than an error and no member at all.
func (m *Manager) AttachCrewMember(ctx context.Context, devID domain.SessionID, role domain.CrewRole, requestedBy domain.SessionID) (domain.SessionRecord, error) {
	member, err := m.attachCrewMemberRow(ctx, devID, role, requestedBy, domain.CrewJoinManual)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	m.tellDevAMemberJoined(ctx, devID, member)
	started, err := m.startCrewMember(ctx, member.ID)
	if err != nil {
		m.logger.Warn("crew: member was attached but could not be started; open its card to start it",
			"crew", devID, "member", member.ID, "error", err)
		return member, nil
	}
	return started, nil
}

// attachCrewMemberRow is the check-then-create half, so the whole of it runs
// under the crew lock: two racing attaches must not both see a free seat. The
// database pins the same rule independently (0047_session_crew_role_unique),
// because a mutex is a property of one process and the invariant should be a
// property of the data. Starting the member happens after this returns, because
// Resume takes the same lock and lockCrew is not reentrant.
func (m *Manager) attachCrewMemberRow(ctx context.Context, devID domain.SessionID, role domain.CrewRole, requestedBy domain.SessionID, reason domain.CrewJoinReason) (domain.SessionRecord, error) {
	defer m.lockCrew(devID)()

	dev, err := m.getRecord(ctx, devID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	// A prepared TODO has no branch and no worktree to share. resolveCrewDev
	// would refuse it too, but "there is no materialized worktree" is a puzzle
	// where the actual answer is one sentence: start it first, and then attach.
	if dev.IsTodo {
		return domain.SessionRecord{}, fmt.Errorf("%w: %s has not been started yet; start it first", ErrInvalidCrew, devID)
	}
	project, err := m.loadProject(ctx, dev.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	// THE POLICY GATE. A project that has turned automatic crew formation off
	// keeps its manual escape hatch for a HUMAN and closes it to every AO
	// session. Read from the loaded ProjectRecord, never from a config payload on
	// the wire: `disableAutoCrew` is a bool with `omitempty`, so absent and false
	// are the same bytes there, and a reader that cannot tell them apart is the
	// exact trap that wiped a config file in #249.
	if requestedBy != "" && project.Config.DisableAutoCrew {
		return domain.SessionRecord{}, fmt.Errorf(
			"%w: %s has \"Never form a crew automatically\" turned on, so an AO session may not put a %s on %s. "+
				"This is the project's policy, not a temporary failure, and there is no flag that overrides it: "+
				"do the work solo and own the smoke checklist yourself. A person can still add one by hand - "+
				"the `+ qa` control on the task in the app, or `ao crew add %s` typed in their own shell - "+
				"so ask them if this task really needs a second agent",
			ErrCrewAutoFormationOff, dev.ProjectID, role, devID, devID)
	}
	// DEV'S DOOR IS NARROWER THAN A PERSON'S, and by exactly one test: the same
	// crewEligible that decided whether dev's prompt was ever told this verb
	// exists. A `mechanical` task is one agent by an explicit decision somebody
	// made when they sized it, and a request to undo that is a person's call, not
	// the agent's. A human's `ao crew add` deliberately skips this - it is the
	// override, and overriding is what it is for.
	if reason == domain.CrewJoinReview && !crewEligible(project, dev.Kind, dev.TaskSize) {
		return domain.SessionRecord{}, fmt.Errorf(
			"%w: %s was tagged `--task-size %s`, which means ONE agent by design, so it cannot ask for a %s. "+
				"Finish the work and own the smoke checklist yourself. If this task turned out to need a second "+
				"pair of eyes after all, that is a person's call: ask them to add one with the `+ qa` control on "+
				"the task in the app, or `ao crew add %s` in their own shell",
			ErrInvalidCrew, devID, dev.TaskSize.WithDefault(), role, devID)
	}
	// One shared eligibility test with the spawn seam: orchestrator, workspace
	// project, terminated dev, no materialized worktree, no nesting, bad role.
	dev, err = m.resolveCrewDev(ctx, project, devID, role)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	members, err := m.crewMembers(ctx, dev)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("attach crew member: %w", err)
	}
	for _, other := range members {
		if other.CrewRole == role {
			// Counts a member that has been stood down: the seat is that session's,
			// and `ao session restore` is how it comes back.
			return domain.SessionRecord{}, fmt.Errorf("%w: %s already has a %s (%s)", ErrCrewRoleTaken, dev.ID, role, other.ID)
		}
	}

	member, err := m.spawnSuspendedCrewMemberLocked(ctx, project, dev, role, reason)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	m.logger.Info("crew: member attached to a running task",
		"crew", dev.ID, "dev", dev.ID, "member", member.ID, "role", string(role), "taskSize", string(dev.TaskSize.WithDefault()))
	// A qa on a crew-off project is a human overruling their own setting, which
	// is allowed and must never be silent. What let the incident this gate fixes
	// run for two days was that nothing anywhere said "a qa appeared on a project
	// that forms none": the agents' reports mentioned it as an ordinary
	// implementation detail and it read as one. So every such attach leaves a
	// WARN naming the project, the task and the member - findable after the fact
	// even when the gate above lets the call through.
	if project.Config.DisableAutoCrew {
		m.logger.Warn("crew: a member was attached to a task on a project that forms no crews automatically",
			"project", dev.ProjectID, "crew", dev.ID, "member", member.ID, "role", string(role), "requestedBy", string(requestedBy))
	}
	return member, nil
}

// CrewDevOf resolves any session to the DEV of the task it belongs to.
//
// A crew member answers with its dev; a solo session answers with itself,
// because a solo session IS its own task - which is the same equality AO_CREW_ID
// relies on, so a caller holding one id never has to know which kind it has.
func (m *Manager) CrewDevOf(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	if !rec.InCrew() || rec.CrewRole.IsDev() {
		return rec, nil
	}
	dev, err := m.getRecord(ctx, rec.CrewID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("crew dev of %s: %w", id, err)
	}
	return dev, nil
}

// RequestCrewReview is DEV asking for the qa that checks its work, and it is the
// ordinary way a task gains one.
//
// It replaces an OBSERVATION, and the replacement is the point. AO used to create
// a qa the first time dev touched the app's runtime - a simulator claim, an `ao
// preview` - which fires when dev is STARTING to drive the app. The qa that
// appeared then went straight for the device dev was still using, and on a
// machine with one booted simulator the two simply fought over it. Nothing about
// dev's tooling can say "the work is done"; dev can, so dev does.
//
// `from` is the CALLER's own session id, which is all the CLI sends (#253: the
// CLI sends identity, never policy). Everything else - which task this is, what
// its size is, what the project allows - the daemon reads for itself from the
// stored record.
//
// A qa may not ask: it would be asking for itself, and a crew has one dev and one
// qa. A solo worker IS its own task's dev, so it asks with its own id and needs
// to know nothing about crew ids.
func (m *Manager) RequestCrewReview(ctx context.Context, from domain.SessionID, role domain.CrewRole) (domain.SessionRecord, error) {
	requester, err := m.getRecord(ctx, from)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	// Asked by the wrong member. It is worth its own sentence rather than falling
	// through to resolveCrewDev's "crews do not nest": a qa running this has
	// misread its own role, and the useful answer says so.
	if requester.InCrew() && !requester.CrewRole.IsDev() {
		return domain.SessionRecord{}, fmt.Errorf(
			"%w: %s is the %s of this task, not its dev; the review is what you are here to DO, not to ask for",
			ErrInvalidCrew, from, requester.CrewRole)
	}
	member, err := m.attachCrewMemberRow(ctx, from, role, from, domain.CrewJoinReview)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	// No message to dev here, unlike the manual door: dev just ran the command and
	// reads the answer on its own stdout.
	started, err := m.startCrewMember(ctx, member.ID)
	if err != nil {
		m.logger.Warn("crew: qa was created but could not be started; open its card to start it",
			"crew", from, "qa", member.ID, "error", err)
		return member, nil
	}
	return started, nil
}

// tellDevAMemberJoined is the one message AO sends dev on its own account, and it
// exists because a system prompt is fixed when a runtime launches while crew
// membership is not.
//
// A dev whose task was never crew-eligible - a `mechanical` one, or any task on a
// project that forms no crews automatically - is launched with the SOLO prompt.
// That prompt tells it the smoke checklist is its own and hands it `ao smoke set`,
// which REPLACES the whole list. The moment a person attaches a qa, that is no
// longer true and the instruction has become destructive: the next `ao smoke set`
// from dev deletes every case its new crewmate wrote. The prompt cannot be
// rewritten under a running agent (only a restore recomputes it, from the row),
// so the correction is delivered the one way a live agent can receive one.
//
// Best effort and never fatal: a member that is on the task with dev unaware is
// still better than a refused attach, and the human asked for the member.
func (m *Manager) tellDevAMemberJoined(ctx context.Context, devID domain.SessionID, member domain.SessionRecord) {
	ctx = msgdelivery.WithOrigin(ctx, msgdelivery.Origin{Trigger: msgdelivery.TriggerCrewNotice})
	if _, err := m.Send(ctx, devID, crewJoinedNotice(member.CrewRole)); err != nil {
		m.logger.Warn("crew: could not tell dev that a member joined its task",
			"crew", devID, "member", member.ID, "error", err)
	}
}

// crewJoinedNotice is what dev is told. It is written in AO's voice and marked as
// such, and it carries only what CHANGES for dev - the two facts a solo prompt
// gets wrong the moment a crewmate exists, and how to address them.
func crewJoinedNotice(role domain.CrewRole) string {
	return "[AO] A **" + string(role) + "** has just been added to this task by a person, and is working in your worktree right now, at the same time as you. " +
		"Two things your standing instructions do not know about:\n\n" +
		"- **The smoke checklist is now SHARED.** Never `ao smoke set` again on this task - it replaces the WHOLE list, so it would delete the cases " + string(role) + " has written. Use `ao smoke add`, `ao smoke edit --case <id>` and `ao smoke remove --case <id>`, which touch only the case they name. Leave `ao smoke record` to " + string(role) + ".\n" +
		"- **One worktree, one git index, and anything exclusive is contended live** - a `git add -A` sweeps up your crewmate's half-written work, and the simulator lease is one device two agents can reach for. Commit the paths you meant to commit, and bracket a build or a test run you want to trust with `ao crew run`.\n\n" +
		"Address it by role, never by id: `ao send --crew " + string(role) + " --about <commit-sha|smoke-case-id> --message \"...\"`. There is no obligation to reply to this."
}
