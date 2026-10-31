import { isObject } from "./chat-event";
import type { ToolCallItem } from "./chat-fold";
import type { ChatRow } from "./chat-rows";
import { drewNothing } from "./chat-widget";

export const MARK_CHAPTER = "mcp__ao__mark_chapter";

export type Chapter = { key: string; title: string; summary: string };

const MAX_TITLE_CHARS = 60;
const START_TITLE = "Session start";

const text = (value: unknown) => (typeof value === "string" ? value.trim() : "");

export function chapterOf(call: ToolCallItem): { title: string; summary: string } | undefined {
	if (call.name !== MARK_CHAPTER || drewNothing(call)) return undefined;
	const input = isObject(call.input) ? call.input : {};
	const title = [...text(input.title)].slice(0, MAX_TITLE_CHARS).join("").trim();
	return title === "" ? undefined : { title, summary: text(input.summary) };
}

function chapterAt(row: ChatRow): Chapter[] {
	const chapter = row.kind === "item" && row.item.kind === "tool_call" ? chapterOf(row.item) : undefined;
	return chapter ? [{ key: row.key, ...chapter }] : [];
}

export function chapters(rows: readonly ChatRow[]): Chapter[] {
	const marked = rows.flatMap(chapterAt);
	const first = rows[0];
	if (marked.length === 0 || !first || marked[0].key === first.key) return marked;
	return [{ key: first.key, title: START_TITLE, summary: "" }, ...marked];
}
