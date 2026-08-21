import { describe, expect, it } from "vitest";
import { canAttachRole, crewChipState, reviewGateState, taskLane, tasksFrom, workerTasks } from "./crew";
import type { SmokeProgress } from "./smoke-test";
import { attentionZone, type SessionStatus, type WorkspaceSession } from "../types/workspace";

function session(id: string, over: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id,
		workspaceId: "demo",
		workspaceName: "Demo",
		title: id,
		provider: "claude-code",
		kind: "worker",
		branch: `feature/${id}`,
		status: "working" as SessionStatus,
		updatedAt: "2026-08-21T00:00:00Z",
		prs: [],
		...over,
	};
}

/** The activity reading of a member whose agent is RUNNING a turn right now. */
const running = { state: "active", lastActivityAt: "2026-08-21T00:00:00Z" } as const;
/** The activity reading of a member that has ENDED its turn and sits at its prompt. */
const parked = { state: "parked", lastActivityAt: "2026-08-21T00:00:00Z" } as const;

/** dev + a qa asleep beside it, in the state a `standard` spawn leaves them. */
function crew(devOver: Partial<WorkspaceSession> = {}, qaOver: Partial<WorkspaceSession> = {}) {
	const dev = session("demo-1", {
		activity: running,
		crew: { id: "demo-1", role: "dev", hasRun: true },
		...devOver,
	});
	const qa = session("demo-2", {
		isSuspended: true,
		status: "idle",
		crew: { id: "demo-1", role: "qa", hasRun: false },
		...qaOver,
	});
	return { dev, qa, sessions: [dev, qa] };
}

const smoke = (over: Partial<SmokeProgress> = {}): SmokeProgress => ({
	total: 0,
	pass: 0,
	fail: 0,
	skip: 0,
	pending: 0,
	checked: 0,
	retired: 0,
	agentPass: 0,
	agentFail: 0,
	agentCaptured: 0,
	...over,
});

describe("tasksFrom", () => {
	it("draws a crew's qa on its dev's task, never as a card of its own", () => {
		const { dev, sessions } = crew();
		const tasks = tasksFrom(sessions);
		expect(tasks).toHaveLength(1);
		expect(tasks[0].dev.id).toBe(dev.id);
		expect(tasks[0].qa?.id).toBe("demo-2");
		expect(tasks[0].isCrew).toBe(true);
	});

	it("leaves a solo session exactly as it was: one task, one member, no crew", () => {
		const solo = session("demo-9");
		const tasks = tasksFrom([solo]);
		expect(tasks).toEqual([{ dev: solo, qa: undefined, members: [solo], isCrew: false }]);
	});

	it("keeps a qa whose dev is not in the list rather than dropping it off the board", () => {
		const { qa } = crew();
		const tasks = tasksFrom([qa]);
		expect(tasks).toHaveLength(1);
		expect(tasks[0].dev.id).toBe(qa.id);
		expect(tasks[0].isCrew).toBe(false);
	});

	it("preserves the order the sessions arrived in, keyed on dev's position", () => {
		const a = session("demo-1");
		const { dev, qa } = crew(
			{ id: "demo-2", crew: { id: "demo-2", role: "dev", hasRun: true } },
			{ id: "demo-3", crew: { id: "demo-2", role: "qa", hasRun: false } },
		);
		const c = session("demo-4");
		expect(workerTasks([a, qa, dev, c]).map((t) => t.dev.id)).toEqual(["demo-1", "demo-2", "demo-4"]);
	});
});

describe("crewChipState", () => {
	it("has exactly three states, and no state for a role that is not in the crew", () => {
		expect(crewChipState(session("a"))).toBe("working");
		expect(crewChipState(session("a", { isSuspended: true }))).toBe("asleep");
		expect(crewChipState(session("a", { isTerminated: true }))).toBe("done");
		expect(crewChipState(session("a", { status: "merged" }))).toBe("done");
	});
});

describe("taskLane — a solo task is untouched", () => {
	// The hard requirement: every task that exists today is solo, and its lane
	// must be the lane attentionZone gives it, for every status there is.
	const statuses: SessionStatus[] = [
		"todo",
		"working",
		"idle",
		"needs_input",
		"no_signal",
		"ci_failed",
		"changes_requested",
		"review_pending",
		"pr_open",
		"draft",
		"approved",
		"mergeable",
		"merged",
		"terminated",
		"unknown",
	];
	for (const status of statuses) {
		it(`${status} lands where attentionZone puts it`, () => {
			const solo = session("demo-9", { status, isTodo: status === "todo" });
			const lane = taskLane({ dev: solo, members: [solo], isCrew: false }, { review: "not run" });
			expect(lane.zone).toBe(attentionZone(solo));
			expect(lane.note).toBe("");
		});
	}

	// The stall rule asks "is anybody working on this?", and on a solo task the
	// answer is regularly NO - a parked agent, a suspended one - without anything
	// being wrong. Almost every task on this machine is solo, so the rule must not
	// be able to reach one at all.
	for (const status of statuses) {
		it(`${status} is unchanged even with nobody running it`, () => {
			for (const over of [
				{ activity: parked },
				{ activity: { state: "exited" as const, lastActivityAt: "2026-08-21T00:00:00Z" } },
				{ isSuspended: true },
				{ isSuspended: true, sleepReason: "turn" as const, statusReason: "idle_aged" as const },
			]) {
				const solo = session("demo-9", { status, isTodo: status === "todo", ...over });
				const lane = taskLane({ dev: solo, members: [solo], isCrew: false }, { review: "not run" });
				expect(lane.zone).toBe(attentionZone(solo));
				expect(lane.note).toBe("");
			}
		});
	}
});

describe("taskLane — the NEEDS YOU trap", () => {
	// THE trap. dev parks after every turn, and a parked agent reads needs_input
	// with reason idle_aged. Under a naive rollup every handed-off task would pile
	// into Needs you and that lane would stop meaning "a human is on the hook".
	it("a parked dev whose qa is working is NOT NEEDS YOU", () => {
		const { dev, qa } = crew(
			{ status: "needs_input", statusReason: "idle_aged", activity: parked },
			{ isSuspended: false, status: "working", activity: running, crew: { id: "demo-1", role: "qa", hasRun: true } },
		);
		const lane = taskLane({ dev, qa, members: [dev, qa], isCrew: true }, { review: "not run" });
		expect(lane.zone).toBe("working");
		expect(lane.note).toBe("qa · Working");
	});

	it("but a dev genuinely blocked at an OPEN PROMPT is still NEEDS YOU", () => {
		const { dev, qa } = crew({ status: "needs_input", statusReason: "waiting_input" });
		const lane = taskLane({ dev, qa, members: [dev, qa], isCrew: true }, { review: "not run" });
		expect(lane.zone).toBe("action");
		expect(lane.note).toBe("");
	});

	it("and a real problem — CI, changes requested, no signal — is still NEEDS YOU", () => {
		for (const status of ["ci_failed", "changes_requested", "no_signal"] as SessionStatus[]) {
			const { dev, qa } = crew({ status, statusReason: "pr_pipeline" });
			expect(taskLane({ dev, qa, members: [dev, qa], isCrew: true }, { review: "not run" }).zone).toBe("action");
		}
	});

	it("names the ROLE when it is qa that is stuck, so the card does not blame dev", () => {
		const { dev, qa } = crew(
			{ status: "needs_input", statusReason: "idle_aged", isSuspended: true },
			{
				isSuspended: false,
				status: "needs_input",
				statusReason: "waiting_input",
				crew: { id: "demo-1", role: "qa", hasRun: true },
			},
		);
		const lane = taskLane({ dev, qa, members: [dev, qa], isCrew: true }, { review: "not run" });
		expect(lane.zone).toBe("action");
		expect(lane.note).toBe("qa · Input needed");
	});

	it("an asleep member never asks for attention on its own account", () => {
		// qa asleep AND carrying a status that would be `action` on its own.
		const { dev, qa } = crew(
			{ status: "working" },
			{ isSuspended: true, status: "no_signal", statusReason: "no_signal" },
		);
		const lane = taskLane({ dev, qa, members: [dev, qa], isCrew: true }, { review: "not run" });
		expect(lane.zone).toBe("working");
	});
});

describe("taskLane — nobody is working on this", () => {
	// THE INCIDENT, as a test. A `standard` crew ran a full task: qa finished its
	// pass and PARKED, dev was asleep with sleep_reason=turn, the queue was empty
	// because qa never told dev it was done, and neither had a pane. Nothing was
	// running and nothing said so - the card read Ready, which looks healthy, and
	// it would have sat like that until a person happened to notice.
	const stalled = () =>
		crew(
			{
				status: "mergeable",
				statusReason: "pr_pipeline",
				isSuspended: true,
				sleepReason: "turn",
				activity: parked,
			},
			{
				isSuspended: false,
				status: "needs_input",
				statusReason: "idle_aged",
				activity: parked,
				crew: { id: "demo-1", role: "qa", hasRun: true },
			},
		);

	it("says so when qa has parked after its pass and dev is asleep", () => {
		const { dev, qa } = stalled();
		const lane = taskLane(
			{ dev, qa, members: [dev, qa], isCrew: true },
			{ review: "not run", smoke: smoke({ total: 1, pass: 1, checked: 1 }) },
		);
		expect(lane.zone).toBe("action");
		expect(lane.note).toBe("Nobody is working on this");
		// A fact about the TASK, not about a member: drawing a sleeping dev's glyph
		// here would paint the board's only SOLID mark on a dead process.
		expect(lane.holder).toBeUndefined();
	});

	it("says so however the checklist happens to stand", () => {
		const { dev, qa } = stalled();
		const task = { dev, qa, members: [dev, qa], isCrew: true };
		// Not loaded; qa stood down and recorded nothing (an empty checklist reads
		// as settled, which is what made the incident read Ready); all green.
		for (const gate of [undefined, smoke(), smoke({ total: 1, pass: 1, checked: 1 })]) {
			expect(taskLane(task, { review: "not run", smoke: gate }).note).toBe("Nobody is working on this");
		}
	});

	it("is not fooled by a member that is awake with no process behind it", () => {
		// The incident's other half: a row that still says awake while its pane is
		// gone. `exited` is the agent's own last word, and it is not working.
		const { dev, qa } = stalled();
		const dead = { ...qa, activity: { state: "exited" as const, lastActivityAt: "2026-08-21T00:00:00Z" } };
		const lane = taskLane({ dev, qa: dead, members: [dev, dead], isCrew: true }, { review: "not run" });
		expect(lane.zone).toBe("action");
		expect(lane.note).toBe("Nobody is working on this");
	});

	it("stays quiet while ONE member is still running", () => {
		const { dev, qa } = stalled();
		const live = { ...qa, activity: running, status: "working" as SessionStatus, statusReason: undefined };
		const lane = taskLane({ dev, qa: live, members: [dev, live], isCrew: true }, { review: "not run" });
		expect(lane.zone).not.toBe("action");
		// dev can land and the checklist has not been played, so the card keeps
		// saying exactly that - what it must not say is that nothing is running.
		expect(lane.note).toBe("qa · Not played yet");
	});

	it("stays quiet for a member merely idle BETWEEN turns", () => {
		// `idle` is "recently active" - the agent paused, it did not stop. Only once
		// the daemon has aged that reading into idle_aged is the turn over.
		const { dev, qa } = stalled();
		const between = {
			...qa,
			status: "idle" as SessionStatus,
			statusReason: "idle" as const,
			activity: { state: "idle" as const, lastActivityAt: "2026-08-21T00:00:00Z" },
		};
		const lane = taskLane({ dev, qa: between, members: [dev, between], isCrew: true }, { review: "not run" });
		expect(lane.zone).not.toBe("action");
	});

	it("does not call a task waiting on CI or on a reviewer stalled", () => {
		// Nobody is working, and that is exactly right: a machine owes the answer
		// and dev is nudged when it lands.
		for (const status of ["pr_open", "review_pending", "draft"] as SessionStatus[]) {
			const { dev, qa } = crew(
				{ status, statusReason: "pr_pipeline", isSuspended: true, sleepReason: "turn", activity: parked },
				{
					isSuspended: true,
					status: "idle",
					activity: parked,
					crew: { id: "demo-1", role: "qa", hasRun: true },
				},
			);
			const lane = taskLane({ dev, qa, members: [dev, qa], isCrew: true }, { review: "not run" });
			expect(lane.zone).toBe("pending");
			expect(lane.note).not.toBe("Nobody is working on this");
		}
	});

	it("does not overwrite an ask the board already names", () => {
		const { dev, qa } = stalled();
		const task = { dev, qa, members: [dev, qa], isCrew: true };
		// The human's play is the only thing left, and rule 3 says which cases.
		expect(taskLane(task, { review: "not run", smoke: smoke({ total: 2, pending: 2, agentPass: 2 }) }).note).toBe(
			"qa · Play the cases",
		);
		// AO's reviewer objected at this head, and that is dev's to answer.
		expect(taskLane(task, { review: "changes" }).note).toBe("review · Changes requested");
	});

	it("does not call a member blocked at an open prompt a stall", () => {
		const { dev, qa } = stalled();
		const asking = {
			...qa,
			statusReason: "waiting_input" as const,
			activity: { state: "waiting_input" as const, lastActivityAt: "2026-08-21T00:00:00Z" },
		};
		const lane = taskLane({ dev, qa: asking, members: [dev, asking], isCrew: true }, { review: "not run" });
		expect(lane.zone).toBe("action");
		expect(lane.note).toBe("qa · Input needed");
	});

	it("says nothing about a task that is over, or one that never started", () => {
		const done = crew(
			{ status: "merged", isTerminated: true, activity: parked },
			{ status: "terminated", isTerminated: true, activity: parked, crew: { id: "demo-1", role: "qa", hasRun: true } },
		);
		expect(taskLane({ ...done, members: [done.dev, done.qa], isCrew: true }, { review: "not run" }).zone).toBe("done");

		const todo = crew({ status: "todo", isTodo: true, activity: parked }, { status: "todo", isTodo: true });
		expect(taskLane({ ...todo, members: [todo.dev, todo.qa], isCrew: true }, { review: "not run" }).zone).toBe("todo");
	});
});

describe("taskLane — READY TO MERGE is an AND", () => {
	const mergeable = { status: "mergeable" as SessionStatus, statusReason: "pr_pipeline" as const };

	it("does not read ready while qa has not been woken at all", () => {
		const { dev, qa } = crew(mergeable);
		const lane = taskLane({ dev, qa, members: [dev, qa], isCrew: true }, { review: "approved", smoke: smoke() });
		expect(lane.zone).toBe("pending");
		expect(lane.note).toBe("qa · Not woken yet");
	});

	it("does not read ready while a person has not played the cases", () => {
		const { dev, qa } = crew(mergeable, { crew: { id: "demo-1", role: "qa", hasRun: true } });
		const lane = taskLane(
			{ dev, qa, members: [dev, qa], isCrew: true },
			{ review: "approved", smoke: smoke({ total: 2, pending: 2 }) },
		);
		expect(lane.zone).toBe("pending");
		expect(lane.note).toBe("qa · Not played yet");
	});

	it("asks the human to play once the machine has run and only their judgement is left", () => {
		const { dev, qa } = crew(mergeable, { crew: { id: "demo-1", role: "qa", hasRun: true } });
		const lane = taskLane(
			{ dev, qa, members: [dev, qa], isCrew: true },
			{ review: "approved", smoke: smoke({ total: 2, pending: 2, agentPass: 2 }) },
		);
		expect(lane.zone).toBe("action");
		expect(lane.note).toBe("qa · Play the cases");
	});

	it("reads ready only when dev can land, qa has played, and review has not objected", () => {
		const { dev, qa } = crew(mergeable, { crew: { id: "demo-1", role: "qa", hasRun: true } });
		const lane = taskLane(
			{ dev, qa, members: [dev, qa], isCrew: true },
			{ review: "approved", smoke: smoke({ total: 2, pass: 2, checked: 2 }) },
		);
		expect(lane.zone).toBe("merge");
	});

	it("a review that asked for changes blocks, whoever is awake", () => {
		const { dev, qa } = crew(mergeable, { crew: { id: "demo-1", role: "qa", hasRun: true } });
		const lane = taskLane(
			{ dev, qa, members: [dev, qa], isCrew: true },
			{ review: "changes", smoke: smoke({ total: 2, pass: 2, checked: 2 }) },
		);
		expect(lane.zone).toBe("action");
		expect(lane.note).toBe("review · Changes requested");
	});

	it("a review that never ran does not block — nothing starts one automatically yet", () => {
		const { dev, qa } = crew(mergeable, { crew: { id: "demo-1", role: "qa", hasRun: true } });
		const lane = taskLane(
			{ dev, qa, members: [dev, qa], isCrew: true },
			{ review: "not run", smoke: smoke({ total: 1, pass: 1, checked: 1 }) },
		);
		expect(lane.zone).toBe("merge");
	});

	it("waits rather than guessing while the checklist has not loaded", () => {
		const { dev, qa } = crew(mergeable, { crew: { id: "demo-1", role: "qa", hasRun: true } });
		const lane = taskLane({ dev, qa, members: [dev, qa], isCrew: true }, { review: "approved" });
		expect(lane.zone).toBe("pending");
	});
});

describe("taskLane — several tasks at once", () => {
	// The proof the trap is not happening: a realistic board where every crew task
	// has just handed off. Under a naive rollup all of them would be in Needs you.
	it("does not pile every handed-off task into Needs you", () => {
		const boards = [1, 2, 3].map((n) => {
			const dev = session(`demo-${n}a`, {
				status: "needs_input",
				statusReason: "idle_aged",
				activity: parked,
				crew: { id: `demo-${n}a`, role: "dev", hasRun: true },
			});
			const qa = session(`demo-${n}b`, {
				status: "idle",
				activity: running,
				crew: { id: `demo-${n}a`, role: "qa", hasRun: true },
			});
			return { dev, qa, members: [dev, qa], isCrew: true };
		});
		const zones = boards.map((task) => taskLane(task, { review: "not run" }).zone);
		expect(zones).toEqual(["working", "working", "working"]);
	});
});

describe("reviewGateState", () => {
	it("reads AO's own verdict at head, and says 'not run' when there is none", () => {
		expect(reviewGateState([])).toBe("not run");
		expect(reviewGateState([{ aoReview: { verdict: "approved" } } as never])).toBe("approved");
		expect(reviewGateState([{ aoReview: { verdict: "changes_requested" } } as never])).toBe("changes");
	});
});

describe("canAttachRole", () => {
	const soloTask = (over: Partial<WorkspaceSession> = {}) => {
		const dev = session("demo-9", over);
		return { dev, members: [dev], isCrew: false };
	};

	it("offers a seat on a solo task that is still going", () => {
		expect(canAttachRole(soloTask({ taskSize: "mechanical" }))).toBe(true);
		// A parked task is not a finished one: the new member arrives asleep, so
		// attaching here wakes nothing.
		expect(canAttachRole(soloTask({ isSuspended: true, status: "idle" }))).toBe(true);
	});

	it("offers nothing where the daemon would refuse", () => {
		// The seat is taken - and a second qa is refused by the database, not just
		// by policy.
		expect(canAttachRole(tasksFrom(crew().sessions)[0])).toBe(false);
		// Over: attaching would create an agent holding a worktree about to be
		// reclaimed, on a branch nobody will push.
		expect(canAttachRole(soloTask({ isTerminated: true, status: "terminated" }))).toBe(false);
		expect(canAttachRole(soloTask({ status: "merged" }))).toBe(false);
		// Not started: starting it forms the crew its size asks for on the way past.
		expect(canAttachRole(soloTask({ status: "todo" }))).toBe(false);
		// Not a task at all: an orchestrator shares one worktree with every other
		// orchestrator of its project.
		expect(canAttachRole(soloTask({ kind: "orchestrator" }))).toBe(false);
	});
});
