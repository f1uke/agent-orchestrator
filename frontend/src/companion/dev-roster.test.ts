import { describe, expect, it } from "vitest";
import { appendActivity, demoRoster, everyStatus } from "./dev-roster";
import { castForSession, HATS, PALETTES } from "./cast";
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

	it("still spreads across every colour and every hat, which is why the search exists", () => {
		const looks = everyStatus().map((entry) => castForSession(entry.sessionId));

		expect(new Set(looks.map((look) => look.palette)).size).toBe(PALETTES.length);
		expect(new Set(looks.map((look) => look.hatId)).size).toBe(HATS.length);
	});

	it("shows every status exactly once", () => {
		expect(everyStatus().map((entry) => entry.status)).toEqual(ALL_COMPANION_STATUSES);
	});

	it("gives the roster exactly one coordinator", () => {
		expect(everyStatus().filter((entry) => entry.kind === "orchestrator")).toHaveLength(1);
	});
});

describe("the demo roster's variety", () => {
	// It exists to be LOOKED at, so it has to actually exercise what it is showing.
	it("pairs a colour with different hats as the roster goes on", () => {
		// Stepping both axes off the index alone repeats the same six pairs for ever,
		// which is the bundled cast again wearing a different hat.
		const byPalette = new Map<string, Set<string>>();
		for (const entry of demoRoster(24)) {
			const look = castForSession(entry.sessionId);
			const hats = byPalette.get(look.palette) ?? new Set<string>();
			hats.add(look.hatId);
			byPalette.set(look.palette, hats);
		}

		for (const [palette, hats] of byPalette) {
			expect(hats.size, `${palette} wears more than one hat`).toBeGreaterThan(1);
		}
	});

	it("spans several projects, or the per-project marker has nothing to tell apart", () => {
		expect(new Set(demoRoster(12).map((entry) => entry.project)).size).toBeGreaterThanOrEqual(3);
	});
});
