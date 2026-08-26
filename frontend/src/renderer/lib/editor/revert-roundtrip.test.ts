import { describe, expect, it } from "vitest";
import type { components } from "../../../api/schema";
import { hunksOf } from "./change-lanes";
import { revertEdit } from "./revert";

type Diff = components["schemas"]["DiffContextResponse"];
type Line = components["schemas"]["DiffContextLineDTO"];

/**
 * The property Discard Change actually promises, asserted end to end rather
 * than one hunk at a time.
 *
 * 🗝 `revert.test.ts` pins each shape of edit on its own, with the base text
 * written out by hand beside it. That cannot catch the failure that matters
 * here: `hunksOf` and `revertEdit` DISAGREEING about where a hunk sits. A hunk
 * classified one line off still reverts to something, and a hand-written
 * expectation written from the same misreading agrees with it. Deriving both
 * sides from ONE payload and demanding the working tree come back byte-identical
 * to the base is what closes that seam — it is the unit-level statement of "git
 * diff is empty afterwards".
 *
 * Reverts are applied LAST hunk first, because an edit that changes the line
 * count invalidates the coordinates of every hunk below it — which is also the
 * order the popover has to use when a reader discards several in a row.
 */
function diff(build: (b: Builder) => void, over: Partial<Diff> = {}): Diff {
	const lines: Line[] = [];
	let oldN = 1;
	let newN = 1;
	const b: Builder = {
		ctx(...texts) {
			for (const text of texts) lines.push({ kind: "context", text, oldLine: oldN++, newLine: newN++ });
		},
		del(...texts) {
			for (const text of texts) lines.push({ kind: "del", text, oldLine: oldN++, newLine: 0 });
		},
		add(...texts) {
			for (const text of texts) lines.push({ kind: "add", text, oldLine: 0, newLine: newN++ });
		},
	};
	build(b);
	return { available: true, truncated: false, mode: "file", path: "a.ts", lines, ...over };
}

type Builder = {
	ctx(...texts: string[]): void;
	del(...texts: string[]): void;
	add(...texts: string[]): void;
};

/** The file as it is at the diff's base — what discarding everything must restore. */
function baseText(d: Diff): string {
	return d.lines
		.filter((l) => l.oldLine > 0)
		.map((l) => l.text)
		.join("\n");
}

/** The file as the editor has it open — the diff's new side. */
function workingText(d: Diff): string {
	return d.lines
		.filter((l) => l.newLine > 0 && l.kind !== "hunk")
		.map((l) => l.text)
		.join("\n");
}

/** Apply one revert to a plain string, the way Monaco applies it to the model. */
function applyRevert(text: string, edit: ReturnType<typeof revertEdit>): string {
	const lines = text.split("\n");
	const offset = (line: number, column: number) => {
		let at = 0;
		for (let i = 1; i < line; i++) at += (lines[i - 1] ?? "").length + 1;
		return at + column - 1;
	};
	return (
		text.slice(0, offset(edit.startLine, edit.startColumn)) +
		edit.text +
		text.slice(offset(edit.endLine, edit.endColumn))
	);
}

/** Discard every hunk in the file, bottom-up, and hand back what is left. */
function discardEverything(d: Diff): string {
	let text = workingText(d);
	for (const hunk of hunksOf(d).reverse()) {
		const lines = text.split("\n");
		text = applyRevert(
			text,
			revertEdit(hunk, lines.length, (line) => (lines[line - 1] ?? "").length + 1),
		);
	}
	return text;
}

describe("discarding every hunk restores the base exactly", () => {
	it("a modified run in the middle of the file", () => {
		const d = diff((b) => {
			b.ctx("package app");
			b.del("func Run() {");
			b.add("func Run(ctx context.Context) {");
			b.ctx("}");
		});

		expect(discardEverything(d)).toBe(baseText(d));
	});

	// 🗝 The end-of-file cases are where whole-line reverting goes wrong: there
	// is no following newline to consume, so the preceding one has to go instead.
	it("an addition at end of file", () => {
		const d = diff((b) => {
			b.ctx("a", "b");
			b.add("appended by an agent");
		});

		expect(discardEverything(d)).toBe(baseText(d));
	});

	it("a deletion at end of file", () => {
		const d = diff((b) => {
			b.ctx("a", "b");
			b.del("the tail someone removed");
		});

		expect(discardEverything(d)).toBe(baseText(d));
	});

	it("an addition on the very first line", () => {
		const d = diff((b) => {
			b.add("// a new header comment");
			b.ctx("a", "b");
		});

		expect(discardEverything(d)).toBe(baseText(d));
	});

	it("a one-line file, replaced outright", () => {
		const d = diff((b) => {
			b.del("was");
			b.add("now");
		});

		expect(discardEverything(d)).toBe(baseText(d));
	});

	// Several hunks in one file is the case that exposes coordinate drift: revert
	// the top one first and every hunk below it has moved.
	it("several hunks at once, reverted bottom-up", () => {
		const d = diff((b) => {
			b.ctx("one");
			b.del("two");
			b.add("TWO", "TWO-AND-A-HALF");
			b.ctx("three");
			b.add("THREE-AND-A-HALF");
			b.ctx("four");
			b.del("five");
			b.ctx("six");
		});

		expect(discardEverything(d)).toBe(baseText(d));
	});

	/**
	 * The branch-under-review shape: nearly every line changed. This is the file
	 * the two gutter lanes were redesigned for, and it is also the one where a
	 * whole-file revert has no surrounding context to be forgiving.
	 */
	it("a file where every line changed", () => {
		const d = diff((b) => {
			b.del("alpha", "beta", "gamma");
			b.add("ALPHA", "BETA", "GAMMA");
		});

		expect(discardEverything(d)).toBe(baseText(d));
	});

	it("a file the branch only added lines to, from empty", () => {
		const d = diff((b) => {
			b.add("first", "second");
		});

		expect(discardEverything(d)).toBe(baseText(d));
	});
});
