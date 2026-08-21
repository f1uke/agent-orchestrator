package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ATTACHING A MEMBER BY HAND to a task that already exists.
//
// This is the manual half of lazy creation. AO creates a qa when dev touches a
// runtime surface (crew_join.go); a task that never touches one - a backend-only
// change, or a `mechanical` task, which is never eligible automatically - still
// gets a qa the moment a human asks for one, from `ao crew add` or the card's
// `+ qa`. Nothing else about it differs: same write, same worktree, same branch,
// and `crew_join_reason` records that a person asked rather than AO observing.
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
// The "is this task finished?" refusal is NOT here: it needs the session's
// derived status, which is assembled from PR facts at service read time. See
// Service.AttachCrewMember.
//
// Starting is BEST EFFORT and deliberately not fatal: the member is on the task
// either way, and a human who asked for a qa would rather have one they can open
// than an error and no member at all.
func (m *Manager) AttachCrewMember(ctx context.Context, devID domain.SessionID, role domain.CrewRole) (domain.SessionRecord, error) {
	member, err := m.attachCrewMemberRow(ctx, devID, role)
	if err != nil {
		return domain.SessionRecord{}, err
	}
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
func (m *Manager) attachCrewMemberRow(ctx context.Context, devID domain.SessionID, role domain.CrewRole) (domain.SessionRecord, error) {
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

	member, err := m.spawnSuspendedCrewMemberLocked(ctx, project, dev, role, domain.CrewJoinManual)
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
	dev, err := m.getRecord(ctx, rec.CrewID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("crew dev of %s: %w", id, err)
	}
	return dev, nil
}
