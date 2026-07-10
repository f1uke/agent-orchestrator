import { describe, expect, it } from "vitest";
import {
	baseName,
	genPrompt,
	hueFor,
	initialsFor,
	originOf,
	providerBadge,
	relativeTime,
	resolveNoteHref,
	splitBodyRuns,
	splitNoteRuns,
	statusFor,
	TOKEN_COLORS,
	tokenizeCode,
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

describe("splitNoteRuns", () => {
	it("renders a GitLab system note's markdown link as a link run with an absolute href", () => {
		const origin = "https://gitlab.com";
		const rel = "/finnomena/mobility/nter-ios-app/-/merge_requests/3028/diffs?diff_id=177522";
		const runs = splitNoteRuns(`changed this line in [version 6 of the diff](${rel})`, origin);
		expect(runs).toEqual([
			{ text: "changed this line in " },
			{ text: "version 6 of the diff", href: `${origin}${rel}` },
		]);
	});
	it("passes absolute link targets through unchanged and needs no origin for text", () => {
		expect(splitNoteRuns("see [here](https://x.test/y)")).toEqual([
			{ text: "see " },
			{ text: "here", href: "https://x.test/y" },
		]);
		expect(splitNoteRuns("no link here")).toEqual([{ text: "no link here" }]);
		expect(splitNoteRuns("")).toEqual([]);
	});
});

describe("resolveNoteHref / originOf", () => {
	it("prefixes host-relative targets with the origin, passes absolute through", () => {
		expect(resolveNoteHref("/a/b", "https://h.test")).toBe("https://h.test/a/b");
		expect(resolveNoteHref("https://h.test/a", "https://other.test")).toBe("https://h.test/a");
		// no origin → cannot resolve, leave as-is (won't open, but never a broken prefix)
		expect(resolveNoteHref("/a/b", "")).toBe("/a/b");
	});
	it("extracts the origin from a PR/MR url", () => {
		expect(originOf("https://gitlab.com/g/r/-/merge_requests/7")).toBe("https://gitlab.com");
		expect(originOf("not a url")).toBe("");
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

describe("tokenizeCode", () => {
	const byText = (line: string, text: string) => tokenizeCode(line).find((t) => t.text === text);

	it("colors keywords, calls, numbers, and line comments", () => {
		const line = "func Foo() { return 42 } // hi";
		expect(byText(line, "func")?.color).toBe(TOKEN_COLORS.keyword);
		expect(byText(line, "Foo")?.color).toBe(TOKEN_COLORS.fn); // capitalized but followed by "("
		expect(byText(line, "return")?.color).toBe(TOKEN_COLORS.keyword);
		expect(byText(line, "42")?.color).toBe(TOKEN_COLORS.number);
		expect(byText(line, "// hi")?.color).toBe(TOKEN_COLORS.comment);
	});

	it("colors capitalized identifiers as types and quoted text as strings", () => {
		const line = 'var x Observer = "hello"';
		expect(byText(line, "var")?.color).toBe(TOKEN_COLORS.keyword);
		expect(byText(line, "Observer")?.color).toBe(TOKEN_COLORS.type);
		expect(byText(line, '"hello"')?.color).toBe(TOKEN_COLORS.string);
		expect(byText(line, "x")?.color).toBe(TOKEN_COLORS.plain);
	});

	it("returns [] for empty input and losslessly reconstructs the line", () => {
		expect(tokenizeCode("")).toEqual([]);
		const line = "  x := y + 1";
		expect(
			tokenizeCode(line)
				.map((t) => t.text)
				.join(""),
		).toBe(line);
	});
});
