// The variables under test. Everything else a run sees (the standing prompt
// buildSystemPrompt composes, the /pstack:poteto-mode prefix #418 adds, the
// fixture repo, the model and settings) is identical across arms.

export const GATE_TURN_ONE =
	"**Turn one: understand it.** Read the code, find out what the task actually means, and decide what you would do about it. Reading, searching, running read-only commands, and writing what you learn into the AO knowledge store need nobody's permission. What you may not do yet is start the change itself: no edit to a file in the repository, no new source file, no commit that implements the task.";

// Arm A: same gate, but turn one opens with the process set-up a loaded skill
// asks for. Names no skill and no plugin, per the convention CheckInGate keeps.
export const GATE_TURN_ONE_SETUP_FIRST =
	"**Turn one: set up, then understand it.** Start turn one with your process, before you read any code: if a skill you loaded tells you to match the task to a playbook and open a todolist of its steps, do that first, opening the playbook's file and copying its steps into your todolist. A todolist (your task-tracking tool, or an uncommitted `todo.md` outside the repository) is not an edit this gate forbids. Then read the code, find out what the task actually means, and decide what you would do about it. Reading, searching, running read-only commands, and writing what you learn into the AO knowledge store need nobody's permission. What you may not do yet is start the change itself: no edit to a file in the repository, no new source file, no commit that implements the task.";

export const NUDGE =
	"AO: your check-in is missing its process set-up. Before this task can go ahead, match it to the playbook your loaded skill names, open that playbook's file, and copy its steps into your todolist verbatim (a step you will not do stays with `skip: <reason>`). Do not edit repository files yet. Then post your check-in again.";

export const STOP_REFUSAL =
	"AO refused this check-in: no playbook file has been read and no todolist holds its steps yet. Match the task to the playbook your loaded skill names, open that playbook's file, copy its steps into your todolist verbatim, then post the check-in again. Do not edit repository files yet.";

export const STOP_MAX_REFUSALS = 3;

export const TASKS = {
	bug: {
		branch: "feature/invoice-hours",
		playbook: "bug-fix",
		wrongPlaybook: "feature",
		brief:
			"Invoices for northwind came out low last month. Their 1h30m kickoff call on 2026-09-01 shows on the invoice as 1.01h instead of 1.50h, so we billed them about 47 minutes short on that line alone. Find out why and fix it so durations with minutes bill correctly. sample.jsonl in the repo has that entry; `node bin/tally.js invoice sample.jsonl --client northwind --rate 95` shows it.",
	},
	feature: {
		branch: "feature/report-by-project",
		playbook: "feature",
		wrongPlaybook: "refactoring",
		brief:
			"Account managers want `tally report` to break hours down per project as well as per client. Add a `--by project` option to `tally report` that prints one row per client and project (entries and hours), sorted by hours, biggest first. `tally report` without the option must print exactly what it prints today.",
	},
	perf: {
		branch: "feature/report-speed",
		playbook: "perf-issue",
		wrongPlaybook: "bug-fix",
		brief:
			"`tally report` has become unusably slow on the full-year export: about 80k entries take over a minute, while invoices for the same file come back in under a second. Make the report fast on files that size without changing its output. The export is not in the repo; any generated file of that size reproduces it.",
	},
};

// Each arm: which gate paragraph the standing prompt carries, whether the
// brief names a playbook on line 2 (and whether that name is right), whether
// AO nudges after a check-in that misses the set-up, and whether a Stop hook
// refuses that check-in.
export const ARMS = {
	base: { gate: "original", named: null, nudge: false, stopHook: false },
	A: { gate: "setup-first", named: null, nudge: false, stopHook: false },
	B: { gate: "setup-first", named: null, nudge: true, stopHook: false },
	C: { gate: "setup-first", named: "correct", nudge: true, stopHook: false },
	"C-wrong": { gate: "setup-first", named: "wrong", nudge: true, stopHook: false },
	D: { gate: "setup-first", named: null, nudge: false, stopHook: true },
	// What AO ships: the prompt buildSystemPrompt composes today, and the real
	// `ao hooks claude-code stop` of the build under test as the Stop hook.
	"base-new": { gate: "composed", named: null, nudge: false, stopHook: "ao" },
};

export function systemPromptFor(arm, composed) {
	if (ARMS[arm].gate === "composed") return composed;
	const original = composed.replace(GATE_TURN_ONE_SETUP_FIRST, GATE_TURN_ONE);
	if (!original.includes(GATE_TURN_ONE))
		throw new Error("composed prompt has no turn-one gate paragraph: did CheckInGate change?");
	return ARMS[arm].gate === "setup-first" ? original.replace(GATE_TURN_ONE, GATE_TURN_ONE_SETUP_FIRST) : original;
}

export function initialPromptFor(arm, task) {
	const lines = ["/pstack:poteto-mode", ""];
	const named = ARMS[arm].named;
	if (named) {
		const playbook = named === "correct" ? task.playbook : task.wrongPlaybook;
		lines.push(`Playbook: ${playbook}. Open playbooks/${playbook}.md and copy its steps into your todolist first.`, "");
	}
	lines.push(task.brief);
	return lines.join("\n");
}
