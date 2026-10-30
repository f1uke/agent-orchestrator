import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { ChatEntry, Direction } from "./chat-event";
import { foldEntries } from "./chat-fold";
import {
	addedRules,
	contextUsage,
	formatTokens,
	latestTodos,
	sessionActivity,
	sessionHistory,
	usageSummary,
} from "./chat-inspect";

function loadFixture(name: string): ChatEntry[] {
	const testdata = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
	return readFileSync(join(testdata, name), "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line) => JSON.parse(line) as ChatEntry);
}

const first = loadFixture("first_session.jsonl");
const resumed = loadFixture("resumed_session.jsonl");
// One AO session's log: the resumed process's entries follow the first's.
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

function finalModelUsage(entries: ChatEntry[]) {
	const results = entries.filter((e) => (e.payload as { type?: string }).type === "result");
	const last = results.at(-1)?.payload as {
		modelUsage: Record<string, { outputTokens: number; cacheReadInputTokens: number }>;
	};
	return Object.values(last.modelUsage)[0];
}

describe("usage and context from the fixtures' results", () => {
	it("sums result.usage over the claude session, which matches claude's own cumulative modelUsage", () => {
		const usage = usageSummary(foldEntries(both));
		const claude = finalModelUsage(both);
		expect(usage).toEqual({
			sessionId: SESSION,
			costUsd: 0.09444599999999999,
			outputTokens: claude.outputTokens,
			cacheReadTokens: claude.cacheReadInputTokens,
		});
		expect(usage?.outputTokens).toBe(1568);
	});

	it("reads context from the turn's last API call, not the turn's sum", () => {
		const twoCallTurn = foldEntries(first.filter((e) => e.seq <= 34));
		expect(contextUsage(twoCallTurn)).toEqual({ tokens: 2 + 19006 + 328, window: 1_000_000 });
		expect(contextUsage(foldEntries(both))).toEqual({ tokens: 4 + 23735 + 104, window: 1_000_000 });
	});

	it("keeps the last known context when an interrupted turn reports no API call", () => {
		const throughInterrupt = foldEntries(both.filter((e) => e.seq <= first.length + 80));
		expect(contextUsage(throughInterrupt)?.tokens).toBe(2 + 23628 + 107);
	});

	it("shows no window it cannot name, and nothing before the first result", () => {
		expect(contextUsage(foldEntries(first.filter((e) => e.seq < 19)))).toBeUndefined();
		expect(usageSummary(foldEntries(first.filter((e) => e.seq < 19)))).toBeUndefined();
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
		const named = foldEntries([
			at("out", { type: "system", subtype: "init", session_id: "s", model: "claude-a" }),
			result(twoModels),
		]);
		expect(contextUsage(named)).toEqual({ tokens: 10, window: 200_000 });
	});
});

describe("history", () => {
	it("has one row per claude process, with its start, turns and the session's cost at its end", () => {
		const tr = foldEntries(both);
		const rows = sessionHistory(tr);
		expect(rows.map(({ itemIndex: _, ...row }) => row)).toEqual([
			{
				startedAt: "2026-10-29T17:08:57.042Z",
				resumed: false,
				sessionId: SESSION,
				turns: 5,
				costUsd: 0.04411749999999999,
			},
			{
				startedAt: "2026-10-29T17:09:36.671Z",
				resumed: true,
				sessionId: SESSION,
				turns: 5,
				costUsd: 0.09444599999999999,
			},
		]);
		expect(rows.map((r) => tr.items[r.itemIndex])).toMatchObject([
			{ kind: "notice", notice: "spawn" },
			{ kind: "notice", notice: "resumed", text: SESSION },
		]);
	});

	it("is empty for an empty log", () => {
		expect(sessionHistory(foldEntries([]))).toEqual([]);
	});
});

describe("permissions", () => {
	it("adds no rule for the fixtures' Always allow (a mode switch) or their denied rule suggestion", () => {
		expect(addedRules(foldEntries(both))).toEqual([]);
	});

	it("adds the rule an allowed request carried, once", () => {
		const request = resumed.find((e) => e.seq === 63)!;
		const suggestions = (request.payload as { request: { permission_suggestions: unknown[] } }).request
			.permission_suggestions;
		const allow = {
			type: "control_response",
			response: {
				subtype: "success",
				request_id: "e56f8ec6-1341-4bc8-8a84-29e56964a6bf",
				response: { behavior: "allow", updatedInput: {}, updatedPermissions: suggestions },
			},
		};
		const upToRequest = resumed.filter((e) => e.seq <= 63);
		const tr = foldEntries([...upToRequest, { seq: 64, ts: "", dir: "in", payload: allow }]);
		expect(addedRules(tr)).toEqual([{ rule: "Bash(rm hello.txt)", behavior: "allow" }]);
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
			{ key: "wf_9", kind: "task", title: "local_workflow", detail: "nightly sweep", state: "stopped", tools: 0 },
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
