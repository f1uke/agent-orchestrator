package lifecycle

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AN AGENT ENDING ITSELF.
//
// Every other route to termination is one AO ordered: a kill, a purge, the
// shutdown sweep, auto-reclaim, a merged PR. Each of those either goes through
// session_manager.Teardown - where the crew rules live - or, for the merge, is
// routed back to the same fan-out through the injected crewReaper.
//
// An agent's own SessionEnd hook was the route that did neither. It set
// is_terminated on the row and stopped, which cost a real crew its worktree: dev
// obeyed a brief that said "do not push, do not open an MR", reported, ended
// itself, and AO filed the task as finished while its qa kept working for eighty
// minutes in a directory that had been freed under it.
//
// So the ending asks two questions before it writes anything.

// applyAgentExit records an ending the agent reported itself.
//
// It runs with m.mu RELEASED. Both branches below re-enter this Manager -
// parkUndelivered and markAgentExited go through mutate, and the crew fan-out
// reaches MarkTerminated on the way - and sync.Mutex is not reentrant. The
// re-read inside mutate is what keeps that safe: a signal that raced in between
// is seen, and both branches are no-ops against a row that has already ended.
func (m *Manager) applyAgentExit(ctx context.Context, rec domain.SessionRecord, s ports.ActivitySignal, now time.Time) error {
	reason := ""
	if s.End != nil {
		reason = s.End.Reason
	}
	at := timeOr(s.Timestamp, now)

	undelivered, err := m.exitLeavesWorkUndelivered(ctx, rec)
	if err != nil {
		return err
	}
	if undelivered {
		if err := m.parkUndelivered(ctx, rec.ID, at); err != nil {
			return err
		}
		m.emitLeftWaitingInput(ctx, rec, domain.ActivityParked, now)
		return nil
	}
	// This ending is the end. A crew is one TASK on one worktree, so dev ending
	// ends the task: the members go first, then dev's row records that it
	// stopped - the same order Teardown fans out in, reached through the same
	// injected hook the merge path uses (SetCrewReaper). Best-effort for the same
	// reason it is there: a member that will not die must not stop dev from
	// recording that it ended, and the surviving member still holds the worktree,
	// so the refcount refuses the destroy and the reclaim log says so.
	if m.crewReaper != nil {
		if err := m.crewReaper(ctx, rec.ID, domain.TerminationCauseDevExited); err != nil {
			slog.Default().Warn("lifecycle: crew teardown on agent exit failed; the member keeps its worktree",
				"session", rec.ID, "err", err)
		}
	}
	if err := m.markAgentExited(ctx, rec.ID, reason, at); err != nil {
		return err
	}
	m.emitLeftWaitingInput(ctx, rec, domain.ActivityExited, now)
	return nil
}

// emitLeftWaitingInput keeps the turn telemetry honest across an ending. A
// session blocked on a permission prompt when its agent quit still LEFT
// waiting_input, and the dwell it accumulated there is the whole point of the
// measurement - dropping it would make an abandoned prompt the one kind that
// never shows up. Nothing is emitted for any other previous state.
func (m *Manager) emitLeftWaitingInput(ctx context.Context, before domain.SessionRecord, to domain.ActivityState, now time.Time) {
	if before.Activity.State != domain.ActivityWaitingInput {
		return
	}
	after := before
	after.Activity = domain.Activity{State: to, LastActivityAt: now}
	for _, ev := range m.waitingInputEvents(after, before.Activity.State, before.Activity.LastActivityAt, now) {
		m.emitTelemetry(ctx, ev)
	}
}

// exitLeavesWorkUndelivered decides whether this ending leaves work nobody has
// seen - the question that separates "the task is finished" from "the task is
// waiting for you".
//
// UNDELIVERED IS: this session owns a materialized worktree and no pull request
// has ever been opened from it. The PR is AO's unit of delivery - it is how work
// leaves the machine and reaches a person - and it is a durable store fact this
// reducer already reads, so the predicate needs no git call from a layer that
// deliberately has no workspace and no runtime.
//
// It is NOT an unpushed-commit check, and deliberately so: asking "are there
// commits?" would need git, would answer a different question (work can be
// committed and still delivered, or uncommitted and worthless), and uncommitted
// work already has its own guard - Teardown's ReasonWorkspaceDirty refusal.
//
// Four things narrow it, each because the alternative is a daily regression:
//
//   - Only a WORKER. An orchestrator is a dispatcher: it never owns a PR and it
//     ends itself routinely, so this predicate would park every project's
//     orchestrator forever.
//   - Not a TODO. A prepared row has no runtime and no work to strand.
//   - It must hold a WORKTREE. A session that never materialized anywhere has
//     nowhere for undelivered work to be; the brief's "exited having done
//     nothing is fine to terminate" is exactly this row.
//   - It must OWN its branch: solo, or a crew's dev. A subordinate owns no
//     branch, no worktree and no PR - it delivers its verdict through dev and
//     its ending fans out to nobody - so keying off "no PR" there would park
//     every healthy qa the moment it finished.
//
// A false positive costs the human one click: an investigation-only worker that
// really was done sits in "Needs you" until it is killed. A false negative cost
// eighty minutes of a live agent working in a deleted directory.
func (m *Manager) exitLeavesWorkUndelivered(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	if rec.Kind != domain.KindWorker || rec.IsTodo {
		return false, nil
	}
	if rec.Metadata.WorkspacePath == "" {
		return false, nil
	}
	if rec.InCrew() && !rec.CrewRole.IsDev() {
		return false, nil
	}
	prs, err := m.store.ListPRsBySession(ctx, rec.ID)
	if err != nil {
		return false, err
	}
	return len(prs) == 0, nil
}

// parkUndelivered holds an ending open instead of filing it as finished.
//
// PARKED says it on the board: the deriver reads parked (for a session that has
// signalled) as needs_input/idle_aged, so the card sits in "Needs you" with its
// worktree intact rather than archiving to Done.
//
// SUSPENDED is what makes that stick. The agent's exit took its tmux with it, so
// a dead-runtime probe follows within the minute and ApplyRuntimeObservation
// would terminate the row 60 seconds later - parking alone would buy a minute and
// nothing else. is_suspended is already the durable "there is no process here"
// fact, and every reader honours it: the reaper skips it, boot reconciliation
// leaves it and its worktree alone, the shutdown sweep skips it, and auto-reclaim
// only ever considers terminated rows. Opening the card resumes the agent into
// the tree its work is still sitting in, which is the affordance a keep-warm
// merged worker already has.
//
// No termination account is recorded, because the session has not ended.
func (m *Manager) parkUndelivered(ctx context.Context, id domain.SessionID, at time.Time) error {
	return m.mutate(ctx, id, func(cur domain.SessionRecord, _ time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated || cur.IsSuspended {
			return cur, false
		}
		next := cur
		next.Activity = domain.Activity{State: domain.ActivityParked, LastActivityAt: at}
		// The deriver reads parked only for a session that has proved its hook
		// pipeline. This signal IS that proof, so a session whose very first
		// report is its ending still lands on needs_input rather than no_signal.
		if next.FirstSignalAt.IsZero() {
			next.FirstSignalAt = at
		}
		next.IsSuspended = true
		next.SleepReason = domain.SleepReasonUndelivered
		next.WokenBy = ""
		return next, true
	})
}

// markAgentExited writes the terminal row for an ending the agent reported.
//
// It is MarkTerminated's sibling rather than MarkTerminated itself: the source is
// the agent, not AO, and whatever the harness said about why is the only account
// of an ending AO did not order, so it is recorded rather than replaced by the
// name of an operation nobody ran.
func (m *Manager) markAgentExited(ctx context.Context, id domain.SessionID, reason string, at time.Time) error {
	return m.mutate(ctx, id, func(cur domain.SessionRecord, _ time.Time) (domain.SessionRecord, bool) {
		if cur.IsTerminated {
			return cur, false
		}
		next := cur
		next.IsTerminated = true
		next.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: at}
		if next.FirstSignalAt.IsZero() {
			next.FirstSignalAt = at
		}
		next.Termination = m.termination(cur, domain.TerminationSourceAgent, reason, at)
		return next, true
	})
}
