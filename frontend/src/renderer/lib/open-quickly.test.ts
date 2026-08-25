import { describe, expect, it } from "vitest";
import { normalizeIndexPath, rankFiles, scoreFile } from "./open-quickly";

/**
 * A fixture with the SHAPE of a real project rather than a handful of happy
 * paths: hand-written Swift and Go beside the generated tree that outranked it
 * in the spike, assets whose names read like source, and two spellings of one
 * file. Every assertion below is one of the four ranking rules the spike
 * measured (see `open-quickly.ts`).
 */
const FIXTURE = [
	"PromotionHub/PromotionHubViewController.swift",
	"PromotionHub/PromotionHubViewModel.swift",
	"PromotionHub/Views/PromoCell.swift",
	"Resources/Images/OG-Promotion-Hub 2.png",
	"Resources/Images/OG-Promotion-Hub.png",
	"Resources/Localizable.strings",
	"DerivedData/Build/Products/GeneratedAssetSymbols/PromotionHubImages.swift",
	"DerivedData/Build/Intermediates/PromotionHubViewController.o",
	"Pods/Alamofire/Source/Alamofire.swift",
	"backend/internal/service/session/workspace_file.go",
	"backend/internal/httpd/controllers/sessions.go",
	"backend/internal/storage/sqlite/gen/queries.sql.go",
	"backend/internal/api/api.pb.go",
	"frontend/src/renderer/components/SessionView.tsx",
	"frontend/src/renderer/components/OpenQuicklyPalette.tsx",
	"frontend/src/renderer/routeTree.gen.ts",
	"frontend/node_modules/react/index.js",
	"package-lock.json",
	"README.md",
];

const paths = (query: string, limit = 8) => rankFiles(FIXTURE, query, limit).map((m) => m.path);
const rankOf = (query: string, path: string) => paths(query, FIXTURE.length).indexOf(path);

describe("rule 1 — a hit that starts the name beats one scattered through it", () => {
	// The spike's own regression: without this rule `OG-Promotion-Hub 2.png`
	// outranked `PromotionHubViewController.swift` for "promohub".
	it("puts PromotionHubViewController.swift above OG-Promotion-Hub 2.png for promohub", () => {
		expect(rankOf("promohub", "PromotionHub/PromotionHubViewController.swift")).toBeLessThan(
			rankOf("promohub", "Resources/Images/OG-Promotion-Hub 2.png"),
		);
		// The whole visible top of the list is hand-written Swift, which is the
		// state the spike was comparing against Xcode — not just one row above one.
		expect(paths("promohub", 3)).toEqual([
			"PromotionHub/PromotionHubViewModel.swift",
			"PromotionHub/PromotionHubViewController.swift",
			"DerivedData/Build/Products/GeneratedAssetSymbols/PromotionHubImages.swift",
		]);
	});

	it("scores a prefix hit above the same characters scattered through a longer name", () => {
		const query = "promohub";
		const prefix = scoreFile("PromotionHubViewController.swift", query, query);
		const scattered = scoreFile("OG-Promotion-Hub 2.swift", query, query);
		expect(prefix?.score).toBeGreaterThan(scattered!.score);
	});

	it("ranks a basename hit above a hit found only in the directory part", () => {
		// "session" is in the directory of workspace_file.go and in the NAME of
		// SessionView.tsx; the named one wins regardless of the rest of the score.
		expect(rankOf("session", "frontend/src/renderer/components/SessionView.tsx")).toBeLessThan(
			rankOf("session", "backend/internal/service/session/workspace_file.go"),
		);
	});

	it("still finds a file whose query only matches its directory", () => {
		expect(paths("controllerssessions")).toContain("backend/internal/httpd/controllers/sessions.go");
	});

	it("puts an exact filename first", () => {
		expect(paths("README.md")[0]).toBe("README.md");
	});
});

describe("rule 2 — assets are not code", () => {
	it("prefers the .swift file to the .png when both match the name equally", () => {
		const query = "hub";
		const code = scoreFile("Hub.swift", query, query)!.score;
		const asset = scoreFile("Hub.png", query, query)!.score;
		expect(code).toBeGreaterThan(asset);
	});

	it("keeps a lock file below source for a query both match", () => {
		expect(rankOf("package", "package-lock.json")).toBeGreaterThan(-1);
		const code = scoreFile("package.go", "package", "package")!.score;
		const data = scoreFile("package-lock.json", "package", "package")!.score;
		expect(code).toBeGreaterThan(data);
	});
});

describe("rule 3 — generated output is demoted by path shape", () => {
	it("ranks the hand-written PromotionHub file above the DerivedData one", () => {
		expect(rankOf("promotionhub", "PromotionHub/PromotionHubViewController.swift")).toBeLessThan(
			rankOf("promotionhub", "DerivedData/Build/Products/GeneratedAssetSymbols/PromotionHubImages.swift"),
		);
	});

	it("demotes node_modules and Pods, which are indistinguishable from source by prefix alone", () => {
		const source = scoreFile("src/react/index.js", "index", "index")!.score;
		const generated = scoreFile("node_modules/react/index.js", "index", "index")!.score;
		expect(source).toBeGreaterThan(generated);
	});

	it("demotes a generated FILE name even outside a generated directory", () => {
		const hand = scoreFile("frontend/src/renderer/routeTree.ts", "routetree", "routetree")!.score;
		const generated = scoreFile("frontend/src/renderer/routeTree.gen.ts", "routetree", "routetree")!.score;
		expect(hand).toBeGreaterThan(generated);
	});

	it("does not treat build.gradle as build output — the rule matches SEGMENTS", () => {
		const plain = scoreFile("app/build.gradle", "build", "build")!.score;
		const output = scoreFile("app/build/out.gradle", "build", "build")!.score;
		expect(plain).toBeGreaterThan(output);
	});
});

describe("rule 4 — dedupe", () => {
	it("shows one row for a file the index spelled two ways", () => {
		const ranked = rankFiles(["./pkg/a.go", "pkg/a.go", "pkg//a.go"], "a.go");
		expect(ranked.map((m) => m.path)).toEqual(["pkg/a.go"]);
	});

	it("normalises the shapes the index can produce", () => {
		expect(normalizeIndexPath("./a//b/c.go")).toBe("a/b/c.go");
		expect(normalizeIndexPath("a\\b\\c.go")).toBe("a/b/c.go");
	});
});

describe("query handling", () => {
	it("returns nothing for an empty or whitespace-only query", () => {
		expect(rankFiles(FIXTURE, "")).toEqual([]);
		expect(rankFiles(FIXTURE, "   ")).toEqual([]);
	});

	it("ignores spaces inside the query, so `promo hub` is `promohub`", () => {
		expect(paths("promo hub")[0]).toBe(paths("promohub")[0]);
	});

	it("is case-insensitive but rewards capitals the user typed", () => {
		expect(paths("PROMOHUB", 3)).toEqual(paths("promohub", 3));
		const typed = scoreFile("PromoCell.swift", "PC", "pc")!.score;
		const untyped = scoreFile("PromoCell.swift", "pc", "pc")!.score;
		expect(typed).toBeGreaterThan(untyped);
	});

	it("matches a camel-hump acronym", () => {
		expect(paths("pcell")).toContain("PromotionHub/Views/PromoCell.swift");
		expect(paths("pvc")[0]).toBe("PromotionHub/PromotionHubViewController.swift");
	});

	it("returns nothing when the query cannot be read out of any path", () => {
		expect(rankFiles(FIXTURE, "zzqqx")).toEqual([]);
	});

	it("honours the limit", () => {
		expect(rankFiles(FIXTURE, "s", 3)).toHaveLength(3);
	});
});

describe("highlight positions", () => {
	it("reports the matched characters, in order, as indices into the path", () => {
		const match = scoreFile("PromotionHub/PromoCell.swift", "promocell", "promocell")!;
		expect(match.positions).toHaveLength("promocell".length);
		expect([...match.positions].sort((a, b) => a - b)).toEqual(match.positions);
		expect(match.positions.map((p) => "PromotionHub/PromoCell.swift"[p].toLowerCase()).join("")).toBe("promocell");
	});

	it("prefers the word-boundary alignment a greedy walk would miss", () => {
		// Greedy takes the `h` of `Hub`'s neighbours first; the scorer holds out
		// for the capital H, which is what makes rule 1 work at all.
		const match = scoreFile("PromotionHubViewController.swift", "promohub", "promohub")!;
		expect(match.positions).toEqual([0, 1, 2, 3, 4, 9, 10, 11]);
	});
});

describe("scale", () => {
	it("ranks a 20 000-path index without falling over", () => {
		const big = Array.from({ length: 20000 }, (_, i) => `pkg/mod${i % 97}/file_${i}_handler.go`);
		const ranked = rankFiles([...big, "PromotionHub/PromotionHubViewController.swift"], "promohub");
		expect(ranked[0].path).toBe("PromotionHub/PromotionHubViewController.swift");
	});
});
