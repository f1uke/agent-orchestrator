import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { ChatEntry, Direction } from "./chat-event";
import { foldEntries } from "./chat-fold";
import { emptyOverlay } from "./chat-stream";
import { chatRows } from "./chat-rows";
import { subagentTranscript, taskList, taskTarget } from "./chat-tasks";

function loadFixture(name: string): ChatEntry[] {
	const testdata = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
	return readFileSync(join(testdata, name), "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line) => JSON.parse(line) as ChatEntry);
}

let seq = 0;
const at = (dir: Direction, payload: unknown): ChatEntry => ({ seq: ++seq, ts: "2026-11-03T09:00:00.000Z", dir, payload });
const toolUse = (id: string, name: string, input: object, parent: string | null = null) =>
	at("out", {
		type: "assistant",
		parent_tool_use_id: parent,
		message: { id: `m-${id}`, content: [{ type: "tool_use", id, name, input }] },
	});
const toolResult = (id: string, content: string, parent: string | null = null) =>
	at("out", {
		type: "user",
		parent_tool_use_id: parent,
		message: { content: [{ type: "tool_result", tool_use_id: id, content, is_error: false }] },
	});
const task = (subtype: string, fields: object) => at("out", { type: "system", subtype, ...fields });
const started = (taskId: string, toolUseId: string, taskType: string, description: string, background = true) =>
	task("task_started", {
		task_id: taskId,
		tool_use_id: toolUseId,
		task_type: taskType,
		description,
		is_backgrounded: background,
	});

const reset = () => {
	seq = 0;
};

describe("taskList", () => {
	const entries = () => {
		reset();
		return [
			toolUse("a1", "Agent", { description: "Map callers", subagent_type: "Explore", prompt: "Find every caller." }),
			started("ta1", "a1", "local_agent", "Map callers"),
			toolUse("b1", "Bash", { command: "go test -count=50 ./...", run_in_background: true }),
			started("tb1", "b1", "local_bash", "Run the soak test"),
			toolUse("a2", "Agent", { description: "Old agent" }),
			started("ta2", "a2", "local_agent", "Old agent"),
			task("task_notification", { task_id: "ta2", tool_use_id: "a2", status: "completed", summary: "All good." }),
		];
	};

	it("splits running from finished, newest call first, and names each task's kind, title, command and prompt", () => {
		const list = taskList(foldEntries(entries()));
		expect(list.running.map((t) => [t.ref, t.kind, t.title])).toEqual([
			["b1", "bash", "Run the soak test"],
			["a1", "agent", "Map callers"],
		]);
		expect(list.running[1]).toMatchObject({ prompt: "Find every caller.", subagentType: "Explore", running: true });
		expect(list.running[0]).toMatchObject({ command: "go test -count=50 ./...", running: true });
		expect(list.finished.map((t) => [t.ref, t.state])).toEqual([["a2", "done"]]);
	});

	it("leaves a foreground task out, since only background work waits", () => {
		reset();
		const list = taskList(
			foldEntries([
				toolUse("b9", "Bash", { command: "ls" }),
				started("tb9", "b9", "local_bash", "List files", false),
			]),
		);
		expect(list.running).toEqual([]);
		expect(list.finished).toEqual([]);
	});

	it("lists a task whose call is unknown by its id and calls it other", () => {
		reset();
		const list = taskList(foldEntries([started("wf_9", "", "local_workflow", "nightly sweep")]));
		expect(list.running.map((t) => [t.ref, t.kind, t.title])).toEqual([["wf_9", "other", "nightly sweep"]]);
	});

	it("puts a task a dead process left behind in Finished as stopped, the newest call first", () => {
		const log = [...entries(), at("host", { kind: "exit", code: 0 })];
		const list = taskList(foldEntries(log));
		expect(list.running).toEqual([]);
		expect(list.finished.map((t) => [t.ref, t.state])).toEqual([
			["a2", "done"],
			["b1", "stopped"],
			["a1", "stopped"],
		]);
	});

	it("counts the tools a subagent ran, from its own lines or its progress report", () => {
		reset();
		const list = taskList(
			foldEntries([
				toolUse("a1", "Agent", { description: "d" }),
				started("ta1", "a1", "local_agent", "d"),
				toolUse("s1", "Grep", {}, "a1"),
				toolUse("s2", "Read", {}, "a1"),
			]),
		);
		expect(list.running[0].toolUses).toBe(2);
	});

	it("is the one list behind the Waiting row, so the two never disagree", () => {
		const log = [
			...entries().slice(0, 4),
			at("out", { type: "result", subtype: "success", num_turns: 1, total_cost_usd: 0 }),
		];
		const tr = foldEntries(log);
		const waiting = chatRows(tr, emptyOverlay).find((r) => r.kind === "waiting");
		expect(waiting?.kind === "waiting" && waiting.tasks.map((t) => t.task.id)).toEqual(taskList(tr).running.map((t) => t.task.id));
	});
});

describe("taskTarget", () => {
	it("finds a listed task by the tool call or by the task id", () => {
		reset();
		const tr = foldEntries([toolUse("a1", "Agent", { description: "d" }), started("ta1", "a1", "local_agent", "d")]);
		expect(taskTarget(tr, "a1")?.task?.id).toBe("ta1");
		expect(taskTarget(tr, "ta1")?.ref).toBe("a1");
		expect(taskTarget(tr, "nope")).toBeUndefined();
	});

	it("finds an Agent call no task event names, running until it has a result", () => {
		reset();
		const call = toolUse("a1", "Agent", { description: "Quick look", prompt: "p" });
		expect(taskTarget(foldEntries([call]), "a1")).toMatchObject({ kind: "agent", title: "Quick look", running: true });
		expect(taskTarget(foldEntries([call, toolResult("a1", "done")]), "a1")).toMatchObject({ running: false });
	});
});

describe("subagentTranscript on the recorded long session", () => {
	const tr = foldEntries(loadFixture("long_session.jsonl"));
	const AGENT = "toolu_01VtE9LeMfgpamXthC5tr7da";

	it("keeps only the lines whose parent is the Agent call, at the top level", () => {
		const sub = subagentTranscript(tr, AGENT);
		expect(sub.items.map((i) => i.kind)).toEqual(["tool_call", "tool_call", "assistant_text"]);
		expect(sub.items.every((i) => i.kind === "turn_end" || ("parentToolUseId" in i && i.parentToolUseId === ""))).toBe(true);
		const calls = sub.items.flatMap((i) => (i.kind === "tool_call" ? [i] : []));
		expect(calls.map((c) => c.name)).toEqual(["Bash", "Bash"]);
		expect(calls[0].result?.isError).toBe(false);
		expect(JSON.stringify(sub.items.at(-1))).toContain("docs/");
	});

	it("leaves the main conversation's own lines out", () => {
		const sub = subagentTranscript(tr, AGENT);
		expect(sub.items.some((i) => i.kind === "user" || i.kind === "turn_end")).toBe(false);
		expect(sub.items.some((i) => i.kind === "tool_call" && i.name === "Agent")).toBe(false);
	});

	it("renders as the same rows the main chat draws: one process group, then the reply", () => {
		const rows = chatRows(subagentTranscript(tr, AGENT), emptyOverlay);
		expect(rows.map((r) => r.kind)).toEqual(["group", "item"]);
		const group = rows[0];
		expect(group.kind === "group" && group.members.map((m) => m.kind)).toEqual(["tool", "tool"]);
	});

	it("is empty for a call with no lines of its own", () => {
		expect(subagentTranscript(tr, "toolu_none").items).toEqual([]);
	});

	it("keeps a nested agent's lines under their own parent, so the nested row can count them and drill in", () => {
		reset();
		const nested = foldEntries([
			toolUse("outer", "Agent", { description: "outer" }),
			toolUse("inner", "Agent", { description: "inner" }, "outer"),
			started("tin", "inner", "local_agent", "inner"),
			toolUse("leaf", "Read", {}, "inner"),
			toolResult("inner", "done", "outer"),
		]);
		const outer = subagentTranscript(nested, "outer");
		expect(outer.items.map((i) => (i.kind === "tool_call" ? [i.id, i.parentToolUseId] : i.kind))).toEqual([
			["inner", ""],
			["leaf", "inner"],
		]);
		expect(outer.tasks.map((t) => t.id)).toEqual(["tin"]);
		const rows = chatRows(outer, emptyOverlay);
		expect(rows).toHaveLength(1);
		expect(rows[0].kind === "item" && rows[0].subagentTools).toBe(1);
		expect(subagentTranscript(nested, "inner").items.map((i) => i.kind === "tool_call" && i.id)).toEqual(["leaf"]);
	});

	it("names the model the subagent's messages name", () => {
		reset();
		const withModel = foldEntries([
			toolUse("a1", "Agent", { description: "d" }),
			at("out", {
				type: "assistant",
				parent_tool_use_id: "a1",
				message: { id: "m", model: "claude-sonnet-5-5", content: [{ type: "text", text: "hi" }] },
			}),
		]);
		expect(subagentTranscript(withModel, "a1").model).toBe("claude-sonnet-5-5");
		expect(subagentTranscript(withModel, "other").model).toBe("");
	});
});
