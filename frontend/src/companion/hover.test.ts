import { describe, expect, it } from "vitest";
import { HOVER_TOOLTIP_DELAY_MS, hoverAt, idleHover, tooltipTarget } from "./hover";

const T = 1_000_000;

describe("hover", () => {
	it("shows nothing at all to begin with", () => {
		expect(tooltipTarget(idleHover(), T)).toBeNull();
	});

	it("holds its tongue until the pointer has genuinely settled", () => {
		// A tooltip that fires on contact would flash at every Proc the pointer
		// crosses on its way somewhere else.
		const state = hoverAt(idleHover(), "a", T);

		expect(tooltipTarget(state, T + HOVER_TOOLTIP_DELAY_MS - 1)).toBeNull();
		expect(tooltipTarget(state, T + HOVER_TOOLTIP_DELAY_MS)).toBe("a");
	});

	it("waits the two-to-three seconds the human asked for", () => {
		expect(HOVER_TOOLTIP_DELAY_MS).toBeGreaterThanOrEqual(2_000);
		expect(HOVER_TOOLTIP_DELAY_MS).toBeLessThanOrEqual(3_000);
	});

	it("starts the clock again when the pointer moves to a different Proc", () => {
		let state = hoverAt(idleHover(), "a", T);
		state = hoverAt(state, "b", T + 1_000);

		expect(tooltipTarget(state, T + 1_000 + HOVER_TOOLTIP_DELAY_MS - 1)).toBeNull();
		expect(tooltipTarget(state, T + 1_000 + HOVER_TOOLTIP_DELAY_MS)).toBe("b");
	});

	it("does not restart the clock while the pointer stays on the same Proc", () => {
		// Every pointer move within a Proc reports the same hover; treating each as a
		// fresh arrival would mean the tooltip never appears while the hand is moving.
		let state = hoverAt(idleHover(), "a", T);
		for (let i = 1; i <= 20; i++) state = hoverAt(state, "a", T + i * 100);

		expect(tooltipTarget(state, T + HOVER_TOOLTIP_DELAY_MS)).toBe("a");
	});

	it("drops the tooltip the moment the pointer leaves", () => {
		let state = hoverAt(idleHover(), "a", T);
		expect(tooltipTarget(state, T + HOVER_TOOLTIP_DELAY_MS)).toBe("a");

		state = hoverAt(state, null, T + HOVER_TOOLTIP_DELAY_MS + 10);

		expect(tooltipTarget(state, T + HOVER_TOOLTIP_DELAY_MS + 11)).toBeNull();
	});
});
