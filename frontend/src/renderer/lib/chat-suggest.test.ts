import { describe, expect, it } from "vitest";
import type { ToolCallItem } from "./chat-fold";
import { chatRows } from "./chat-rows";
import { emptyOverlay } from "./chat-stream";
import {
	liveSuggestions,
	SUGGEST_SESSION,
	SUGGEST_SKILL,
	sessionSuggestionOf,
	skillSuggestionOf,
} from "./chat-suggest";

let n = 0;
const call = (name: string, input: unknown, over: Partial<ToolCallItem> = {}): ToolCallItem => ({
	kind: "tool_call",
	id: `s${++n}`,
	name,
	parentToolUseId: "",
	input,
	startedTs: "",
	result: { isError: false, content: "" },
	...over,
});

describe("sessionSuggestionOf", () => {
	it("trims the title and the prompt", () => {
		expect(sessionSuggestionOf(call(SUGGEST_SESSION, { title: "  Fix it ", prompt: " Do the work. " }))).toEqual({
			title: "Fix it",
			prompt: "Do the work.",
		});
	});

	it("cuts the title to 60 characters and the prompt to 4000", () => {
		const got = sessionSuggestionOf(call(SUGGEST_SESSION, { title: "t".repeat(90), prompt: "p".repeat(5000) }));
		expect(got?.title).toHaveLength(60);
		expect(got?.prompt).toHaveLength(4000);
	});

	it("needs both a title and a prompt", () => {
		expect(sessionSuggestionOf(call(SUGGEST_SESSION, { title: "  ", prompt: "x" }))).toBeUndefined();
		expect(sessionSuggestionOf(call(SUGGEST_SESSION, { title: "x" }))).toBeUndefined();
		expect(sessionSuggestionOf(call(SUGGEST_SESSION, { title: "x", prompt: 3 }))).toBeUndefined();
		expect(sessionSuggestionOf(call(SUGGEST_SESSION, "nope"))).toBeUndefined();
	});

	it("ignores another tool, and a call that errored, was denied or was cancelled", () => {
		const input = { title: "x", prompt: "y" };
		expect(sessionSuggestionOf(call("Read", input))).toBeUndefined();
		expect(
			sessionSuggestionOf(call(SUGGEST_SESSION, input, { result: { isError: true, content: "" } })),
		).toBeUndefined();
		const permission = (status: "denied" | "cancelled") =>
			({ requestId: "r", status }) as unknown as ToolCallItem["permission"];
		expect(sessionSuggestionOf(call(SUGGEST_SESSION, input, { permission: permission("denied") }))).toBeUndefined();
		expect(sessionSuggestionOf(call(SUGGEST_SESSION, input, { permission: permission("cancelled") }))).toBeUndefined();
	});
});

describe("skillSuggestionOf", () => {
	it("drops a leading slash and trims the name and the reason", () => {
		expect(skillSuggestionOf(call(SUGGEST_SKILL, { name: " /verify ", reason: " Drives the app. " }))).toEqual({
			name: "verify",
			reason: "Drives the app.",
		});
	});

	it("accepts namespaced skill names", () => {
		expect(skillSuggestionOf(call(SUGGEST_SKILL, { name: "pstack:babysit", reason: "r" }))?.name).toBe(
			"pstack:babysit",
		);
	});

	it("refuses a name that could carry more than a skill name", () => {
		for (const name of ["", "/", "a b", "verify now", "verify\n/rm", "a/b", "../x", "x".repeat(101), 7]) {
			expect(skillSuggestionOf(call(SUGGEST_SKILL, { name, reason: "r" })), String(name)).toBeUndefined();
		}
	});

	it("cuts the reason to 200 characters and allows it to be empty", () => {
		expect(skillSuggestionOf(call(SUGGEST_SKILL, { name: "a", reason: "r".repeat(300) }))?.reason).toHaveLength(200);
		expect(skillSuggestionOf(call(SUGGEST_SKILL, { name: "a" }))?.reason).toBe("");
	});

	it("ignores another tool and a failed call", () => {
		expect(skillSuggestionOf(call(SUGGEST_SESSION, { name: "a", reason: "r" }))).toBeUndefined();
		expect(
			skillSuggestionOf(call(SUGGEST_SKILL, { name: "a", reason: "r" }, { result: { isError: true, content: "" } })),
		).toBeUndefined();
	});
});

describe("liveSuggestions", () => {
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

	it("names the newest card of each kind separately", () => {
		const s1 = call(SUGGEST_SESSION, { title: "a", prompt: "b" });
		const k1 = call(SUGGEST_SKILL, { name: "a", reason: "r" });
		const s2 = call(SUGGEST_SESSION, { title: "c", prompt: "d" });
		expect(liveSuggestions(rowsOf(s1, k1, s2))).toEqual({ session: s2.id, skill: k1.id });
		expect(liveSuggestions(rowsOf())).toEqual({});
	});

	it("skips a card that drew nothing", () => {
		const good = call(SUGGEST_SESSION, { title: "a", prompt: "b" });
		const bad = call(SUGGEST_SESSION, { title: "c", prompt: "d" }, { result: { isError: true, content: "" } });
		expect(liveSuggestions(rowsOf(good, bad)).session).toBe(good.id);
	});
});
