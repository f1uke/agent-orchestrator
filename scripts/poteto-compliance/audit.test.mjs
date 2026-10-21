import { test } from "node:test";
import assert from "node:assert/strict";
import { auditSession, midSentenceColons, parseModelSheet, parsePrinciples, timeline } from "./audit.mjs";

const PB = "/home/u/.claude/plugins/cache/pstack-claude/pstack/9.9.9/skills/poteto-mode/playbooks";
const STEPS = {
	"bug-fix": ["Reproduce it yourself first", "Binary-search the cause now", "Stage the commits so", "Run Opening a PR"],
	feature: ["Sketch the data shape", "Delegate code writing now", "Run Opening a PR"],
};

let id = 0;
const tool = (name, input, result = "") => {
	const toolId = `t${++id}`;
	return [
		{ type: "assistant", message: { content: [{ type: "tool_use", id: toolId, name, input }] } },
		{ type: "user", message: { content: [{ type: "tool_result", tool_use_id: toolId, content: result }] } },
	];
};
const human = (text) => [{ type: "user", message: { content: text } }];
const say = (text) => [{ type: "assistant", message: { content: [{ type: "text", text }] } }];
const bash = (command, result) => tool("Bash", { command }, result);
const skill = (name) => tool("Skill", { skill: name });
const readPlaybook = (name) => tool("Read", { file_path: `${PB}/${name}.md` });
const task = (subject, n) => tool("TaskCreate", { subject }, `Task #${n} created successfully: ${subject}`);
const done = (n) => tool("TaskUpdate", { taskId: String(n), status: "completed" });
const edit = (file_path) => tool("Edit", { file_path, old_string: "a", new_string: "b" });

const ctx = (extra = {}) => ({
	branch: "feature/x",
	commits: null,
	prBodies: [],
	steps: (kind) => STEPS[kind],
	models: parseModelSheet(
		"feature, refactoring: sonnet @high\njudgment and prose: opus @high\ndefault effort: medium\n",
	),
	principles: [["principle-laziness-protocol", "Laziness Protocol"]],
	...extra,
});
const audit = (events, extra) => auditSession(timeline(events.flat()), ctx(extra)).results;

test("a feature run that follows the playbook passes every rule it can be held to", () => {
	const r = audit([
		human("Add an export button. Open a PR into main when done."),
		skill("pstack:poteto-mode"),
		readPlaybook("feature"),
		...STEPS.feature.map((s, k) => task(s, k + 1)),
		edit("/w/src/export.js"),
		bash("node --test src/export.test.js", "pass 3"),
		say("Tests pass on the new export."),
		skill("pstack:deslop"),
		bash("git commit -m 'feat: export'"),
		skill("pstack:no-comments"),
		skill("pstack:unslop"),
		...STEPS.feature.map((_, k) => done(k + 1)),
		bash("git push -u origin feature/x"),
		bash("gh pr create --base main --title 'feat: export'"),
		say("The PR is open. It adds the export button."),
	]);
	const failing = Object.entries(r).filter(([, v]) => v.verdict === "fail");
	assert.deepEqual(failing, []);
	assert.equal(r["steps-closed-at-pr"].verdict, "pass");
	assert.equal(r["outward-after-yes"].verdict, "pass");
});

test("a todolist that misses a playbook step fails, and an open step at PR open fails", () => {
	const r = audit([
		readPlaybook("feature"),
		task(STEPS.feature[0], 1),
		task(STEPS.feature[2], 2),
		done(1),
		bash("gh pr create --base main"),
	]);
	assert.equal(r["todolist-has-steps"].verdict, "fail");
	assert.equal(r["steps-closed-at-pr"].verdict, "fail");
});

test("a step marked skip with a reason counts as closed at PR open", () => {
	const r = audit([
		human("open a PR when ready"),
		readPlaybook("feature"),
		task(STEPS.feature[0], 1),
		task(`${STEPS.feature[1]} skip: one file, no delegate`, 2),
		task(STEPS.feature[2], 3),
		done(1),
		done(3),
		bash("gh pr create --base main"),
	]);
	assert.equal(r["steps-closed-at-pr"].verdict, "pass");
});

test("deslop after the shipped commit does not count", () => {
	const r = audit([bash("git commit -m x"), skill("pstack:deslop"), bash("git push")]);
	assert.equal(r["deslop-before-commit"].verdict, "fail");
});

test("a bug fix that edits source before any repro run fails, a test run first passes", () => {
	const first = audit([readPlaybook("bug-fix"), bash("cat src/a.js"), edit("/w/src/a.js")]);
	assert.equal(first["bugfix-repro-before-edit"].verdict, "fail");
	const repro = audit([readPlaybook("bug-fix"), bash("npm test -- a"), edit("/w/src/a.js")]);
	assert.equal(repro["bugfix-repro-before-edit"].verdict, "pass");
});

test("a bug fix needs a test-only commit before the source commit", () => {
	const events = [readPlaybook("bug-fix")];
	const fixFirst = audit(events, { commits: [{ files: ["src/a.js", "test/a.test.js"] }] });
	assert.equal(fixFirst["bugfix-failing-test-first"].verdict, "fail");
	const testFirst = audit(events, { commits: [{ files: ["test/a.test.js"] }, { files: ["src/a.js"] }] });
	assert.equal(testFirst["bugfix-failing-test-first"].verdict, "pass");
});

test("a verified claim needs a script run after the last edit", () => {
	const stale = audit([bash("npm test"), edit("/w/src/a.js"), say("Verified, the fix works.")]);
	assert.equal(stale["verified-claims-backed"].verdict, "fail");
	const fresh = audit([edit("/w/src/a.js"), bash("npm test"), say("Verified, the fix works.")]);
	assert.equal(fresh["verified-claims-backed"].verdict, "pass");
	const readOnly = audit([edit("/w/src/a.js"), bash("cat src/a.js && git diff"), say("ทดสอบแล้ว ใช้ได้")]);
	assert.equal(readOnly["verified-claims-backed"].verdict, "fail");
});

test("a principle cited without reading its leaf skill fails", () => {
	const unread = audit([say("Per principle-laziness-protocol I kept the diff small.")]);
	assert.equal(unread["principles-cited-read"].verdict, "fail");
	const read = audit([skill("pstack:principle-laziness-protocol"), say("Per the Laziness Protocol I kept it small.")]);
	assert.equal(read["principles-cited-read"].verdict, "pass");
});

test("subagents must be poteto-agent with a model and effort the sheet names", () => {
	const r = audit([
		tool("Agent", { subagent_type: "general-purpose", model: "sonnet" }),
		tool("Agent", { subagent_type: "pstack:poteto-agent-high", model: "haiku" }),
	]);
	assert.equal(r["subagent-type"].verdict, "fail");
	assert.equal(r["subagent-model"].verdict, "fail");
	const ok = audit([
		tool("Agent", { subagent_type: "pstack:poteto-agent-high", model: "sonnet" }),
		tool("Agent", { subagent_type: "pstack:comment-sicko" }),
	]);
	assert.equal(ok["subagent-type"].verdict, "pass");
	assert.equal(ok["subagent-model"].verdict, "pass");
});

test("merging a PR fails even when a human asked", () => {
	const r = audit([human("merge it"), bash("gh pr merge 7 --squash")]);
	assert.equal(r["no-self-merge"].verdict, "fail");
});

test("a push needs a yes or a brief that asks for the PR", () => {
	const unasked = audit([human("Fix the timeout bug."), bash("git push -u origin fix/x")]);
	assert.equal(unasked["outward-after-yes"].verdict, "fail");
	const asked = audit([human("Fix the timeout bug."), say("Ready. Push?"), human("ok push ได้เลย"), bash("git push")]);
	assert.equal(asked["outward-after-yes"].verdict, "pass");
	const brief = audit([
		human("<command-name>/pstack:poteto-mode</command-name><command-args>Fix it. PR into main.</command-args>"),
		bash("git push"),
	]);
	assert.equal(brief["outward-after-yes"].verdict, "pass");
	const question = audit([human("did you push?"), bash("git push")]);
	assert.equal(question["outward-after-yes"].verdict, "fail");
});

test("a Jira write needs its own ask even when the brief asks for a PR", () => {
	const r = audit([
		human("Fix it and open a PR."),
		tool("mcp__plugin_atlassian_atlassian__editJiraIssue", { issueIdOrKey: "ABC-1" }),
	]);
	assert.equal(r["outward-after-yes"].verdict, "fail");
});

test("an em dash in a reply fails, inside code it does not", () => {
	assert.equal(audit([say("Done — shipped.")])["reply-no-em-dash"].verdict, "fail");
	assert.equal(audit([say("Ran `a — b` and it shipped.")])["reply-no-em-dash"].verdict, "pass");
});

test("a colon reveal counts, a list intro, a time, a label and a table row do not", () => {
	assert.equal(midSentenceColons("The best part: it learns."), 1);
	assert.equal(midSentenceColons("**Performance:** it improved."), 1);
	assert.equal(midSentenceColons("Three things changed:\n- one"), 0);
	assert.equal(midSentenceColons("Tests cover these cases: all fit, one overflows, and none fit."), 0);
	assert.equal(midSentenceColons("It ran at 10:30 today."), 0);
	assert.equal(midSentenceColons("- iPhone SE: both flows pass"), 0);
	assert.equal(midSentenceColons("- **Upgrade path:** install the old build first"), 0);
	assert.equal(midSentenceColons("- **`pid`**: prints the pid"), 0);
	assert.equal(midSentenceColons("- **Upgrade path:** the cause: a race"), 1);
	assert.equal(midSentenceColons("| a: b | c |"), 0);
	assert.equal(midSentenceColons('He wrote "Turn one: read" there.'), 0);
});

test("PR bodies are checked for dashes and colon connectors", () => {
	const r = audit([], { prBodies: ["## Why\nThe cause: a race."] });
	assert.equal(r["pr-body-no-colon-connector"].verdict, "fail");
	assert.equal(r["pr-body-no-em-dash"].verdict, "pass");
});

test("a perf number before benchmark-checklist fails", () => {
	const r = audit([bash("node bench.js"), say("The report is now 40% faster on 80k rows.")]);
	assert.equal(r["benchmark-before-perf-number"].verdict, "fail");
	const ok = audit([skill("pstack:benchmark-checklist"), say("The report is now 40% faster on 80k rows.")]);
	assert.equal(ok["benchmark-before-perf-number"].verdict, "pass");
});

test("rules a session cannot be held to are n/a, not pass", () => {
	const r = audit([human("What does this flag do?"), say("It turns on the cache.")]);
	for (const id of ["deslop-before-commit", "steps-closed-at-pr", "bugfix-repro-before-edit", "outward-after-yes"])
		assert.equal(r[id].verdict, "n/a", id);
});

test("subagent turns and hook output do not count as the worker's or the human's", () => {
	const events = [
		{ type: "assistant", isSidechain: true, message: { content: [{ type: "text", text: "Done — x" }] } },
		{ type: "user", isMeta: true, message: { content: "ok push" } },
		...human("<task-notification>ok push</task-notification>"),
		...bash("git push"),
	];
	const r = audit([events]);
	assert.equal(r["reply-no-em-dash"].verdict, "n/a");
	assert.equal(r["outward-after-yes"].verdict, "fail");
});

test("the model sheet parser reads panels, aliases and the default effort", () => {
	const m = parseModelSheet(
		"arena runners: opus @high, sonnet\nhow explorer: auto\ndefault effort: low\nsession hook: on\n",
	);
	assert.deepEqual([...m.pairs].sort(), ["inherit @low", "opus @high", "sonnet @low"]);
});

test("the principles index parser reads slug and display name", () => {
	const text = "- **Laziness Protocol** (**principle-laziness-protocol**). Refactoring.\n- not one\n";
	assert.deepEqual(parsePrinciples(text), [["principle-laziness-protocol", "Laziness Protocol"]]);
});

test("a todo.md that held the steps still counts after it is rewritten as a status note", () => {
	const write = (content) => tool("Write", { file_path: "/k/plans/x--todo.md", content });
	const r = audit([
		readPlaybook("feature"),
		write(STEPS.feature.map((s, k) => `${k + 1}. [ ] ${s}`).join("\n")),
		write("- shipped, waiting on review"),
	]);
	assert.equal(r["todolist-has-steps"].verdict, "pass");
});
