import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	consumeSplitDragClick,
	DROP_PANE_ATTR,
	registerSplitDropHandler,
	startSplitDrag,
	useSplitDragStore,
} from "./split-drag";
import type { DragSource } from "./split-layout";

// A pane element the hit-test can land on, with a fixed rect (jsdom has no
// layout, so getBoundingClientRect is stubbed).
function makePane(sessionId: string, rect: { left: number; top: number; width: number; height: number }): HTMLElement {
	const el = document.createElement("div");
	el.setAttribute(DROP_PANE_ATTR, sessionId);
	el.getBoundingClientRect = () =>
		({
			left: rect.left,
			top: rect.top,
			right: rect.left + rect.width,
			bottom: rect.top + rect.height,
			width: rect.width,
			height: rect.height,
			x: rect.left,
			y: rect.top,
			toJSON: () => "",
		}) as DOMRect;
	document.body.appendChild(el);
	return el;
}

function pointerEvent(type: string, clientX: number, clientY: number, button = 0): PointerEvent {
	return Object.assign(new Event(type, { bubbles: true, cancelable: true }), {
		clientX,
		clientY,
		button,
		pointerId: 1,
	}) as unknown as PointerEvent;
}

function reactPointerDown(clientX: number, clientY: number) {
	return { button: 0, clientX, clientY, pointerId: 1 } as unknown as React.PointerEvent;
}

const paneSource: DragSource = { kind: "pane", sessionId: "a" };

beforeEach(() => {
	useSplitDragStore.setState({ drag: null, pointer: null, hover: null });
	document.body.innerHTML = "";
});

afterEach(() => {
	// Flush the click-suppression timeout and any dangling gesture.
	vi.useRealTimers();
});

describe("startSplitDrag threshold", () => {
	it("does NOT begin a drag for a press that stays within the threshold (a click)", () => {
		makePane("b", { left: 0, top: 0, width: 400, height: 400 });
		startSplitDrag(paneSource, "A", reactPointerDown(10, 10));
		window.dispatchEvent(pointerEvent("pointermove", 12, 12));
		expect(useSplitDragStore.getState().drag).toBeNull();
		window.dispatchEvent(pointerEvent("pointerup", 12, 12));
		expect(consumeSplitDragClick()).toBe(false); // click proceeds normally
	});

	it("begins a drag once the pointer passes the threshold, tracking pointer + hover", () => {
		makePane("b", { left: 100, top: 0, width: 400, height: 400 });
		startSplitDrag(paneSource, "A", reactPointerDown(10, 10));
		// Move well past the threshold, into pane b's centre (swap zone).
		window.dispatchEvent(pointerEvent("pointermove", 300, 200));
		const state = useSplitDragStore.getState();
		expect(state.drag?.source).toEqual(paneSource);
		expect(state.hover).toEqual({ sessionId: "b", zone: "center" });
	});
});

describe("startSplitDrag drop", () => {
	it("calls the registered drop handler with the pane + zone under the release point", () => {
		makePane("b", { left: 0, top: 0, width: 400, height: 400 });
		const onDrop = vi.fn();
		const unregister = registerSplitDropHandler(onDrop);

		startSplitDrag(paneSource, "A", reactPointerDown(10, 10));
		window.dispatchEvent(pointerEvent("pointermove", 380, 200)); // right edge strip
		window.dispatchEvent(pointerEvent("pointerup", 380, 200));

		expect(onDrop).toHaveBeenCalledWith(paneSource, "b", "right");
		expect(useSplitDragStore.getState().drag).toBeNull();
		unregister();
	});

	it("suppresses exactly one click after a real drag", () => {
		makePane("b", { left: 0, top: 0, width: 400, height: 400 });
		startSplitDrag(paneSource, "A", reactPointerDown(10, 10));
		window.dispatchEvent(pointerEvent("pointermove", 300, 200));
		window.dispatchEvent(pointerEvent("pointerup", 300, 200));

		expect(consumeSplitDragClick()).toBe(true); // the drag's trailing click is eaten
		expect(consumeSplitDragClick()).toBe(false); // and only that one
	});

	it("does not drop when the release is outside every pane", () => {
		makePane("b", { left: 0, top: 0, width: 100, height: 100 });
		const onDrop = vi.fn();
		const unregister = registerSplitDropHandler(onDrop);

		startSplitDrag(paneSource, "A", reactPointerDown(10, 10));
		window.dispatchEvent(pointerEvent("pointermove", 500, 500)); // nowhere near pane b
		window.dispatchEvent(pointerEvent("pointerup", 500, 500));

		expect(onDrop).not.toHaveBeenCalled();
		unregister();
	});
});

describe("registerSplitDropHandler", () => {
	it("unregister only clears its own handler", () => {
		const first = vi.fn();
		const second = vi.fn();
		const unregisterFirst = registerSplitDropHandler(first);
		registerSplitDropHandler(second);
		unregisterFirst(); // stale unregister must not clear `second`

		makePane("b", { left: 0, top: 0, width: 400, height: 400 });
		startSplitDrag(paneSource, "A", reactPointerDown(10, 10));
		window.dispatchEvent(pointerEvent("pointermove", 200, 200));
		window.dispatchEvent(pointerEvent("pointerup", 200, 200));
		expect(second).toHaveBeenCalled();
		expect(first).not.toHaveBeenCalled();
	});
});
