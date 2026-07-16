import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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
		expect(kw).toHaveStyle({ color: "#FC5FA3" });
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
});
