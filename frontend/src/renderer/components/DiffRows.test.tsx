import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { TOKEN_COLORS } from "../lib/comment-inbox";
import { DiffRows } from "./DiffRows";

const lines = [
	{ kind: "context", oldLine: 10, newLine: 10, text: "func Foo() {" },
	{ kind: "del", oldLine: 11, newLine: 0, text: "  return old" },
	{ kind: "add", oldLine: 0, newLine: 11, text: "  return next" },
];

describe("DiffRows", () => {
	it("renders +/- signs and syntax-highlights the code text", () => {
		render(<DiffRows lines={lines} size="narrow" />);
		// keyword `func` is tokenized into its own colored span
		const kw = screen.getByText("func");
		expect(kw.tagName.toLowerCase()).toBe("span");
		expect(kw).toHaveStyle({ color: TOKEN_COLORS.keyword });
		// both the removed and added line texts render `return` as keyword spans
		expect(screen.getAllByText("return")).toHaveLength(2);
	});

	it("pins an anchor node after the given line index", () => {
		render(<DiffRows lines={lines} size="wide" anchorIndex={2} anchorNode={<div data-testid="anchor">comment</div>} />);
		expect(screen.getByTestId("anchor")).toBeInTheDocument();
	});

	it("renders an Xcode-style gutter change bar for marked lines", () => {
		const marks = new Map<number, "added" | "modified" | "removed">([
			[0, "modified"],
			[1, "added"],
			[2, "removed"],
		]);
		render(<DiffRows lines={lines} size="wide" changeMarks={marks} />);
		expect(screen.getByTestId("change-bar-0")).toHaveAttribute("data-change", "modified");
		expect(screen.getByTestId("change-bar-1")).toHaveAttribute("data-change", "added");
		expect(screen.getByTestId("change-bar-2")).toHaveAttribute("data-change", "removed");
	});

	it("renders no change bar when changeMarks is absent (Reviews path unchanged)", () => {
		render(<DiffRows lines={lines} size="narrow" />);
		expect(screen.queryByTestId("change-bar-0")).toBeNull();
	});

	it("renders no hunk separator when every line is adjacent", () => {
		render(<DiffRows lines={lines} size="wide" />);
		expect(screen.queryByTestId("hunk-separator")).toBeNull();
	});

	it("separates two distant regions, naming the range and how many lines are hidden", () => {
		const gapped = [
			{ kind: "context", oldLine: 13, newLine: 13, text: "  d := 5" },
			{ kind: "context", oldLine: 14, newLine: 14, text: "  e := 6" },
			{ kind: "hunk", oldLine: 80, newLine: 80, text: "@@ -80,5 +80,5 @@ func second() {" },
			{ kind: "context", oldLine: 80, newLine: 80, text: "  p := 1" },
		];
		render(<DiffRows lines={gapped} size="wide" />);
		const sep = screen.getByTestId("hunk-separator");
		expect(sep).toHaveTextContent("@@ -80,5 +80,5 @@");
		// git's section heading orients the reader without re-reading the code
		expect(sep).toHaveTextContent("func second() {");
		// lines 15..79 are skipped
		expect(sep).toHaveTextContent("65 lines hidden");
		// chrome, not content: no +/- sign glyph and no add/del row tint
		expect(sep.textContent).not.toMatch(/[+-]\s*$/);
	});

	it("marks lines skipped before a diff that does not start at line 1", () => {
		const midFile = [
			{ kind: "hunk", oldLine: 40, newLine: 40, text: "@@ -40,3 +40,3 @@ func mid() {" },
			{ kind: "context", oldLine: 40, newLine: 40, text: "  a := 1" },
		];
		render(<DiffRows lines={midFile} size="wide" />);
		expect(screen.getByTestId("hunk-separator")).toHaveTextContent("39 lines hidden");
	});

	it("counts hidden lines from the old side when the new side is empty (deleted region)", () => {
		const deleted = [
			{ kind: "del", oldLine: 5, newLine: 0, text: "  gone" },
			{ kind: "hunk", oldLine: 60, newLine: 0, text: "@@ -60,2 +0,0 @@" },
			{ kind: "del", oldLine: 60, newLine: 0, text: "  also gone" },
		];
		render(<DiffRows lines={deleted} size="wide" />);
		expect(screen.getByTestId("hunk-separator")).toHaveTextContent("54 lines hidden");
	});

	it("uses the singular for a one-line gap", () => {
		const gapped = [
			{ kind: "context", oldLine: 10, newLine: 10, text: "  a" },
			{ kind: "hunk", oldLine: 12, newLine: 12, text: "@@ -12,1 +12,1 @@" },
			{ kind: "context", oldLine: 12, newLine: 12, text: "  b" },
		];
		render(<DiffRows lines={gapped} size="wide" />);
		expect(screen.getByTestId("hunk-separator")).toHaveTextContent("1 line hidden");
	});
});
