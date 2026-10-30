import type { ChatRow } from "./chat-rows";

export type RailTurn = { turn: number; key: string; prompt: string; reply: string };

const PROMPT_CHARS = 50;
const REPLY_CHARS = 360;

function plain(markdown: string): string {
	return markdown
		.replace(/```[^\n]*\n?/g, "")
		.replace(/^\s*(#+|[-*+]|\d+\.)\s+/gm, "")
		.replace(/[*_`]/g, "")
		.replace(/\s+/g, " ")
		.trim();
}

function clip(text: string, chars: number): string {
	if (text.length <= chars) return text;
	const cut = text.slice(0, chars);
	const space = cut.lastIndexOf(" ");
	return `${(space > chars / 2 ? cut.slice(0, space) : cut).replace(/[\s,.;:]+$/, "")}…`;
}

function opening(row: ChatRow): string | undefined {
	if (row.kind !== "item") return undefined;
	if (row.item.kind === "user") return row.item.text.replace(/^\[from @[^\]]*\]\s*/, "");
	if (row.item.kind === "task_message" && row.item.opensTurn) return row.item.summary;
	return undefined;
}

export function railTurns(rows: readonly ChatRow[]): RailTurn[] {
	const turns: RailTurn[] = [];
	for (const row of rows) {
		const prompt = opening(row);
		if (prompt !== undefined) {
			turns.push({ turn: turns.length + 1, key: row.key, prompt: clip(plain(prompt), PROMPT_CHARS), reply: "" });
		} else if (row.kind === "item" && row.item.kind === "assistant_text" && turns.length > 0) {
			turns[turns.length - 1].reply = clip(plain(row.item.text), REPLY_CHARS);
		}
	}
	return turns;
}

export function activeTurnIndex(starts: readonly number[], probe: number): number {
	let active = 0;
	starts.forEach((start, i) => {
		if (start <= probe) active = i;
	});
	return active;
}
