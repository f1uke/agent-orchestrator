import { isObject, unfinished } from "./chat-event";
import type { BackgroundTask, ChatItem, Pending, ToolCallItem, Transcript } from "./chat-fold";
import type { StreamingBlock, StreamOverlay } from "./chat-stream";
import { type LiveTool, type ProcessSummary, liveVerb, summarizeTools, toolRunning } from "./chat-tools";

/** A step inside a process group: a tool call, or the thinking between tool calls. */
export type ProcessMember =
	| { kind: "tool"; itemIndex: number; call: ToolCallItem; subagentTools: number; task?: BackgroundTask }
	| { kind: "thinking"; itemIndex: number; text: string };

export type WaitingTask = { task: BackgroundTask; command: string; since: string };

export type ChatRow =
	| {
			key: string;
			kind: "item";
			item: ChatItem;
			subagentTools: number;
			task?: BackgroundTask;
			reply?: string;
	  }
	| { key: string; kind: "pending"; pending: Pending }
	| { key: string; kind: "streaming"; block: StreamingBlock; caret: boolean }
	| { key: string; kind: "group"; members: ProcessMember[]; summary: ProcessSummary }
	| { key: string; kind: "waiting"; tasks: WaitingTask[] }
	/** Claude thinks with nothing else running. `since` is absent when only streamed thinking says so. */
	| { key: string; kind: "thinking_live"; since?: string; text: string }
	| {
			key: string;
			kind: "turn";
			rows: ChatRow[];
			summary: ProcessSummary;
			messages: number;
			durationMs?: number;
	  };

export const itemRowKey = (itemIndex: number) => `i${itemIndex}`;

// What an item does to the run of tool calls being collected: joins it, ends it
// and shows as its own row, or is not shown in the main column at all.
type Role = "process" | "break" | "skip";

const OWN_ROW_TOOLS = new Set(["AskUserQuestion", "ExitPlanMode"]);

function role(item: ChatItem, queued: ReadonlySet<string>): Role {
	switch (item.kind) {
		case "user":
			return queued.has(item.uuid) ? "skip" : "break";
		case "assistant_text":
			return item.parentToolUseId !== "" || item.text.trim() === "" ? "skip" : "break";
		case "thinking":
			return item.parentToolUseId !== "" || item.text.trim() === "" ? "skip" : "process";
		case "tool_call":
			if (item.parentToolUseId !== "") return "skip";
			return OWN_ROW_TOOLS.has(item.name) || item.permission?.status === "pending" ? "break" : "process";
		case "turn_end":
		case "task_message":
		case "notice":
			return "break";
	}
}

const opensTurn = (row: ChatRow) =>
	row.kind === "item" && (row.item.kind === "user" || (row.item.kind === "task_message" && row.item.opensTurn));

function liveTool(calls: readonly ToolCallItem[]): LiveTool | undefined {
	const running = calls.filter(toolRunning).at(-1);
	return running && { verb: liveVerb(running), since: running.startedTs };
}

function memberRow(member: ProcessMember): ChatRow {
	const key = itemRowKey(member.itemIndex);
	if (member.kind === "thinking") {
		return { key, kind: "item", item: { kind: "thinking", text: member.text, parentToolUseId: "" }, subagentTools: 0 };
	}
	return { key, kind: "item", item: member.call, subagentTools: member.subagentTools, task: member.task };
}

const isItem = <K extends ChatItem["kind"]>(row: ChatRow, kind: K): row is Extract<ChatRow, { kind: "item" }> =>
	row.kind === "item" && row.item.kind === kind;

function callsOf(rows: readonly ChatRow[]): ToolCallItem[] {
	return rows.flatMap((row) => {
		if (row.kind === "group") return row.members.flatMap((m) => (m.kind === "tool" ? [m.call] : []));
		return isItem(row, "tool_call") ? [row.item as ToolCallItem] : [];
	});
}

function tasksOf(rows: readonly ChatRow[]): Map<string, BackgroundTask> {
	const tasks = new Map<string, BackgroundTask>();
	for (const row of rows) {
		const linked = row.kind === "group" ? row.members : [row];
		for (const r of linked) {
			if (r.kind === "tool" && r.task) tasks.set(r.call.id, r.task);
			if (r.kind === "item" && r.item.kind === "tool_call" && r.task) tasks.set(r.item.id, r.task);
		}
	}
	return tasks;
}

function turnRow(userKey: string, body: ChatRow[], durationMs: number | undefined): ChatRow | undefined {
	const only = body.length === 1 && body[0].kind === "group" ? body[0] : undefined;
	const rows = only ? only.members.map(memberRow) : body;
	const tasks = tasksOf(rows);
	const summary = summarizeTools(callsOf(rows), (id) => tasks.get(id));
	if (summary.counts.length === 0) return undefined;
	const messages = rows.filter((row) => isItem(row, "assistant_text")).length;
	return { key: `t${userKey}`, kind: "turn", rows, summary, messages, durationMs };
}

// A finished turn keeps its opening message and its last reply visible. What
// the agent did in between folds into one row. A turn that is still open,
// failed or interrupted stays open.
function foldTurns(rows: ChatRow[]): ChatRow[] {
	const out: ChatRow[] = [];
	for (let i = 0; i < rows.length; i++) {
		const row = rows[i];
		out.push(row);
		if (!opensTurn(row)) continue;

		let end = -1;
		let interrupted = false;
		for (let j = i + 1; j < rows.length && !opensTurn(rows[j]); j++) {
			const next = rows[j];
			if (isItem(next, "notice") && next.item.kind === "notice" && next.item.notice === "interrupted")
				interrupted = true;
			if (isItem(next, "turn_end")) {
				end = j;
				break;
			}
		}
		const ended = end >= 0 ? rows[end] : undefined;
		if (ended?.kind !== "item" || ended.item.kind !== "turn_end" || interrupted || ended.item.isError) continue;

		let last = end;
		for (let j = end - 1; j > i; j--) {
			if (isItem(rows[j], "assistant_text")) {
				last = j;
				break;
			}
		}
		const folded = turnRow(row.key, rows.slice(i + 1, last), ended.item.durationMs);
		if (!folded) continue;
		out.push(folded);
		i = last - 1;
	}
	return out;
}

function waitingRow(transcript: Transcript, rows: readonly ChatRow[]): ChatRow | undefined {
	const last = rows.at(-1);
	if (transcript.busy || !last || !isItem(last, "turn_end")) return undefined;
	const running = transcript.tasks.filter((t) => unfinished(t.state) && t.background !== false);
	if (running.length === 0) return undefined;
	const calls = new Map(
		transcript.items.flatMap((item) => (item.kind === "tool_call" ? [[item.id, item] as const] : [])),
	);
	const tasks = running.map((task) => {
		const call = calls.get(task.toolUseId);
		const input = call && isObject(call.input) ? call.input : {};
		const command = typeof input.command === "string" ? input.command : "";
		return { task, command, since: task.startedTs || call?.startedTs || "" };
	});
	return { key: "waiting", kind: "waiting", tasks };
}

// The one live row while claude thinks: no tool runs, nothing waits on the
// user or on background work, and the newest signal is thinking. That signal
// is a thinking_tokens event during a running turn, or an open thinking
// stream, which only a running turn has.
function thinkingRow(transcript: Transcript, overlay: StreamOverlay, rows: readonly ChatRow[]): ChatRow | undefined {
	if (transcript.pending.length > 0 || rows.at(-1)?.kind === "waiting") return undefined;
	const running = transcript.items.some((i) => i.kind === "tool_call" && i.parentToolUseId === "" && toolRunning(i));
	if (running) return undefined;
	const last = overlay.blocks.at(-1);
	if (last?.kind === "text") return undefined;
	const text = last?.kind === "thinking" ? last.text : "";
	const since = transcript.busy ? transcript.thinkingSince : undefined;
	if (since === undefined && text.trim() === "") return undefined;
	const row: ChatRow = { key: "thinking-live", kind: "thinking_live", text };
	if (since !== undefined) row.since = since;
	return row;
}

export function chatRows(
	transcript: Transcript,
	overlay: StreamOverlay,
	queued: ReadonlySet<string> = new Set(),
): ChatRow[] {
	const subagentTools = new Map<string, number>();
	const toolIds = new Set<string>();
	for (const item of transcript.items) {
		if (item.kind !== "tool_call") continue;
		toolIds.add(item.id);
		if (item.parentToolUseId !== "") {
			subagentTools.set(item.parentToolUseId, (subagentTools.get(item.parentToolUseId) ?? 0) + 1);
		}
	}
	const taskByToolUse = new Map(transcript.tasks.filter((t) => t.toolUseId !== "").map((t) => [t.toolUseId, t]));
	const taskById = new Map(transcript.tasks.map((t) => [t.id, t]));
	const taskOf = (item: ChatItem) =>
		item.kind === "tool_call"
			? taskByToolUse.get(item.id)
			: item.kind === "task_message"
				? taskById.get(item.taskId)
				: undefined;

	let rows: ChatRow[] = [];
	let run: ProcessMember[] = [];
	let reply: string | undefined;
	const flush = (atTail: boolean) => {
		const calls = run.flatMap((m) => (m.kind === "tool" ? [m.call] : []));
		const live = atTail ? liveTool(calls) : undefined;
		if (calls.length >= 2 || live) {
			const summary = summarizeTools(calls, (id) => taskByToolUse.get(id));
			summary.live = live;
			rows.push({ key: `g${run[0].itemIndex}`, kind: "group", members: run, summary });
		} else {
			rows.push(...run.map(memberRow));
		}
		run = [];
	};

	transcript.items.forEach((item, index) => {
		switch (role(item, queued)) {
			case "skip":
				return;
			case "break": {
				flush(false);
				const row: ChatRow = {
					key: itemRowKey(index),
					kind: "item",
					item,
					subagentTools: item.kind === "tool_call" ? (subagentTools.get(item.id) ?? 0) : 0,
					task: taskOf(item),
				};
				if (item.kind === "user" || (item.kind === "task_message" && item.opensTurn)) reply = undefined;
				else if (item.kind === "assistant_text") reply = item.text;
				else if (item.kind === "turn_end") {
					if (reply !== undefined) row.reply = reply;
					reply = undefined;
				}
				rows.push(row);
				return;
			}
			case "process": {
				if (item.kind === "thinking") {
					const prev = run.at(-1);
					if (prev?.kind === "thinking") prev.text += `\n\n${item.text}`;
					else run.push({ kind: "thinking", itemIndex: index, text: item.text });
				} else if (item.kind === "tool_call") {
					run.push({
						kind: "tool",
						itemIndex: index,
						call: item,
						subagentTools: subagentTools.get(item.id) ?? 0,
						task: taskByToolUse.get(item.id),
					});
				}
			}
		}
	});
	flush(true);
	rows = foldTurns(rows);
	const waiting = waitingRow(transcript, rows);
	if (waiting) rows.push(waiting);

	for (const pending of transcript.pending) {
		if (!toolIds.has(pending.toolUseId)) rows.push({ key: `p${pending.requestId}`, kind: "pending", pending });
	}
	const thinking = thinkingRow(transcript, overlay, rows);
	const streamed = thinking ? overlay.blocks.slice(0, -1) : overlay.blocks;
	let lastText = -1;
	streamed.forEach((block, i) => {
		if (block.kind === "text") lastText = i;
	});
	streamed.forEach((block, i) => {
		if (block.kind === "thinking" && block.text.trim() === "") return;
		rows.push({ key: `s${block.messageId}:${block.index}`, kind: "streaming", block, caret: i === lastText });
	});
	if (thinking) rows.push(thinking);
	return rows;
}
