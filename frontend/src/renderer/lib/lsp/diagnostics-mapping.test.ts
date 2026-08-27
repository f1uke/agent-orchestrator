import { describe, expect, test } from "vitest";
import { countMarkers, SEVERITY_TABLE, TAG_TABLE, toMonacoMarkerRange, toMonacoMarkers } from "./diagnostics-mapping";

/**
 * The two things that can be silently wrong about a diagnostic: which severity
 * it is, and where it sits. Both render a complete, plausible result when they
 * are wrong.
 *
 * 🗝 The IDENTITY of each severity is proved where it belongs — at COMPILE time,
 * by `satisfies SeverityMap` in the module itself, which reaches Monaco's enum
 * through a type-only import and fails with "Type '4' is not assignable to type
 * 'MarkerSeverity.Error'" the moment a number moves. It cannot be proved here:
 * the barrel that carries the runtime enum boots the whole editor on import, and
 * `monaco-editor`'s export map has no side-effect-free path to it. So the
 * numbers below are Monaco's, spelled out, and what these tests add is the thing
 * tsc CANNOT see — that the two lists are not accidentally the same list.
 */

/** Monaco's `MarkerSeverity`, which is a bitmask counting UP in urgency. */
const MarkerSeverity = { Hint: 1, Info: 2, Warning: 4, Error: 8 } as const;
/** Monaco's `MarkerTag`. */
const MarkerTag = { Unnecessary: 1, Deprecated: 2 } as const;

const at = (line: number, character: number, endLine = line, endCharacter = character + 3) => ({
	start: { line, character },
	end: { line: endLine, character: endCharacter },
});

describe("the severity table", () => {
	// 🗝 The trap. LSP counts 1..4 DOWN in urgency; Monaco is a bitmask counting
	// UP. Every LSP value is also a legal Monaco value, so a cast type-checks and
	// silently turns every error into a hint - which draws no squiggle at all.
	test("maps LSP's dense list onto Monaco's bitmask, by name", () => {
		expect(SEVERITY_TABLE[1]).toBe(MarkerSeverity.Error);
		expect(SEVERITY_TABLE[2]).toBe(MarkerSeverity.Warning);
		expect(SEVERITY_TABLE[3]).toBe(MarkerSeverity.Info);
		expect(SEVERITY_TABLE[4]).toBe(MarkerSeverity.Hint);
	});

	// Without this a naive cast would pass the test above for 2 of the 4.
	test("no LSP severity maps to ITSELF, which is what a cast would do", () => {
		for (const [lsp, monaco] of Object.entries(SEVERITY_TABLE)) {
			expect(monaco, `LSP severity ${lsp} maps to itself`).not.toBe(Number(lsp));
		}
	});

	test("an absent severity is an ERROR, never the quietest thing on the list", () => {
		const [marker] = toMonacoMarkers([{ range: at(0, 0), message: "no severity" }], () => null);
		expect(marker.severity).toBe(MarkerSeverity.Error);
	});

	test("an unknown severity is an ERROR too", () => {
		const [marker] = toMonacoMarkers([{ range: at(0, 0), severity: 9, message: "?" }], () => null);
		expect(marker.severity).toBe(MarkerSeverity.Error);
	});
});

describe("the tag table", () => {
	// The two lists agree - which is exactly why they are asserted rather than
	// cast. An agreement nobody checked is a coincidence.
	test("matches Monaco's own", () => {
		expect(TAG_TABLE[1]).toBe(MarkerTag.Unnecessary);
		expect(TAG_TABLE[2]).toBe(MarkerTag.Deprecated);
	});

	test("an unknown tag is dropped rather than passed through", () => {
		const [marker] = toMonacoMarkers([{ range: at(0, 0), message: "m", tags: [1, 47] }], () => null);
		expect(marker.tags).toEqual([MarkerTag.Unnecessary]);
	});

	// sourcekit-lsp sends `tags: []` on every diagnostic. An empty array on the
	// marker is not the same as no tags to Monaco's renderer.
	test("an empty tag list leaves the field off entirely", () => {
		const [marker] = toMonacoMarkers([{ range: at(0, 0), message: "m", tags: [] }], () => null);
		expect(marker).not.toHaveProperty("tags");
	});
});

describe("the range", () => {
	test("LSP is 0-based in both axes; Monaco is 1-based in both", () => {
		expect(toMonacoMarkerRange(at(104, 17, 104, 42))).toEqual({
			startLineNumber: 105,
			startColumn: 18,
			endLineNumber: 105,
			endColumn: 43,
		});
	});

	// 🗝 Monaco draws NOTHING for a marker whose start equals its end, so a
	// zero-width diagnostic would be counted in the header and invisible on the
	// line - a problem that exists everywhere except where you would look for it.
	test("a zero-width range is widened by one column so the squiggle exists", () => {
		expect(toMonacoMarkerRange({ start: { line: 3, character: 8 }, end: { line: 3, character: 8 } })).toEqual({
			startLineNumber: 4,
			startColumn: 9,
			endLineNumber: 4,
			endColumn: 10,
		});
	});

	test("a backwards range collapses to its start rather than selecting backwards", () => {
		expect(toMonacoMarkerRange({ start: { line: 5, character: 9 }, end: { line: 2, character: 1 } })).toEqual({
			startLineNumber: 6,
			startColumn: 10,
			endLineNumber: 6,
			endColumn: 11,
		});
	});

	test("a multi-line range is carried whole", () => {
		expect(toMonacoMarkerRange(at(1, 2, 4, 6))).toMatchObject({ startLineNumber: 2, endLineNumber: 5, endColumn: 7 });
	});
});

describe("the payload", () => {
	test("carries the message, the source and the code", () => {
		// Shaped exactly like a real sourcekit-lsp diagnostic, captured on the iOS app.
		const [marker] = toMonacoMarkers(
			[{ range: at(104, 17, 104, 42), severity: 2, tags: [], source: "SourceKit", message: "Todo - refactor variant" }],
			() => null,
		);
		expect(marker).toMatchObject({
			severity: MarkerSeverity.Warning,
			source: "SourceKit",
			message: "Todo - refactor variant",
			startLineNumber: 105,
		});
	});

	test("a numeric code becomes a string, and an absent one stays absent", () => {
		const [withCode] = toMonacoMarkers([{ range: at(0, 0), message: "m", code: 42 }], () => null);
		expect(withCode.code).toBe("42");
		const [without] = toMonacoMarkers([{ range: at(0, 0), message: "m" }], () => null);
		expect(without.code).toBeUndefined();
	});

	test("a diagnostic with no range or no message is dropped, not half-rendered", () => {
		expect(
			toMonacoMarkers(
				[
					{ range: at(0, 0), message: "kept" },
					{ message: "no range" } as never,
					{ range: at(1, 0) } as never,
					null as never,
				],
				() => null,
			),
		).toHaveLength(1);
	});

	test("no diagnostics at all is an empty list, never a throw", () => {
		expect(toMonacoMarkers(undefined, () => null)).toEqual([]);
		expect(toMonacoMarkers([], () => null)).toEqual([]);
	});
});

describe("related information", () => {
	const related = (uri: string) => ({
		range: at(0, 0),
		message: "m",
		relatedInformation: [{ location: { uri, range: at(9, 1, 9, 5) }, message: "declared here" }],
	});

	test("is mapped through the caller's URI world", () => {
		const [marker] = toMonacoMarkers([related("file:///w/other.go")], (uri) => `ao-${uri}`);
		expect(marker.relatedInformation).toEqual([
			{
				resource: "ao-file:///w/other.go",
				message: "declared here",
				startLineNumber: 10,
				startColumn: 2,
				endLineNumber: 10,
				endColumn: 6,
			},
		]);
	});

	// A resource that cannot be addressed renders as a row that goes nowhere,
	// which is worse than not offering the row.
	test("an unaddressable file is dropped, and the marker survives", () => {
		const [marker] = toMonacoMarkers([related("file:///elsewhere.go")], () => null);
		expect(marker.message).toBe("m");
		expect(marker).not.toHaveProperty("relatedInformation");
	});
});

describe("counting", () => {
	test("errors and warnings only - a hint is not a problem anybody asked about", () => {
		const markers = toMonacoMarkers(
			[
				{ range: at(0, 0), severity: 1, message: "e" },
				{ range: at(1, 0), severity: 1, message: "e" },
				{ range: at(2, 0), severity: 2, message: "w" },
				{ range: at(3, 0), severity: 3, message: "i" },
				{ range: at(4, 0), severity: 4, message: "h" },
			],
			() => null,
		);
		expect(countMarkers(markers)).toEqual({ errors: 2, warnings: 1 });
	});
});
