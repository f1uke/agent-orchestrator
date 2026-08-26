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

/**
 * The four ranking rules against SWIFT noise, taken from what a real iOS app
 * actually returns rather than from what a fixture makes convenient.
 *
 * 🗝 Slice 2 already implements all four for paths, and this suite reuses that
 * scorer rather than growing a second one - so what is pinned here is that the
 * FILE rules survive contact with symbol-shaped input, which is where they were
 * measured against Xcode's own Open Quickly in the first place.
 */
describe("Swift, at real-project scale", () => {
	const IOS = "/Users/x/nter-ios-app";

	test("fuzzy noise loses to the symbol you asked for", () => {
		// Measured, and NOT a readiness artefact the way the spike read it: querying
		// "AppDelegateViewModel" on the real app returns the two right hits plus an
		// Objective-C selector and a 700-character Swift initialiser that both match
		// as SUBSEQUENCES, and those two never go away no matter how long the index
		// has had to settle. Ranking is what makes them harmless.
		const ranked = rankSymbols(
			[
				hit({
					name: "initWithAppID:appName:loginTooltipEnabled:loginTooltipText:defaultShareMode:advertisingIDEnabled:implicitLoggingEnabled:dialogConfigurations:dialogFlows:timestamp:errorConfiguration:",
					uri: `file://${IOS}/Pods/FBSDKCoreKit/FBSDKServerConfiguration.m`,
				}),
				hit({
					name: "init(indvEmail:customerIdPhoto1:ocrRef:title:titleOther:sex:firstNameTh:lastNameTh:appDelegateLike:viewModelish:)",
					uri: `file://${IOS}/NterApp/Models/MandatoryModel.swift`,
				}),
				hit({ name: "AppDelegateViewModel", kind: 5, uri: `file://${IOS}/NterApp/AppDelegateViewModel.swift` }),
			],
			"AppDelegateViewModel",
			IOS,
		);
		expect(ranked[0].name).toBe("AppDelegateViewModel");
		expect(ranked[0].path).toBe("NterApp/AppDelegateViewModel.swift");
	});

	test("generated asset symbols are demoted by PATH SHAPE, not by root prefix", () => {
		// The spike's sharpest ranking finding: `ImageResource.*` and `_R.image.*`
		// out of DerivedData/.../GeneratedAssetSymbols outnumbered the hand-written
		// hits and took every visible row. The index answers with paths from the
		// tree that was BUILT, so a root-prefix rule would not have caught them.
		const ranked = rankSymbols(
			[
				// Same NAME on both, so the only thing that can separate them is the
				// shape of the path - which is exactly the rule under test.
				hit({
					name: "couponBookSearch",
					uri: "file:///Users/x/Library/Developer/Xcode/DerivedData/NterWorkspace-abc/Build/Intermediates.noindex/GeneratedAssetSymbols/ImageResource.swift",
				}),
				hit({ name: "couponBookSearch", kind: 6, uri: `file://${IOS}/NterApp/Coupon/CouponBookSearch.swift` }),
			],
			"couponbooksearch",
			IOS,
		);
		expect(ranked[0].path).toBe("NterApp/Coupon/CouponBookSearch.swift");
	});

	test("assets are not code", () => {
		const ranked = rankSymbols(
			[
				hit({ name: "PromotionHub", uri: `file://${IOS}/Assets/OG-PromotionHub.png` }),
				hit({ name: "PromotionHub", kind: 5, uri: `file://${IOS}/NterApp/PromotionHub.swift` }),
			],
			"promotionhub",
			IOS,
		);
		expect(ranked[0].path).toBe("NterApp/PromotionHub.swift");
	});

	test("one declaration built for three architectures is ONE row", () => {
		// 🗝 The index holds one unit per built arch and target, so on this project
		// every symbol arrives two or three times. The key is (name, kind, uri,
		// line) rather than name alone, so two genuinely distinct declarations of
		// the same name in one file both survive.
		const one = { name: "AppDelegateViewModel", kind: 5, uri: `file://${IOS}/NterApp/AppDelegateViewModel.swift` };
		const ranked = rankSymbols(
			[hit({ ...one, line: 44 }), hit({ ...one, line: 44 }), hit({ ...one, line: 44 }), hit({ ...one, line: 90 })],
			"appdelegateviewmodel",
			IOS,
		);
		expect(ranked).toHaveLength(2);
		expect(ranked.map((r) => r.line).sort((a, b) => a - b)).toEqual([44, 90]);
	});

	test("a definition in ANOTHER checkout keeps its absolute path", () => {
		// The index answers with paths from the tree that was built, which on this
		// machine is routinely a different AO worktree of the same iOS app. Dropping
		// those rows, or relativising them against the wrong root, is how ⌘⇧O grows
		// a dead row that opens nothing.
		const ranked = rankSymbols(
			[
				hit({
					name: "DisposeBag",
					kind: 5,
					uri: "file:///Users/x/.ao/data/worktrees/nter/other/Pods/RxSwift/DisposeBag.swift",
				}),
			],
			"disposebag",
			IOS,
		);
		expect(ranked[0].path).toBe("/Users/x/.ao/data/worktrees/nter/other/Pods/RxSwift/DisposeBag.swift");
	});
});
