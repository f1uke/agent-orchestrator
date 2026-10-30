import type { ChatItem, Pending, Transcript } from "./chat-fold";
import type { StreamingBlock, StreamOverlay } from "./chat-stream";

export type ChatRow =
	| { key: string; kind: "item"; item: ChatItem; subagentTools: number }
	| { key: string; kind: "pending"; pending: Pending }
	| { key: string; kind: "streaming"; block: StreamingBlock; caret: boolean };

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

	const rows: ChatRow[] = [];
	transcript.items.forEach((item, index) => {
		if (item.kind === "tool_call" && item.parentToolUseId !== "") return;
		if (item.kind === "assistant_text" && item.text.trim() === "") return;
		if (item.kind === "user" && queued.has(item.uuid)) return;
		const count = item.kind === "tool_call" ? (subagentTools.get(item.id) ?? 0) : 0;
		rows.push({ key: `i${index}`, kind: "item", item, subagentTools: count });
	});
	for (const pending of transcript.pending) {
		if (!toolIds.has(pending.toolUseId)) rows.push({ key: `p${pending.requestId}`, kind: "pending", pending });
	}
	let lastText = -1;
	overlay.blocks.forEach((block, i) => {
		if (block.kind === "text") lastText = i;
	});
	overlay.blocks.forEach((block, i) => {
		rows.push({ key: `s${block.messageId}:${block.index}`, kind: "streaming", block, caret: i === lastText });
	});
	return rows;
}
