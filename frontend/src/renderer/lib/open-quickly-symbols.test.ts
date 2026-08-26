import { describe, expect, test } from "vitest";
import { parseWorkspaceSymbols, rankSymbols, type SymbolHit } from "./open-quickly-symbols";

const ROOT = "/w";
const hit = (over: Partial<SymbolHit> & { name: string; uri: string }): SymbolHit => ({
	kind: 12,
	line: 1,
	column: 1,
	...over,
});

describe("parseWorkspaceSymbols", () => {
	test("reads the SymbolInformation shape, 0-based LSP → 1-based editor", () => {
		expect(
			parseWorkspaceSymbols([
				{
					name: "ConfinedPath",
					kind: 12,
					containerName: "previewutil",
					location: { uri: "file:///w/a.go", range: { start: { line: 210, character: 5 } } },
				},
			]),
		).toEqual([
			{ name: "ConfinedPath", kind: 12, containerName: "previewutil", uri: "file:///w/a.go", line: 211, column: 6 },
		]);
	});

	test("reads the WorkspaceSymbol shape, whose location may carry only a uri", () => {
		expect(parseWorkspaceSymbols([{ name: "X", kind: 5, location: { uri: "file:///w/b.go" } }])).toEqual([
			{ name: "X", kind: 5, containerName: undefined, uri: "file:///w/b.go", line: 1, column: 1 },
		]);
	});

	test("null, non-arrays and malformed entries yield an empty list, never a throw", () => {
		for (const bad of [null, undefined, {}, "x", 7, [null], [{ name: 1 }], [{ name: "X" }]]) {
			expect(parseWorkspaceSymbols(bad)).toEqual([]);
		}
	});
});

describe("rankSymbols", () => {
	test("a prefix match beats a scattered one", () => {
		// The spike's measured rule: without it `OG-Promotion-Hub 2.png` outranked
		// `PromotionHubViewController.swift` for "promohub".
		const ranked = rankSymbols(
			[hit({ name: "somethingPromoHubbish", uri: "file:///w/a.go" }), hit({ name: "PromoHub", uri: "file:///w/b.go" })],
			"promohub",
			ROOT,
		);
		expect(ranked[0].name).toBe("PromoHub");
	});

	test("dedupes per declaration - an index holds one unit per built target", () => {
		const dup = hit({ name: "Portfolio", uri: "file:///w/m.go", line: 4, column: 6 });
		expect(rankSymbols([dup, { ...dup }, { ...dup }], "portfolio", ROOT)).toHaveLength(1);
	});

	test("two declarations of the same name at different lines are NOT deduped", () => {
		const a = hit({ name: "Handle", uri: "file:///w/m.go", line: 4 });
		const b = hit({ name: "Handle", uri: "file:///w/m.go", line: 90 });
		expect(rankSymbols([a, b], "handle", ROOT)).toHaveLength(2);
	});

	test("generated paths are demoted by path SHAPE", () => {
		const ranked = rankSymbols(
			[
				hit({ name: "Widget", uri: "file:///w/node_modules/pkg/widget.go" }),
				hit({ name: "Widget", uri: "file:///w/internal/widget.go" }),
			],
			"widget",
			ROOT,
		);
		expect(ranked[0].path).toBe("internal/widget.go");
	});

	test("carries a workspace-relative path inside the root and an absolute one outside", () => {
		const ranked = rankSymbols(
			[
				hit({ name: "Printf", uri: "file:///usr/local/go/src/fmt/print.go" }),
				hit({ name: "Printf", uri: "file:///w/x.go" }),
			],
			"printf",
			ROOT,
		);
		expect(ranked.map((r) => r.path)).toContain("x.go");
		expect(ranked.map((r) => r.path)).toContain("/usr/local/go/src/fmt/print.go");
	});

	test("a symbol whose name cannot contain the query is dropped", () => {
		expect(rankSymbols([hit({ name: "Alpha", uri: "file:///w/a.go" })], "zzz", ROOT)).toEqual([]);
	});

	test("an empty query returns nothing rather than every symbol", () => {
		expect(rankSymbols([hit({ name: "X", uri: "file:///w/a.go" })], "   ", ROOT)).toEqual([]);
	});

	test("respects the limit", () => {
		const many = Array.from({ length: 200 }, (_, i) => hit({ name: `Widget${i}`, uri: `file:///w/a${i}.go` }));
		expect(rankSymbols(many, "widget", ROOT, 25)).toHaveLength(25);
	});

	test("positions index into the NAME, so the palette underlines the right characters", () => {
		const ranked = rankSymbols([hit({ name: "ConfinedPath", uri: "file:///w/a.go" })], "conf", ROOT);
		expect(ranked[0].positions).toEqual([0, 1, 2, 3]);
	});
});
