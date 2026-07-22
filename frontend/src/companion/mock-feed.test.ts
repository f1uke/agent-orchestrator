import { describe, expect, it, vi } from "vitest";
import { modeFor } from "./mode";
import { MOCK_FEED_CYCLE_STEPS, MOCK_FEED_STEP_MS, createMockFeed, mockActivitiesAt } from "./mock-feed";

describe("mockActivitiesAt", () => {
	it("keeps the same session ids at every step, so Procs persist instead of respawning", () => {
		const first = mockActivitiesAt(0).map((a) => a.sessionId);
		for (let step = 1; step < MOCK_FEED_CYCLE_STEPS; step++) {
			expect(mockActivitiesAt(step).map((a) => a.sessionId)).toEqual(first);
		}
	});

	it("exercises all four behaviour modes within one cycle", () => {
		const modes = new Set<string>();
		for (let step = 0; step < MOCK_FEED_CYCLE_STEPS; step++) {
			for (const activity of mockActivitiesAt(step)) modes.add(modeFor(activity.status));
		}

		expect([...modes].sort()).toEqual(["amble", "anchor", "still", "summon"]);
	});

	it("cycles, so a long-running overlay keeps getting states", () => {
		expect(mockActivitiesAt(37)).toEqual(mockActivitiesAt(37 + MOCK_FEED_CYCLE_STEPS));
	});
});

describe("createMockFeed", () => {
	it("pushes the first snapshot immediately, then a new one each step", () => {
		vi.useFakeTimers();
		try {
			const feed = createMockFeed();
			const seen: string[][] = [];
			const stop = feed.subscribe((activities) => seen.push(activities.map((a) => a.status)));

			expect(seen).toHaveLength(1);
			vi.advanceTimersByTime(MOCK_FEED_STEP_MS * 2);
			expect(seen).toHaveLength(3);

			stop();
			vi.advanceTimersByTime(MOCK_FEED_STEP_MS * 3);
			expect(seen).toHaveLength(3);
		} finally {
			vi.useRealTimers();
		}
	});
});
