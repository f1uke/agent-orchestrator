package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A CREW is the one or two long-lived sessions that belong to ONE task and share
// ONE worktree: dev, which owns the branch, the worktree and the PR, and at most
// one subordinate (qa) that works in dev's tree.
//
// Everything in this file is written so that a SOLO session - every session an
// ordinary spawn creates - takes the zero-value path. The crew fields are empty,
// crewMembers returns nothing, and each guard is a no-op. That is deliberate:
// the lifetime paths this feature touches (teardown, reclaim, the boot and
// shutdown save-and-teardown) are the paths a regression would be worst in, so
// solo behaviour is decided by the absence of data rather than by a branch
// somebody has to remember to keep correct.

// resolveCrewDev validates a crew spawn and returns the dev session it joins.
//
// A crew member is not a task of its own: it inherits dev's branch and dev's
// worktree, and it may never outlive dev. So dev must be a live worker, and it
// must not itself be a subordinate - crews are flat by construction, which is
// what makes "tear the crew down" a single fan-out rather than a walk.
func (m *Manager) resolveCrewDev(ctx context.Context, project domain.ProjectRecord, devID domain.SessionID, role domain.CrewRole) (domain.SessionRecord, error) {
	if !role.Valid() || role.IsDev() {
		return domain.SessionRecord{}, fmt.Errorf("%w: role %q is not a joinable crew role", ErrInvalidCrew, role)
	}
	// A workspace project materialises MANY worktrees per session, and its
	// capture/destroy path (saveAndTeardownWorkspaceProject) is reached before the
	// shared-worktree guard - so a crew there could still remove a live members
	// trees on a daemon restart. Refused outright rather than half-supported: the
	// multi-repo shape is not part of this capability and is not exercised by any
	// of its tests.
	if project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		return domain.SessionRecord{}, fmt.Errorf("%w: project %s is a workspace project, which cannot host a crew", ErrInvalidCrew, project.ID)
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

// crewKeepsWorkspace reports whether this session must leave the shared worktree
// standing. It is the refcount, and it asks a different question of each role,
// because the two roles have a different relationship to the tree.
//
//   - A SUBORDINATE never removes it while its dev row exists. The tree is dev's:
//     dev cut the branch, dev holds the PR, and dev is itself a reclaim candidate
//     the moment it finishes, so deferring cannot strand the disk. Ownership, not
//     liveness, is the right test here - it also means a crew teardown removes the
//     tree exactly once, from dev, instead of racing its own fan-out.
//   - DEV keeps it while any subordinate is still ALIVE on it. Alive is "not
//     terminated": a suspended member is paused, and that tree is exactly what it
//     resumes into. In the normal path the fan-out has already ended every
//     subordinate by the time dev asks, so this only bites when the fan-out could
//     not - and then keeping the tree is the safe answer, and the reclaim log says
//     so once per reason.
//
// It is deliberately CREW-scoped rather than path-scoped. A path-scoped refcount
// would be more general, but every orchestrator of a project already shares ONE
// worktree path (the directory is named for the project, not the session) and
// SpawnOrchestrator's check-then-spawn is explicitly not atomic, so a project can
// legitimately hold two non-terminated orchestrator rows on one path. Path-scoping
// would start refusing orchestrator teardowns that succeed today - a visible
// change on the solo path, which is the one thing this capability may not cause.
// A session that is not in a crew takes neither branch.
func (m *Manager) crewKeepsWorkspace(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	if !rec.InCrew() || rec.Metadata.WorkspacePath == "" {
		return false, nil
	}
	members, err := m.crewMembers(ctx, rec)
	if err != nil {
		return false, err
	}
	return crewKeepsWorkspaceGiven(rec, members), nil
}

// crewKeepsWorkspaceGiven is crewKeepsWorkspace's decision over an already-read
// member list.
func crewKeepsWorkspaceGiven(rec domain.SessionRecord, members []domain.SessionRecord) bool {
	if !rec.InCrew() || rec.Metadata.WorkspacePath == "" {
		return false
	}
	for _, other := range members {
		if other.Metadata.WorkspacePath != rec.Metadata.WorkspacePath {
			continue
		}
		if rec.CrewRole.IsDev() {
			if !other.IsTerminated {
				return true // a live subordinate is still working in it
			}
			continue
		}
		if other.CrewRole.IsDev() {
			return true // not ours to remove
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
// exactly right while one session owns one branch - and exactly wrong for a crew,
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

// teardownCrewSubordinates tears down every non-dev member of rec's crew before
// dev itself goes. It is a no-op for a solo session and for a subordinate.
//
// Best-effort by design: a member that will not die must not prevent dev from
// terminating. The consequence of failing is visible and self-correcting - the
// surviving member still holds the worktree, so the refcount refuses the destroy
// and the reclaim log records a `workspace_shared` refusal naming the branch,
// which is a complete recovery instruction. The consequence of ABORTING dev's
// teardown would be a task nobody can finish.
func (m *Manager) teardownCrewSubordinates(ctx context.Context, dev domain.SessionRecord, cause string) {
	members, err := m.crewMembers(ctx, dev)
	if err != nil {
		m.logger.Warn("teardown: could not read crew; tearing dev down alone",
			"sessionID", dev.ID, "crew", dev.CrewID, "error", err)
		return
	}
	for _, member := range subordinatesFirst(members) {
		if member.CrewRole.IsDev() || member.IsTerminated {
			continue
		}
		m.logger.Info("teardown: ending crew member with its dev",
			"sessionID", member.ID, "crew", dev.CrewID, "role", string(member.CrewRole), "cause", cause)
		if _, err := m.Teardown(ctx, member.ID, cause); err != nil {
			m.logger.Error("teardown: crew member teardown failed; its worktree will be kept",
				"sessionID", member.ID, "crew", dev.CrewID, "error", err)
		}
	}
}

// saveKeepingSharedWorktree is saveAndTeardownOne for a crew member whose share
// of the worktree is not its to remove: everything except touching the tree.
//
// Both roles reach it. A subordinate defers to dev; dev defers to a subordinate
// that is still alive (a suspended one, say, which the shutdown sweep skips
// entirely). Either way nothing is removed, so there is nothing to preserve: the
// uncommitted work simply stays in the tree it is already in.
//
// It writes the same restore marker a solo save writes, with an EMPTY preserved
// ref - there is nothing to preserve, because nothing was removed. RestoreAll
// then relaunches the member into the worktree that is still on disk (Restore
// returns an already-registered worktree rather than re-adding it), so the member
// comes back exactly where it was.
func (m *Manager) saveKeepingSharedWorktree(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, destroyRuntime bool) error {
	row := domain.SessionWorktreeRecord{
		SessionID:    rec.ID,
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       rec.Metadata.Branch,
		WorktreePath: ws.Path,
		State:        "removed",
	}
	if err := m.store.UpsertSessionWorktree(ctx, row); err != nil {
		return fmt.Errorf("save %s: upsert worktree row: %w", rec.ID, err)
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID, domain.TerminationCauseDaemonShutdown); err != nil {
		return fmt.Errorf("save %s: mark terminated: %w", rec.ID, err)
	}
	handle := runtimeHandle(rec.Metadata)
	if destroyRuntime && handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			m.logger.Warn("save-teardown: crew member runtime destroy failed", "sessionID", rec.ID, "error", err)
		}
	}
	m.logger.Info("save-teardown: kept a crew worktree that is not this session to remove",
		"sessionID", rec.ID, "crew", rec.CrewID, "path", ws.Path)
	return nil
}

// crewSubordinatesFirst moves every crew DEV to the back of a session list,
// which puts every subordinate ahead of every dev while leaving the relative
// order of everything else alone. A list with no crew in it is returned
// unchanged, by identity, so the shutdown sweep on an ordinary machine iterates
// exactly the slice it always did.
func crewSubordinatesFirst(recs []domain.SessionRecord) []domain.SessionRecord {
	isCrewDev := func(rec domain.SessionRecord) bool { return rec.InCrew() && rec.CrewRole.IsDev() }
	anyDev := false
	for _, rec := range recs {
		if isCrewDev(rec) {
			anyDev = true
			break
		}
	}
	if !anyDev {
		return recs
	}
	out := make([]domain.SessionRecord, 0, len(recs))
	devs := make([]domain.SessionRecord, 0, 1)
	for _, rec := range recs {
		if isCrewDev(rec) {
			devs = append(devs, rec)
			continue
		}
		out = append(out, rec)
	}
	return append(out, devs...)
}

// crewDevsFirst moves every crew DEV to the FRONT of a session list, leaving the
// relative order of everything else alone. A list with no crew in it is returned
// unchanged, by identity, so the boot restore pass on an ordinary machine
// iterates exactly the slice it always did.
//
// It exists for RestoreAll. Only one member of a crew can hold the awake slot, so
// only one can carry a restore marker - but a database written before this rule
// existed, or one a bug got at, can hold two, and then the FIRST one restored wins
// and the second is refused. Restoring dev first makes that outcome deterministic
// and puts the slot where it belongs: dev owns the branch, the PR and the report.
func crewDevsFirst(recs []domain.SessionRecord) []domain.SessionRecord {
	isCrewDev := func(rec domain.SessionRecord) bool { return rec.InCrew() && rec.CrewRole.IsDev() }
	anyDev := false
	for _, rec := range recs {
		if isCrewDev(rec) {
			anyDev = true
			break
		}
	}
	if !anyDev {
		return recs
	}
	out := make([]domain.SessionRecord, 0, len(recs))
	for _, rec := range recs {
		if isCrewDev(rec) {
			out = append(out, rec)
		}
	}
	for _, rec := range recs {
		if !isCrewDev(rec) {
			out = append(out, rec)
		}
	}
	return out
}
