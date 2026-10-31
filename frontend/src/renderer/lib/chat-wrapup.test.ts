import { describe, expect, it } from "vitest";
import type { ToolCallItem } from "./chat-fold";
import { chatRows } from "./chat-rows";
import { emptyOverlay } from "./chat-stream";
import { latestWrapUpId, WRAP_UP, wrapUpOf } from "./chat-wrapup";

let n = 0;
const wrap = (input: unknown, over: Partial<ToolCallItem> = {}): ToolCallItem => ({
	kind: "tool_call",
	id: `w${++n}`,
	name: WRAP_UP,
	parentToolUseId: "",
	input,
	startedTs: "",
	result: { isError: false, content: "" },
	...over,
});

describe("wrapUpOf", () => {
	it("reads the recap, the ask and the replies", () => {
		expect(wrapUpOf(wrap({ recap: " Done. ", ask: " Commit? ", replies: ["Yes, commit", "Not yet"] }))).toEqual({
			recap: "Done.",
			ask: "Commit?",
			replies: ["Yes, commit", "Not yet"],
		});
	});

	it("keeps an ask that has no replies", () => {
		expect(wrapUpOf(wrap({ recap: "Done.", ask: "Commit?" }))).toEqual({ recap: "Done.", ask: "Commit?", replies: [] });
	});

	it("keeps at most two replies, cut to 24 characters, dropping blanks and non-strings", () => {
		const got = wrapUpOf(wrap({ recap: "r", replies: ["  ", 3, "a".repeat(40), "b", "c"] }));
		expect(got?.replies).toEqual(["a".repeat(24), "b"]);
	});

	it("needs a recap, and ignores a failed or another tool's call", () => {
		expect(wrapUpOf(wrap({ recap: "  ", ask: "q" }))).toBeUndefined();
		expect(wrapUpOf(wrap({ ask: "q" }))).toBeUndefined();
		expect(wrapUpOf(wrap({ recap: "r" }, { result: { isError: true, content: "" } }))).toBeUndefined();
		expect(wrapUpOf(wrap({ recap: "r" }, { name: "Read" }))).toBeUndefined();
	});
});

describe("latestWrapUpId", () => {
	it("names the last wrap-up in the rows, and none when there is none", () => {
		const rowsOf = (...calls: ToolCallItem[]) =>
			chatRows(
				{
					claudeSessionIds: [],
					items: calls,
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
				},
				emptyOverlay,
				new Set(),
			);
		const a = wrap({ recap: "one" });
		const b = wrap({ recap: "two" });
		expect(latestWrapUpId(rowsOf(a, b))).toBe(b.id);
		expect(latestWrapUpId(rowsOf())).toBeUndefined();
	});
});
