package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A CREW is the one or two long-lived sessions that belong to ONE task and share
// ONE worktree: dev, which owns the branch, the worktree and the PR, and at most
// one subordinate (qa) that works in dev's tree.
//
// Everything in this file is written so that a SOLO session — every session an
// ordinary spawn creates — takes the zero-value path. The crew fields are empty,
// crewMembers returns nothing, and each guard is a no-op. That is deliberate:
// the lifetime paths this feature touches (teardown, reclaim, the boot and
// shutdown save-and-teardown) are the paths a regression would be worst in, so
// solo behaviour is decided by the absence of data rather than by a branch
// somebody has to remember to keep correct.

// resolveCrewDev validates a crew spawn and returns the dev session it joins.
//
// A crew member is not a task of its own: it inherits dev's branch and dev's
// worktree, and it may never outlive dev. So dev must be a live worker, and it
// must not itself be a subordinate — crews are flat by construction, which is
// what makes "tear the crew down" a single fan-out rather than a walk.
func (m *Manager) resolveCrewDev(ctx context.Context, devID domain.SessionID, role domain.CrewRole) (domain.SessionRecord, error) {
	if !role.Valid() || role.IsDev() {
		return domain.SessionRecord{}, fmt.Errorf("%w: role %q is not a joinable crew role", ErrInvalidCrew, role)
	}
	dev, ok, err := m.store.GetSession(ctx, devID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("crew spawn: %w", err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("%w: dev %s does not exist", ErrInvalidCrew, devID)
	}
	if dev.Kind == domain.KindOrchestrator {
		return domain.SessionRecord{}, fmt.Errorf("%w: %s is an orchestrator, not a task", ErrInvalidCrew, devID)
	}
	if dev.IsTerminated {
		return domain.SessionRecord{}, fmt.Errorf("%w: dev %s is terminated", ErrInvalidCrew, devID)
	}
	if dev.Metadata.Branch == "" || dev.Metadata.WorkspacePath == "" {
		return domain.SessionRecord{}, fmt.Errorf("%w: dev %s has no materialized worktree to share", ErrInvalidCrew, devID)
	}
	if dev.InCrew() && !dev.CrewRole.IsDev() {
		return domain.SessionRecord{}, fmt.Errorf("%w: %s is itself a %s member; crews do not nest", ErrInvalidCrew, devID, dev.CrewRole)
	}
	return dev, nil
}

// recordCrew writes the membership onto BOTH rows once the member has actually
// materialized. Doing it after materialize (rather than at seed time) means a
// spawn that fails part-way leaves no half-formed crew behind: the seed row is
// rolled back and dev is still the solo session it was.
func (m *Manager) recordCrew(ctx context.Context, dev domain.SessionRecord, memberID domain.SessionID, role domain.CrewRole) error {
	now := m.clock()
	if !dev.InCrew() {
		if _, err := m.store.SetSessionCrew(ctx, dev.ID, dev.ID, domain.CrewRoleDev, now); err != nil {
			return fmt.Errorf("crew spawn: mark dev %s: %w", dev.ID, err)
		}
	}
	if _, err := m.store.SetSessionCrew(ctx, memberID, dev.ID, role, now); err != nil {
		return fmt.Errorf("crew spawn: mark member %s: %w", memberID, err)
	}
	return nil
}

// crewMembers returns every OTHER session in rec's crew. It is empty for a solo
// session, which is what keeps every caller's solo path unchanged.
//
// It reads the full session list rather than a dedicated query: a crew holds two
// rows, the callers are teardown-rate rather than poll-rate, and the paths that
// need it (SaveAndTeardownAll, reconcileLive) already hold that list.
func (m *Manager) crewMembers(ctx context.Context, rec domain.SessionRecord) ([]domain.SessionRecord, error) {
	if !rec.InCrew() {
		return nil, nil
	}
	all, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	return siblingsOf(rec, all), nil
}

// siblingsOf picks rec's crewmates out of a session list.
func siblingsOf(rec domain.SessionRecord, all []domain.SessionRecord) []domain.SessionRecord {
	if !rec.InCrew() {
		return nil
	}
	out := make([]domain.SessionRecord, 0, 1)
	for _, other := range all {
		if other.ID == rec.ID || other.CrewID != rec.CrewID {
			continue
		}
		out = append(out, other)
	}
	return out
}

// workspaceHeldByLiveCrewMember reports whether tearing rec's worktree down would
// pull it out from under a crewmate that is still alive on it.
//
// This is the refcount, and it is deliberately CREW-scoped rather than
// path-scoped. A path-scoped refcount would be more general, but every
// orchestrator of a project already shares ONE worktree path (the directory is
// named for the project, not the session) and SpawnOrchestrator's check-then-spawn
// is explicitly not atomic, so a project can legitimately hold two non-terminated
// orchestrator rows on one path. Path-scoping would start refusing orchestrator
// teardowns that succeed today — a visible change on the solo path, which is the
// one thing this capability may not cause. Crew-scoping is a no-op for every
// session that is not in a crew.
//
// "Alive" is NOT terminated. A suspended member is paused, not finished: its
// worktree is exactly what it resumes into.
func (m *Manager) workspaceHeldByLiveCrewMember(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	members, err := m.crewMembers(ctx, rec)
	if err != nil {
		return false, err
	}
	return anyLiveHolder(rec, members), nil
}

// anyLiveHolder is workspaceHeldByLiveCrewMember's decision, over an already-read
// member list.
func anyLiveHolder(rec domain.SessionRecord, members []domain.SessionRecord) bool {
	if rec.Metadata.WorkspacePath == "" {
		return false
	}
	for _, other := range members {
		if other.IsTerminated {
			continue
		}
		if other.Metadata.WorkspacePath == rec.Metadata.WorkspacePath {
			return true
		}
	}
	return false
}

// subordinatesFirst orders a crew so every non-dev member comes before dev.
//
// Teardown fans out in this order (design 1.10: qa first, then dev, then
// reclaim) and so does the shutdown sweep. Ordering matters because dev is the
// member that captures uncommitted work and removes the shared tree: if dev went
// first it would find a live subordinate holding the tree, decline, and leave the
// capture to whichever member happened to run last. The result would still be
// lossless, but the preserve ref would be recorded against the wrong session.
func subordinatesFirst(members []domain.SessionRecord) []domain.SessionRecord {
	out := make([]domain.SessionRecord, 0, len(members))
	for _, m := range members {
		if !m.CrewRole.IsDev() {
			out = append(out, m)
		}
	}
	for _, m := range members {
		if m.CrewRole.IsDev() {
			out = append(out, m)
		}
	}
	return out
}

// runtimeNameBranch decides the branch a runtime handle is NAMED after.
//
// The tmux adapter mirrors a session's branch into its tmux session name, which
// makes `tmux ls` line up with the branch and the worktree directory. That is
// exactly right while one session owns one branch — and exactly wrong for a crew,
// whose members share one branch and would therefore share one tmux: one Destroy
// would kill both agents, and the idle sweep could not tell whose runtime it was
// reaping.
//
// A non-dev member is named after its SESSION ID instead, which is what an empty
// branch selects (tmux.SessionNameFor). That fallback is not new: the reviewer
// pane already uses it. dev and every solo session keep branch naming unchanged.
func runtimeNameBranch(branch string, role domain.CrewRole) string {
	if role.Valid() && !role.IsDev() {
		return ""
	}
	return branch
}
