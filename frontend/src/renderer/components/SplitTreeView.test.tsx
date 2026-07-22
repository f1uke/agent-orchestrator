import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SplitTreeView } from "./SplitTreeView";
import { leaf, MIN_PANE_HEIGHT, MIN_PANE_WIDTH, SPLIT_HANDLE_SIZE, type SplitNode } from "../lib/split-layout";

// jsdom has no layout engine; render panels as plain boxes.
vi.mock("./ui/resizable", () => ({
	ResizablePanelGroup: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
	ResizableHandle: () => <div data-testid="split-handle" />,
	ResizablePanel: ({ children, id }: { children?: ReactNode; id: string }) => (
		<div data-testid={`panel-${id}`}>{children}</div>
	),
}));

// a | (b / c)
const tree: SplitNode = {
	kind: "split",
	orientation: "horizontal",
	ratio: 0.5,
	first: leaf("a"),
	second: { kind: "split", orientation: "vertical", ratio: 0.5, first: leaf("b"), second: leaf("c") },
};

const onFocusPane = vi.fn();
const onRatioChange = vi.fn();

function renderTree(focused = "a") {
	return render(
		<SplitTreeView
			focusedSessionId={focused}
			onFocusPane={onFocusPane}
			onRatioChange={onRatioChange}
			renderPane={(sessionId, isFocused) => (
				<div data-testid={`pane-${sessionId}`}>
					{isFocused ? "focused" : "blurred"}
					<div data-split-pane-controls>
						<button data-testid={`control-${sessionId}`} type="button">
							pane control
						</button>
					</div>
				</div>
			)}
			root={tree}
		/>,
	);
}

// Give the panes real geometry (a 2x2-ish arrangement: a left, b top-right,
// c bottom-right) so the geometric focus movement has rects to measure.
function layoutRects() {
	const boxes: Record<string, DOMRect> = {
		a: { left: 0, top: 0, width: 400, height: 600, right: 400, bottom: 600, x: 0, y: 0, toJSON: () => "" },
		b: { left: 401, top: 0, width: 400, height: 300, right: 801, bottom: 300, x: 401, y: 0, toJSON: () => "" },
		c: { left: 401, top: 301, width: 400, height: 300, right: 801, bottom: 601, x: 401, y: 301, toJSON: () => "" },
	};
	for (const el of Array.from(document.querySelectorAll<HTMLElement>("[data-split-pane]"))) {
		const rect = boxes[el.dataset.splitPane as string];
		el.getBoundingClientRect = () => rect;
	}
}

beforeEach(() => {
	onFocusPane.mockReset();
	onRatioChange.mockReset();
});

describe("SplitTreeView", () => {
	it("renders one pane per leaf and tells each whether it is focused", () => {
		renderTree("b");
		expect(screen.getByTestId("pane-a")).toHaveTextContent("blurred");
		expect(screen.getByTestId("pane-b")).toHaveTextContent("focused");
		expect(screen.getByTestId("pane-c")).toHaveTextContent("blurred");
	});

	it("focuses an unfocused pane on mousedown, and leaves the focused one alone", () => {
		renderTree("a");
		fireEvent.mouseDown(screen.getByTestId("pane-b"));
		expect(onFocusPane).toHaveBeenCalledWith("b");

		onFocusPane.mockReset();
		fireEvent.mouseDown(screen.getByTestId("pane-a"));
		expect(onFocusPane).not.toHaveBeenCalled();
	});

	it("does NOT move focus when the press lands on the pane's own controls", () => {
		// Focusing on the control press would flip the toolbar mid-gesture and
		// swallow the click — the control must work on the first press.
		renderTree("a");
		fireEvent.mouseDown(screen.getByTestId("control-b"));
		expect(onFocusPane).not.toHaveBeenCalled();
	});

	it("floors the scrollable region at the tree's required extent", () => {
		const { container } = renderTree();
		const scroller = container.firstElementChild as HTMLElement;
		expect(scroller.className).toContain("overflow-auto");
		const inner = scroller.firstElementChild as HTMLElement;
		// a | (b / c): two pane widths across, two pane heights down the right column.
		expect(inner.style.minWidth).toBe(`${MIN_PANE_WIDTH * 2 + SPLIT_HANDLE_SIZE}px`);
		expect(inner.style.minHeight).toBe(`${MIN_PANE_HEIGHT * 2 + SPLIT_HANDLE_SIZE}px`);
	});

	it("moves focus geometrically with cmd/ctrl+alt+arrows", () => {
		renderTree("a");
		layoutRects();

		fireEvent.keyDown(window, { key: "ArrowRight", metaKey: true, altKey: true });
		expect(onFocusPane).toHaveBeenCalledWith("b");

		onFocusPane.mockReset();
		fireEvent.keyDown(window, { key: "ArrowLeft", metaKey: true, altKey: true });
		expect(onFocusPane).not.toHaveBeenCalled(); // already at the left edge
	});

	it("ignores arrows without the full modifier chord", () => {
		renderTree("a");
		layoutRects();
		fireEvent.keyDown(window, { key: "ArrowRight", metaKey: true });
		fireEvent.keyDown(window, { key: "ArrowRight", altKey: true });
		expect(onFocusPane).not.toHaveBeenCalled();
	});

	it("marks only the focused pane with the accent ring overlay", () => {
		renderTree("b");
		const focusedWrapper = document.querySelector('[data-split-pane="b"]')!;
		const blurredWrapper = document.querySelector('[data-split-pane="a"]')!;
		expect(focusedWrapper.querySelector(".pointer-events-none")).not.toBeNull();
		expect(blurredWrapper.querySelector(".pointer-events-none")).toBeNull();
	});
});
