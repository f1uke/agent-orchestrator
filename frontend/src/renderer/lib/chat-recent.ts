import type { ChatEntry } from "./chat-event";
import type { ChatFolder } from "./chat-fold";

export type RecentChat = { entries: ChatEntry[]; folder: ChatFolder; bytes: number };

export class RecentChats {
	private held = new Map<string, RecentChat>();

	constructor(private readonly limits: { sessions: number; bytes: number }) {}

	put(sessionId: string, chat: RecentChat): void {
		this.held.delete(sessionId);
		if (chat.bytes > this.limits.bytes) return;
		this.held.set(sessionId, chat);
		let total = [...this.held.values()].reduce((sum, h) => sum + h.bytes, 0);
		for (const [id, h] of this.held) {
			if (this.held.size <= this.limits.sessions && total <= this.limits.bytes) break;
			this.held.delete(id);
			total -= h.bytes;
		}
	}

	// The open session's hook keeps adding to the log it took, so the cache
	// must not hand the same log to a second owner.
	take(sessionId: string): RecentChat | undefined {
		const held = this.held.get(sessionId);
		this.held.delete(sessionId);
		return held;
	}

	clear(): void {
		this.held.clear();
	}
}
