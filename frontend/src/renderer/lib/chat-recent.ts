import type { ChatEntry } from "./chat-event";
import type { ChatFolder } from "./chat-fold";

export type RecentChat = { entries: ChatEntry[]; folder: ChatFolder; bytes: number };

/**
 * The logs of the chat sessions opened last, so switching back to one shows it
 * at once and asks the daemon only for what it logged since. Bounded by count
 * and by the summed size of the entries as they came off the socket; the
 * session put longest ago is dropped first. A session is taken out while it is
 * open, so one log has one owner.
 */
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

	take(sessionId: string): RecentChat | undefined {
		const held = this.held.get(sessionId);
		this.held.delete(sessionId);
		return held;
	}

	clear(): void {
		this.held.clear();
	}
}
