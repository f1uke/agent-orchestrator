package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// WHEN A TASK GAINS A QA: dev's first touch of a runtime surface.
//
// Every task starts as dev alone. It gains a qa the first time dev does
// something only a running app can be the point of - takes the simulator lease,
// or points `ao preview` at the app. Both are already facts the daemon owns (the
// per-session sim lease, and sessions.preview_url / preview_revision), so this
// costs no new instrumentation.
//
// WHY THAT SIGNAL, argued rather than assumed (design §1.12.1):
//
//   - It is an OBSERVATION, not a request. It never routes through dev's
//     judgement about whether its own work needs testing, which was rejected on
//     incentive grounds and stays rejected.
//   - "First commit" is the obvious rival and is worse twice over: there is no
//     per-session local-commit observer (HEAD is read through the SCM providers,
//     for PRs), so it would need new instrumentation - and it fires on EVERY
//     task, which reinstates precisely the cost lazy creation exists to remove.
//   - `ao smoke set` is a dead candidate: the checklist protocol belongs to qa,
//     so nothing authors one before qa exists.
//
// WHAT IT KILLS: a backend-only task never touches a runtime surface, so it never
// gets a qa and never pays the turns a qa spends learning there is nothing to
// exercise. WHAT IT COSTS, plainly: a backend change with subtle testable
// behaviour gets no second pair of eyes (`ao crew add` by hand is still there),
// and most of the context saving the old spawn-time analysis measured is
// forfeited - it needed a trigger inside the first few turns, and a runtime touch
// is usually later than that.
//
// FIRING ONCE is a property of the data, not of a flag: creating qa writes dev's
// crew columns in the same breath, and wantsCrew is false for a session already
// in a crew. It stays false after that qa is stood down, so the transition is
// absent -> present, one way, once, never back.

// NoteRuntimeTouch is the trigger: dev just did `reason`, so give this task the
// qa its size asks for, if it does not have one and is allowed one.
//
// It is BEST EFFORT and SILENT by design. The caller is a sim claim or an `ao
// preview` - commands about something else entirely - and failing one of those
// because a crew could not be formed would break the thing dev was actually
// doing in order to add a member the human can add by hand. Every refusal is an
// ordinary outcome (a mechanical task, a workspace project, an orchestrator, a
// task that already has a qa) and says nothing.
//
// It is SYNCHRONOUS: dev's command waits for qa to exist and start. That is once
// per task, and it buys the guarantee that the next command dev runs already
// sees the crew it just created - a member appearing several seconds later, in
// the middle of dev's next turn, is a worse surprise than a claim that took a
// moment.
func (m *Manager) NoteRuntimeTouch(ctx context.Context, id domain.SessionID, reason domain.CrewJoinReason) {
	if !reason.Automatic() {
		return
	}
	qa, ok := m.createCrewMemberForTouch(ctx, id, reason)
	if !ok {
		return
	}
	m.logger.Info("crew: qa joined the task", "crew", id, "dev", id, "qa", qa.ID, "reason", string(reason))
	// Outside the crew lock: Resume takes it itself, and lockCrew is not
	// reentrant. A member that fails to start is still ON the task and a human can
	// open it, which is why this is logged rather than rolled back.
	if _, err := m.startCrewMember(ctx, qa.ID); err != nil {
		m.logger.Warn("crew: qa was created but could not be started; open its card to start it",
			"crew", id, "qa", qa.ID, "error", err)
	}
}

// createCrewMemberForTouch is the check-then-create half, under the crew lock so
// two touches arriving together (a claim and a preview in the same instant)
// cannot both see a free seat. It returns false for every ordinary refusal.
func (m *Manager) createCrewMemberForTouch(ctx context.Context, id domain.SessionID, reason domain.CrewJoinReason) (domain.SessionRecord, bool) {
	dev, err := m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, false
	}
	project, err := m.loadProject(ctx, dev.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, false
	}
	// Cheap answer first, outside the lock: the overwhelming majority of touches
	// are by a task that already has its qa, or by one that will never have one.
	if !wantsCrew(project, dev) {
		return domain.SessionRecord{}, false
	}
	defer m.lockCrew(dev.ID)()
	// Re-read under the lock: the seat may have been taken between the two.
	dev, err = m.getRecord(ctx, dev.ID)
	if err != nil || !wantsCrew(project, dev) {
		return domain.SessionRecord{}, false
	}
	qa, err := m.spawnSuspendedCrewMemberLocked(ctx, project, dev, domain.CrewRoleQA, reason)
	if err != nil {
		m.logger.Warn("crew: could not create qa for this task; it continues solo",
			"sessionID", dev.ID, "reason", string(reason), "error", err)
		return domain.SessionRecord{}, false
	}
	return qa, true
}

// startCrewMember brings a just-created member up.
//
// A member is created suspended and started immediately, rather than launched
// outright, because Resume is the one path that knows how to bring a session up
// in a worktree that already exists and has a live agent in it. It is recorded as
// WokenByWake: AO asked for it by name, which is what `ao crew wake` records too.
func (m *Manager) startCrewMember(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	return m.Resume(ctx, id, domain.WokenByWake)
}
