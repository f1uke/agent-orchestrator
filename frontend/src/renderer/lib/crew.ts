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
 * Whether a task's qa is AWAKE or ASLEEP - the one fact the sidebar rail puts on
 * a task's row, and the one it is easiest to get wrong.
 *
 * Two live states and no more, because the rail is answering one question: is
 * there a second agent on this piece of work, and is it UP? A task with no qa
 * gets `undefined` and the row draws NOTHING - a task that never needed a second
 * agent is the ordinary case (more so since a project can turn automatic crew
 * formation off entirely), so an empty seat there would nag about a chair nobody
 * meant to fill.
 *
 * ## Awake is about a PROCESS, not about a turn
 *
 * `awake` means there is an agent up: a pane a human, a nudge or a queued
 * message can put back to work this instant. It deliberately does NOT mean
 * "taking a turn right now", so a qa that ran, ended its turn and is sitting at
 * its prompt is awake here while the board's rollup says nobody is WORKING on
 * the task. Those are two different questions with two different answers, and
 * flattening them would break the second one: {@link isWorking} exists to catch
 * a crew that has quietly stopped, and it must keep counting a parked member as
 * stopped. So the pip's words never claim work - it says awake, and the card
 * says whether anyone is mid-turn.
 *
 * ## Why this is not just `crewChipState`
 *
 * `crewChipState` is AO's structural `Awake()` - `!IsTerminated && !IsSuspended
 * && !IsTodo` - and AO is deliberately the sole author of those flags. That
 * makes it the right thing to REFUSE on and the wrong thing to make a claim on,
 * because three states with no process at all come out of it as "working". Each
 * is demoted here:
 *
 *  - `todo` - prepared and never materialized: no branch, no worktree, no
 *    runtime. `Awake()` excludes it and `crewChipState` does not.
 *  - {@link neverStarted} - on the task with `hasRun` false. A member is created
 *    and started in one breath, so this is what is left when that START FAILED.
 *    It is not suspended, so it would otherwise read awake.
 *  - `activity.state === "exited"` - the harness's own report that the pane is
 *    GONE. That is positive evidence of death rather than absence of evidence,
 *    and it is the one dead-but-awake case a row that is sitting still can
 *    actually see.
 *
 * `no_signal` and `active_stale` are deliberately NOT folded in. Both are
 * absence of evidence, and "a failed probe is never proof of death" is
 * load-bearing everywhere else in this app.
 *
 * ## What it still cannot see
 *
 * A qa killed without its hook ever reporting - SIGKILL, a daemon restart
 * mid-turn - still reads awake. Nothing here can fix that: the corpse probe
 * (`reconcileCrewPeers`) runs only when the OTHER member is woken, so a crew
 * whose rows are both sitting still is never settled.
 */
export type QaPresence = {
	state: "awake" | "asleep";
	/**
	 * The fact behind the state, in the vocabulary the card and the idle chip
	 * already use. `paused` and `not started` are kept apart because only one of
	 * them is a pause and only one of them has a Start button.
	 */
	detail: "awake" | "paused" | "not started" | "no agent";
};

export function qaPresence(qa: WorkspaceSession | undefined): QaPresence | undefined {
	if (!qa) return undefined;
	// Finished and torn down is not a LIVE state, so it is not one of the two.
	// The rail never sees this (it filters merged and terminated sessions out
	// before grouping), but the rule must answer for callers that do not.
	if (crewChipState(qa) === "done") return undefined;
	if (qa.status === "todo") return { state: "asleep", detail: "not started" };
	if (neverStarted(qa)) return { state: "asleep", detail: "not started" };
	if (qa.activity?.state === "exited") return { state: "asleep", detail: "no agent" };
	if (crewChipState(qa) === "asleep") return { state: "asleep", detail: "paused" };
	return { state: "awake", detail: "awake" };
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
 * One quiet sentence under the crew strip, and it answers whichever of two
 * questions this card raises.
 *
 * On a CREW: `qa joined · dev asked for a review`. The card can change shape
 * while somebody is looking at it, and once it does the merge gate has an input
 * it did not have a moment ago - a task reading READY TO MERGE can drop to IN
 * REVIEW. That is the gate working, not a glitch, and the line is the difference
 * between those two readings.
 *
 * On a SOLO task that DROVE THE APP: `dev drove the app · no qa was asked for`.
 * AO used to put a qa on that task by itself, the moment it saw the app being
 * driven. It no longer does - that fired while dev was still using the device,
 * and the qa it created fought dev for it - so dev asks instead, when it thinks
 * the work is done. Removing a trigger and saying nothing would hand back the
 * failure it was buying protection against: a task that finished with nobody but
 * its author having looked, and nothing anywhere saying so. This is the human's
 * half of that warning, and it sits beside the `+ qa` control that answers it.
 *
 * Both are DERIVED, not stored: one small enum on the member's row, one on dev's.
 *
 * Undefined for a solo task that never drove anything, and for a member created
 * before AO recorded the reason: saying nothing is the honest answer, and a card
 * with nothing to explain explains nothing.
 */
export function crewJoinLine(task: Task): string | undefined {
	const reason = task.qa?.crew?.joinReason;
	if (reason) return `qa joined · ${CREW_JOIN_CAUSE[reason]}`;
	if (task.isCrew) return undefined;
	const touch = task.dev.runtimeTouch;
	if (!touch) return undefined;
	return `${RUNTIME_TOUCH_CAUSE[touch]} · no qa was asked for`;
}

/**
 * What each reason SAYS. `review` is the ordinary one - dev decided the change
 * was ready - and `manual` names the person, because a human asking is its own
 * explanation. `sim` and `preview` are retired and appear only on members created
 * before dev did the asking.
 */
const CREW_JOIN_CAUSE: Record<NonNullable<SessionCrew["joinReason"]>, string> = {
	review: "dev asked for a review",
	manual: "you added it",
	sim: "dev opened the simulator",
	preview: "dev opened a preview",
};

/** What driving the app was, said the way the join line above says things. */
const RUNTIME_TOUCH_CAUSE: Record<NonNullable<WorkspaceSession["runtimeTouch"]>, string> = {
	sim: "dev drove the simulator",
	preview: "dev opened a preview",
};

/**
 * What to CALL the session holding one of this task's exclusive resources - a
 * simulator lease, today.
 *
 * Two agents on one task can hold two simulators at once, and "who has which"
 * is asked at the device more than anywhere else, so the answer has to be in the
 * vocabulary of the member switcher one strip above it. A holder that is a
 * member of THIS task is named by its role (`dev`, `qa`); anything else keeps
 * its `@id`, because a bare role would be a lie about which task it belongs to.
 * The id is never lost - callers put it in the tooltip.
 *
 * A solo session has no role at all, so every one-agent task reads exactly as it
 * reads today.
 */
export function crewHolderLabel(task: Task | undefined, holder: string | undefined): string {
	const role = task?.members.find((member) => member.id === holder)?.crew?.role;
	return role ?? `@${holder ?? "another session"}`;
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
