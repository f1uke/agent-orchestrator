package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ATTACHING A MEMBER to a task that already exists.
//
// The design (design-multi-agent-per-task-ux §1.1) offers "add qa to a solo
// task" as a manual escape hatch and calls it a WAKE - "the session exists,
// suspended". That is true for a `standard` or `deep` task, whose qa was formed
// at spawn. It is NOT true for a `mechanical` one, or for any task spawned
// before the crew was turned on: qa was never created, so adding one is a
// CREATE, and until this file there was no way to create a crew member outside
// the spawn path. This is the hole that closes.
//
// Two properties carry over from formCrew unchanged, because it is literally the
// same write:
//
//   - The new member is BORN SUSPENDED - one INSERT with is_suspended set. It
//     never holds the crew slot, so #225's exclusion is never argued with in
//     order to add the member it protects, and there is no instant at which two
//     members of the crew are awake.
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

// AttachCrewMember adds a member in `role` to the task dev works on.
//
// It is check-then-create, so the whole thing runs under the crew lock: two
// racing attaches must not both see a free seat. The database pins the same rule
// independently (0047_session_crew_role_unique), because a mutex is a property
// of one process and the invariant should be a property of the data.
//
// The "is this task finished?" refusal is NOT here: it needs the session's
// derived status, which is assembled from PR facts at service read time. See
// Service.AttachCrewMember.
func (m *Manager) AttachCrewMember(ctx context.Context, devID domain.SessionID, role domain.CrewRole) (domain.SessionRecord, error) {
	defer m.lockCrew(devID)()

	dev, err := m.getRecord(ctx, devID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	// A prepared TODO has no branch and no worktree to share. resolveCrewDev
	// would refuse it too, but "there is no materialized worktree" is a puzzle
	// where the actual answer is one sentence: start it, and StartTodo forms the
	// crew its size asks for on the way through.
	if dev.IsTodo {
		return domain.SessionRecord{}, fmt.Errorf("%w: %s has not been started yet; start it first", ErrInvalidCrew, devID)
	}
	project, err := m.loadProject(ctx, dev.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, err
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

	member, err := m.spawnSuspendedCrewMemberLocked(ctx, project, dev, role, joinedLate)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	m.logger.Info("crew: member attached to a running task",
		"crew", dev.ID, "dev", dev.ID, "member", member.ID, "role", string(role), "taskSize", string(dev.TaskSize.WithDefault()))
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
	dev, err := m.getRecord(ctx, domain.SessionID(rec.CrewID))
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("crew dev of %s: %w", id, err)
	}
	return dev, nil
}
