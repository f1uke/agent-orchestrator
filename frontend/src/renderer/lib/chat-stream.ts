// With partial messages on, claude emits one `assistant` event per content
// block, in block order, all under the message id. So once k blocks of a
// message have been logged, the blocks at index < k are settled.

import { type ChatEntry, isObject, parseStreamDelta } from "./chat-event";

export type StreamingBlock = {
	messageId: string;
	index: number;
	kind: "text" | "thinking";
	text: string;
};

/** A tool call whose input is still streaming. The log only gets the tool_use once its input is complete. */
export type StreamingTool = {
	messageId: string;
	index: number;
	name: string;
	/** Characters of input received so far. */
	chars: number;
	/** The first characters of the input, enough to read a leading file_path. */
	head: string;
};

const HEAD_CHARS = 512;

export type StreamOverlay = {
	blocks: readonly StreamingBlock[];
	tool?: StreamingTool;
	current: string;
	logged: Readonly<Record<string, number>>;
};

export const emptyOverlay: StreamOverlay = { blocks: [], current: "", logged: {} };

export function applyDelta(overlay: StreamOverlay, raw: unknown): StreamOverlay {
	const delta = parseStreamDelta(raw);
	if (!delta || delta.parentToolUseId !== "") return overlay;
	if (delta.messageStart !== undefined) return { ...overlay, current: delta.messageStart };
	const messageId = delta.messageId ?? overlay.current;
	if (delta.index < (overlay.logged[messageId] ?? 0)) return overlay;
	if (delta.blockStart?.type === "tool_use") {
		const tool = { messageId, index: delta.index, name: delta.blockStart.name, chars: 0, head: "" };
		return { ...overlay, tool };
	}
	if (delta.kind === "input_json") {
		const { tool } = overlay;
		if (!tool || tool.messageId !== messageId || tool.index !== delta.index) return overlay;
		const head = tool.head.length < HEAD_CHARS ? (tool.head + delta.text).slice(0, HEAD_CHARS) : tool.head;
		return { ...overlay, tool: { ...tool, chars: tool.chars + delta.text.length, head } };
	}
	if (delta.kind !== "text" && delta.kind !== "thinking") return overlay;
	const at = overlay.blocks.findIndex((b) => b.messageId === messageId && b.index === delta.index);
	const blocks = [...overlay.blocks];
	if (at < 0) blocks.push({ messageId, index: delta.index, kind: delta.kind, text: delta.text });
	else blocks[at] = { ...blocks[at], text: blocks[at].text + delta.text };
	return { ...overlay, blocks };
}

export function settleOverlay(overlay: StreamOverlay, entry: ChatEntry): StreamOverlay {
	if (!isObject(entry.payload)) return overlay;
	const payload = entry.payload;
	if (entry.dir === "host") return payload.kind === "spawn" || payload.kind === "exit" ? emptyOverlay : overlay;
	if (entry.dir !== "out") return overlay;
	if (payload.type === "result") return emptyOverlay;
	if (payload.type !== "assistant" || !isObject(payload.message) || payload.parent_tool_use_id) return overlay;
	const messageId = typeof payload.message.id === "string" ? payload.message.id : "";
	const content = payload.message.content;
	const count = Array.isArray(content) ? content.length : 1;
	const logged = { ...overlay.logged, [messageId]: (overlay.logged[messageId] ?? 0) + count };
	const blocks = overlay.blocks.filter((b) => b.messageId !== messageId || b.index >= logged[messageId]);
	const tool = overlay.tool && overlay.tool.messageId === messageId && overlay.tool.index < logged[messageId] ? undefined : overlay.tool;
	return { ...overlay, blocks, tool, logged };
}
