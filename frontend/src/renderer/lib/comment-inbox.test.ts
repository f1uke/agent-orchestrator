import { describe, expect, it } from "vitest";
import {
	baseName,
	genPrompt,
	hueFor,
	initialsFor,
	providerBadge,
	relativeTime,
	splitBodyRuns,
	statusFor,
} from "./comment-inbox";

describe("splitBodyRuns", () => {
	it("splits inline code runs on backticks (odd = code)", () => {
		expect(splitBodyRuns("use `foo` here")).toEqual([
			{ text: "use ", code: false },
			{ text: "foo", code: true },
			{ text: " here", code: false },
		]);
	});
	it("drops empty runs and handles no code", () => {
		expect(splitBodyRuns("plain text")).toEqual([{ text: "plain text", code: false }]);
		expect(splitBodyRuns("`code`")).toEqual([{ text: "code", code: true }]);
		expect(splitBodyRuns("")).toEqual([]);
	});
});

describe("genPrompt", () => {
	it("builds the reviewer prompt with path:line and a blockquoted body", () => {
		const p = genPrompt("a/b.go", 42, "fix this");
		expect(p).toContain("A reviewer left this unresolved comment on a/b.go:42");
		expect(p).toContain("> fix this");
		expect(p).toContain("reply on the thread summarizing what you did.");
	});
});

describe("initialsFor", () => {
	it("uses first letters of two word-parts, else first two chars", () => {
		expect(initialsFor("f1uke")).toBe("F1");
		expect(initialsFor("claude")).toBe("CL");
		expect(initialsFor("m.rivera")).toBe("MR");
		expect(initialsFor("")).toBe("?");
	});
});

describe("hueFor", () => {
	it("is deterministic and in range", () => {
		const a = hueFor("alice");
		expect(a).toBe(hueFor("alice"));
		expect(a).toBeGreaterThanOrEqual(0);
		expect(a).toBeLessThan(360);
	});
});

describe("statusFor", () => {
	it("prioritizes conflict, then changes-requested, else open", () => {
		expect(statusFor("changes_requested", "conflicting").kind).toBe("conflict");
		expect(statusFor("changes_requested", "mergeable").kind).toBe("changes");
		expect(statusFor("approved", "mergeable").kind).toBe("open");
		expect(statusFor(undefined, undefined).label).toBe("Open");
	});
});

describe("providerBadge", () => {
	it("maps gitlab to GL and everything else to GH", () => {
		expect(providerBadge("gitlab")).toBe("GL");
		expect(providerBadge("github")).toBe("GH");
		expect(providerBadge("")).toBe("GH");
	});
});

describe("relativeTime", () => {
	const now = Date.parse("2026-07-09T12:00:00Z");
	it("formats common buckets", () => {
		expect(relativeTime("2026-07-09T11:59:30Z", now)).toBe("just now");
		expect(relativeTime("2026-07-09T11:30:00Z", now)).toBe("30m ago");
		expect(relativeTime("2026-07-09T10:00:00Z", now)).toBe("2h ago");
		expect(relativeTime("2026-07-08T12:00:00Z", now)).toBe("1d ago");
		expect(relativeTime("not-a-date", now)).toBe("");
	});
});

describe("baseName", () => {
	it("returns the last path segment", () => {
		expect(baseName("backend/internal/foo.go")).toBe("foo.go");
		expect(baseName("foo.go")).toBe("foo.go");
	});
});
