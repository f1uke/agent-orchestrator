package sessionmanager

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// FORMING THE CREW: turning `--task-size` into a second session.
//
// A `standard` or `deep` task is worked by dev AND qa; a `mechanical` one by dev
// alone (domain.TaskSize.WantsCrew, which is the single place that rule lives).
// This file is what makes that true for a real spawn, and it has exactly one
// shape to get right: qa is BORN SUSPENDED.
//
// Born suspended means the row is INSERTED already asleep - one write, with
// dev's branch, dev's worktree and qa's kickoff prompt on it, and no runtime, no
// tmux and no provisioning. Three things follow, and each of them is why it is
// done this way rather than by spawning qa and then putting it to sleep:
//
//   - There is no instant at which two members of the crew are awake, so
//     one-awake-at-a-time (#225) never has to be argued with in order to form the
//     crew it protects. domain.Awake() is false for a suspended row, so qa simply
//     never holds the slot.
//   - `ao send --session <qa-id>` works from t0: the id exists, and #217's queue
//     holds the message until qa wakes.
//   - Nothing is spent. A qa that is never woken costs one row.
//
// qa's system prompt is NOT persisted: relaunchRestoredSession recomputes it from
// the row (including crew_role) at wake time, which is also how an edited qa base
// reaches a qa that was created before the edit.

// formCrew gives a freshly materialized dev the crew its task size asks for.
//
// It is BEST EFFORT on purpose. By the time it runs, dev exists, holds a
// worktree and is already working on the task; failing the spawn now would throw
// all of that away over a member the human can add later. A crew that did not
// form is visible (no qa chip on the card) and recoverable, where a spawn that
// rolled back a working dev is neither.
//
// A solo outcome is the correct outcome for anything that is not an ordinary
// single-repo worker task: mechanical size, an orchestrator, a workspace project
// (whose multi-worktree capture path is reached before the shared-worktree guard,
// so a crew there could still remove a live member's trees - #224), or a dev that
// somehow has no materialized tree to share.
func (m *Manager) formCrew(ctx context.Context, project domain.ProjectRecord, dev domain.SessionRecord) domain.SessionRecord {
	if !wantsCrew(project, dev) {
		return dev
	}
	qa, err := m.spawnSuspendedCrewMember(ctx, project, dev, domain.CrewRoleQA, joinedAtSpawn)
	if err != nil {
		m.logger.Warn("crew: could not form the crew; the task continues solo",
			"sessionID", dev.ID, "role", string(domain.CrewRoleQA), "error", err)
		return dev
	}
	m.logger.Info("crew: formed", "crew", dev.ID, "dev", dev.ID, "qa", qa.ID, "taskSize", string(dev.TaskSize.WithDefault()))
	// Re-read dev: recordCrew has just stamped its crew columns, and every caller
	// returns this record to the API.
	fresh, err := m.getRecord(ctx, dev.ID)
	if err != nil {
		return dev
	}
	return fresh
}

// wantsCrew is the whole eligibility test, in one place so the answer is the same
// from Spawn and from StartTodo.
func wantsCrew(project domain.ProjectRecord, dev domain.SessionRecord) bool {
	if dev.Kind != domain.KindWorker || dev.InCrew() {
		return false
	}
	if !dev.TaskSize.WantsCrew() {
		return false
	}
	if project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		return false
	}
	return dev.Metadata.Branch != "" && dev.Metadata.WorkspacePath != ""
}

// spawnSuspendedCrewMember creates one crew member asleep in dev's worktree.
//
// It deliberately does NOT go through materialize: there is no branch to cut (it
// is dev's), no worktree to create (it is dev's, and a live agent is in it), no
// provisioning to run (re-running post-create commands would fire an install into
// a tree somebody is working in) and no runtime to launch. What is left is the
// row, and the row is written already suspended.
func (m *Manager) spawnSuspendedCrewMember(ctx context.Context, project domain.ProjectRecord, dev domain.SessionRecord, role domain.CrewRole, join crewJoin) (domain.SessionRecord, error) {
	// Serialized against every other slot decision for this crew, exactly as a
	// crew Spawn is. dev's id is the crew key whether or not the crew exists yet.
	defer m.lockCrew(dev.ID)()
	return m.spawnSuspendedCrewMemberLocked(ctx, project, dev, role, join)
}

// spawnSuspendedCrewMemberLocked is the body above, for a caller that ALREADY
// holds the crew lock.
//
// It exists because AttachCrewMember is check-then-create - "does this task
// already have a qa?" then "create one" - and those two have to be atomic
// together or two racing attaches both see a free seat. lockCrew is not
// reentrant (#225 takes it at the OUTER entries only, so the guard beneath it
// cannot re-enter), so the lock has to move out to the entry point rather than
// be taken twice.
func (m *Manager) spawnSuspendedCrewMemberLocked(ctx context.Context, project domain.ProjectRecord, dev domain.SessionRecord, role domain.CrewRole, join crewJoin) (domain.SessionRecord, error) {
	if !role.Valid() || role.IsDev() {
		return domain.SessionRecord{}, fmt.Errorf("%w: role %q is not a joinable crew role", ErrInvalidCrew, role)
	}
	harness := effectiveHarness(dev.Harness, domain.KindWorker, project.Config)
	if _, ok := m.agents.Agent(harness); !ok {
		return domain.SessionRecord{}, fmt.Errorf("%w: %q", ErrUnknownHarness, harness)
	}
	now := m.clock()
	seed := domain.SessionRecord{
		ProjectID:   dev.ProjectID,
		IssueID:     dev.IssueID,
		Kind:        domain.KindWorker,
		CreatedAt:   now,
		UpdatedAt:   now,
		Harness:     harness,
		DisplayName: dev.DisplayName,
		Activity:    domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		BaseBranch:  dev.BaseBranch,
		PRTarget:    dev.PRTarget,
		TaskSize:    dev.TaskSize.WithDefault(),
		CreatedBy:   dev.CreatedBy,
		// Asleep from the first byte written about it. This is the whole
		// mechanism: no runtime is created, so nothing has to be torn down, and
		// Awake() is false, so the crew slot is never taken.
		IsSuspended: true,
		// ...and asleep for a REASON that is recorded, so that merely opening qa's
		// card does not hand it the turn dev is still holding (domain/sleep.go).
		SleepReason: domain.SleepReasonTurn,
		Metadata: domain.SessionMetadata{
			// dev's tree, dev's branch. The worktree directory is derived from the
			// BRANCH, so these two fields ARE the share (#224).
			Branch:        dev.Metadata.Branch,
			WorkspacePath: dev.Metadata.WorkspacePath,
			// The turn qa gets when somebody wakes it. restoreArgv replays this
			// through GetLaunchCommand when the adapter has no conversation to
			// resume, which on a first wake is always the case - and a promptless
			// worker is refused outright (ErrNotResumable), so this must not be
			// empty.
			Prompt: crewMemberKickoff(role, dev, join),
		},
	}
	rec, err := m.store.CreateSession(ctx, seed)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("crew member: create: %w", err)
	}
	if err := m.recordCrew(ctx, dev, rec.ID, role); err != nil {
		// The row exists but is not attached to the task: it would be an orphan
		// nobody can reach through the crew. Remove it rather than leave it.
		m.rollbackSpawnSeedRow(ctx, rec.ID)
		return domain.SessionRecord{}, err
	}
	return m.getRecord(ctx, rec.ID)
}

// crewJoin says WHEN a member was added to its crew. It is the one thing the
// kickoff prompt has to differ on, so it is a named type rather than a bare bool
// at the call sites.
type crewJoin bool

const (
	// joinedAtSpawn: the crew was decided by --task-size and formed while dev was
	// still materializing. Nothing about the task exists yet that qa has to be
	// careful of.
	joinedAtSpawn crewJoin = false
	// joinedLate: a human attached this member to a task that was already
	// running, and possibly already finished its work. See crewLateArrival.
	joinedLate crewJoin = true
)

// crewMemberKickoff is the first turn a woken crew member reads.
//
// It carries dev's own brief verbatim, because the thing qa has to judge is
// whether the change does what the task asked for - and by the time qa wakes,
// dev's conversation is somewhere qa cannot see. Everything else qa needs is
// standing instruction (prompts.KindQA), not a per-task message, so this stays
// short: a task with no brief still yields a non-empty prompt.
func crewMemberKickoff(role domain.CrewRole, dev domain.SessionRecord, join crewJoin) string {
	var b strings.Builder
	b.WriteString("You are ")
	b.WriteString(string(role))
	b.WriteString(" on this task, and it is your turn. dev has been working in the worktree you are in now - read the branch's diff against its base branch to see what actually changed, rather than assuming the brief below was followed.")
	if join == joinedLate {
		b.WriteString("\n\n")
		b.WriteString(crewLateArrival)
	}
	if brief := strings.TrimSpace(dev.Metadata.Prompt); brief != "" {
		b.WriteString("\n\nThe brief dev was given:\n\n")
		b.WriteString(brief)
	}
	b.WriteString("\n\nTriage what is worth verifying, write and RUN what a machine can assert, record what you found, and hand back to dev.")
	return b.String()
}

// crewLateArrival is the ONE thing a qa attached after the fact has to be told
// that a spawn-time qa does not.
//
// Everything else a late arrival inherits is inherited the same way either
// way - dev's branch, dev's worktree, dev's brief, dev's id as AO_CREW_ID - and
// the sentence above ("read the diff, do not assume the brief was followed") was
// already written for an agent with no shared history.
//
// What is genuinely different is that DEV DID NOT KNOW IT WOULD GET A QA. A
// spawn-time qa arrives before there is a PR and before anyone has written a
// checklist. A late one arrives after: dev has been running the whole time with
// the SOLO system prompt, which carries the smoke-checklist protocol, so a
// checklist may already exist, may already carry the human's verdicts, and is
// exactly the artefact #226's id trap destroys when a case is re-sent under a
// new name.
const crewLateArrival = "You were added to this task AFTER it started, so treat what is already there as work in progress rather than a blank page. dev has been running alone with the solo instructions, which include the smoke-checklist protocol: read the PR and `ao smoke list \"$AO_CREW_ID\"` BEFORE you write anything. If a case is already on that checklist, re-send it under the id it already has - `ao smoke set` replaces the whole list, and a case whose name changes loses the human's verdict, note and screenshots. There may also be an open PR with CI and review history; read it rather than re-deriving it."

// CrewMember returns the session filling `role` on this session's task, if any.
// It answers for either member (ask dev for its qa, or qa for its dev), so a
// caller that holds one id can reach the other without knowing which it has.
func (m *Manager) CrewMember(ctx context.Context, id domain.SessionID, role domain.CrewRole) (domain.SessionRecord, bool, error) {
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	if rec.CrewRole == role {
		return rec, true, nil
	}
	members, err := m.crewMembers(ctx, rec)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	for _, other := range members {
		if other.CrewRole == role {
			return other, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

// WakeCrewMember gives the crew slot to `id`, whoever is holding it.
//
// It is the ONE affordance the design asks for at this stage: a way to say "qa's
// turn now", with no policy about when that should happen. It goes THROUGH
// #225's exclusion rather than around it - the holder is released (suspended,
// tmux reaped) and the taker is Resumed, which takes crewSlotGuard - so it cannot
// produce the two-agents-in-one-checkout state the guard exists to prevent, and
// a failed take leaves the slot free rather than held by a corpse.
//
// A member that is already awake is returned unchanged: "wake qa" when qa is
// already up is a no-op, not an error.
func (m *Manager) WakeCrewMember(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, err := m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("wake crew member: %w", err)
	}
	if !rec.InCrew() {
		return domain.SessionRecord{}, fmt.Errorf("%w: %s is not part of a crew", ErrInvalidCrew, id)
	}
	if rec.IsTerminated {
		return domain.SessionRecord{}, fmt.Errorf("%w: %s is terminated", ErrInvalidCrew, id)
	}
	holder, ok, err := m.crewHolderOf(ctx, rec)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("wake crew member: %w", err)
	}
	if ok && holder.ID == id {
		return rec, nil
	}
	if !ok {
		// Nobody holds it - the ordinary resume path takes it, guard and all.
		return m.Resume(ctx, id, domain.WokenByWake)
	}
	return m.HandOverCrewSlot(ctx, holder.ID, id, domain.WokenByWake)
}
