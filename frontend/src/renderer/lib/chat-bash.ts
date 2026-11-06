import type { BashItem } from "./chat-fold";

/** The text typed after "!" when the box held nothing before and now starts with it, so typing or pasting enters bash mode. */
export function bashModeEntry(previous: string, next: string): string | null {
	return previous === "" && next.startsWith("!") ? next.slice(1) : null;
}

export type BashBlock = "turn" | "bash";

/** Why a command cannot run now, or null when it can. The host refuses the same two cases. */
export function bashBlock({ busy, bashBusy }: { busy: boolean; bashBusy: boolean }): BashBlock | null {
	if (bashBusy) return "bash";
	return busy ? "turn" : null;
}

export const BASH_HINT = "Runs in the worktree as you. Claude sees the output next message.";

export const BASH_BLOCK_TEXT: Record<BashBlock, string> = {
	turn: "Claude is working. Wait for the turn to end to run a command.",
	bash: "A command is still running or being passed to Claude. Wait for it to finish.",
};

/** Output lines shown before the row scrolls and offers to expand. */
export const BASH_VISIBLE_LINES = 14;

export function bashOutputLines(item: BashItem): number {
	let lines = 0;
	for (const chunk of item.chunks) for (const ch of chunk.text) if (ch === "\n") lines += 1;
	const last = item.chunks.at(-1)?.text;
	return last !== undefined && !last.endsWith("\n") ? lines + 1 : lines;
}

export type BashBadge = { label: string; tone: "running" | "ok" | "error" | "stopped" };

export function bashBadge(item: BashItem): BashBadge {
	if (item.state === "running") return { label: "running", tone: "running" };
	if (item.state === "stopped") return { label: "stopped", tone: "stopped" };
	if (item.signal) return { label: item.signal, tone: "error" };
	const code = item.code ?? 0;
	return { label: `exit ${code}`, tone: code === 0 ? "ok" : "error" };
}
