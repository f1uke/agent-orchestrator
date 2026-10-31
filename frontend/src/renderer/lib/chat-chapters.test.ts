import { describe, expect, it } from "vitest";
import { chapterOf, chapters, MARK_CHAPTER } from "./chat-chapters";
import type { ChatItem, ToolCallItem, Transcript, UserItem } from "./chat-fold";
import { chatRows } from "./chat-rows";
import { emptyOverlay } from "./chat-stream";

let n = 0;
const call = (name: string, input: unknown = {}, over: Partial<ToolCallItem> = {}): ToolCallItem => ({
	kind: "tool_call",
	id: `c${++n}`,
	name,
	parentToolUseId: "",
	input,
	startedTs: "",
	result: { isError: false, content: "" },
	...over,
});
const mark = (title: unknown, summary?: string, over: Partial<ToolCallItem> = {}) =>
	call(MARK_CHAPTER, { title, summary }, over);
const user = (uuid = "u"): UserItem => ({ kind: "user", uuid, text: "go", images: [], state: "completed", ts: "" });
const say = (text: string): ChatItem => ({ kind: "assistant_text", messageId: "m", text, parentToolUseId: "" });
const turnEnd = (): ChatItem => ({
	kind: "turn_end",
	subtype: "success",
	isError: false,
	numTurns: 1,
	costUsd: 0,
	sessionId: "s",
	usage: { inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheCreationTokens: 0 },
	contextWindows: {},
	ts: "",
});
const rowsOf = (items: ChatItem[]) =>
	chatRows(
		{
			claudeSessionIds: [],
			items,
			pending: [],
			queued: [],
			busy: false,
			permissionMode: "default",
			costUsd: 0,
			commands: [],
			models: [],
			model: "",
			tasks: [],
			lastSeq: 0,
		} satisfies Transcript,
		emptyOverlay,
		new Set(),
	);

describe("chapterOf", () => {
	it("reads the trimmed title and summary", () => {
		expect(chapterOf(mark("  Auth bug fix  ", " the login race "))).toEqual({
			title: "Auth bug fix",
			summary: "the login race",
		});
	});

	it("cuts a long title to 60 characters", () => {
		expect(chapterOf(mark("x".repeat(90)))?.title).toBe("x".repeat(60));
	});

	it("has no chapter for another tool, a blank title, a failed call or a denied one", () => {
		expect(chapterOf(call("Read", { title: "Nope" }))).toBeUndefined();
		expect(chapterOf(mark("   "))).toBeUndefined();
		expect(chapterOf(mark(7))).toBeUndefined();
		expect(chapterOf(mark("A", undefined, { result: { isError: true, content: "" } }))).toBeUndefined();
		expect(
			chapterOf(mark("A", undefined, { permission: { status: "denied" } } as Partial<ToolCallItem>)),
		).toBeUndefined();
	});
});

describe("chapters", () => {
	it("is empty when nothing marked a chapter", () => {
		expect(chapters(rowsOf([user(), say("hi"), call("Read"), call("Bash"), say("done")]))).toEqual([]);
	});

	it("lists the chapters in order, led by Session start, each keyed by its row", () => {
		const rows = rowsOf([user(), say("hi"), mark("Explore"), say("a"), mark("Build", "the renderer"), say("b")]);
		expect(chapters(rows)).toEqual([
			{ key: rows[0].key, title: "Session start", summary: "" },
			{ key: rows[2].key, title: "Explore", summary: "" },
			{ key: rows[4].key, title: "Build", summary: "the renderer" },
		]);
	});

	it("adds no Session start when the first row is itself a chapter", () => {
		const rows = rowsOf([mark("First"), say("a"), mark("Second")]);
		expect(chapters(rows).map((c) => c.title)).toEqual(["First", "Second"]);
	});

	it("keeps the chapters of a finished turn that folded", () => {
		const rows = rowsOf([
			user(),
			mark("Explore"),
			call("Read"),
			call("Bash"),
			mark("Build"),
			call("Edit"),
			call("Read"),
			say("done"),
			turnEnd(),
		]);
		expect(rows.some((r) => r.kind === "turn")).toBe(true);
		const found = chapters(rows);
		expect(found.map((c) => c.title)).toEqual(["Session start", "Explore", "Build"]);
		found.forEach((c) => expect(rows.some((r) => r.key === c.key)).toBe(true));
	});
});
