import { describe, expect, it } from "vitest";
import { bashBadge, bashBlock, bashModeEntry, bashOutputLines } from "./chat-bash";
import type { BashItem } from "./chat-fold";

const item = (over: Partial<BashItem> = {}): BashItem => ({
	kind: "bash",
	id: "b1",
	command: "ls",
	cwd: "/w",
	chunks: [],
	state: "done",
	code: 0,
	signal: null,
	truncated: false,
	ts: "",
	...over,
});

describe("bashModeEntry", () => {
	it.each([
		["", "!", ""],
		["", "!git status", "git status"],
		["", "!a\nb", "a\nb"],
	])("enters bash mode when an empty box becomes %j", (previous, next, rest) => {
		expect(bashModeEntry(previous, next)).toBe(rest);
	});

	it.each([
		["", "git !status"],
		["", " !ls"],
		["x", "x!"],
		["!", "!!"],
	])("stays a prompt for %j then %j", (previous, next) => {
		expect(bashModeEntry(previous, next)).toBeNull();
	});
});

describe("bashBlock", () => {
	it("names the reason a command cannot run, a running command first", () => {
		expect(bashBlock({ busy: false, bashBusy: false })).toBeNull();
		expect(bashBlock({ busy: true, bashBusy: false })).toBe("turn");
		expect(bashBlock({ busy: false, bashBusy: true })).toBe("bash");
		expect(bashBlock({ busy: true, bashBusy: true })).toBe("bash");
	});
});

describe("bashBadge", () => {
	it("tells running, stopped, a signal, a failure and success apart", () => {
		expect(bashBadge(item({ state: "running", code: null }))).toEqual({ label: "running", tone: "running" });
		expect(bashBadge(item({ state: "stopped", code: null }))).toEqual({ label: "stopped", tone: "stopped" });
		expect(bashBadge(item({ code: null, signal: "SIGSEGV" }))).toEqual({ label: "SIGSEGV", tone: "error" });
		expect(bashBadge(item({ code: 2 }))).toEqual({ label: "exit 2", tone: "error" });
		expect(bashBadge(item({ code: 0 }))).toEqual({ label: "exit 0", tone: "ok" });
	});
});

describe("bashOutputLines", () => {
	it("counts a last line that has no newline yet", () => {
		const chunks = (...texts: string[]) => texts.map((text) => ({ stream: "stdout" as const, text }));
		expect(bashOutputLines(item({ chunks: [] }))).toBe(0);
		expect(bashOutputLines(item({ chunks: chunks("a\nb\n") }))).toBe(2);
		expect(bashOutputLines(item({ chunks: chunks("a\nb") }))).toBe(2);
		expect(bashOutputLines(item({ chunks: chunks("a\n", "b\nc") }))).toBe(3);
	});
});
