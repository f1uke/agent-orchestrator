import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import {
	type AttentionZone,
	type CrewRole,
	type SessionCrew,
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
 * long-lived agents. Every task starts as dev alone. It GAINS a qa when the work
 * turns out to need one - when dev first drives the app, or when a human asks -
 * and a task with nothing to exercise never gains one at all. The board draws one
 * card per TASK, never one per session, so this file is what turns a flat session
 * list into tasks and answers the two questions a card asks: which lane am I in,
 * and what is each member doing.
 *
 * Because membership CHANGES mid-task, two things follow and both are deliberate:
 * a card can gain a chip while you are looking at it, and it can move BACKWARD
 * one lane when it does (the merge gate gains a real input it did not have). The
 * join line - {@link crewJoinLine} - is what makes that legible.
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
 * - `working` - an agent is running. Both members can be, and normally are.
 * - `asleep`  - no process. Either paused, or never started; {@link neverStarted}
 *   is what tells those apart, because only one of them has a Start button.
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
 * Whether this member has NEVER RUN: it is on the task and nothing has been
 * spent on it. A member is created and started in one breath now, so this is no
 * longer the ordinary state of a fresh qa - it is what is left when that START
 * FAILED, which is deliberately not fatal: the member is on the task, visible,
 * and opening its card starts it.
 *
 * It is what the card and the pane read instead of a sleep state, because
 * "asleep" and "never started" answer different questions and only one of them
 * has a button. There is deliberately no "waiting its turn" any more: both
 * members run at the same time, so nothing is waiting for anything.
 */
export function neverStarted(member: WorkspaceSession): boolean {
	return Boolean(member.crew) && !member.crew?.hasRun && !member.isTerminated;
}

/**
 * Whether this member is AWAKE AND WORKING - the one question the stall rule is
 * built on, and the one it is easiest to get wrong.
 *
 * Three facts, in order, and each excludes something deliberately:
 *
 *  - {@link crewChipState} answers whether there is a PROCESS at all. Asleep and
 *    finished are both "no", which is AO's own `Awake()` and nothing new.
 *  - The agent's own last word about its turn. `parked` is the harness saying
 *    outright that the turn ENDED and it is sitting at an empty prompt; `exited`
 *    is the pane being gone. Neither is work, however alive the row looks.
 *  - `idle_aged` - the daemon has already decided a quiet idle is "assumed
 *    waiting". Below that line an `idle` member is merely BETWEEN TURNS, which is
 *    not the same thing as stopped, and it counts as working.
 *
 * Two exclusions are worth stating because they are what keeps the stall rule
 * from crying wolf. A member blocked at an OPEN PROMPT counts as working: the
 * task is not silent, it is asking a person a question, and the rollup names the
 * role and the question one rule later - "nobody is working on this" would be a
 * vaguer answer to a card that already has a better one. And a member with NO
 * activity reading at all counts as working: a missing reading is absence of
 * evidence, and a false stall warning teaches people to ignore the lane that is
 * supposed to mean act now.
 *
 * It reads the member and nothing else - no baton, no `sleep_reason`, no crew
 * shape - so it answers the same way for one awake member or two.
 */
function isWorking(member: WorkspaceSession): boolean {
	if (crewChipState(member) !== "working") return false;
	const state = member.activity?.state;
	if (state === "parked" || state === "exited") return false;
	return member.statusReason !== "idle_aged";
}

/**
 * Whether this task can still GAIN a member - which is what decides whether the
 * card offers `+ qa` at all.
 *
 * It is offered on every solo task, which is now every task that has not needed
 * a qa yet - a card gaining a `+ qa` button is not a sign that something went
 * wrong with its crew.
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
 * `qa joined · dev opened the simulator` - one sentence, under the crew strip.
 *
 * It exists because the card can CHANGE SHAPE while somebody is looking at it,
 * and once it does, the merge gate has an input it did not have a moment ago: a
 * task reading READY TO MERGE can drop to IN REVIEW. That is the gate working,
 * not a glitch, and this line is the difference between those two readings.
 *
 * It is DERIVED, not stored: there is exactly one transition (absent -> present,
 * one way, once), so the daemon records one small enum on the member's row and
 * everything else - when, and to which task - is already on the record.
 *
 * Undefined for a solo task and for a member created before AO recorded the
 * reason: saying nothing is the honest answer, and a card with no crew has
 * nothing to explain.
 */
export function crewJoinLine(task: Task): string | undefined {
	const reason = task.qa?.crew?.joinReason;
	if (!reason) return undefined;
	return `qa joined · ${CREW_JOIN_CAUSE[reason]}`;
}

/**
 * What each reason SAYS. Two of them name what dev did, because that is the
 * event and it is also the first thing worth looking at; the third names the
 * person, because a human asking is its own explanation.
 */
const CREW_JOIN_CAUSE: Record<NonNullable<SessionCrew["joinReason"]>, string> = {
	sim: "dev opened the simulator",
	preview: "dev opened a preview",
	manual: "you added it",
};

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
	 * `qa · Next up` is a fact about whose move it is). Returning the member rather
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
 * Whose move the card SAYS it is - said, not decided. AO builds no scheduler here,
 * so this is only what the board reports: qa until it has had a turn on this
 * change, then dev, which owns the pull request and has to land it.
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
 *  0. NOBODY is working on it and nothing else owes it a move -> Needs you,
 *     `Nobody is working on this`. Ahead of every other rule, because a card that
 *     reads healthy while nothing runs is the worst failure a real run produced
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
	// NO QA IS A PASS, NOT A PENDING - and under lazy creation this is the branch
	// most tasks live in, not an edge case. A task that never needs a qa never
	// gets one, so a smoke gate that waited for a verdict nobody will ever record
	// would hold every backend change out of Ready to merge for ever. The absent
	// member is simply not an input: the task reads exactly as a solo task does,
	// which is also what keeps the solo board byte-for-byte what it is today.
	if (!qa) return { zone: attentionZone(dev), note: "", holder: dev };

	// The terminal lanes, which is what "and the task is not over" means below: a
	// finished task and one that was never started are both correctly quiet.
	if (members.every((member) => attentionZone(member) === "done")) return { zone: "done", note: "" };
	if (dev.isTodo) return { zone: "todo", note: "", holder: dev };

	const lane = crewLane(task, qa, gates);

	// 0. NOBODY IS WORKING ON THIS, and that is the whole finding: a `standard`
	// crew ran a full task, qa parked after its pass, dev was asleep, no message
	// passed between them and no pane existed for either - and the card read
	// Ready, which looks healthy. It would have sat like that indefinitely.
	//
	// It takes priority over every rule below because those rules answer "what is
	// this task waiting FOR", and a task nothing is working on is not waiting for
	// anything - including, in the observed run, when dev's PR could have landed.
	//
	// The one thing it must not do is cry wolf, so it defers to a board that
	// already has a better answer: any lane that NAMES an ask (a member at an open
	// prompt, AO's reviewer objecting, a checklist only a person can play) is
	// already telling you to act, more precisely than this could. A task waiting on
	// CI or on a reviewer is likewise quiet on purpose - a machine owes that answer
	// and dev is nudged when it lands.
	if (!members.some(isWorking) && !someoneElseOwesTheMove(task, lane)) {
		return { zone: "action", note: NOBODY_IS_WORKING };
	}
	return lane;
}

/**
 * The caption a stalled task carries. A fact about the TASK, so it takes no role
 * prefix and hands the card no holder: the lane came from nobody.
 */
const NOBODY_IS_WORKING = "Nobody is working on this";

/**
 * Whether something OTHER than an idle agent owes this task its next move, in
 * which case its quiet is expected rather than a stall.
 *
 * Two answers, and they are different kinds of thing. A lane already in `action`
 * has NAMED an ask and a person can act on it right now - replacing `qa · Play
 * the cases` with `Nobody is working on this` would trade a specific instruction
 * for a vague one. A member sitting in the `pending` zone on its PR PIPELINE is
 * waiting on a machine or a reviewer: CI has not finished, or review has not come
 * back. Nobody should be working, and when the answer lands the PR nudge wakes
 * dev without a human touching anything.
 */
function someoneElseOwesTheMove(task: Task, lane: TaskLane): boolean {
	if (lane.zone === "action") return true;
	return task.members.some((member) => attentionZone(member) === "pending" && member.statusReason === "pr_pipeline");
}

/** Rules 1-5: what the task is waiting FOR, given that somebody could act on it. */
function crewLane(task: Task, qa: WorkspaceSession, gates: TaskGates): TaskLane {
	const { dev, members } = task;
	const awake = members.filter((member) => crewChipState(member) === "working");

	// 1. A live agent is stuck on something only a person can give it.
	const blocked = awake.find(isBlockedOnAPerson);
	if (blocked) return { zone: "action", note: roleNote(blocked), holder: blocked };

	// AO's reviewer asked for changes at this head. Nobody has to be awake for
	// that to be true, and it is dev's to answer.
	if (gates.review === "changes") return { zone: "action", note: "review · Changes requested" };

	// 2/3. dev's work can land: the AND, and what is still owed when it cannot.
	if (attentionZone(dev) === "merge") {
		if (!dev.crew?.hasRun || !qa.crew?.hasRun) return { zone: "pending", note: "qa · Not started yet" };
		if (gates.smoke && smokeSettled(gates.smoke)) return { zone: "merge", note: "", holder: dev };
		if (awaitingOnlyTheHumansPlay(gates.smoke)) return { zone: "action", note: "qa · Play the cases" };
		return { zone: "pending", note: "qa · Not played yet" };
	}

	// 4. Somebody is WORKING - asked of the member that is working rather than of
	// the first awake one, because a parked dev beside a running qa is awake and
	// has nothing to report.
	const holder = members.find(isWorking);
	if (holder && attentionZone(holder) !== "action") {
		return { zone: attentionZone(holder), note: roleNote(holder), holder };
	}

	// 5. NOBODY IS MID-TURN: whoever was working has ended its turn and the task's
	// next step belongs to the other agent. Both members may run at once, so this
	// is not a handover waiting to happen - it is the board naming whose move it is.
	//
	// This is the second half of the trap, and the half that is easy to miss: a
	// dev that parks is still AWAKE, so a rollup that only skipped asleep members
	// would put it straight back into Needs you.
	//
	// Rule 0 sits in front of this: a task where NOBODY is working reaches here
	// only when something other than an agent owes the next move (CI, a reviewer).
	return { zone: "pending", note: `${nextUp(qa)} · Next up` };
}

/** Every worker task on the board, already grouped and laned. */
export function workerTasks(sessions: WorkspaceSession[]): Task[] {
	return tasksFrom(sessions.filter((session) => !isOrchestratorSession(session)));
}
