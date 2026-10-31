import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { ChatEntry } from "./chat-event";
import {
	type ChatItem,
	type NoticeKind,
	type ToolCallItem,
	type Transcript,
	type UserItem,
	foldEntries,
} from "./chat-fold";
import { type ChatRow, chatRows } from "./chat-rows";
import { emptyOverlay } from "./chat-stream";

const user = (uuid = "u1", text = "go"): UserItem => ({
	kind: "user",
	uuid,
	text,
	images: [],
	state: "completed",
	ts: "",
});
const say = (text: string, parentToolUseId = ""): ChatItem => ({
	kind: "assistant_text",
	messageId: "m",
	text,
	parentToolUseId,
});
const think = (text: string, parentToolUseId = ""): ChatItem => ({ kind: "thinking", text, parentToolUseId });
const notice = (kind: NoticeKind): ChatItem => ({ kind: "notice", notice: kind, text: "", ts: "" });
const turnEnd = (isError = false): ChatItem => ({
	kind: "turn_end",
	subtype: isError ? "error_during_execution" : "success",
	isError,
	numTurns: 1,
	costUsd: 0,
	sessionId: "s",
	usage: { inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheCreationTokens: 0 },
	contextWindows: {},
	ts: "",
});

const taskMessage = (opensTurn = false, taskId = "k1"): ChatItem => ({
	kind: "task_message",
	ts: "",
	taskId,
	toolUseId: "",
	status: "completed",
	summary: "done",
	result: "",
	opensTurn,
});

let n = 0;
function tool(
	name: string,
	input: unknown = {},
	result: "ok" | "error" | "none" = "ok",
	parentToolUseId = "",
): ToolCallItem {
	n += 1;
	return {
		kind: "tool_call",
		id: `tu${n}`,
		name,
		parentToolUseId,
		input,
		startedTs: "",
		result: result === "none" ? undefined : { isError: result === "error", content: "" },
	};
}

function transcript(items: ChatItem[], extra: Partial<Transcript> = {}): Transcript {
	return {
		claudeSessionIds: [],
		items,
		pending: [],
		queued: [],
		busy: false,
		permissionMode: "default",
		costUsd: 0,
		commands: [],
		models: [],
		model: "",
		tasks: [],
		lastSeq: 0,
		...extra,
	};
}

const rowsOf = (items: ChatItem[], extra: Partial<Transcript> = {}) =>
	chatRows(transcript(items, extra), emptyOverlay, new Set());

const shape = (rows: ChatRow[]) => rows.map((r) => (r.kind === "item" ? r.item.kind : r.kind));

const group = (rows: ChatRow[], at = 0) => {
	const found = rows.filter((r): r is Extract<ChatRow, { kind: "group" }> => r.kind === "group");
	return found[at];
};

describe("chatRows process groups", () => {
	it("folds the tool calls between two replies into one group", () => {
		const rows = rowsOf([user(), say("looking"), tool("Read"), tool("Bash"), tool("Grep"), say("found it")]);
		expect(shape(rows)).toEqual(["user", "assistant_text", "group", "assistant_text"]);
		expect(group(rows).members).toHaveLength(3);
	});

	it("leaves a lone tool call as a plain row", () => {
		const rows = rowsOf([user(), say("hi"), tool("Read"), say("ok")]);
		expect(shape(rows)).toEqual(["user", "assistant_text", "tool_call", "assistant_text"]);
	});

	it("keeps a lone tool call and its thinking as plain rows", () => {
		const rows = rowsOf([user(), think("hmm"), tool("Read"), say("ok")]);
		expect(shape(rows)).toEqual(["user", "thinking", "tool_call", "assistant_text"]);
	});

	it("starts a new group after each reply", () => {
		const rows = rowsOf([user(), tool("Read"), tool("Read"), say("a"), tool("Bash"), tool("Bash")]);
		expect(shape(rows)).toEqual(["user", "group", "assistant_text", "group"]);
	});

	it("counts commands, files and todo updates in the summary", () => {
		const rows = rowsOf([
			user(),
			tool("Bash"),
			tool("Bash", {}, "error"),
			tool("Read", { file_path: "a.go" }),
			tool("Read", { file_path: "a.go" }),
			tool("TaskUpdate", { taskId: "1" }),
			tool("TaskUpdate", { taskId: "2" }),
		]);
		expect(group(rows).summary).toMatchObject({
			counts: [
				{ kind: "commands", count: 2 },
				{ kind: "reads", count: 1 },
				{ kind: "todos", count: 2 },
			],
			failed: 1,
		});
	});

	it("does not let an empty thinking block or an empty reply split a run", () => {
		const rows = rowsOf([user(), tool("Read"), think(""), say("  "), tool("Read")]);
		expect(shape(rows)).toEqual(["user", "group"]);
		expect(group(rows).members.map((m) => m.kind)).toEqual(["tool", "tool"]);
	});

	it("drops empty thinking from the rows altogether", () => {
		expect(shape(rowsOf([user(), think(""), say("hi")]))).toEqual(["user", "assistant_text"]);
	});

	it("merges adjacent thinking into one member and keeps it in the run", () => {
		const rows = rowsOf([user(), tool("Read"), think("first"), think("second"), tool("Read")]);
		const members = group(rows).members;
		expect(members.map((m) => m.kind)).toEqual(["tool", "thinking", "tool"]);
		expect(members[1]).toMatchObject({ kind: "thinking", text: "first\n\nsecond" });
	});

	it("breaks the run at a question, a plan and a tool waiting for permission", () => {
		const waiting: ToolCallItem = {
			...tool("Bash", {}, "none"),
			permission: { requestId: "r1", status: "pending", message: "", updatedPermissions: [] },
		};
		const rows = rowsOf([
			user(),
			tool("Read"),
			tool("Read"),
			tool("AskUserQuestion", {}, "none"),
			tool("Read"),
			tool("Read"),
			tool("ExitPlanMode", {}, "none"),
			tool("Read"),
			tool("Read"),
			waiting,
		]);
		expect(shape(rows)).toEqual(["user", "group", "tool_call", "group", "tool_call", "group", "tool_call"]);
	});

	it("keeps subagent tool calls, text and thinking out of the main column", () => {
		const agent = tool("Agent", { description: "d" });
		const rows = rowsOf([
			user(),
			agent,
			tool("Read"),
			tool("Read", {}, "ok", agent.id),
			tool("Bash", {}, "ok", agent.id),
			think("sub thought", agent.id),
			say("sub answer", agent.id),
			say("main answer"),
		]);
		expect(shape(rows)).toEqual(["user", "group", "assistant_text"]);
		expect(group(rows).members).toHaveLength(2);
		expect(rows.filter((r) => r.kind === "item" && r.item.kind === "assistant_text")).toHaveLength(1);
		expect(group(rows).members[0]).toMatchObject({ kind: "tool", subagentTools: 2 });
	});

	it("skips queued user messages", () => {
		const queued = { ...user("q1", "later"), state: "queued" };
		const rows = chatRows(transcript([user(), tool("Read"), queued, tool("Read")]), emptyOverlay, new Set(["q1"]));
		expect(shape(rows)).toEqual(["user", "group"]);
	});
});

describe("chatRows live detail", () => {
	it("names the newest tool that has no result in plain words, with when it started", () => {
		const running = { ...tool("Read", { file_path: "/work/project/docs/a.md" }, "none"), startedTs: "T1" };
		const rows = rowsOf([user(), tool("Bash", { command: "go test" }), running]);
		expect(group(rows).summary.live).toEqual({ verb: "Reading a.md", since: "T1" });
	});

	it("shows a lone running tool at the tail as a live group, and a lone finished one as its own row", () => {
		const live = rowsOf([user(), say("on it"), tool("Bash", { command: "go test" }, "none")]);
		expect(shape(live)).toEqual(["user", "assistant_text", "group"]);
		expect(group(live).summary.live?.verb).toBe("Running a command");
		const done = rowsOf([user(), say("on it"), tool("Bash", { command: "go test" })]);
		expect(shape(done)).toEqual(["user", "assistant_text", "tool_call"]);
	});

	it("has no live detail once every tool has a result", () => {
		expect(group(rowsOf([user(), tool("Read"), tool("Read")])).summary.live).toBeUndefined();
	});

	it("has no live detail once a turn end follows, even with a tool still unanswered", () => {
		const rows = rowsOf([user(), tool("Read"), tool("Read", {}, "none"), notice("exit")]);
		expect(group(rows).summary.live).toBeUndefined();
	});
});

describe("chatRows errored tools", () => {
	it("keeps an errored call in the group's members and counts it as failed", () => {
		const rows = rowsOf([user(), tool("Bash", {}, "error"), tool("Read")]);
		expect(group(rows).summary.failed).toBe(1);
		expect(group(rows).members[0]).toMatchObject({ kind: "tool" });
	});

	it("counts a background command whose task failed, though its tool result was fine", () => {
		const bash = tool("Bash", { command: "scripts/slow-check.sh", run_in_background: true });
		const task = { id: "t1", toolUseId: bash.id, description: "", taskType: "local_bash", state: "error" as const };
		const rows = rowsOf([user(), bash, tool("Read"), say("done"), turnEnd()], { tasks: [task] });
		const turn = rows.find((r) => r.kind === "turn");
		expect(turn?.kind === "turn" && turn.summary.failed).toBe(1);
	});
});

describe("chatRows background tasks", () => {
	it("hands a call its linked task", () => {
		const agent = tool("Agent", { description: "d" });
		const task = { id: "t1", toolUseId: agent.id, description: "", taskType: "local_agent", state: "running" as const };
		const rows = rowsOf([user(), agent], { tasks: [task] });
		const row = rows[1];
		expect(row.kind === "item" && row.task).toEqual(task);
	});
});

describe("chatRows turn fold", () => {
	const finished = (extra: ChatItem[] = []) => [
		user(),
		say("on it"),
		tool("Bash"),
		tool("Bash"),
		tool("Edit", { file_path: "a.go" }),
		say("all done"),
		...extra,
		turnEnd(),
	];

	it("folds everything between the opening user message and the last reply", () => {
		const rows = rowsOf(finished());
		expect(shape(rows)).toEqual(["user", "turn", "assistant_text", "turn_end"]);
		const turn = rows[1];
		expect(turn.kind === "turn" && turn.messages).toBe(1);
		expect(turn.kind === "turn" && turn.summary.counts).toEqual([
			{ kind: "commands", count: 2 },
			{ kind: "edits", count: 1 },
		]);
		expect(turn.kind === "turn" && shape(turn.rows)).toEqual(["assistant_text", "group"]);
	});

	it("carries the turn's duration onto its fold", () => {
		const items = finished();
		items[items.length - 1] = { ...(items.at(-1) as Extract<ChatItem, { kind: "turn_end" }>), durationMs: 36_000 };
		const turn = rowsOf(items)[1];
		expect(turn.kind === "turn" && turn.durationMs).toBe(36_000);
	});

	it("leaves the open turn unfolded", () => {
		const items = finished();
		items.pop();
		expect(shape(rowsOf(items))).toEqual(["user", "assistant_text", "group", "assistant_text"]);
	});

	it("leaves a failed turn unfolded", () => {
		const items = finished();
		items[items.length - 1] = turnEnd(true);
		expect(shape(rowsOf(items))).not.toContain("turn");
	});

	it("leaves an interrupted turn unfolded", () => {
		const rows = rowsOf(finished([notice("interrupted")]));
		expect(shape(rows)).not.toContain("turn");
	});

	it("folds each finished turn on its own and keeps the later open one", () => {
		const rows = rowsOf([...finished(), user("u2", "again"), tool("Read"), tool("Read")]);
		expect(shape(rows)).toEqual(["user", "turn", "assistant_text", "turn_end", "user", "group"]);
	});

	it("does not fold a turn that ran no tool", () => {
		const rows = rowsOf([user(), say("a"), say("b"), turnEnd()]);
		expect(shape(rows)).toEqual(["user", "assistant_text", "assistant_text", "turn_end"]);
	});

	it("hands each turn end the turn's last reply, for its answer bar", () => {
		const rows = rowsOf([...finished(), user("u2", "and?"), tool("Read"), turnEnd()]);
		const ends = rows.filter((r) => r.kind === "item" && r.item.kind === "turn_end");
		expect(ends.map((r) => r.kind === "item" && r.reply)).toEqual(["all done", undefined]);
	});
});

describe("chatRows widgets", () => {
	const widget = (result: "ok" | "error" | "none" = "ok", code = "<svg></svg>") =>
		tool("mcp__ao__show_widget", { title: "flow", widget_code: code }, result);

	it("keeps a drawn visual in view after its folded turn, ahead of the last reply", () => {
		const rows = rowsOf([
			user(),
			say("looking"),
			tool("Read"),
			tool("Bash"),
			widget(),
			tool("Grep"),
			say("here it is"),
			turnEnd(),
		]);
		expect(shape(rows)).toEqual(["user", "turn", "tool_call", "assistant_text", "turn_end"]);
		const turn = rows[1];
		expect(
			turn.kind === "turn" &&
				turn.rows.some((r) => r.kind === "item" && r.item.kind === "tool_call" && r.item.name.endsWith("show_widget")),
		).toBe(false);
		expect(turn.kind === "turn" && turn.summary.counts.map((c) => c.kind)).toEqual(["commands", "reads", "searches"]);
	});

	it("keeps each visual of a folded turn, in order", () => {
		const rows = rowsOf([
			user(),
			tool("Read"),
			tool("Read"),
			widget("ok", "<svg>1</svg>"),
			widget("ok", "<svg>2</svg>"),
			say("done"),
			turnEnd(),
		]);
		const drawn = rows.flatMap((r) => (r.kind === "item" && r.item.kind === "tool_call" ? [r.item.input] : []));
		expect(drawn).toEqual([
			{ title: "flow", widget_code: "<svg>1</svg>" },
			{ title: "flow", widget_code: "<svg>2</svg>" },
		]);
	});

	it("gives a visual its own row instead of a place in a process group", () => {
		const rows = rowsOf([user(), say("a"), tool("Read"), widget("none"), tool("Bash"), say("b")]);
		expect(shape(rows)).toEqual(["user", "assistant_text", "tool_call", "tool_call", "tool_call", "assistant_text"]);
	});

	it("folds a call that failed with the rest of the turn, since there is no visual to keep", () => {
		const rows = rowsOf([user(), tool("Read"), tool("Bash"), widget("error"), say("sorry"), turnEnd()]);
		expect(shape(rows)).toEqual(["user", "turn", "assistant_text", "turn_end"]);
	});
});

describe("chatRows task messages", () => {
	it("shows a task message as its own row that breaks the run of calls around it", () => {
		const rows = rowsOf([user(), tool("Bash"), tool("Bash"), taskMessage(), tool("Read"), tool("Read")]);
		expect(shape(rows)).toEqual(["user", "group", "task_message", "group"]);
	});

	it("hands a task message its task", () => {
		const task = {
			id: "k1",
			toolUseId: "",
			description: "Summarize docs",
			taskType: "local_agent",
			state: "done" as const,
		};
		const row = rowsOf([user(), taskMessage()], { tasks: [task] })[1];
		expect(row.kind === "item" && row.task).toEqual(task);
	});

	it("folds a turn that a task message opened, keeping the message visible", () => {
		const rows = rowsOf([
			user(),
			say("started it"),
			turnEnd(),
			taskMessage(true),
			tool("Bash"),
			tool("Bash"),
			say("it passed"),
			turnEnd(),
		]);
		expect(shape(rows)).toEqual([
			"user",
			"assistant_text",
			"turn_end",
			"task_message",
			"turn",
			"assistant_text",
			"turn_end",
		]);
	});
});

describe("chatRows waiting on background work", () => {
	const soak = { ...tool("Bash", { command: "go test -count=50 ./...", run_in_background: true }), id: "bg" };
	const running = {
		id: "k1",
		toolUseId: "bg",
		description: "Run the soak test",
		taskType: "local_bash",
		state: "running" as const,
		background: true,
		startedTs: "2026-10-30T10:00:05.000Z",
	};
	const ended = [user(), soak, say("started it"), turnEnd()];

	it("ends the rows with what the finished turn left running, its command and since when", () => {
		const rows = rowsOf(ended, { tasks: [running] });
		const last = rows.at(-1)!;
		expect(last.kind).toBe("waiting");
		expect(last.kind === "waiting" && last.tasks).toEqual([
			{ task: running, command: "go test -count=50 ./...", since: "2026-10-30T10:00:05.000Z" },
		]);
	});

	it("has no waiting row once the task ended, while the turn still runs, or for a foreground task", () => {
		const done = { ...running, state: "done" as const };
		expect(rowsOf(ended, { tasks: [done] }).at(-1)?.kind).not.toBe("waiting");
		expect(rowsOf(ended.slice(0, 3), { tasks: [running], busy: true }).at(-1)?.kind).not.toBe("waiting");
		expect(rowsOf(ended, { tasks: [{ ...running, background: false }] }).at(-1)?.kind).not.toBe("waiting");
	});
});

describe("chatRows on the recorded long session", () => {
	const dir = join(dirname(fileURLToPath(import.meta.url)), "../../../../backend/internal/chatproto/testdata");
	const entries = readFileSync(join(dir, "long_session.jsonl"), "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line) => JSON.parse(line) as ChatEntry);
	const tr = foldEntries(entries);
	const walk = (rows: ChatRow[]): ChatRow[] => rows.flatMap((r) => (r.kind === "turn" ? [r, ...walk(r.rows)] : [r]));

	it("shows far fewer rows than the log has items, with no empty thinking and no subagent text", () => {
		const rows = chatRows(tr, emptyOverlay, new Set());
		expect(rows.length).toBeLessThan(tr.items.length / 2);
		const all = walk(rows);
		const thoughts = all.flatMap((r) => (r.kind === "item" && r.item.kind === "thinking" ? [r.item] : []));
		expect(thoughts.every((t) => t.text.trim() !== "")).toBe(true);
		const texts = all.flatMap((r) => (r.kind === "item" && r.item.kind === "assistant_text" ? [r.item] : []));
		expect(texts.every((t) => t.parentToolUseId === "")).toBe(true);
		expect(rows.some((r) => r.kind === "turn")).toBe(true);
	});
});

describe("chatRows live thinking", () => {
	const SINCE = "2026-10-31T11:53:12.000Z";
	const afterTool = () => [user(), tool("Read"), tool("Grep")];
	const overlayOf = (...blocks: Array<{ kind: "text" | "thinking"; text: string }>) => ({
		...emptyOverlay,
		blocks: blocks.map((b, index) => ({ messageId: "m9", index, ...b })),
	});
	const live = (rows: ChatRow[]) => rows.filter((r) => r.kind === "thinking_live");

	it("ends the rows with one Thinking row when only thinking_tokens followed a tool result", () => {
		const rows = rowsOf(afterTool(), { busy: true, thinkingSince: SINCE });
		expect(rows.at(-1)).toEqual({ key: "thinking-live", kind: "thinking_live", since: SINCE, text: "" });
	});

	it("needs a real thinking signal and a running turn", () => {
		expect(live(rowsOf(afterTool(), { busy: true }))).toEqual([]);
		expect(live(rowsOf(afterTool(), { busy: false, thinkingSince: SINCE }))).toEqual([]);
	});

	it("never shows while a tool runs, a request waits, or claude streams text", () => {
		const running = [user(), tool("Read"), tool("Bash", { command: "go test ./..." }, "none")];
		expect(live(rowsOf(running, { busy: true, thinkingSince: SINCE }))).toEqual([]);
		const pending = {
			requestId: "r1",
			toolName: "Bash",
			toolUseId: "x",
			kind: "permission" as const,
			input: {},
			suggestions: [],
			defaultToNo: false,
		};
		expect(live(rowsOf(afterTool(), { busy: true, thinkingSince: SINCE, pending: [pending] }))).toEqual([]);
		const streamingText = chatRows(
			transcript(afterTool(), { busy: true, thinkingSince: SINCE }),
			overlayOf({ kind: "thinking", text: "hmm" }, { kind: "text", text: "Found it" }),
		);
		expect(live(streamingText)).toEqual([]);
	});

	it("never stacks with the Waiting on row", () => {
		const soak = { ...tool("Bash", { command: "soak", run_in_background: true }), id: "bg" };
		const task = {
			id: "k1",
			toolUseId: "bg",
			description: "soak",
			taskType: "local_bash",
			state: "running" as const,
			background: true,
			startedTs: SINCE,
		};
		const rows = rowsOf([user(), soak, say("started"), turnEnd()], { tasks: [task], thinkingSince: SINCE });
		expect(rows.filter((r) => r.kind === "waiting" || r.kind === "thinking_live").map((r) => r.kind)).toEqual([
			"waiting",
		]);
	});

	it("takes over a streaming thinking block, so its text shows in the one live row", () => {
		const rows = chatRows(
			transcript(afterTool(), { busy: true }),
			overlayOf({ kind: "thinking", text: "The counter is read twice.\n\nMaybe the reset races.\n" }),
		);
		expect(rows.filter((r) => r.kind === "streaming")).toEqual([]);
		expect(live(rows)).toEqual([
			{ key: "thinking-live", kind: "thinking_live", text: "The counter is read twice.\n\nMaybe the reset races.\n" },
		]);
	});
});
