import { isObject } from "./chat-event";
import type { ToolCallItem } from "./chat-fold";
import type { ChatRow } from "./chat-rows";
import { drewNothing } from "./chat-widget";

export const WRAP_UP = "mcp__ao__wrap_up";

export type WrapUp = { recap: string; ask?: string; replies: string[] };

const MAX_REPLIES = 2;
const MAX_REPLY_CHARS = 24;

const text = (value: unknown) => (typeof value === "string" ? value.trim() : "");

export function wrapUpOf(call: ToolCallItem): WrapUp | undefined {
	if (call.name !== WRAP_UP || drewNothing(call)) return undefined;
	const input = isObject(call.input) ? call.input : {};
	const recap = text(input.recap);
	if (recap === "") return undefined;
	const replies = (Array.isArray(input.replies) ? input.replies : [])
		.map((reply) => [...text(reply)].slice(0, MAX_REPLY_CHARS).join("").trim())
		.filter((reply) => reply !== "")
		.slice(0, MAX_REPLIES);
	const ask = text(input.ask);
	return ask === "" ? { recap, replies } : { recap, ask, replies };
}

export function latestWrapUpId(rows: readonly ChatRow[]): string | undefined {
	let id: string | undefined;
	for (const row of rows) {
		if (row.kind === "item" && row.item.kind === "tool_call" && wrapUpOf(row.item)) id = row.item.id;
	}
	return id;
}
