import { describe, expect, it, vi } from "vitest";
import { castForSession } from "./cast";
import { modeFor } from "./mode";
import { ALL_COMPANION_STATUSES, sceneFor } from "./scene";
import { MOCK_FEED_CYCLE_STEPS, MOCK_FEED_STEP_MS, createMockFeed, mockActivitiesAt } from "./mock-feed";

describe("mockActivitiesAt", () => {
	it("keeps the same session ids at every step, so Procs persist instead of respawning", () => {
		const first = mockActivitiesAt(0).map((a) => a.sessionId);
		for (let step = 1; step < MOCK_FEED_CYCLE_STEPS; step++) {
			expect(mockActivitiesAt(step).map((a) => a.sessionId)).toEqual(first);
		}
	});

	it("shows several DIFFERENT states at once, at every step", () => {
		// The mock is how the overlay gets looked at. A roster that is all "idle"
		// makes correct art look broken — which is half of why the first build
		// appeared to show nothing at all.
		for (let step = 0; step < MOCK_FEED_CYCLE_STEPS; step++) {
			const statuses = new Set(mockActivitiesAt(step).map((a) => a.status));

			expect(statuses.size, `step ${step}`).toBeGreaterThanOrEqual(5);
		}
	});

	it("shows several DIFFERENT characters at once", () => {
		// The demo roster is what the human judges the feature by, and the first cut
		// of it put FIVE of eight sessions on the same character — the very
		// all-identical look this PR exists to remove. Assignment is a stable hash,
		// so the guard is on the visible outcome, not on the hash's bulk uniformity.
		const roster = mockActivitiesAt(0).map((a) => castForSession(a.sessionId).id);
		const counts = new Map<string, number>();
		for (const id of roster) counts.set(id, (counts.get(id) ?? 0) + 1);

		expect(new Set(roster).size).toBeGreaterThanOrEqual(5);
		expect(Math.max(...counts.values())).toBeLessThanOrEqual(2);
	});

	it("shows several different scenes at once, not just different labels", () => {
		const scenes = new Set(
			mockActivitiesAt(0).map((a) => {
				const scene = sceneFor(a.status);
				return `${scene.ground}/${scene.held}/${scene.emit}/${scene.cord}`;
			}),
		);

		expect(scenes.size).toBeGreaterThanOrEqual(5);
	});

	it("reaches every one of the fifteen states across a cycle", () => {
		const seen = new Set<string>();
		for (let step = 0; step < MOCK_FEED_CYCLE_STEPS; step++) {
			for (const activity of mockActivitiesAt(step)) seen.add(activity.status);
		}

		expect([...seen].sort()).toEqual([...ALL_COMPANION_STATUSES].sort());
	});

	it("exercises all four behaviour modes within one cycle", () => {
		const modes = new Set<string>();
		for (let step = 0; step < MOCK_FEED_CYCLE_STEPS; step++) {
			for (const activity of mockActivitiesAt(step)) modes.add(modeFor(activity.status));
		}

		expect([...modes].sort()).toEqual(["amble", "anchor", "still", "summon"]);
	});

	it("keeps the roster small enough to fit a screen", () => {
		// Each Proc plus its scene is ~150px wide. More than this and they overlap on
		// a laptop display, which reads as a bug rather than as a busy desktop.
		expect(mockActivitiesAt(0).length).toBeLessThanOrEqual(8);
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
