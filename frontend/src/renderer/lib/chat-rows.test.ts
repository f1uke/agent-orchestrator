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
import { type ChatRow, chatRows, rowIndexOfItem } from "./chat-rows";
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

	it("finds a folded item's row", () => {
		const rows = rowsOf(finished());
		expect(rowIndexOfItem(rows, 3)).toBe(1);
		expect(rowIndexOfItem(rows, 0)).toBe(0);
		expect(rowIndexOfItem(rows, 99)).toBe(-1);
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

describe("chatRows chapters and wrap-up", () => {
	const chapter = (title: string, result: "ok" | "error" | "none" = "ok") =>
		tool("mcp__ao__mark_chapter", { title }, result);
	const wrapUp = (recap = "Done.", result: "ok" | "error" | "none" = "ok") =>
		tool("mcp__ao__wrap_up", { recap }, result);
	const titleOf = (row: ChatRow) => (row.kind === "item" && row.item.kind === "tool_call" ? row.item.input : undefined);

	it("gives a chapter its own row instead of a place in a process group", () => {
		const rows = rowsOf([user(), say("a"), tool("Read"), chapter("Build"), tool("Bash"), say("b")]);
		expect(shape(rows)).toEqual(["user", "assistant_text", "tool_call", "tool_call", "tool_call", "assistant_text"]);
	});

	it("keeps each chapter between the folds of the work around it", () => {
		const rows = rowsOf([
			user(),
			chapter("Explore"),
			tool("Read"),
			tool("Grep"),
			chapter("Build"),
			tool("Edit"),
			tool("Bash"),
			say("done"),
			turnEnd(),
		]);
		expect(shape(rows)).toEqual(["user", "tool_call", "turn", "tool_call", "turn", "assistant_text", "turn_end"]);
		expect([rows[1], rows[3]].map(titleOf)).toEqual([{ title: "Explore" }, { title: "Build" }]);
	});

	it("keeps a wrap-up in view after its turn folds, ahead of the last reply", () => {
		const rows = rowsOf([user(), tool("Read"), tool("Edit"), wrapUp("Tests pass."), say("closing"), turnEnd()]);
		expect(shape(rows)).toEqual(["user", "turn", "tool_call", "assistant_text", "turn_end"]);
		expect(titleOf(rows[2])).toEqual({ recap: "Tests pass." });
	});

	it("folds a chapter or wrap-up call that failed, since there is nothing to show", () => {
		const rows = rowsOf([user(), tool("Read"), chapter("Bad", "error"), wrapUp("x", "error"), say("sorry"), turnEnd()]);
		expect(shape(rows)).toEqual(["user", "turn", "assistant_text", "turn_end"]);
	});
});

describe("chatRows task list", () => {
	const create = (subject: string, id: number) => ({
		...tool("TaskCreate", { subject, description: subject }),
		result: { isError: false, content: `Task #${id} created successfully: ${subject}` },
	});
	const update = (taskId: string, status: string) => tool("TaskUpdate", { taskId, status });
	const tasksRow = (rows: ChatRow[]) => rows.find((r) => r.kind === "tasks");
	const listed = (rows: ChatRow[]) => {
		const row = tasksRow(rows);
		return row?.kind === "tasks" ? row.todos.map((t) => [t.content, t.status]) : undefined;
	};

	it("shows the live list as the last row of the turn, after the work", () => {
		const rows = rowsOf([user(), create("One", 1), create("Two", 2), update("1", "completed"), say("working on two")]);
		expect(listed(rows)).toEqual([
			["One", "completed"],
			["Two", "pending"],
		]);
		expect(rows.at(-1)?.kind).toBe("tasks");
	});

	it("also follows a finished turn that folded", () => {
		const rows = rowsOf([user(), create("One", 1), update("1", "completed"), tool("Read"), say("done"), turnEnd()]);
		expect(listed(rows)).toEqual([["One", "completed"]]);
		expect(rows.at(-1)?.kind).toBe("tasks");
	});

	it("moves off a finished turn once the user writes again, and returns when the next turn touches it", () => {
		const first = [user(), create("One", 1), say("ok"), turnEnd()];
		expect(tasksRow(rowsOf([...first, user("u2", "next")]))).toBeUndefined();
		expect(listed(rowsOf([...first, user("u2", "next"), update("1", "completed")]))).toEqual([["One", "completed"]]);
	});

	it("is absent when no todo tool ran, or the list is empty", () => {
		expect(tasksRow(rowsOf([user(), say("hi"), tool("Read")]))).toBeUndefined();
		expect(tasksRow(rowsOf([user(), tool("TodoWrite", { todos: [] })]))).toBeUndefined();
	});

	it("sits after the waiting row and before a streaming row", () => {
		const rows = chatRows(
			transcript([user(), create("One", 1), say("hi")]),
			{ ...emptyOverlay, blocks: [{ messageId: "s", index: 0, kind: "text", text: "typing" }] },
			new Set(),
		);
		expect(rows.map((r) => r.kind).slice(-2)).toEqual(["tasks", "streaming"]);
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

describe("chatRows suggestions", () => {
	const session = (result: "ok" | "error" | "none" = "ok") =>
		tool("mcp__ao__suggest_session", { title: "Fix it", prompt: "Do it." }, result);
	const skill = (result: "ok" | "error" | "none" = "ok") =>
		tool("mcp__ao__suggest_skill", { name: "verify", reason: "r" }, result);

	it("gives each card its own row instead of a place in a process group", () => {
		const rows = rowsOf([user(), say("a"), tool("Read"), session(), tool("Bash"), skill(), say("b")]);
		expect(shape(rows)).toEqual([
			"user",
			"assistant_text",
			"tool_call",
			"tool_call",
			"tool_call",
			"tool_call",
			"assistant_text",
		]);
	});

	it("keeps both cards in view after their turn folds", () => {
		const rows = rowsOf([user(), tool("Read"), tool("Edit"), session(), skill(), say("closing"), turnEnd()]);
		expect(shape(rows)).toEqual(["user", "turn", "tool_call", "tool_call", "assistant_text", "turn_end"]);
	});

	it("folds a card that failed, since there is nothing to show", () => {
		const rows = rowsOf([
			user(),
			tool("Read"),
			tool("Edit"),
			session("error"),
			skill("error"),
			say("sorry"),
			turnEnd(),
		]);
		expect(shape(rows)).toEqual(["user", "turn", "assistant_text", "turn_end"]);
	});
});
