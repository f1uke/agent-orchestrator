import type { editor } from "monaco-editor";
import { describe, expect, test } from "vitest";
import {
	DIAGNOSTIC_LINE_ERROR_CLASS,
	DIAGNOSTIC_LINE_WARNING_CLASS,
	diagnosticLineDecorations,
} from "./diagnostic-lines";

/**
 * Every assertion here is on the DENSITY rules, because they are the half of
 * this feature that can be silently wrong: a band drawn twice on one line, or a
 * band drawn on forty lines because one server reported a forty-line range,
 * still renders and still looks deliberate.
 */

const marker = (severity: number, startLineNumber: number, endLineNumber = startLineNumber): editor.IMarkerData =>
	({
		severity,
		message: `m${startLineNumber}`,
		startLineNumber,
		startColumn: 1,
		endLineNumber,
		endColumn: 4,
	}) as editor.IMarkerData;

const ERROR = 8;
const WARNING = 4;
const INFO = 2;
const HINT = 1;

const linesOf = (decorations: editor.IModelDeltaDecoration[]) =>
	decorations.map((d) => [d.range.startLineNumber, d.options.className] as const);

describe("diagnosticLineDecorations", () => {
	test("bands an error and a warning in their own colours", () => {
		expect(linesOf(diagnosticLineDecorations([marker(ERROR, 3), marker(WARNING, 7)]))).toEqual([
			[3, DIAGNOSTIC_LINE_ERROR_CLASS],
			[7, DIAGNOSTIC_LINE_WARNING_CLASS],
		]);
	});

	test("one band per line, however many diagnostics land on it", () => {
		// Five translucent bands stacked on one line multiply into a near-opaque
		// slab — the line with the most to say would be the least readable one in
		// the file.
		const decorations = diagnosticLineDecorations([
			marker(WARNING, 12),
			marker(WARNING, 12),
			marker(WARNING, 12),
			marker(WARNING, 12),
			marker(WARNING, 12),
		]);
		expect(linesOf(decorations)).toEqual([[12, DIAGNOSTIC_LINE_WARNING_CLASS]]);
	});

	test("the worst severity on a line wins, in either arrival order", () => {
		expect(linesOf(diagnosticLineDecorations([marker(WARNING, 5), marker(ERROR, 5)]))).toEqual([
			[5, DIAGNOSTIC_LINE_ERROR_CLASS],
		]);
		expect(linesOf(diagnosticLineDecorations([marker(ERROR, 5), marker(WARNING, 5)]))).toEqual([
			[5, DIAGNOSTIC_LINE_ERROR_CLASS],
		]);
	});

	test("a multi-line range bands only the line it STARTS on", () => {
		// An unterminated block is reported as a range the size of the construct.
		// Painting all of it makes the band's size a function of the code rather
		// than of the problem.
		expect(linesOf(diagnosticLineDecorations([marker(ERROR, 10, 40)]))).toEqual([[10, DIAGNOSTIC_LINE_ERROR_CLASS]]);
	});

	test("info and hint carry no band", () => {
		expect(diagnosticLineDecorations([marker(INFO, 2), marker(HINT, 3)])).toEqual([]);
	});

	test("bands come back in ascending line order", () => {
		// `deltaDecorations` diffs positionally: an unsorted list churns every id
		// on a publish whose diagnostics did not move.
		const decorations = diagnosticLineDecorations([marker(WARNING, 30), marker(ERROR, 4), marker(WARNING, 11)]);
		expect(decorations.map((d) => d.range.startLineNumber)).toEqual([4, 11, 30]);
	});

	test("the band is whole-line and claims neither the ruler nor the minimap", () => {
		// Monaco's own marker decorations already put this diagnostic in both. A
		// second entry would draw every mark twice.
		const [only] = diagnosticLineDecorations([marker(ERROR, 1)]);
		expect(only.options.isWholeLine).toBe(true);
		expect(only.options.overviewRuler).toBeUndefined();
		expect(only.options.minimap).toBeUndefined();
	});

	test("an out-of-range line number is dropped rather than banded", () => {
		expect(diagnosticLineDecorations([marker(ERROR, 0), marker(ERROR, Number.NaN)])).toEqual([]);
	});
});
