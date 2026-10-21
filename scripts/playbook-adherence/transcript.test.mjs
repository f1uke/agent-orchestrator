import { test } from "node:test";
import assert from "node:assert/strict";
import { analyzeTurn, anySetup } from "./transcript.mjs";

const PB = "/home/u/.claude/plugins/cache/pstack-claude/pstack/9.9.9/skills/poteto-mode/playbooks";
const steps = (n) => Array.from({ length: n }, (_, i) => `step ${i + 1} words one two three four`);
const read = (name) => ({ name: "Read", input: { file_path: `${PB}/${name}.md` } });
const task = (text) => ({ name: "TaskCreate", input: { subject: text } });
const bash = (command) => ({ name: "Bash", input: { command } });
const opts = { steps: steps(3) };

test("playbook read and every step copied before exploring passes", () => {
	const a = analyzeTurn(
		[{ name: "Skill", input: {} }, read("bug-fix"), ...steps(3).map(task), bash("cat src/duration.js")],
		"bug-fix",
		opts,
	);
	assert.equal(a.setupBeforeExploration, true);
	assert.equal(a.playbookReadAt, 1);
});

test("exploring before the playbook read fails, as in the reported worker", () => {
	const a = analyzeTurn([bash("git ls-files"), read("bug-fix"), ...steps(3).map(task)], "bug-fix", opts);
	assert.equal(a.setupDone, true);
	assert.equal(a.setupBeforeExploration, false);
});

test("the wrong playbook fails even with a full todolist", () => {
	const a = analyzeTurn([read("feature"), ...steps(3).map(task), bash("ls")], "bug-fix", opts);
	assert.equal(a.setupBeforeExploration, false);
	assert.deepEqual(a.playbooksRead, ["feature"]);
});

test("a todolist missing a step fails", () => {
	const a = analyzeTurn([read("bug-fix"), ...steps(2).map(task), bash("ls")], "bug-fix", opts);
	assert.equal(a.stepsCovered, 2);
	assert.equal(a.setupBeforeExploration, false);
});

test("a todo.md checklist counts as a todolist", () => {
	const todo = {
		name: "Write",
		input: {
			file_path: "/w/todo.md",
			content: steps(3)
				.map((s) => `- [ ] ${s}`)
				.join("\n"),
		},
	};
	assert.equal(analyzeTurn([read("bug-fix"), todo], "bug-fix", opts).setupBeforeExploration, true);
});

test("reading a playbook with cat counts, reading the skill does not explore", () => {
	const a = analyzeTurn(
		[bash(`cat ${PB}/perf-issue.md`), { name: "Read", input: { file_path: "/home/u/.claude/plugins/x/SKILL.md" } }],
		"perf-issue",
		opts,
	);
	assert.deepEqual(a.playbooksRead, ["perf-issue"]);
	assert.equal(a.firstExplorationAt, null);
});

test("AO's check needs both a playbook read and a todolist", () => {
	assert.equal(anySetup([read("feature")]), false);
	assert.equal(anySetup([task("x")]), false);
	assert.equal(anySetup([read("feature"), task("x")]), true);
});

test("a nudged turn may rely on the playbook read in turn one", () => {
	const turnTwo = [...steps(3).map(task), bash("cat src/report.js")];
	assert.equal(analyzeTurn(turnTwo, "bug-fix", opts).setupBeforeExploration, false);
	assert.equal(analyzeTurn(turnTwo, "bug-fix", { ...opts, playbookAlreadyRead: true }).setupBeforeExploration, true);
});

test("writing plans to the knowledge store and reporting with ao is not code exploration", () => {
	const a = analyzeTurn(
		[bash("cat > ~/.ao/knowledge/mer/plans/x.md <<'EOF2'\nplan\nEOF2"), bash("ao send --session mer-0 --message hi")],
		"bug-fix",
		opts,
	);
	assert.equal(a.firstExplorationAt, null);
});
