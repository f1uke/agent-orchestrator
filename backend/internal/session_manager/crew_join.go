package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// WHEN A TASK GAINS A QA: dev asks for one, when dev believes the work is done.
//
// It used to be an OBSERVATION: the first time dev touched a runtime surface -
// took the simulator lease, or pointed `ao preview` at the app - AO created the
// task's qa on the spot. That fired at the wrong end of the work. Touching the
// runtime is the moment dev STARTS driving the app, not the moment it is
// finished, so a qa woke up while dev was still installing builds and reading
// screens, and the two reached for the same device at the same time and fought
// over it. On a machine with one booted simulator that is not a nuisance, it is
// two agents undoing each other's work.
//
// So the trigger is gone and the verb is dev's: `ao crew review` (crew_attach.go,
// domain.CrewJoinReview), run when dev thinks the change is ready to be checked.
// The member that knows whether the work is done is the one that says so, which
// is precisely what an observation of dev's tooling could never tell.
//
// WHAT THE OBSERVATION IS STILL FOR. Removing a trigger and adding nothing gives
// back the failure it was buying protection against: a task that finished with no
// qa attached at all, silently, because nobody thought about it. So the touch is
// still recorded - as a FACT on dev's own row, creating nobody - and it is the
// input to the warning that replaces the trigger: a task that drove the app and
// reports its close-out with no qa ever on it is told so, out loud, in the
// message and on the card. See service/session.unreviewedRuntime.
//
// A task that never drives a runtime surface - a backend-only change - records
// nothing, is never nudged, and stays the one-agent task it should be.

// NoteRuntimeTouch records that this session just drove the app.
//
// It is BEST EFFORT and SILENT by design, for the same reason it always was: the
// caller is a sim claim or an `ao preview` - commands about something else
// entirely - and failing one of those because a fact could not be written would
// break the thing dev was actually doing.
//
// It is write-once at the STORE (the update matches only a row with no touch
// recorded), so a renewal, a second claim and a re-pointed preview are all
// no-ops. Nothing here checks eligibility: whether the task may have a qa at all
// is a question for the moment somebody asks for one, and recording what a
// mechanical task did with the app costs nothing and keeps the fact honest.
func (m *Manager) NoteRuntimeTouch(ctx context.Context, id domain.SessionID, touch domain.RuntimeTouch) {
	if !touch.Valid() {
		return
	}
	recorded, err := m.store.SetSessionRuntimeTouch(ctx, id, touch, m.clock())
	if err != nil {
		m.logger.Warn("crew: could not record that this task drove the app",
			"sessionID", id, "touch", string(touch), "error", err)
		return
	}
	if recorded {
		m.logger.Info("crew: this task drove the app", "sessionID", id, "touch", string(touch))
	}
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
