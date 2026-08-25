/**
 * ⌘⇧O ranking, measured against a REAL workspace index rather than a fixture
 * chosen to make the rules look right.
 *
 * `open-quickly.test.ts` pins each of the four ranking rules against a handful
 * of paths built to isolate it. That is the right shape for a rule, and it is
 * also the shape that cannot fail the way ranking actually fails in use: the
 * obvious answer is beaten by some unrelated file nobody thought to put in the
 * fixture. The distractors here were not invented — they are every path this
 * repository tracked when the fixture was taken, generated output and vendored
 * trees and 1 700-odd real names included.
 *
 * The fixture is FROZEN on purpose. Reading `git ls-files` at test time would
 * make these assertions move whenever a file is renamed, which is how a ranking
 * test quietly stops asserting anything. Refresh it deliberately:
 *
 *     git ls-files > frontend/src/renderer/lib/testdata/real-workspace-index.txt
 *
 * Assertions are about ORDER between named competitors, never about an exact
 * top-N list — the weights in `open-quickly.ts` are meant to be tunable, and a
 * test that pins the whole list would turn every tuning into a failure.
 */
import { describe, expect, it } from "vitest";
import fixture from "./testdata/real-workspace-index.txt?raw";
import { rankFiles } from "./open-quickly";

const PATHS = fixture.split("\n").filter(Boolean);

/** Where `path` sits in the ranking for `query`, or -1. Whole index, no cap. */
function rankOf(query: string, path: string): number {
	return rankFiles(PATHS, query, PATHS.length).findIndex((m) => m.path === path);
}

function top(query: string, limit = 1): string[] {
	return rankFiles(PATHS, query, limit).map((m) => m.path);
}

it("indexes a workspace big enough for the distractors to be real", () => {
	expect(PATHS.length).toBeGreaterThan(1500);
});

describe("the obvious answer wins on a real tree", () => {
	// Each of these is a name a person would actually type to reach that file,
	// against ~1 700 competitors rather than six.
	it.each([
		["sessionview", "frontend/src/renderer/components/SessionView.tsx"],
		["openquickly", "frontend/src/renderer/components/OpenQuicklyPalette.tsx"],
		["workspacefile", "frontend/src/renderer/components/WorkspaceFileView.tsx"],
		["sessions.go", "backend/internal/httpd/controllers/sessions.go"],
		["prsql", "backend/internal/storage/sqlite/queries/pr.sql"],
	])("puts %s first for %s", (query, path) => {
		expect(top(query)).toEqual([path]);
	});

	it("finds a file by its camel humps alone", () => {
		// `swf` is not a substring of anything — only the humps of
		// useWorkspaceFiles spell it, and a greedy walk would not prefer them.
		expect(top("swf")).toEqual(["frontend/src/renderer/hooks/useWorkspaceFiles.ts"]);
	});

	it("keeps the implementation above its own test file", () => {
		// Both basenames start with the query; the test file is strictly longer.
		// A tie broken the other way would put the test first for every source
		// file in the repository, which is the wrong default for a jump-to.
		expect(rankOf("sessionview", "frontend/src/renderer/components/SessionView.tsx")).toBeLessThan(
			rankOf("sessionview", "frontend/src/renderer/components/SessionView.test.tsx"),
		);
	});
});

describe("rule 3 — generated output loses to hand-written source", () => {
	// The case the spike's rule exists for, and this repository happens to hold
	// it exactly: two files named `db.go`, one written by a person and one
	// emitted by sqlc into `gen/`. The basenames are IDENTICAL, so nothing about
	// the name can separate them — only the shape of the path can, which is what
	// rule 3 claims to rank on.
	it("ranks the hand-written db.go above the generated one of the same name", () => {
		expect(rankOf("db.go", "backend/internal/storage/sqlite/db.go")).toBeLessThan(
			rankOf("db.go", "backend/internal/storage/sqlite/gen/db.go"),
		);
	});

	it("does not let sqlc output take the first row for a query it matches", () => {
		// `models` matches `gen/models.go` exactly by stem, and it still must not
		// be the answer while a hand-written file matches at all.
		expect(top("models")[0]).not.toContain("/gen/");
	});

	it("demotes every generated path below a hand-written match for prsql", () => {
		const ranked = rankFiles(PATHS, "prsql", 6).map((m) => m.path);
		expect(ranked[0]).not.toContain("/gen/");
		// The hand-written .sql queries outrank the .sql.go files sqlc wrote from
		// them, even though the generated names contain the query just as well.
		expect(rankOf("prsql", "backend/internal/storage/sqlite/queries/pr.sql")).toBeLessThan(
			rankOf("prsql", "backend/internal/storage/sqlite/gen/pr.sql.go"),
		);
	});
});

describe("rule 4 — dedupe, against real paths", () => {
	it("shows one row when the index spells a real path two ways", () => {
		const doubled = [...PATHS, "./frontend/src/renderer/components/SessionView.tsx"];
		const hits = rankFiles(doubled, "sessionview", 10).filter((m) => m.path.endsWith("components/SessionView.tsx"));
		expect(hits).toHaveLength(1);
	});
});

describe("what the palette is handed is always usable", () => {
	// Cheap structural invariants over every result of a broad query. They cost
	// one pass and they are what catches an offset bug on a real path shape:
	// `positions` is what the palette underlines, so a stale or out-of-range
	// index there is a visible defect, not an internal detail.
	it("returns real, in-order, in-range matches for every row", () => {
		for (const query of ["session", "test", "sql", "view", "e"]) {
			const results = rankFiles(PATHS, query, 50);
			expect(results.length).toBeGreaterThan(0);
			const known = new Set(PATHS);
			for (const match of results) {
				expect(known.has(match.path)).toBe(true);
				expect(match.positions).toHaveLength(query.length);
				for (let i = 0; i < match.positions.length; i++) {
					const at = match.positions[i];
					expect(at).toBeGreaterThanOrEqual(0);
					expect(at).toBeLessThan(match.path.length);
					if (i > 0) expect(at).toBeGreaterThan(match.positions[i - 1]);
					// The character underlined must be the character typed.
					expect(match.path[at].toLowerCase()).toBe(query[i].toLowerCase());
				}
			}
		}
	});

	it("scores descending, so the first row really is the best one", () => {
		const results = rankFiles(PATHS, "session", 50);
		for (let i = 1; i < results.length; i++) {
			expect(results[i - 1].score).toBeGreaterThanOrEqual(results[i].score);
		}
	});

	it("is deterministic — the same query twice is the same list", () => {
		expect(rankFiles(PATHS, "store", 20).map((m) => m.path)).toEqual(rankFiles(PATHS, "store", 20).map((m) => m.path));
	});
});
