import { describe, expect, it } from "vitest";
import { appendActivity, demoRoster, everyStatus } from "./dev-roster";
import { CAST, castForSession } from "./cast";
import { ALL_COMPANION_STATUSES } from "./scene";

describe("the playground's invented roster", () => {
	// It handed the same session ref to several entries — two indices wanting the
	// same character scan overlapping ranges and can settle on the same ref — so
	// the band filled with duplicate Procs standing on one spot, duplicate name
	// chips, and a screen of collided bubbles. 27 Procs, 11 distinct sessions.
	it("never hands the same session ref to two entries", () => {
		const ids = everyStatus().map((entry) => entry.sessionId);

		expect(new Set(ids).size).toBe(ids.length);
	});

	it("stays unique for a big roster too", () => {
		const ids = demoRoster(40).map((entry) => entry.sessionId);

		expect(new Set(ids).size).toBe(40);
	});

	it("keeps a ref clear of the ones a roster is already using when one is added", () => {
		let roster = demoRoster(6);
		for (let i = 0; i < 12; i++) roster = appendActivity(roster);

		expect(new Set(roster.map((entry) => entry.sessionId)).size).toBe(roster.length);
	});

	it("still spreads across the whole cast, which is why the search exists at all", () => {
		const faces = new Set(everyStatus().map((entry) => castForSession(entry.sessionId).id));

		expect(faces.size).toBe(CAST.length);
	});

	it("shows every status exactly once", () => {
		expect(everyStatus().map((entry) => entry.status)).toEqual(ALL_COMPANION_STATUSES);
	});

	it("gives the roster exactly one coordinator", () => {
		expect(everyStatus().filter((entry) => entry.kind === "orchestrator")).toHaveLength(1);
	});
});
