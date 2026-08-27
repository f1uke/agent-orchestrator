import { describe, expect, test } from "vitest";
import { plaintextAsMarkdown, toMonacoHover } from "./hover-mapping";

/**
 * `textDocument/hover` has four legal answer shapes and this app sees three of
 * them from two servers. Every case here is one that renders SOMETHING when it
 * is wrong — a signature as garbled emphasis, a type on the wrong word — rather
 * than throwing.
 *
 * The payloads are the ones the real servers sent while this slice was being
 * measured.
 */

describe("the shapes", () => {
	// sourcekit-lsp on the real iOS app, verbatim.
	test("MarkupContent markdown is carried through untouched", () => {
		const hover = toMonacoHover({
			contents: { kind: "markdown", value: "```swift\nprivate let disposeBag: DisposeBag\n```\n" },
		});
		expect(hover?.contents).toEqual([
			{ value: "```swift\nprivate let disposeBag: DisposeBag\n```\n", isTrusted: false },
		]);
	});

	// gopls, verbatim.
	test("gopls's markdown, with its own fence, survives", () => {
		const hover = toMonacoHover({
			contents: { kind: "markdown", value: "```go\npackage filepath\n```\n\n---\n\nPackage filepath…" },
		});
		expect(hover?.contents[0].value).toContain("```go");
	});

	test("a bare string is markdown, by the spec's own definition of MarkedString", () => {
		expect(toMonacoHover({ contents: "**bold**" })?.contents).toEqual([{ value: "**bold**", isTrusted: false }]);
	});

	test("a MarkedString with a language becomes a fenced block in that language", () => {
		expect(toMonacoHover({ contents: { language: "go", value: "func F() error" } })?.contents).toEqual([
			{ value: "```go\nfunc F() error\n```", isTrusted: false },
		]);
	});

	test("an array is every part, in order", () => {
		const hover = toMonacoHover({ contents: [{ language: "go", value: "var x int" }, "Some prose."] });
		expect(hover?.contents.map((c) => c.value)).toEqual(["```go\nvar x int\n```", "Some prose."]);
	});

	// 🗝 A fence inside the value would close ours early and render the rest of
	// the signature as prose — a hover that is subtly, plausibly wrong.
	test("a value containing a fence gets a longer fence", () => {
		const hover = toMonacoHover({ contents: { language: "swift", value: "/// ```\n/// example\n/// ```" } });
		expect(hover?.contents[0].value.startsWith("````swift")).toBe(true);
		expect(hover?.contents[0].value.endsWith("````")).toBe(true);
	});
});

describe("plaintext is not markdown", () => {
	// #258 established the rule for completion documentation. Monaco's hover
	// accepts IMarkdownString and NOTHING ELSE, so here it has to be escaped
	// rather than typed away.
	test("markdown metacharacters are escaped", () => {
		expect(plaintextAsMarkdown("_private_ *count* [1]")).toBe("\\_private\\_ \\*count\\* \\[1\\]");
	});

	test("a newline becomes a paragraph break, or markdown folds the line away", () => {
		expect(plaintextAsMarkdown("line one\nline two")).toBe("line one\n\nline two");
	});

	test("a MarkupContent that is not markdown goes through the escape", () => {
		const hover = toMonacoHover({ contents: { kind: "plaintext", value: "count_of_items" } });
		expect(hover?.contents[0].value).toBe("count\\_of\\_items");
	});

	// The spec makes `kind` optional, and "absent" must not silently mean
	// "markdown" — that is the case that renders a doc comment as emphasis.
	test("a MarkupContent with no kind is treated as plaintext, not as markdown", () => {
		expect(toMonacoHover({ contents: { value: "a_b_c" } })?.contents[0].value).toBe("a\\_b\\_c");
	});
});

describe("the range", () => {
	test("LSP is 0-based in both axes; Monaco is 1-based in both", () => {
		const hover = toMonacoHover({
			contents: "x",
			range: { start: { line: 55, character: 16 }, end: { line: 55, character: 26 } },
		});
		expect(hover?.range).toEqual({ startLineNumber: 56, startColumn: 17, endLineNumber: 56, endColumn: 27 });
	});

	// Monaco falls back to the word under the pointer, which is the right
	// default. Inventing a range would put the highlight somewhere the server
	// never claimed it was.
	test("an absent range stays absent", () => {
		expect(toMonacoHover({ contents: "x" })).not.toHaveProperty("range");
	});

	test("a malformed range is dropped, and the contents survive", () => {
		const hover = toMonacoHover({ contents: "x", range: { start: { line: 1 } } as never });
		expect(hover?.contents).toHaveLength(1);
		expect(hover).not.toHaveProperty("range");
	});
});

describe("nothing to show", () => {
	// 🗝 `null`, not an empty hover. Monaco treats the two identically
	// (`getHover.js:51` requires a range AND non-empty contents), so returning
	// null is what leaves one place able to tell them apart — which is where the
	// log line goes.
	test.each([
		["a null result", null],
		["no result at all", undefined],
		["null contents", { contents: null }],
		["an empty array", { contents: [] }],
		["an empty string", { contents: "" }],
		["whitespace only", { contents: { kind: "markdown", value: "   \n" } }],
		["a MarkedString with no value", { contents: { language: "go" } }],
		["something that is not an object", 42],
	])("%s answers null", (_name, result) => {
		expect(toMonacoHover(result)).toBeNull();
	});

	test("an array whose parts are all empty answers null rather than an empty hover", () => {
		expect(toMonacoHover({ contents: ["", { value: "" }] })).toBeNull();
	});

	test("one empty part among real ones is dropped, not rendered as a gap", () => {
		expect(toMonacoHover({ contents: ["", "real"] })?.contents.map((c) => c.value)).toEqual(["real"]);
	});
});
