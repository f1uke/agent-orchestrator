import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { ChatEntry, Direction } from "./chat-event";
import { foldEntries } from "./chat-fold";
import { mockChatEntries } from "./chat-mock";
import { contextUsage, formatTokens, latestTodos, sessionActivity, shownTodos } from "./chat-inspect";

function loadFixture(name: string): ChatEntry[] {
	const testdata = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
	return readFileSync(join(testdata, name), "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line) => JSON.parse(line) as ChatEntry);
}

const first = loadFixture("first_session.jsonl");
const resumed = loadFixture("resumed_session.jsonl");
const both = [...first, ...resumed.map((e) => ({ ...e, seq: e.seq + first.length }))];
const SESSION = "12e38fc9-a4a5-4819-b046-4107629b2c8b";

let seq = 0;
const at = (dir: Direction, payload: unknown): ChatEntry => ({ seq: ++seq, ts: "", dir, payload });
const toolUse = (id: string, name: string, input: object, parent: string | null = null) =>
	at("out", {
		type: "assistant",
		parent_tool_use_id: parent,
		message: { id: `m-${id}`, content: [{ type: "tool_use", id, name, input }] },
	});
const toolResult = (id: string, content: string, isError = false) =>
	at("out", {
		type: "user",
		message: { content: [{ type: "tool_result", tool_use_id: id, content, is_error: isError }] },
	});
const task = (subtype: string, fields: object) =>
	at("out", { type: "system", subtype, session_id: SESSION, uuid: `u${seq}`, ...fields });

describe("context from the fixtures' results", () => {
	it("reads context from the turn's last API call, not the turn's sum", () => {
		const twoCallTurn = foldEntries(first.filter((e) => e.seq <= 34));
		expect(contextUsage(twoCallTurn)).toEqual({ tokens: 2 + 19006 + 328, window: 1_000_000 });
		expect(contextUsage(foldEntries(both))).toEqual({ tokens: 4 + 23735 + 104, window: 1_000_000 });
	});

	it("keeps the last known context when an interrupted turn reports no API call", () => {
		const throughInterrupt = foldEntries(both.filter((e) => e.seq <= first.length + 80));
		expect(contextUsage(throughInterrupt)?.tokens).toBe(2 + 23628 + 107);
	});

	it("knows the context from the first response, before any result, though not its window", () => {
		const early = foldEntries(first.filter((e) => e.seq < 19));
		expect(early.items.some((i) => i.kind === "turn_end")).toBe(false);
		expect(contextUsage(early)?.tokens).toBeGreaterThan(0);
		expect(contextUsage(early)?.window).toBeUndefined();
		expect(contextUsage(foldEntries([]))).toBeUndefined();
	});

	it("shows no window it cannot name", () => {
		seq = 0;
		const result = (modelUsage: object) =>
			at("out", {
				type: "result",
				subtype: "success",
				session_id: "s",
				total_cost_usd: 0.1,
				usage: { iterations: [{ input_tokens: 1, cache_read_input_tokens: 9, cache_creation_input_tokens: 0 }] },
				modelUsage,
			});
		const twoModels = { "claude-a": { contextWindow: 200_000 }, "claude-b": { contextWindow: 1_000_000 } };
		expect(contextUsage(foldEntries([result({})]))).toEqual({ tokens: 10 });
		expect(contextUsage(foldEntries([result(twoModels)]))).toEqual({ tokens: 10 });
		const switched = foldEntries([
			at("out", { type: "system", subtype: "init", session_id: "s", model: "claude-c" }),
			result({ "claude-a": { contextWindow: 200_000 } }),
		]);
		expect(contextUsage(switched)).toEqual({ tokens: 10 });
		const named = foldEntries([
			at("out", { type: "system", subtype: "init", session_id: "s", model: "claude-a" }),
			result(twoModels),
		]);
		expect(contextUsage(named)).toEqual({ tokens: 10, window: 200_000 });
	});
});

describe("todos", () => {
	const todos = (items: object[]) => ({ todos: items });
	const item = (content: string, status: string) => ({ content, status, activeForm: `${content}…` });

	it("is undefined until claude writes a list", () => {
		expect(latestTodos(foldEntries(first))).toBeUndefined();
	});

	it("is the latest top-level TodoWrite that was not refused", () => {
		seq = 0;
		const tr = foldEntries([
			toolUse("w1", "TodoWrite", todos([item("Read", "in_progress"), item("Fix", "pending")])),
			toolResult("w1", "ok"),
			toolUse("w2", "TodoWrite", todos([item("Read", "completed"), item("Fix", "in_progress")])),
			toolResult("w2", "ok"),
			toolUse("agent", "Agent", { description: "look" }),
			toolUse("w3", "TodoWrite", todos([item("Subagent's own", "pending")]), "agent"),
			toolUse("w4", "TodoWrite", todos([item("Refused", "pending")])),
			toolResult("w4", "denied", true),
		]);
		expect(latestTodos(tr)).toEqual([
			{ content: "Read", status: "completed", activeForm: "Read…" },
			{ content: "Fix", status: "in_progress", activeForm: "Fix…" },
		]);
	});
});

describe("the todo bar's list", () => {
	const write = (id: string, ...statuses: string[]) => [
		toolUse(id, "TodoWrite", {
			todos: statuses.map((status, i) => ({ content: `step ${i}`, activeForm: `step ${i}…`, status })),
		}),
		toolResult(id, "ok"),
	];
	const input = (uuid: string) => at("in", { type: "user", uuid, message: { content: "next" } });
	const lifecycle = (uuid: string, state: string) =>
		at("out", { type: "command_lifecycle", command_uuid: uuid, state });

	it("is the latest list while any item is open, across later turns", () => {
		seq = 0;
		const tr = foldEntries([...write("w1", "completed", "in_progress"), input("u2"), lifecycle("u2", "started")]);
		expect(shownTodos(tr)?.map((t) => t.status)).toEqual(["completed", "in_progress"]);
	});

	it("keeps an all-completed list until the next user turn starts, then hides it", () => {
		seq = 0;
		const done = [...write("w1", "completed", "completed")];
		expect(shownTodos(foldEntries(done))).toHaveLength(2);
		seq = 0;
		const queued = [...write("w1", "completed", "completed"), input("u2"), lifecycle("u2", "queued")];
		expect(shownTodos(foldEntries(queued))).toHaveLength(2);
		seq = 0;
		const started = [...write("w1", "completed", "completed"), input("u2"), lifecycle("u2", "started")];
		expect(shownTodos(foldEntries(started))).toBeUndefined();
	});

	it("is nothing with no list or an empty one", () => {
		seq = 0;
		expect(shownTodos(foldEntries([]))).toBeUndefined();
		seq = 0;
		expect(shownTodos(foldEntries(write("w1")))).toBeUndefined();
	});
});

describe("subagents and background", () => {
	it("follows an Agent call from running to done, counting its tools", () => {
		seq = 0;
		const call = toolUse("a1", "Agent", { subagent_type: "Explore", description: "find callers" });
		const sub = [toolUse("s1", "Grep", {}, "a1"), toolUse("s2", "Read", {}, "a1")];
		const running = sessionActivity(foldEntries([call, ...sub]));
		expect(running).toEqual([
			{ key: "a1", kind: "agent", title: "Explore", detail: "find callers", state: "running", tools: 2 },
		]);
		const done = sessionActivity(foldEntries([call, ...sub, toolResult("a1", "3 callers")]));
		expect(done[0].state).toBe("done");
		const failed = sessionActivity(foldEntries([call, toolResult("a1", "boom", true)]));
		expect(failed[0].state).toBe("error");
	});

	it("lists a background Bash only while it has no result, and stops it when the process ends", () => {
		seq = 0;
		const bg = toolUse("b1", "Bash", { command: "go test -count=50 ./...", run_in_background: true });
		const fg = toolUse("b2", "Bash", { command: "ls" });
		expect(sessionActivity(foldEntries([bg, fg]))).toEqual([
			{
				key: "b1",
				kind: "background",
				title: "Bash · background",
				detail: "go test -count=50 ./...",
				state: "running",
				tools: 0,
			},
		]);
		expect(sessionActivity(foldEntries([bg, toolResult("b1", "started")]))).toEqual([]);
		const exited = foldEntries([bg, at("host", { kind: "exit", code: 0 })]);
		expect(sessionActivity(exited).map((a) => a.state)).toEqual(["stopped"]);
	});

	it("follows the task_* events: a linked task keeps its call listed, an unlinked one is listed by id", () => {
		seq = 0;
		const entries = [
			toolUse("b1", "Bash", { command: "npm test", run_in_background: true }),
			toolResult("b1", "Command running in background with ID: bash_1"),
			task("task_started", { task_id: "bash_1", tool_use_id: "b1", description: "npm test", task_type: "local_bash" }),
			task("task_started", { task_id: "wf_9", description: "nightly sweep", task_type: "local_workflow" }),
		];
		expect(sessionActivity(foldEntries(entries)).map((a) => [a.key, a.kind, a.state])).toEqual([
			["wf_9", "task", "running"],
			["b1", "background", "running"],
		]);
		const settled = foldEntries([
			...entries,
			task("task_notification", { task_id: "bash_1", tool_use_id: "b1", status: "completed", summary: "ok" }),
			task("task_updated", { task_id: "wf_9", patch: { status: "killed" } }),
		]);
		expect(sessionActivity(settled)).toEqual([
			{ key: "wf_9", kind: "task", title: "Task", detail: "nightly sweep", state: "stopped", tools: 0 },
			{ key: "b1", kind: "background", title: "Bash · background", detail: "npm test", state: "done", tools: 0 },
		]);
		const exited = foldEntries([...entries, at("host", { kind: "exit", code: null })]);
		expect(exited.tasks.map((t) => t.state)).toEqual(["stopped", "stopped"]);
	});

	it("puts unfinished work first, newest first", () => {
		seq = 0;
		const tr = foldEntries([
			toolUse("a1", "Agent", { description: "old" }),
			toolResult("a1", "ok"),
			toolUse("a2", "Agent", { description: "newer" }),
			toolResult("a2", "ok"),
			toolUse("a3", "Agent", { description: "still going" }),
		]);
		expect(sessionActivity(tr).map((a) => a.detail)).toEqual(["still going", "newer", "old"]);
	});
});

it("formats token counts the way the rail shows them", () => {
	expect([999, 1568, 31_200, 350_380, 1_900_000, 2_000_000].map(formatTokens)).toEqual([
		"999",
		"1.6k",
		"31.2k",
		"350k",
		"1.9M",
		"2M",
	]);
});

it("gives the dev:web stub's session a todo list, running background work and a known window", () => {
	const tr = foldEntries(mockChatEntries(first));
	expect(latestTodos(tr)?.map((t) => t.status)).toEqual([
		"completed",
		"completed",
		"completed",
		"in_progress",
		"pending",
		"pending",
	]);
	expect(sessionActivity(tr).map((a) => [a.title, a.state, a.tools])).toEqual([
		["Bash · background", "running", 0],
		["Explore", "done", 3],
	]);
	expect(contextUsage(tr)).toEqual({ tokens: 124_000, window: 200_000 });
});

describe("todos from TaskCreate and TaskUpdate", () => {
	const created = (id: string, subject: string, n: number) => [
		toolUse(id, "TaskCreate", { subject, description: "d" }),
		toolResult(id, `Task #${n} created successfully: ${subject}`),
	];

	it("builds the list from creates and applies updates by task id", () => {
		seq = 0;
		const tr = foldEntries([
			...created("c1", "Find failing tests", 1),
			...created("c2", "Fix the cause", 2),
			toolUse("u1", "TaskUpdate", { taskId: "1", status: "completed" }),
			toolResult("u1", "Updated task #1 status"),
			toolUse("u2", "TaskUpdate", { taskId: "2", status: "in_progress", subject: "Fix the real cause" }),
		]);
		expect(latestTodos(tr)).toEqual([
			{ content: "Find failing tests", activeForm: "Find failing tests", status: "completed" },
			{ content: "Fix the real cause", activeForm: "Fix the cause", status: "in_progress" },
		]);
	});

	it("drops a deleted task and ignores a refused call or a subagent's", () => {
		seq = 0;
		const tr = foldEntries([
			...created("c1", "One", 1),
			...created("c2", "Two", 2),
			toolUse("u1", "TaskUpdate", { taskId: "1", status: "deleted" }),
			toolUse("u2", "TaskUpdate", { taskId: "2", status: "completed" }),
			toolResult("u2", "denied", true),
			toolUse("agent", "Agent", { description: "x" }),
			toolUse("u3", "TaskUpdate", { taskId: "2", status: "completed" }, "agent"),
		]);
		expect(latestTodos(tr)?.map((t) => [t.content, t.status])).toEqual([["Two", "pending"]]);
	});

	it("reads the todo list out of the recorded long session", () => {
		const tr = foldEntries(loadFixture("long_session.jsonl"));
		expect(latestTodos(tr)?.map((t) => [t.content, t.status])).toEqual([
			["Find failing tests", "completed"],
			["Fix the cause", "completed"],
			["Verify and review results", "completed"],
		]);
	});

	it("lets a later TodoWrite replace the list", () => {
		seq = 0;
		const tr = foldEntries([
			...created("c1", "Old", 1),
			toolUse("w1", "TodoWrite", { todos: [{ content: "New", status: "pending", activeForm: "News" }] }),
			toolResult("w1", "ok"),
		]);
		expect(latestTodos(tr)?.map((t) => t.content)).toEqual(["New"]);
	});
});

describe("the recorded long session", () => {
	const tr = foldEntries(loadFixture("long_session.jsonl"));
	const activity = sessionActivity(tr);

	it("lists neither a foreground Bash task nor a raw task type", () => {
		expect(activity.filter((a) => a.kind === "task")).toEqual([]);
		expect(activity.map((a) => a.title)).toEqual(expect.arrayContaining(["Bash · background", "Explore"]));
		expect(activity).toHaveLength(2);
		expect(JSON.stringify(activity)).not.toContain("local_bash");
	});

	it("takes the agent's tool count and state from its linked task", () => {
		const agent = activity.find((a) => a.kind === "agent");
		expect(agent).toMatchObject({ state: "done", tools: 2 });
	});
});
