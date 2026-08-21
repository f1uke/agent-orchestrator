import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import {
	type AttentionZone,
	type CrewRole,
	type WorkspaceSession,
	attentionZone,
	isOrchestratorSession,
} from "../types/workspace";
import { statusLabel } from "./status-glyph";
import type { SmokeProgress } from "./smoke-test";

/**
 * A TASK, and the lane it belongs in.
 *
 * A task is one worktree, one branch, one pull request - and one or two
 * long-lived agents. `mechanical` gets dev alone; `standard`/`deep` get dev plus
 * a qa that writes, runs and records the tests. The board draws one card per
 * TASK, never one per session, so this file is what turns a flat session list
 * into tasks and answers the two questions a card asks: which lane am I in, and
 * what is each member doing.
 *
 * Everything here is keyed on the presence of `session.crew`. A solo session
 * carries none, so every function below answers for it exactly as the app
 * answered before the crew existed - by delegating straight to `attentionZone`,
 * not by a parallel rule someone has to remember to keep in step.
 */

/** dev first, then qa: the order the crew strip and the sidebar draw them in. */
export const CREW_ROLE_ORDER: CrewRole[] = ["dev", "qa"];

export type Task = {
	/** The member that owns the branch, the worktree and the pull request. */
	dev: WorkspaceSession;
	/** The member that verifies it. Absent on a solo task, and that is structural. */
	qa?: WorkspaceSession;
	/** dev first, then qa. One entry for a solo task. */
	members: WorkspaceSession[];
	/** True when this task has more than one agent on it. */
	isCrew: boolean;
};

/**
 * Groups a session list into tasks: a crew's members collapse onto their dev,
 * and every other session is a task of one.
 *
 * Order is preserved by DEV's position in the input, so a board that sorted its
 * sessions still sorts the same way; a qa is never a task of its own, so it can
 * never appear as a second card for work that is already on screen.
 *
 * A qa whose dev is missing from the list (a filtered view, a half-torn-down
 * crew) is promoted to its own task rather than dropped. Losing a live agent off
 * the board entirely is a worse failure than showing it without its partner.
 */
export function tasksFrom(sessions: WorkspaceSession[]): Task[] {
	const devs = new Map<string, WorkspaceSession>();
	for (const session of sessions) {
		if (session.crew?.role === "dev") devs.set(session.crew.id, session);
	}
	const qaByCrew = new Map<string, WorkspaceSession>();
	for (const session of sessions) {
		if (session.crew?.role === "qa" && devs.has(session.crew.id)) qaByCrew.set(session.crew.id, session);
	}
	const out: Task[] = [];
	for (const session of sessions) {
		const crew = session.crew;
		if (crew?.role === "qa" && devs.has(crew.id)) continue; // drawn on its dev's card
		const qa = crew?.role === "dev" ? qaByCrew.get(crew.id) : undefined;
		out.push({ dev: session, qa, members: qa ? [session, qa] : [session], isCrew: Boolean(qa) });
	}
	return out;
}

/** The task a session belongs to, out of an already-grouped list. */
export function taskOf(tasks: Task[], sessionId: string): Task | undefined {
	return tasks.find((task) => task.members.some((member) => member.id === sessionId));
}

/**
 * What a crew chip says about one member. Three states and no more:
 *
 * - `working` - it has the turn and an agent is running.
 * - `asleep`  - it is waiting its turn. No process, worktree kept, one wake away.
 * - `done`    - it has finished and been torn down.
 *
 * There is deliberately no "absent" state. A role that is not in the crew has no
 * chip at all: absence is STRUCTURAL, and drawing an empty seat would nag about
 * a chair the task deliberately chose not to fill.
 */
export type CrewChipState = "working" | "asleep" | "done";

export function crewChipState(member: WorkspaceSession): CrewChipState {
	if (member.isTerminated || member.status === "merged" || member.status === "terminated") return "done";
	return member.isSuspended ? "asleep" : "working";
}

/**
 * Whether this member HAS THE TURN right now - the renderer's mirror of the
 * daemon's `domain.Awake()`, same three facts in the same order.
 *
 * It exists because "sleeps the other one" is only a true promise against a
 * member that is actually running. A finished member (its PR merged), one that
 * is already asleep, and one that never started are all stopped: waking this
 * member sleeps nobody, and the daemon's WakeCrewMember takes the ordinary
 * resume path without touching them.
 */
export function holdsTheTurn(member: WorkspaceSession): boolean {
	return !member.isTerminated && !member.isSuspended && !member.isTodo && member.status !== "merged";
}

/**
 * Whether this task can still GAIN a member - which is what decides whether the
 * card offers `+ qa` at all.
 *
 * An affordance that can only fail is worse than no affordance, so the three
 * refusals the daemon would give are asked here first:
 *
 * - the task already has a crew (there is no seat left; a second qa is refused
 *   by the database, not just by policy),
 * - it is a prepared TODO (nothing is materialized yet - starting it forms the
 *   crew its size asks for on the way through),
 * - it is finished (merged, or torn down). Attaching to a task that is over
 *   would create an agent holding a worktree about to be reclaimed.
 *
 * An orchestrator is not a task at all: it shares one worktree with every other
 * orchestrator of its project, so a crew there is a category error.
 */
export function canAttachRole(task: Task): boolean {
	if (task.isCrew) return false;
	const dev = task.dev;
	if (isOrchestratorSession(dev)) return false;
	if (dev.status === "todo") return false;
	return crewChipState(dev) !== "done";
}

/**
 * The review GATE - not a teammate.
 *
 * Review has no session: each pass is an ephemeral run that reads the diff,
 * reports a verdict and closes. So it gets a pip rather than a chip, and its
 * states are verdicts rather than activities. `not run` is a real answer, and
 * the one most tasks have.
 */
export type ReviewGateState = "approved" | "changes" | "not run";

/**
 * AO's own review verdict at the head of the task's most actionable PR.
 *
 * This is AO's reviewer, NOT the forge's approvals - those are human reviewers
 * on the pull request and are already folded into the session's status by the
 * daemon. A verdict is only ever recorded at the commit it reviewed, so a green
 * pip cannot be stale.
 */
export function reviewGateState(prs: SessionPRSummary[]): ReviewGateState {
	const verdict = prs[0]?.aoReview?.verdict;
	if (verdict === "approved") return "approved";
	if (verdict === "changes_requested") return "changes";
	return "not run";
}

/** What the rollup can see beyond the sessions themselves. */
export type TaskGates = {
	/** The human's smoke verdicts. Undefined while the checklist has not loaded. */
	smoke?: SmokeProgress;
	review: ReviewGateState;
};

export type TaskLane = {
	zone: AttentionZone;
	/**
	 * Short "who, and what" for the card, e.g. `qa · ready to wake`. Empty when
	 * the lane speaks for itself - which is always, on a solo task.
	 */
	note: string;
	/**
	 * The member whose OWN status produced this lane - the one holding the ball.
	 * The card draws its gutter glyph from it.
	 *
	 * It is absent when the lane came from a fact about the TASK rather than about
	 * a member (`qa · Play the cases` is a fact about the checklist and the human;
	 * `qa · Ready to wake` is a fact about the baton). Returning the member rather
	 * than leaving the card to parse the note is not merely tidier: drawing a
	 * sleeping qa's glyph for "play the cases" would paint the board's only SOLID
	 * mark - the one reserved for a genuinely live agent - on a dead process.
	 */
	holder?: WorkspaceSession;
};

/**
 * A crew member's `needs_input` is only a call on YOU when something is actually
 * blocked on you.
 *
 * This is the whole answer to the trap this feature had to avoid. `attentionZone`
 * is a pure function of `status`, and `needs_input` covers two different facts
 * that AO already separates one layer down (the parked/waiting_input split):
 *
 *   - `waiting_input` - a prompt is OPEN in the agent's pane and it is blocked on
 *     a person answering it.
 *   - `idle_aged`     - the agent's turn simply ENDED. Nothing is open and nobody
 *     is blocked.
 *
 * On a SOLO task those mean the same thing, because you are the only other party
 * - which is exactly why solo behaviour must not change, and does not. On a CREW
 * task there is another agent whose move it is, and a turn ending is the handoff,
 * not an escalation. Without this distinction every task would land in Needs you
 * the moment dev stopped typing, and the lane that means "a human is on the hook"
 * would become the biggest lane on the board and stop meaning anything.
 */
function isBlockedOnAPerson(member: WorkspaceSession): boolean {
	return attentionZone(member) === "action" && member.statusReason !== "idle_aged";
}

/** Has a person judged every case a person still has to judge? */
function smokeSettled(smoke: SmokeProgress | undefined): boolean {
	return Boolean(smoke) && smoke!.fail === 0 && smoke!.pending === 0;
}

/** Did a machine already run these cases, leaving only the human's play? */
function awaitingOnlyTheHumansPlay(smoke: SmokeProgress | undefined): boolean {
	if (!smoke || smoke.pending === 0) return false;
	return smoke.agentPass + smoke.agentFail + smoke.agentCaptured > 0;
}

/**
 * Whose turn the card SAYS is waiting - said, not decided. AO builds no scheduler
 * here (the handover policy is meant to be chosen after watching real tasks), so
 * this is only what the board reports: qa until it has had a turn on this change,
 * then dev, which owns the pull request and has to land it.
 */
function nextUp(qa: WorkspaceSession): CrewRole {
	if (!qa.crew?.hasRun) return "qa";
	return crewChipState(qa) === "working" ? "dev" : "qa";
}

/**
 * `qa · Input needed` - the ROLE in lower case, then the status exactly as the
 * card would have written it.
 *
 * dev gets NO prefix and no note at all: dev is the task, so a card whose ball
 * holder is dev reads precisely as it reads today. Naming the role only when it
 * is not dev is what keeps the crew invisible until it has something to say.
 */
function roleNote(member: WorkspaceSession): string {
	const role = member.crew?.role;
	return role && role !== "dev" ? `${role} · ${statusLabel(member)}` : "";
}

/**
 * The lane one TASK belongs in, rolled up from its members.
 *
 * A solo task delegates to `attentionZone` and stops - the board it produces is
 * byte-for-byte the board that exists today. Everything below it is the crew
 * rule, in the design's stated priority order:
 *
 *  1. an AWAKE member is genuinely blocked on a person -> Needs you, named
 *  2. dev's work can land AND qa has signed off AND review has not objected
 *     -> Ready to merge. THIS is the AND the feature exists for
 *  3. everything an agent can do is done and only the human's play remains
 *     -> Needs you (`qa · Play the cases`), because nothing else can advance it
 *  4. an awake member is working -> its own lane
 *  5. nobody is awake -> In review, naming what the task is waiting for
 *
 * Rule 2's third input is deliberately "review has not OBJECTED" rather than
 * "review has approved". A review pass is an ephemeral run that something has to
 * start, and nothing starts one automatically yet - so requiring approval would
 * park every crew task in In review forever, which is a worse lie than the one
 * being fixed. A `changes_requested` verdict is real information at this exact
 * head and does block, exactly as it does on the Summary strip.
 */
export function taskLane(task: Task, gates: TaskGates): TaskLane {
	const { dev, qa, members } = task;
	if (!qa) return { zone: attentionZone(dev), note: "", holder: dev };

	if (members.every((member) => attentionZone(member) === "done")) return { zone: "done", note: "" };
	if (dev.isTodo) return { zone: "todo", note: "", holder: dev };

	const awake = members.filter((member) => crewChipState(member) === "working");

	// 1. A live agent is stuck on something only a person can give it.
	const blocked = awake.find(isBlockedOnAPerson);
	if (blocked) return { zone: "action", note: roleNote(blocked), holder: blocked };

	// AO's reviewer asked for changes at this head. Nobody has to be awake for
	// that to be true, and it is dev's to answer.
	if (gates.review === "changes") return { zone: "action", note: "review · Changes requested" };

	// 2/3. dev's work can land: the AND, and what is still owed when it cannot.
	if (attentionZone(dev) === "merge") {
		if (!dev.crew?.hasRun || !qa.crew?.hasRun) return { zone: "pending", note: "qa · Not woken yet" };
		if (gates.smoke && smokeSettled(gates.smoke)) return { zone: "merge", note: "", holder: dev };
		if (awaitingOnlyTheHumansPlay(gates.smoke)) return { zone: "action", note: "qa · Play the cases" };
		return { zone: "pending", note: "qa · Not played yet" };
	}

	// 4. Somebody has the turn and is using it.
	const holder = awake[0];
	if (holder && attentionZone(holder) !== "action") {
		return { zone: attentionZone(holder), note: roleNote(holder), holder };
	}

	// 5. THE BATON IS DOWN. Either nothing is running, or the member that is
	// running has ENDED ITS TURN - rule 1 already took every awake member that is
	// genuinely blocked on a person, so an `action` zone reaching here can only be
	// a turn that finished. Both are the same fact about the task: its next step
	// belongs to the other agent, and the human's part is one click rather than a
	// decision to reason about.
	//
	// This is the second half of the trap, and the half that is easy to miss: a
	// dev that parks without being stood down is still AWAKE, so a rollup that
	// only skipped asleep members would put it straight back into Needs you.
	return { zone: "pending", note: `${nextUp(qa)} · Ready to wake` };
}

/** Every worker task on the board, already grouped and laned. */
export function workerTasks(sessions: WorkspaceSession[]): Task[] {
	return tasksFrom(sessions.filter((session) => !isOrchestratorSession(session)));
}
