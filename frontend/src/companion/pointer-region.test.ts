import { describe, expect, it, beforeEach, vi } from "vitest";
import {
	FIGURE_ATTRIBUTE,
	SURFACE_ATTRIBUTE,
	createInteractionTracker,
	isOverPet,
	isOverSurface,
	ownsPointer,
} from "./pointer-region";

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

// PROTOTYPE (terminal bubble)
describe("isOverSurface", () => {
	it("is true anywhere on a surface, including the gaps between its controls", () => {
		// The opposite rule to a Proc: a Proc takes the pointer per painted pixel, a
		// card takes every pixel it covers. A terminal you could click THROUGH would
		// put keystrokes into whatever was behind it.
		const body = build(
			`<div ${SURFACE_ATTRIBUTE}="true" id="card"><div id="gap"></div><button id="close"></button></div>`,
		);

		expect(isOverSurface(body.querySelector("#card"))).toBe(true);
		expect(isOverSurface(body.querySelector("#gap"))).toBe(true);
		expect(isOverSurface(body.querySelector("#close"))).toBe(true);
	});

	it("is false everywhere else, so the desktop keeps working around the card", () => {
		const body = build(`<div class="companion-stage" id="band"></div>`);

		expect(isOverSurface(body.querySelector("#band"))).toBe(false);
		expect(isOverSurface(null)).toBe(false);
	});
});

describe("ownsPointer", () => {
	it("covers both things on the overlay that are not scenery", () => {
		const body = build(
			`<div id="band"></div><svg ${FIGURE_ATTRIBUTE}><rect id="pet" /></svg><div ${SURFACE_ATTRIBUTE}="true" id="card"></div>`,
		);

		expect(ownsPointer(body.querySelector("#pet"))).toBe(true);
		expect(ownsPointer(body.querySelector("#card"))).toBe(true);
		expect(ownsPointer(body.querySelector("#band"))).toBe(false);
	});
});

describe("the tracker, with a terminal open", () => {
	it("takes the pointer over the card and gives it back the moment it leaves", () => {
		// This is what keeps an open terminal from killing the whole desktop: the
		// window is interactive over the CARD, not for as long as the card exists.
		const onChange = vi.fn();
		const tracker = createInteractionTracker(onChange);
		const body = build(`<div id="band"></div><div ${SURFACE_ATTRIBUTE}="true" id="card"></div>`);

		tracker.update(body.querySelector("#card"));
		tracker.update(body.querySelector("#band"));

		expect(onChange.mock.calls).toEqual([[true], [false]]);
	});
});
