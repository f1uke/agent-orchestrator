package sessionmanager

import (
	"context"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// BOTH MEMBERS AWAKE, IN ONE WORKTREE.
//
// This file used to enforce the opposite. #225 refused to bring a crew member up
// while its crewmate was awake, because qa's `go test` over a tree dev is halfway
// through writing produces a result that is meaningless AND LOOKS FINE. The
// exclusion was the only thing that made a shared checkout safe, so it had to be
// enforced rather than asked for in a prompt.
//
// What replaced it is not a weaker promise, it is a different one:
//
//	Do not prevent the race. DETECT it and invalidate the result.
//
// A member brackets every build, test or device pass (`ao crew run --start` …
// `--end`, internal/service/crewrun) and a filesystem watcher counts writes to
// the worktree across that interval. A run the tree moved under is DISCARDED -
// never passed, never failed. That is exact where the exclusion was merely
// coarse, and it costs nothing when nobody is racing.
//
// So dev and qa now run at the same time, in one checkout, and neither waits for
// the other. The reasons for one worktree are unchanged and were the human's
// call: `xcodebuild` shares its cache within a worktree and iOS DerivedData
// caching is keyed on the source path, so a second worktree means full rebuilds
// and a second multi-gigabyte cache.
//
// WHAT SURVIVES THE EXCLUSION. The guard did two things and only one of them was
// the refusal. The other was to PROBE a member whose row claims to be awake and
// put a corpse to sleep, and that half is now the whole point of this file: a
// member whose agent died mid-turn still reads as awake off its row, and #236's
// "nobody is working on this" is derived from exactly that column. A crew showing
// a dead member as working is the same lie #236 exists to catch, one layer down.
//
// A SOLO session - every session an ordinary spawn creates - takes the first line
// of every function here and returns: no query, no probe, no behaviour change.

// The routes by which a session can become awake, named for the log line. This
// list IS the checklist: a new way to bring a session up has to appear here, and
// therefore has to have been given a reconciliation point.
const (
	routeSpawn    = "spawn"
	routeRestore  = "restore"
	routeResume   = "resume"
	routeRelaunch = "relaunch"
	routeRestart  = "restart"
)

// lockCrew serialises the crew-shape decisions for ONE crew and returns the
// unlock.
//
// It did not exist only for the exclusion and it outlives it: forming a crew is
// check-then-create ("does this task already have a qa?" then "create one"), so
// two attaches that arrive together would otherwise both see a free seat. The
// review engine's lockWorker is the same shape for the same reason.
//
// A session in NO crew returns a no-op unlock and never touches the map, so an
// ordinary session's restore is not serialised against anything - a manager-wide
// lock held across a relaunch (git worktree restore, tmux create) would be a real
// behaviour change on the solo path, and this must not be one.
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

// reconcileCrewPeers is what each of the five wake routes calls now, and it
// NEVER refuses: bringing this member up is allowed whatever its crewmate is
// doing.
//
// What it does instead is settle the crewmates' rows against their runtimes. A
// member that claims to be awake is probed:
//
//   - alive           -> left exactly as it is. Two awake members is the point.
//   - definitively dead -> marked suspended, so the board stops reporting a
//     corpse as a working agent. Its card stays in its lane, its worktree stays
//     on disk, and opening it resumes it like any other paused session.
//   - probe failed    -> left alone. A failed probe is never proof of death, a
//     load-bearing rule everywhere else in this daemon, and the cost of guessing
//     wrong is now nothing worse than a stale row that the next route settles.
//
// It is best effort for the same reason: nothing downstream depends on it having
// run, so a reconciliation failure must not cost the caller its wake.
func (m *Manager) reconcileCrewPeers(ctx context.Context, rec domain.SessionRecord, route string) {
	if !rec.InCrew() {
		return
	}
	members, err := m.crewMembers(ctx, rec)
	if err != nil {
		m.logger.Warn("crew: could not read the crew to settle its rows",
			"sessionID", rec.ID, "crew", rec.CrewID, "route", route, "error", err)
		return
	}
	for _, other := range members {
		if other.ID == rec.ID || !other.Awake() {
			continue
		}
		alive, err := m.holderStillRunning(ctx, other)
		if err != nil || alive {
			continue
		}
		m.logger.Warn("crew: a member's row says awake but its runtime is gone; marking it asleep",
			"sessionID", other.ID, "crew", other.CrewID, "role", string(other.CrewRole),
			"handle", other.Metadata.RuntimeHandleID, "route", route, "wokenBy", rec.ID)
		// Idle, not a turn: there are no turns any more. What is true of this row is
		// that there is no process behind it and looking at its card should bring it
		// back - which is precisely what SleepReasonIdle means to every reader.
		if err := m.lcm.MarkSuspended(ctx, other.ID, domain.SleepReasonIdle); err != nil {
			m.logger.Warn("crew: could not mark a dead member asleep",
				"sessionID", other.ID, "crew", other.CrewID, "error", err)
		}
	}
}

// holderStillRunning probes whether a member that claims to be awake really has
// an agent behind it. A member with no handle at all never ran.
func (m *Manager) holderStillRunning(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return false, nil
	}
	return m.runtime.IsAlive(ctx, handle)
}
