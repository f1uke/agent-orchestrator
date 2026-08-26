package sessionmanager

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// FORMING THE CREW: creating qa the moment the task turns out to need one.
//
// A `standard` or `deep` task is worked by dev AND qa; a `mechanical` one by dev
// alone (domain.TaskSize.WantsCrew). What --task-size decides is whether a task
// MAY have a qa - not that it has one from t0. Every spawn creates exactly one
// session, and qa is created later, by the trigger in crew_join.go, the first
// time dev touches a runtime surface. A task that never touches one - a
// backend-only change - never gets a qa and never pays for one.
//
// This file is the WRITE: given a dev and a reason, put one member in dev's
// worktree. It has one shape to get right, and it is no longer "born suspended":
//
//   - The row is INSERTED asleep - one write, with dev's branch, dev's worktree
//     and qa's kickoff prompt on it - and then STARTED. Suspended-then-resumed
//     rather than launched outright because there is no branch to cut, no
//     worktree to create and no provisioning to run: what materialize does for a
//     spawn is exactly what must NOT happen to a tree a live agent is working in.
//   - It starts AWAKE because there is nothing left for it to wait for. qa was
//     born suspended to satisfy one-awake-at-a-time; both members run at once
//     now, that rule is gone, and the wake control went with the baton bar - so a
//     qa created asleep is a qa nothing can start, which is exactly the stall a
//     real iOS task hit.
//   - Waking is BEST EFFORT and happens outside the crew lock. A member that
//     failed to start is still on the task, visible, and startable by opening its
//     card - strictly better than refusing to create it at all.
//
// qa's system prompt is NOT persisted: relaunchRestoredSession recomputes it from
// the row (including crew_role) at wake time, which is also how an edited qa base
// reaches a qa that was created before the edit.

// wantsCrew is the whole eligibility test for giving THIS session a qa, in one
// place so the trigger and the manual attach answer it the same way.
//
// The first clause is also what makes the trigger fire ONCE. dev gains its crew
// columns in the same write that creates qa, so a task that already has one
// answers false here for ever - and it stays false when that qa is later stood
// down, which is the monotonic absent -> present, one way, once, never back that
// the design asks for.
func wantsCrew(project domain.ProjectRecord, dev domain.SessionRecord) bool {
	if dev.InCrew() {
		return false
	}
	if dev.IsTerminated || dev.IsTodo {
		return false
	}
	if !crewEligible(project, dev.Kind, dev.TaskSize) {
		return false
	}
	return dev.Metadata.Branch != "" && dev.Metadata.WorkspacePath != ""
}

// crewEligible is the part of the eligibility test that is knowable BEFORE the
// session is materialized: what kind of session this is, how big the task is,
// and what kind of project it lives in. It is split out because dev's SYSTEM
// PROMPT has to be built from it (promptCrewRole), and the prompt is built
// before there is a materialized record to ask.
//
// It is also where a project that has turned AUTOMATIC crew formation off is
// answered, and this function is the only correct home for that answer. It sits
// upstream of BOTH consumers - the trigger that would create qa, and the prompt -
// and the prompt is the one that would fail silently: a spawn composes it before
// any crew exists, so a flag read further downstream would leave dev holding the
// CREW prompt, which tells it the smoke checklist belongs to a qa this project
// never creates and that `ao smoke set` from it is refused. Nobody would write a
// checklist, and nothing would say so.
//
// What it deliberately does NOT gate is the MANUAL path. `ao crew add` and the
// topbar's `+ qa` go through resolveCrewDev, which never calls this function, so
// a human can still opt one task into a qa by hand - which is the whole reason
// this switch turns off automatic formation rather than crews. The manual path
// reads the flag ONCE MORE, at its own seam, for a different question: whether
// the CALLER is a person (crew_attach.go). That is not this test duplicated -
// this one asks whether AO may form a crew unasked, that one asks who is
// allowed to ask.
func crewEligible(project domain.ProjectRecord, kind domain.SessionKind, size domain.TaskSize) bool {
	if kind != domain.KindWorker {
		return false
	}
	if !size.WantsCrew() {
		return false
	}
	if project.Config.DisableAutoCrew {
		return false
	}
	return project.Kind.WithDefault() != domain.ProjectKindWorkspace
}

// promptCrewRole answers WHOSE PROMPT this spawn is building, which is not the
// same question as "is this session in a crew".
//
// Under lazy creation those two answers are apart for most of a task's life and
// often for all of it: a `standard` spawn creates dev alone, and dev's crew
// columns are not written until something creates a qa - which may be never. But
// dev's SYSTEM PROMPT is fixed when its runtime launches, long before that, and
// has to be true on both sides of the event. So the prompt is built from the
// spawn's INTENT - "this task is ALLOWED a qa" - and the crew block it produces
// is written for a dev that is alone right now and tells it exactly what summons
// the second agent (prompts.CrewProtocol).
//
// A `mechanical` spawn, an orchestrator and a workspace project answer "" here
// and get the solo prompt byte-for-byte unchanged.
func promptCrewRole(project domain.ProjectRecord, cfg ports.SpawnConfig) domain.CrewRole {
	if cfg.CrewRole != "" {
		return cfg.CrewRole
	}
	if crewEligible(project, cfg.Kind, cfg.TaskSize) {
		return domain.CrewRoleDev
	}
	return ""
}

// A member's row is written asleep, by the Locked function below, for a caller
// that then STARTS it (startCrewMember).
//
// It deliberately does NOT go through materialize: there is no branch to cut (it
// is dev's), no worktree to create (it is dev's, and a live agent is in it), no
// provisioning to run (re-running post-create commands would fire an install into
// a tree somebody is working in) and no runtime to launch here. What is left is
// the row, and the row is written suspended so that Resume - the one code path
// that knows how to bring a session up in an existing worktree - is what starts
// it, under its own lock and after the crew lock is released.
//
// Both callers (the trigger and the manual attach) are check-then-create, so both
// already hold the crew lock: there is only the Locked form, and taking the lock
// again here would deadlock, since lockCrew is not reentrant.

// spawnSuspendedCrewMemberLocked writes the member's row. Its caller holds the
// crew lock: "does this task already have a qa?" then "create one" has to be
// atomic, or two racing callers both see a free seat.
func (m *Manager) spawnSuspendedCrewMemberLocked(ctx context.Context, project domain.ProjectRecord, dev domain.SessionRecord, role domain.CrewRole, reason domain.CrewJoinReason) (domain.SessionRecord, error) {
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
		// WHAT CREATED IT: dev opening the simulator, dev opening a preview, or a
		// human. One transition, so one value, written once with the row - and the
		// only durable state the join line under the crew strip needs.
		CrewJoinReason: reason,
		// Asleep for as long as it takes the caller to call Resume on it: the row
		// has to exist before anything can be launched into it, and a half-written
		// crew must not be visible as an awake member with no runtime. It is asleep
		// the way anything else with no process is asleep, so if the start fails
		// the member is merely one a human can open (domain/sleep.go).
		IsSuspended: true,
		SleepReason: domain.SleepReasonIdle,
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
			Prompt: crewMemberKickoff(role, dev, reason),
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

// crewMemberKickoff is the first turn a woken crew member reads.
//
// It carries dev's own brief verbatim, because the thing qa has to judge is
// whether the change does what the task asked for - and by the time qa wakes,
// dev's conversation is somewhere qa cannot see. Everything else qa needs is
// standing instruction (prompts.KindQA), not a per-task message, so this stays
// short: a task with no brief still yields a non-empty prompt.
func crewMemberKickoff(role domain.CrewRole, dev domain.SessionRecord, reason domain.CrewJoinReason) string {
	var b strings.Builder
	b.WriteString("You are ")
	b.WriteString(string(role))
	b.WriteString(" on this task. dev is working in the worktree you are in now, at the same time as you - read the branch's diff against its base branch to see what actually changed, rather than assuming the brief below was followed.")
	b.WriteString("\n\n")
	b.WriteString(crewArrival(reason))
	if brief := strings.TrimSpace(dev.Metadata.Prompt); brief != "" {
		b.WriteString("\n\nThe brief dev was given:\n\n")
		b.WriteString(brief)
	}
	// The last sentence names the handback CONCRETELY. qa's floor carries the
	// obligation in full, but the kickoff is the turn qa actually reads, and the
	// run that stalled ended precisely here - work done, nobody told.
	b.WriteString("\n\nTriage what is worth verifying, write and RUN what a machine can assert, record what you found, and finish by handing back to dev with `ao send --crew dev --about <sha>` - the commit you tested, what you recorded and retired, and what is left for the human. Hand back even if the answer is that there was nothing to exercise. dev is working in this same worktree while you do, so bracket anything you want to trust with `ao crew run`.")
	return b.String()
}

// crewArrival is what a joining member is told about the task it is walking into,
// and there is now always something to say: EVERY qa arrives after dev started
// work, because none is created until dev's work asks for one.
//
// The constant fact, whatever created it: DEV DID NOT KNOW IT WOULD GET A QA.
// AO refuses `ao smoke set` from a crew's dev only once a qa EXISTS, so up to
// this moment dev was free to author the checklist - and that checklist may
// already carry the human's verdicts, which is exactly the artefact #226's id
// trap destroys when a case is re-sent under a new name. Everything else a
// joining member inherits it inherits the same way whatever created it - dev's
// branch, dev's worktree, dev's brief, dev's id as AO_CREW_ID.
//
// What differs by reason is only the FIRST SENTENCE: an automatic join can say
// what dev was doing, which is also the first thing worth looking at.
func crewArrival(reason domain.CrewJoinReason) string {
	return crewArrivalOpening(reason) + " " + crewArrivalCommon
}

func crewArrivalOpening(reason domain.CrewJoinReason) string {
	switch reason {
	case domain.CrewJoinSim:
		return "AO created you because dev CLAIMED THE SIMULATOR - it is looking at this change on a device, which is the moment a task stops being something only unit tests can judge. The device is yours from here: dev has been told to hand it over, so claim it (`ao sim claim`) rather than assuming it is free."
	case domain.CrewJoinPreview:
		return "AO created you because dev pointed `ao preview` AT THE RUNNING APP - there is a live surface to exercise, which is the moment a task stops being something only unit tests can judge. Start by looking at what dev was looking at (`ao session get \"$AO_CREW_ID\"` carries the preview URL)."
	default:
		return "A HUMAN added you to this task."
	}
}

const crewArrivalCommon = "dev has been working alone until now, so treat what is already there as work in progress rather than a blank page: read the PR and `ao smoke list \"$AO_CREW_ID\"` BEFORE you write anything. If a case is already on that checklist, re-send it under the id it already has - `ao smoke set` replaces the whole list, and a case whose name changes loses the human's verdict, note and screenshots. There may also be an open PR with CI and review history; read it rather than re-deriving it."

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

// WakeCrewMember starts `id`, and TOUCHES NOBODY ELSE.
//
// It used to be a handover: the holder was released - suspended, tmux reaped -
// before the taker came up, because only one member could be awake. Both members
// run at once now, so there is nothing to take the turn from and nothing to
// stand down. What is left is an ordinary Resume, kept as a named call because
// "start qa on this task" is still a thing a human and the orchestrator ask for
// by role rather than by session id.
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
	if rec.Awake() {
		return rec, nil
	}
	return m.Resume(ctx, id, domain.WokenByWake)
}
