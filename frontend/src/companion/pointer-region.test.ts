import { describe, expect, it, beforeEach, vi } from "vitest";
import { FIGURE_ATTRIBUTE, createInteractionTracker, isOverPet } from "./pointer-region";

function build(html: string): HTMLElement {
	document.body.innerHTML = html;
	return document.body;
}

beforeEach(() => {
	document.body.innerHTML = "";
});

describe("isOverPet", () => {
	it("is false over the empty band, which is nearly all of the overlay", () => {
		const body = build(`<div class="companion-stage" id="empty"></div>`);

		expect(isOverPet(body.querySelector("#empty"))).toBe(false);
	});

	it("is false over a Proc's own bounding box", () => {
		// THE BUG. The wrapper is the whole drawn frame — figure plus the ground prop
		// hanging off one side and the held prop off the other — and it is almost all
		// transparent. Treating it as the pet made ~150px of dead band per Proc eat
		// every click along the bottom of the screen.
		const body = build(`<div data-proc id="frame"><svg><g ${FIGURE_ATTRIBUTE}><rect id="body"/></g></svg></div>`);

		expect(isOverPet(body.querySelector("#frame"))).toBe(false);
	});

	it("is true over the Proc itself", () => {
		const body = build(`<div data-proc><svg><g ${FIGURE_ATTRIBUTE}><rect id="body"/></g></svg></div>`);

		expect(isOverPet(body.querySelector("#body"))).toBe(true);
	});

	it("is false over the scenery, which is not the pet", () => {
		// A desk is decoration. Grabbing at it should reach the desktop underneath.
		const body = build(
			`<div data-proc><svg><g data-slot="ground"><rect id="desk"/></g><g ${FIGURE_ATTRIBUTE}><rect/></g></svg></div>`,
		);

		expect(isOverPet(body.querySelector("#desk"))).toBe(false);
	});

	it("survives a target that is not an element at all", () => {
		expect(isOverPet(null)).toBe(false);
		expect(isOverPet(document)).toBe(false);
	});
});

describe("createInteractionTracker", () => {
	it("takes the pointer when it reaches a Proc and gives it straight back", () => {
		const onChange = vi.fn();
		const body = build(`<div data-proc><svg><g ${FIGURE_ATTRIBUTE}><rect id="body"/></g></svg><b id="gap"></b></div>`);
		const tracker = createInteractionTracker(onChange);

		tracker.update(body.querySelector("#body"));
		tracker.update(body.querySelector("#gap"));

		expect(onChange.mock.calls).toEqual([[true], [false]]);
	});

	it("only speaks when the answer changes, because every move would otherwise cross IPC", () => {
		const onChange = vi.fn();
		const body = build(`<div data-proc><svg><g ${FIGURE_ATTRIBUTE}><rect id="body"/></g></svg></div>`);
		const tracker = createInteractionTracker(onChange);

		for (let i = 0; i < 10; i++) tracker.update(body.querySelector("#body"));

		expect(onChange).toHaveBeenCalledTimes(1);
	});

	it("starts click-through, so the desktop is never dead before the first move", () => {
		const onChange = vi.fn();
		const body = build(`<div id="empty"></div>`);
		const tracker = createInteractionTracker(onChange);

		tracker.update(body.querySelector("#empty"));

		expect(onChange).not.toHaveBeenCalled();
	});

	it("hands the pointer back when it leaves the window entirely", () => {
		// A pointer can leave without ever crossing off the pet — flick it off the
		// bottom of the screen and no further move arrives. Left latched, the band
		// would eat clicks until the pointer happened to wander back over it.
		const onChange = vi.fn();
		const body = build(`<div data-proc><svg><g ${FIGURE_ATTRIBUTE}><rect id="body"/></g></svg></div>`);
		const tracker = createInteractionTracker(onChange);

		tracker.update(body.querySelector("#body"));
		tracker.release();

		expect(onChange.mock.calls).toEqual([[true], [false]]);
	});

	it("stays interactive while a Proc is being held, wherever the pointer goes", () => {
		// Dragging a Proc pulls the pointer off it constantly. Dropping back to
		// click-through mid-drag would hand the rest of the gesture to the desktop.
		const onChange = vi.fn();
		const body = build(`<div data-proc><svg><g ${FIGURE_ATTRIBUTE}><rect id="body"/></g></svg><b id="gap"></b></div>`);
		const tracker = createInteractionTracker(onChange);

		tracker.update(body.querySelector("#body"));
		tracker.hold(true);
		tracker.update(body.querySelector("#gap"));

		expect(onChange.mock.calls).toEqual([[true]]);

		tracker.hold(false);
		tracker.update(body.querySelector("#gap"));

		expect(onChange.mock.calls).toEqual([[true], [false]]);
	});
});
