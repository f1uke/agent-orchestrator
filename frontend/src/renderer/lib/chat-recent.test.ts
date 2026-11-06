import { describe, expect, it } from "vitest";
import type { ChatEntry } from "./chat-event";
import { ChatFolder } from "./chat-fold";
import { RecentChats } from "./chat-recent";

const entry = (seq: number, text = "x"): ChatEntry => ({
	seq,
	ts: "",
	dir: "out",
	payload: { type: "assistant", message: { id: `m${seq}`, content: [{ type: "text", text }] } },
});

const log = (n: number, text = "x") => {
	const entries = Array.from({ length: n }, (_, i) => entry(i + 1, text));
	const folder = new ChatFolder();
	for (const e of entries) folder.apply(e);
	return { entries, folder };
};
const put = (recent: RecentChats, id: string, chat: ReturnType<typeof log>) =>
	recent.put(id, { ...chat, bytes: chat.entries.reduce((sum, e) => sum + JSON.stringify(e).length, 0) });

describe("RecentChats", () => {
	it("hands back the log it was given, once, folded up to its last seq", () => {
		const recent = new RecentChats({ sessions: 4, bytes: 1e9 });
		put(recent, "a", log(3));
		const got = recent.take("a");
		expect(got?.entries.map((e) => e.seq)).toEqual([1, 2, 3]);
		expect(got?.folder.lastSeq).toBe(3);
		expect(got?.folder.transcript().items).toHaveLength(3);
		expect(recent.take("a")).toBeUndefined();
	});

	it("keeps the most recently put sessions and drops the oldest past the count", () => {
		const recent = new RecentChats({ sessions: 2, bytes: 1e9 });
		put(recent, "a", log(1));
		put(recent, "b", log(1));
		put(recent, "c", log(1));
		expect(recent.take("a")).toBeUndefined();
		expect(recent.take("b")).toBeDefined();
		expect(recent.take("c")).toBeDefined();
	});

	it("drops the oldest sessions while their logs together pass the byte bound", () => {
		const big = "y".repeat(1000);
		const recent = new RecentChats({ sessions: 4, bytes: 2500 });
		put(recent, "a", log(1, big));
		put(recent, "b", log(1, big));
		put(recent, "c", log(1, big));
		expect(recent.take("a")).toBeUndefined();
		expect(recent.take("b")).toBeDefined();
		expect(recent.take("c")).toBeDefined();
	});

	it("does not keep a single log larger than the whole bound", () => {
		const recent = new RecentChats({ sessions: 4, bytes: 100 });
		put(recent, "a", log(1, "z".repeat(500)));
		expect(recent.take("a")).toBeUndefined();
	});

	it("putting a session again replaces its log and makes it the newest", () => {
		const recent = new RecentChats({ sessions: 2, bytes: 1e9 });
		put(recent, "a", log(1));
		put(recent, "b", log(1));
		put(recent, "a", log(2));
		put(recent, "c", log(1));
		expect(recent.take("b")).toBeUndefined();
		expect(recent.take("a")?.entries).toHaveLength(2);
	});
});
