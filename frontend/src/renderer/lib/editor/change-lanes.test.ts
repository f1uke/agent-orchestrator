import { describe, expect, it } from "vitest";
import type { components } from "../../../api/schema";
import { branchLaneLines, firstHunkLine, hunksOf, originalTextFrom } from "./change-lanes";

type Diff = components["schemas"]["DiffContextResponse"];
type Line = components["schemas"]["DiffContextLineDTO"];

/**
 * A tiny diff builder that tracks old and new cursors separately, exactly as
 * the backend parser does. Building fixtures by hand is how the line numbering
 * quietly goes wrong once a hunk has added a net line.
 */
function diff(build: (b: Builder) => void, over: Partial<Diff> = {}): Diff {
	const lines: Line[] = [];
	let oldN = 1;
	let newN = 1;
	const b: Builder = {
		ctx(count) {
			for (let i = 0; i < count; i++)
				lines.push({ kind: "context", text: `ctx${oldN}`, oldLine: oldN++, newLine: newN++ });
		},
		del(...texts) {
			for (const text of texts) lines.push({ kind: "del", text, oldLine: oldN++, newLine: 0 });
		},
		add(...texts) {
			for (const text of texts) lines.push({ kind: "add", text, oldLine: 0, newLine: newN++ });
		},
		skip() {
			lines.push({ kind: "hunk", text: "@@", oldLine: oldN, newLine: newN });
		},
	};
	build(b);
	return { available: true, truncated: false, mode: "file", path: "a.ts", lines, ...over };
}

type Builder = {
	ctx(count: number): void;
	del(...texts: string[]): void;
	add(...texts: string[]): void;
	skip(): void;
};

const EMPTY: Diff = { available: false, truncated: false, mode: "file", path: "a.ts", lines: [] };

describe("hunksOf", () => {
	it("classifies a replacement as modified, over the new-side lines", () => {
		const d = diff((b) => {
			b.ctx(3); // 1-3
			b.del("was a", "was b");
			b.add("now a"); // 4
			b.ctx(2);
		});

		expect(hunksOf(d)).toEqual([{ start: 4, end: 4, kind: "modified", oldText: ["was a", "was b"] }]);
	});

	it("classifies a pure insertion as added, with no old text", () => {
		const d = diff((b) => {
			b.ctx(2);
			b.add("new one", "new two"); // 3-4
			b.ctx(1);
		});

		expect(hunksOf(d)).toEqual([{ start: 3, end: 4, kind: "added", oldText: [] }]);
	});

	// A deletion has no line of its own. The marker sits ON the new-side line the
	// removed content used to precede, so start === end.
	it("classifies a pure deletion as a zero-height marker on the following line", () => {
		const d = diff((b) => {
			b.ctx(2); // 1-2
			b.del("gone");
			b.ctx(2); // 3-4
		});

		expect(hunksOf(d)).toEqual([{ start: 3, end: 3, kind: "removed", oldText: ["gone"] }]);
	});

	// At EOF there IS no following line, so the marker sits one past the last.
	it("puts an end-of-file deletion one line past the end", () => {
		const d = diff((b) => {
			b.ctx(2); // 1-2
			b.del("last");
		});

		expect(hunksOf(d)).toEqual([{ start: 3, end: 3, kind: "removed", oldText: ["last"] }]);
	});

	it("keeps separate runs separate, and survives a skip marker between them", () => {
		const d = diff((b) => {
			b.ctx(1);
			b.add("first"); // 2
			b.ctx(1);
			b.skip();
			b.ctx(1);
			b.del("second was");
			b.add("second now");
			b.ctx(1);
		});

		expect(hunksOf(d).map((h) => h.kind)).toEqual(["added", "modified"]);
		expect(hunksOf(d)[0].start).toBe(2);
		expect(hunksOf(d)[1].start).toBeGreaterThan(2);
	});

	it("handles a change on the very first line", () => {
		const d = diff((b) => {
			b.del("old first");
			b.add("new first"); // 1
			b.ctx(2);
		});

		expect(hunksOf(d)).toEqual([{ start: 1, end: 1, kind: "modified", oldText: ["old first"] }]);
	});

	it("returns nothing for an unavailable or empty diff", () => {
		expect(hunksOf(EMPTY)).toEqual([]);
		expect(hunksOf(undefined)).toEqual([]);
	});
});

describe("branchLaneLines", () => {
	it("marks every new-side line a hunk covers, and the boundary line of a deletion", () => {
		const d = diff((b) => {
			b.ctx(2);
			b.add("a", "b"); // 3, 4
			b.ctx(1); // 5
			b.del("gone"); // marker on 6
			b.ctx(2);
		});

		expect(branchLaneLines(d)).toEqual([3, 4, 6]);
	});

	it("is empty for a file the branch did not touch", () => {
		expect(branchLaneLines(EMPTY)).toEqual([]);
	});
});

describe("firstHunkLine", () => {
	it("is the first changed new-side line, not line 1", () => {
		const d = diff((b) => {
			b.ctx(40);
			b.add("finally"); // 41
		});

		expect(firstHunkLine(d)).toBe(41);
	});

	it("is null when there is nothing to land on", () => {
		expect(firstHunkLine(EMPTY)).toBeNull();
		expect(firstHunkLine(undefined)).toBeNull();
	});
});

describe("originalTextFrom", () => {
	it("replays every old-side row, context and deletions alike", () => {
		const d = diff((b) => {
			b.ctx(2); // ctx1, ctx2
			b.del("was three");
			b.add("is three");
			b.ctx(1); // ctx4
		});

		expect(originalTextFrom(d)).toBe("ctx1\nctx2\nwas three\nctx4");
	});

	// 🗝 The guard that matters. A payload the server truncated, or one windowed
	// to three lines of context, is MISSING lines — replaying it would produce a
	// confidently wrong "original" and a diff editor full of invented changes.
	it("refuses a truncated payload rather than inventing the missing lines", () => {
		const d = diff(
			(b) => {
				b.ctx(2);
				b.add("x");
			},
			{ truncated: true },
		);

		expect(originalTextFrom(d)).toBeNull();
	});

	it("refuses a windowed payload, which a skip marker identifies", () => {
		const d = diff((b) => {
			b.ctx(1);
			b.add("x");
			b.skip();
			b.ctx(1);
			b.add("y");
		});

		expect(originalTextFrom(d)).toBeNull();
	});

	it("is null for an unavailable diff", () => {
		expect(originalTextFrom(EMPTY)).toBeNull();
		expect(originalTextFrom(undefined)).toBeNull();
	});
});
