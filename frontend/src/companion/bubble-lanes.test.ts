import { describe, expect, it } from "vitest";
import { assignBubbleLanes, MAX_BUBBLE_LANES, type BubbleSpan } from "./bubble-lanes";

function span(id: string, left: number, width = 200): BubbleSpan {
	return { id, left, right: left + width };
}

describe("stacking bubbles that would collide", () => {
	// Two Procs standing close both have something to say, and both need reading.
	// Suppressing one, or arguing about which paints in front, answers the wrong
	// question: the band is crowded and the sky above it is empty.
	it("leaves a lone bubble at its Proc's head", () => {
		expect(assignBubbleLanes([span("a", 100)]).get("a")).toBe(0);
	});

	it("leaves well-separated bubbles all at the bottom lane", () => {
		const lanes = assignBubbleLanes([span("a", 0), span("b", 400), span("c", 900)]);

		expect([...lanes.values()]).toEqual([0, 0, 0]);
	});

	it("lifts the second of two overlapping bubbles above the first", () => {
		const lanes = assignBubbleLanes([span("a", 100), span("b", 180)]);

		expect(lanes.get("a")).toBe(0);
		expect(lanes.get("b")).toBe(1);
	});

	it("puts a third overlapping bubble above them both", () => {
		const lanes = assignBubbleLanes([span("a", 100), span("b", 180), span("c", 260)]);

		expect([lanes.get("a"), lanes.get("b"), lanes.get("c")]).toEqual([0, 1, 2]);
	});

	it("drops back to the bottom lane as soon as there is room again", () => {
		const lanes = assignBubbleLanes([span("a", 100), span("b", 180), span("c", 700)]);

		expect(lanes.get("c")).toBe(0);
	});

	it("reuses a lane that an earlier bubble has already finished in", () => {
		const lanes = assignBubbleLanes([span("a", 0), span("b", 100), span("c", 210)]);

		expect(lanes.get("a")).toBe(0);
		expect(lanes.get("b")).toBe(1);
		expect(lanes.get("c")).toBe(0);
	});

	it("stops stacking at the cap rather than walking off the top of the display", () => {
		const many = Array.from({ length: 8 }, (_, i) => span(`p${i}`, i * 20));
		const lanes = assignBubbleLanes(many);

		expect(Math.max(...lanes.values())).toBe(MAX_BUBBLE_LANES - 1);
	});

	it("arranges the same Procs the same way whatever order they arrive in", () => {
		const spans = [span("a", 100), span("b", 180), span("c", 260)];
		const forwards = assignBubbleLanes(spans);
		const backwards = assignBubbleLanes([...spans].reverse());

		expect([...backwards.entries()].sort()).toEqual([...forwards.entries()].sort());
	});
});
