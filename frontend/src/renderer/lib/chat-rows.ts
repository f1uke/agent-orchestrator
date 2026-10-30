import type { BackgroundTask, ChatItem, Pending, ToolCallItem, Transcript } from "./chat-fold";
import type { StreamingBlock, StreamOverlay } from "./chat-stream";
import { type ProcessSummary, describeTool, summarizeTools } from "./chat-tools";

/** A step inside a process group: a tool call, or the thinking between tool calls. */
export type ProcessMember =
	| { kind: "tool"; itemIndex: number; call: ToolCallItem; subagentTools: number; task?: BackgroundTask }
	| { kind: "thinking"; itemIndex: number; text: string };

export type ChatRow =
	| { key: string; kind: "item"; item: ChatItem; subagentTools: number; task?: BackgroundTask }
	| { key: string; kind: "pending"; pending: Pending }
	| { key: string; kind: "streaming"; block: StreamingBlock; caret: boolean }
	| { key: string; kind: "group"; members: ProcessMember[]; summary: ProcessSummary }
	| { key: string; kind: "turn"; rows: ChatRow[]; summary: ProcessSummary; messages: number };

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
		case "notice":
			return "break";
	}
}

function liveDetail(calls: readonly ToolCallItem[], root: string): string | undefined {
	const running = calls.filter((call) => !call.result).at(-1);
	if (!running) return undefined;
	const view = describeTool(running, { root });
	return view.arg || view.title;
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

function turnRow(userKey: string, rows: ChatRow[]): ChatRow | undefined {
	const summary = summarizeTools(callsOf(rows));
	if (summary.counts.length === 0) return undefined;
	const messages = rows.filter((row) => isItem(row, "assistant_text")).length;
	return { key: `t${userKey}`, kind: "turn", rows, summary, messages };
}

// A finished turn keeps its opening message and its last reply visible. What
// the agent did in between folds into one row. A turn that is still open,
// failed or interrupted stays open.
function foldTurns(rows: ChatRow[]): ChatRow[] {
	const out: ChatRow[] = [];
	for (let i = 0; i < rows.length; i++) {
		const row = rows[i];
		out.push(row);
		if (!isItem(row, "user")) continue;

		let end = -1;
		let interrupted = false;
		for (let j = i + 1; j < rows.length && !isItem(rows[j], "user"); j++) {
			const next = rows[j];
			if (isItem(next, "notice") && next.item.kind === "notice" && next.item.notice === "interrupted")
				interrupted = true;
			if (isItem(next, "turn_end")) {
				end = j;
				break;
			}
		}
		const ended = end >= 0 ? rows[end] : undefined;
		if (!ended || interrupted || (ended.kind === "item" && ended.item.kind === "turn_end" && ended.item.isError))
			continue;

		let last = end;
		for (let j = end - 1; j > i; j--) {
			if (isItem(rows[j], "assistant_text")) {
				last = j;
				break;
			}
		}
		const folded = turnRow(row.key, rows.slice(i + 1, last));
		if (!folded) continue;
		out.push(folded);
		i = last - 1;
	}
	return out;
}

export function chatRows(
	transcript: Transcript,
	overlay: StreamOverlay,
	queued: ReadonlySet<string> = new Set(),
	root = "",
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

	let rows: ChatRow[] = [];
	let run: ProcessMember[] = [];
	const flush = (atTail: boolean) => {
		const calls = run.flatMap((m) => (m.kind === "tool" ? [m.call] : []));
		if (calls.length >= 2) {
			const summary = summarizeTools(calls);
			if (atTail) summary.live = liveDetail(calls, root);
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
			case "break":
				flush(false);
				rows.push({
					key: itemRowKey(index),
					kind: "item",
					item,
					subagentTools: item.kind === "tool_call" ? (subagentTools.get(item.id) ?? 0) : 0,
					task: item.kind === "tool_call" ? taskByToolUse.get(item.id) : undefined,
				});
				return;
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

	for (const pending of transcript.pending) {
		if (!toolIds.has(pending.toolUseId)) rows.push({ key: `p${pending.requestId}`, kind: "pending", pending });
	}
	let lastText = -1;
	overlay.blocks.forEach((block, i) => {
		if (block.kind === "text") lastText = i;
	});
	overlay.blocks.forEach((block, i) => {
		if (block.kind === "thinking" && block.text.trim() === "") return;
		rows.push({ key: `s${block.messageId}:${block.index}`, kind: "streaming", block, caret: i === lastText });
	});
	return rows;
}

function holdsItem(row: ChatRow, itemIndex: number): boolean {
	switch (row.kind) {
		case "item":
			return row.key === itemRowKey(itemIndex);
		case "group":
			return row.members.some((m) => m.itemIndex === itemIndex);
		case "turn":
			return row.rows.some((inner) => holdsItem(inner, itemIndex));
		default:
			return false;
	}
}

/** The top-level row that shows an item, whether it stands alone or sits in a group or a folded turn. */
export function rowIndexOfItem(rows: readonly ChatRow[], itemIndex: number): number {
	return rows.findIndex((row) => holdsItem(row, itemIndex));
}
