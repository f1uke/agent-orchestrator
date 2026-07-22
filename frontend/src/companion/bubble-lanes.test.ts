import { describe, expect, it } from "vitest";
import { BUBBLE_STACK_GAP, MAX_BUBBLE_LANES, stackBubbles, type BubbleBox } from "./bubble-lanes";

/** A one-line card, which is what almost every bubble actually is. */
function box(id: string, left: number, width = 145, height = 33): BubbleBox {
	return { id, left, right: left + width, height };
}

describe("stacking bubbles that would collide", () => {
	// Two Procs standing close both have something to say, and both need reading.
	// Suppressing one, or arguing about which paints in front, answers the wrong
	// question: the band is crowded and the sky above it is empty.
	it("leaves a lone bubble at its Proc's head", () => {
		expect(stackBubbles([box("a", 100)]).get("a")).toBe(0);
	});

	it("leaves well-separated bubbles all at their Procs' heads", () => {
		expect([...stackBubbles([box("a", 0), box("b", 400), box("c", 900)]).values()]).toEqual([0, 0, 0]);
	});

	it("does NOT lift a bubble that merely comes close", () => {
		// The bug the human saw: cards were measured at the widest a card COULD be
		// (200px), so a 145px card was lifted clear of a neighbour it never touched.
		const lifted = stackBubbles([box("a", 100), box("b", 260)]);

		expect(lifted.get("a")).toBe(0);
		expect(lifted.get("b")).toBe(0);
	});

	it("lifts a bubble that genuinely overlaps the one before it", () => {
		expect(stackBubbles([box("a", 100), box("b", 180)]).get("b")).toBeGreaterThan(0);
	});

	it("lifts it by the height of the card it is sitting on, not by the tallest card there could be", () => {
		// Three lines' worth of air above a one-line card reads as two unrelated
		// things rather than as a stack.
		expect(stackBubbles([box("a", 100), box("b", 180)]).get("b")).toBe(33 + BUBBLE_STACK_GAP);
	});

	it("clears a THREE-line card by three lines, because that is what is actually below it", () => {
		const tall = { ...box("a", 100), height: 65 };

		expect(stackBubbles([tall, box("b", 180)]).get("b")).toBe(65 + BUBBLE_STACK_GAP);
	});

	it("puts a third overlapping bubble above them both", () => {
		// All three genuinely overlap: 100-245, 180-325, 200-345.
		const offsets = stackBubbles([box("a", 100), box("b", 180), box("c", 200)]);

		expect(offsets.get("a")).toBe(0);
		expect(offsets.get("b")).toBe(33 + BUBBLE_STACK_GAP);
		expect(offsets.get("c")).toBe(2 * (33 + BUBBLE_STACK_GAP));
	});

	it("drops back down as soon as there is room again", () => {
		expect(stackBubbles([box("a", 100), box("b", 180), box("c", 700)]).get("c")).toBe(0);
	});

	it("reuses a lane an earlier bubble has already finished in", () => {
		const offsets = stackBubbles([box("a", 0), box("b", 100), box("c", 150)]);

		expect(offsets.get("a")).toBe(0);
		expect(offsets.get("b")).toBeGreaterThan(0);
		// c starts past a's right edge, so the bottom lane is free again.
		expect(offsets.get("c")).toBe(0);
	});

	it("stops stacking at the cap rather than walking off the top of the display", () => {
		const many = Array.from({ length: 8 }, (_, i) => box(`p${i}`, i * 20));
		const offsets = stackBubbles(many);

		expect(new Set(offsets.values()).size).toBe(MAX_BUBBLE_LANES);
	});

	it("arranges the same Procs the same way whatever order they arrive in", () => {
		const boxes = [box("a", 100), box("b", 180), box("c", 260)];
		const forwards = stackBubbles(boxes);
		const backwards = stackBubbles([...boxes].reverse());

		expect([...backwards.entries()].sort()).toEqual([...forwards.entries()].sort());
	});
});
