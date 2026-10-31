import { isObject } from "./chat-event";
import type { ToolCallItem } from "./chat-fold";
import type { ChatRow } from "./chat-rows";
import { drewNothing } from "./chat-widget";

export const SUGGEST_SESSION = "mcp__ao__suggest_session";
export const SUGGEST_SKILL = "mcp__ao__suggest_skill";

export type SessionSuggestion = { title: string; prompt: string };
export type SkillSuggestion = { name: string; reason: string };

const MAX_TITLE_CHARS = 60;
const MAX_PROMPT_CHARS = 4000;
const MAX_REASON_CHARS = 200;
const SKILL_NAME = /^[A-Za-z0-9:_-]{1,100}$/;

const text = (value: unknown) => (typeof value === "string" ? value.trim() : "");
const cut = (value: string, max: number) => [...value].slice(0, max).join("").trim();

export function sessionSuggestionOf(call: ToolCallItem): SessionSuggestion | undefined {
	if (call.name !== SUGGEST_SESSION || drewNothing(call)) return undefined;
	const input = isObject(call.input) ? call.input : {};
	const title = cut(text(input.title), MAX_TITLE_CHARS);
	const prompt = cut(text(input.prompt), MAX_PROMPT_CHARS);
	return title === "" || prompt === "" ? undefined : { title, prompt };
}

export function skillSuggestionOf(call: ToolCallItem): SkillSuggestion | undefined {
	if (call.name !== SUGGEST_SKILL || drewNothing(call)) return undefined;
	const input = isObject(call.input) ? call.input : {};
	const name = text(input.name).replace(/^\//, "");
	if (!SKILL_NAME.test(name)) return undefined;
	return { name, reason: cut(text(input.reason), MAX_REASON_CHARS) };
}

export const isSuggestion = (call: ToolCallItem) =>
	sessionSuggestionOf(call) !== undefined || skillSuggestionOf(call) !== undefined;

/** The newest card of each kind is the only one whose buttons act. */
export type LiveSuggestions = { session?: string; skill?: string };

export function liveSuggestions(rows: readonly ChatRow[]): LiveSuggestions {
	const live: LiveSuggestions = {};
	for (const row of rows) {
		if (row.kind !== "item" || row.item.kind !== "tool_call") continue;
		if (sessionSuggestionOf(row.item)) live.session = row.item.id;
		if (skillSuggestionOf(row.item)) live.skill = row.item.id;
	}
	return live;
}
