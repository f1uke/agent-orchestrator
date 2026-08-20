package sessionmanager

import (
	"context"
	"fmt"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ONE AWAKE AT A TIME.
//
// The two members of a crew share ONE worktree. If both are running, qa's
// `go test` reads a tree dev is halfway through writing - the results are
// meaningless, and a half-saved file fails the run outright. On an iOS task they
// also contend for the `ao sim` lease, which locks every other session off the
// device. So the exclusion is not tidiness: it is the only thing that makes a
// shared worktree safe at all, and it has to be ENFORCED rather than asked for in
// a prompt.
//
// What is excluded is being AWAKE - having a running agent (domain.Awake: not
// terminated, not suspended, not a TODO). See the comment on that method for why
// the activity state, which the design's baton parks on, cannot carry this.
//
// The HOLDER of the slot is not stored anywhere. It is DERIVED: the crew's one
// awake row. Two columns that already exist, written by the existing lifecycle
// reducer on every path, so there is no memo to keep in step and nothing to
// reconcile at boot - after a daemon restart the holder is simply whatever the
// recovered rows say, recovered by the same rules that recover a solo session.
//
// A SOLO session - every session an ordinary spawn creates - takes the first line
// of every function here and returns: no query, no probe, no behaviour change.

// The routes by which a session can become awake, named for the refusal message
// and the log line. This list IS the checklist: a new way to bring a session up
// has to appear here, and therefore has to have been given a guard.
const (
	routeSpawn    = "spawn"
	routeRestore  = "restore"
	routeResume   = "resume"
	routeRelaunch = "relaunch"
	routeRestart  = "restart"
)

// lockCrew serialises the slot decisions for ONE crew and returns the unlock.
//
// Without it the guard is a check-then-act: two wakes that arrive together (two
// clicks on the board, a wake racing a boot restore) can both read a free slot
// and both bring a member up, which is precisely the state the guard exists to
// make impossible. The review engine's lockWorker is the same shape for the same
// reason.
//
// A session in NO crew returns a no-op unlock and never touches the map, so an
// ordinary session's restore is not serialised against anything - a manager-wide
// lock held across a relaunch (git worktree restore, tmux create) would be a real
// behaviour change on the solo path, and this must not be one.
//
// Taken only at the OUTER entry points (Spawn's crew branch, Restore, Resume,
// restartInPlace, RestoreAll's per-session body). The guard itself never locks,
// so the inner call at the relaunch chokepoint cannot re-enter it.
func (m *Manager) lockCrew(crewID domain.SessionID) func() {
	if crewID == "" {
		return func() {}
	}
	m.crewMu.Lock()
	if m.crewLocks == nil {
		m.crewLocks = make(map[domain.SessionID]*sync.Mutex)
	}
	mu, ok := m.crewLocks[crewID]
	if !ok {
		mu = &sync.Mutex{}
		m.crewLocks[crewID] = mu
	}
	m.crewMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// crewSlotGuard refuses to make rec awake while a DIFFERENT member of its crew
// already is. It is the enforcement point, and every route that can bring a
// member up calls it: Spawn (a new member), Restore, Resume (and therefore Wake)
// restartInPlace, and relaunchRestoredSession - the chokepoint the boot restore
// pass funnels through.
//
// It does not trust the row on its own. A row that CLAIMS the slot is probed, so
// a holder whose agent died mid-turn cannot keep the crew locked out:
//
//   - alive          -> refuse, naming the holder.
//   - definitively dead -> steal the slot: the corpse is marked suspended (its
//     card stays in its lane, its worktree stays on disk, Resume brings it back)
//     and the caller proceeds.
//   - probe failed   -> refuse, saying so. A failed probe is never proof of death
//     (a load-bearing rule everywhere else in this daemon). Not a deadlock: the
//     next attempt probes again, and boot reconciliation settles it for good.
//
// Refusing is the safe direction. Letting a second member up while the first is
// genuinely running is the exact corruption this exists to prevent; refusing when
// the first is in fact dead costs one retry.
func (m *Manager) crewSlotGuard(ctx context.Context, rec domain.SessionRecord, route string) error {
	if !rec.InCrew() {
		return nil
	}
	members, err := m.crewMembers(ctx, rec)
	if err != nil {
		return fmt.Errorf("crew slot: read crew %s: %w", rec.CrewID, err)
	}
	return m.refuseWhileAnyAwake(ctx, members, string(rec.ID), route)
}

// crewSlotGuardForNewMember is crewSlotGuard for a member that does not exist
// yet, and it has to look at a DIFFERENT set of rows.
//
// At crew-spawn time dev is usually still SOLO: recordCrew writes the membership
// only after the new member has materialized, so that a spawn which fails
// part-way leaves no half-formed crew behind. Asking dev whether it is "in a
// crew" therefore answers no, and crewSlotGuard would wave the spawn straight
// through. The set that matters is the crew that is ABOUT to exist: dev itself,
// plus any crewmate dev already has.
func (m *Manager) crewSlotGuardForNewMember(ctx context.Context, dev domain.SessionRecord, route string) error {
	members, err := m.crewMembers(ctx, dev) // empty while dev is still solo
	if err != nil {
		return fmt.Errorf("crew slot: read crew %s: %w", dev.CrewID, err)
	}
	return m.refuseWhileAnyAwake(ctx, append(members, dev), "a new crew member", route)
}

// refuseWhileAnyAwake is the shared body of both guards: refuse while any of
// these rows really holds the slot, and steal it from one that only claims to.
func (m *Manager) refuseWhileAnyAwake(ctx context.Context, candidates []domain.SessionRecord, requester, route string) error {
	for _, other := range candidates {
		if !other.Awake() {
			continue
		}
		alive, err := m.holderStillRunning(ctx, other)
		if err != nil {
			return fmt.Errorf("%w: %s (%s) holds the crew slot and its runtime could not be probed: %w",
				ErrCrewBusy, other.ID, roleLabel(other), err)
		}
		if alive {
			return fmt.Errorf("%w: %s (%s) is awake in %s; release it before %s brings up %s",
				ErrCrewBusy, other.ID, roleLabel(other), other.Metadata.WorkspacePath, route, requester)
		}
		// The holder is a corpse: its row says awake but its runtime is gone. Free
		// the slot rather than leaving the crew deadlocked until the next boot.
		m.logger.Warn("crew slot: the holder's runtime is gone; releasing the slot it still claimed",
			"sessionID", other.ID, "crew", other.CrewID, "role", string(other.CrewRole),
			"handle", other.Metadata.RuntimeHandleID, "route", route, "requestedBy", requester)
		if err := m.lcm.MarkSuspended(ctx, other.ID); err != nil {
			return fmt.Errorf("crew slot: release dead holder %s: %w", other.ID, err)
		}
	}
	return nil
}

// roleLabel names a session's crew role for a human. dev is still SOLO when it
// blocks the spawn that would form its crew, so the honest label there is what it
// is at that moment, not an empty pair of brackets.
func roleLabel(rec domain.SessionRecord) string {
	if !rec.InCrew() {
		return "not yet in a crew"
	}
	return string(rec.CrewRole)
}

// holderStillRunning probes whether a member that claims the slot really has an
// agent behind it. A member with no handle at all never ran, so it is not
// holding anything.
func (m *Manager) holderStillRunning(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return false, nil
	}
	return m.runtime.IsAlive(ctx, handle)
}

// CrewSlotHolder reports which member of this session's crew is awake right now.
// It is the observable form of the derivation - the same question the guard asks
// - so "who holds it" can be answered at any moment without a stored field. A
// solo session is never a holder and reports none.
func (m *Manager) CrewSlotHolder(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("crew slot holder %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, false, fmt.Errorf("crew slot holder %s: %w", id, ErrNotFound)
	}
	return m.crewHolderOf(ctx, rec)
}

// CrewTreeWriter names the session that is currently AWAKE in worker's checkout,
// i.e. the one that could be writing files in it. It is the review engine's
// TreeWriter hook (wired in the daemon), phrased in the terms review cares about
// so that package needs no crew vocabulary of its own.
//
// A SOLO worker reports nobody, always. Its tree has exactly one writer and that
// writer IS the session under review; reviewing while it works is what AO has
// always done, and nothing here changes it. Only a shared worktree - a crew - can
// report a writer.
func (m *Manager) CrewTreeWriter(ctx context.Context, worker domain.SessionRecord) (domain.SessionID, bool, error) {
	holder, ok, err := m.crewHolderOf(ctx, worker)
	if err != nil || !ok {
		return "", false, err
	}
	return holder.ID, true, nil
}

// crewHolderOf is the derivation itself: the crew's one awake row.
func (m *Manager) crewHolderOf(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, bool, error) {
	if !rec.InCrew() {
		return domain.SessionRecord{}, false, nil
	}
	if rec.Awake() {
		return rec, true, nil
	}
	members, err := m.crewMembers(ctx, rec)
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("crew slot holder %s: %w", rec.ID, err)
	}
	for _, other := range members {
		if other.Awake() {
			return other, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

// ReleaseCrewSlot puts a session to sleep so the crew's slot is free: mark it
// suspended, then reap its tmux. It is exactly the pair the idle sweep uses
// (MarkSuspended then reapRuntimeIfAlive), in that order, so the reaped agent's
// late "exited" hook is ignored rather than racing in to terminate the card.
//
// The session keeps its card, its lane, its worktree and its transcript; Resume
// brings it back where it was. Releasing an already-released session is a no-op,
// so a caller may release unconditionally.
//
// It deliberately does NOT require the session to be in a crew yet. Forming a
// crew means spawning a second member into a running dev's tree, and the slot has
// to be free before that spawn is allowed - but dev is recorded as a crew member
// only AFTER the new member materializes (recordCrew), so at the moment it must
// stand down it is still solo. Refusing here would make a crew impossible to
// form. It IS refused for an orchestrator, which shares its worktree with every
// other orchestrator of the project and is not a task.
//
// Best-effort on the runtime half, deliberately: a tmux that will not die must
// not leave the slot recorded as held. The record is what the guard reads, and a
// stray pane is reaped later (agent exit, idle sweep, daemon restart).
func (m *Manager) ReleaseCrewSlot(ctx context.Context, id domain.SessionID) error {
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return fmt.Errorf("release crew slot: %w", err)
	}
	if rec.Kind == domain.KindOrchestrator {
		return fmt.Errorf("%w: %s is an orchestrator, not a task member", ErrInvalidCrew, id)
	}
	if !rec.Awake() {
		return nil
	}
	if err := m.lcm.MarkSuspended(ctx, id); err != nil {
		return fmt.Errorf("release crew slot %s: %w", id, err)
	}
	if err := m.reapRuntimeIfAlive(ctx, id, runtimeHandle(rec.Metadata)); err != nil {
		m.logger.Warn("crew slot: released the slot but the runtime would not go",
			"sessionID", id, "crew", rec.CrewID, "error", err)
	}
	m.logger.Info("crew slot: released", "sessionID", id, "crew", rec.CrewID, "role", string(rec.CrewRole))
	return nil
}

// HandOverCrewSlot passes the slot from one crew member to another: release
// `from`, then wake `to`.
//
// This is the MECHANISM only. It deliberately contains no policy about who
// should go next, no queue and no scheduler - that decision belongs with qa,
// which does not exist yet.
//
// It cannot deadlock, and the ordering is why: the release is unconditional and
// depends on nothing about the taker, so if the wake then fails the slot is FREE
// rather than held by a member nobody is using. Either member can be resumed
// afterwards, by this call again or by a human opening the card. The opposite
// order - wake first - would have to hold both awake for an instant, which is the
// one state this whole file exists to make impossible.
func (m *Manager) HandOverCrewSlot(ctx context.Context, from, to domain.SessionID) (domain.SessionRecord, error) {
	if from == to {
		return domain.SessionRecord{}, fmt.Errorf("%w: cannot hand the crew slot to its own holder %s", ErrInvalidCrew, from)
	}
	fromRec, err := m.getRecord(ctx, from)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("hand over crew slot: %w", err)
	}
	toRec, err := m.getRecord(ctx, to)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("hand over crew slot: %w", err)
	}
	if !fromRec.InCrew() || !toRec.InCrew() || fromRec.CrewID != toRec.CrewID {
		return domain.SessionRecord{}, fmt.Errorf("%w: %s and %s are not members of one crew", ErrInvalidCrew, from, to)
	}
	if toRec.IsTerminated {
		return domain.SessionRecord{}, fmt.Errorf("%w: %s is terminated and cannot take the crew slot", ErrInvalidCrew, to)
	}
	if err := m.ReleaseCrewSlot(ctx, from); err != nil {
		return domain.SessionRecord{}, err
	}
	m.logger.Info("crew slot: handing over", "crew", fromRec.CrewID, "from", from, "to", to)
	return m.Resume(ctx, to)
}
