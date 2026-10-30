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

export type StreamOverlay = {
	blocks: readonly StreamingBlock[];
	current: string;
	logged: Readonly<Record<string, number>>;
};

export const emptyOverlay: StreamOverlay = { blocks: [], current: "", logged: {} };

export function applyDelta(overlay: StreamOverlay, raw: unknown): StreamOverlay {
	const delta = parseStreamDelta(raw);
	if (!delta || delta.parentToolUseId !== "") return overlay;
	if (delta.messageStart !== undefined) return { ...overlay, current: delta.messageStart };
	if (delta.kind !== "text" && delta.kind !== "thinking") return overlay;
	const messageId = delta.messageId ?? overlay.current;
	if (delta.index < (overlay.logged[messageId] ?? 0)) return overlay;
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
	return { ...overlay, blocks, logged };
}
