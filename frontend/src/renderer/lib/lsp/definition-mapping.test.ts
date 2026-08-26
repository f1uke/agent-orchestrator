import { describe, expect, test } from "vitest";
import { toMonacoDefinitions } from "./definition-mapping";

// Plain strings stand in for URIs: the mapper is generic in the URI type, so
// what is under test here is the coordinate arithmetic and nothing else.
const modelUri = "ao-file:///s1/backend/x.go";
const toModelUri = (uri: string) => uri.replace("file://", "ao-file://");

describe("toMonacoDefinitions", () => {
	test("a single Location becomes one link, 0-based LSP → 1-based Monaco", () => {
		// Off-by-one here is not a crash, it is a ⌘click that lands one line above
		// the definition - which reads as "the feature is a bit broken" forever.
		const links = toMonacoDefinitions(
			{ uri: "file:///a/b.go", range: { start: { line: 10, character: 4 }, end: { line: 10, character: 9 } } },
			modelUri,
			toModelUri,
		);
		expect(links).toHaveLength(1);
		expect(links[0].range).toMatchObject({ startLineNumber: 11, startColumn: 5, endLineNumber: 11, endColumn: 10 });
	});

	test("an array of Locations becomes one link each", () => {
		const loc = (line: number) => ({
			uri: "file:///a/b.go",
			range: { start: { line, character: 0 }, end: { line, character: 1 } },
		});
		expect(toMonacoDefinitions([loc(0), loc(5)], modelUri, toModelUri)).toHaveLength(2);
	});

	test("LocationLink (linkSupport) uses targetSelectionRange when present", () => {
		// gopls answers with LocationLinks because we advertise linkSupport. Reading
		// targetRange instead lands on the top of the declaration's whole body.
		const links = toMonacoDefinitions(
			[
				{
					targetUri: "file:///a/b.go",
					targetRange: { start: { line: 3, character: 0 }, end: { line: 20, character: 1 } },
					targetSelectionRange: { start: { line: 3, character: 5 }, end: { line: 3, character: 12 } },
				},
			],
			modelUri,
			toModelUri,
		);
		expect(links[0].range).toMatchObject({ startLineNumber: 4, startColumn: 6 });
	});

	test("a LocationLink with no selection range falls back to targetRange", () => {
		const links = toMonacoDefinitions(
			[
				{
					targetUri: "file:///a/b.go",
					targetRange: { start: { line: 7, character: 0 }, end: { line: 9, character: 1 } },
				},
			],
			modelUri,
			toModelUri,
		);
		expect(links[0].range).toMatchObject({ startLineNumber: 8, startColumn: 1 });
	});

	test("null, empty and malformed results all become an empty array, never a throw", () => {
		// A provider that throws takes Monaco's whole ⌘click gesture down with it.
		for (const bad of [null, undefined, [], {}, [{}], "nope", 7, [{ uri: "file:///a.go" }]]) {
			expect(toMonacoDefinitions(bad, modelUri, toModelUri)).toEqual([]);
		}
	});
});
