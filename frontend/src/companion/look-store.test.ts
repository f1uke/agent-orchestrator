import { describe, expect, it } from "vitest";
import { APPEARANCE_AXES, castForSession, defaultLook, type AxisId } from "./cast";
import {
	castFor,
	chooseLook,
	clearLookChoice,
	isAxisChosen,
	parseLookOverrides,
	pruneLookOverrides,
	resolveLook,
	serializeLookOverrides,
	type LookOverrides,
} from "./look-store";

const REF = "agent-orchestrator-168";
const OTHER = "agent-orchestrator-169";

/** An option on `axis` that is NOT the one this session would get by default. */
function anotherOption(axisId: AxisId, sessionRef: string): string {
	const axis = APPEARANCE_AXES.find((entry) => entry.id === axisId)!;
	const mine = defaultLook(sessionRef)[axisId];
	return axis.options.find((option) => option.id !== mine)!.id;
}

describe("resolving a look", () => {
	it("gives the hash default when nobody has chosen anything", () => {
		// The whole promise of the feature: a new session gets a face instantly, with
		// no action. Choosing is a bonus.
		expect(resolveLook(REF, {})).toEqual(defaultLook(REF));
	});

	it("lets a chosen axis win while the OTHER axis stays on the hash", () => {
		// Per-axis is the point. Changing the hat must not silently re-roll the colour.
		const hat = anotherOption("hat", REF);
		const overrides = chooseLook({}, REF, "hat", hat);

		expect(resolveLook(REF, overrides).hat).toBe(hat);
		expect(resolveLook(REF, overrides).palette).toBe(defaultLook(REF).palette);
	});

	it("works the same way round: a chosen colour leaves the hat alone", () => {
		const palette = anotherOption("palette", REF);
		const overrides = chooseLook({}, REF, "palette", palette);

		expect(resolveLook(REF, overrides).palette).toBe(palette);
		expect(resolveLook(REF, overrides).hat).toBe(defaultLook(REF).hat);
	});

	it("takes a choice on EVERY axis there is", () => {
		// Looped over the registry rather than written out, so a third axis is covered
		// by this test the day it is added.
		let overrides: LookOverrides = {};
		const wanted: Record<string, string> = {};
		for (const axis of APPEARANCE_AXES) {
			wanted[axis.id] = anotherOption(axis.id, REF);
			overrides = chooseLook(overrides, REF, axis.id, wanted[axis.id]);
		}

		expect(resolveLook(REF, overrides)).toEqual(wanted);
	});

	it("keeps a choice that happens to equal the default, because it was still a choice", () => {
		const mine = defaultLook(REF).hat;
		const overrides = chooseLook({}, REF, "hat", mine);

		expect(isAxisChosen(overrides, REF, "hat")).toBe(true);
		expect(resolveLook(REF, overrides).hat).toBe(mine);
	});

	it("falls back to the default for an option this build does not have", () => {
		// A palette removed in a later version, or a hand-edited localStorage. The
		// wrong answer is a Proc with no colour; the right one is the one it always had.
		const overrides = chooseLook({}, REF, "palette", "chartreuse") as LookOverrides;

		expect(resolveLook(REF, overrides).palette).toBe(defaultLook(REF).palette);
		expect(isAxisChosen(overrides, REF, "palette")).toBe(false);
	});

	it("ignores a stored key that is not an axis at all", () => {
		// Forward compatibility: a newer build writes an axis this one has never heard
		// of, and this one must still draw a Proc rather than fall over.
		const overrides = { [REF]: { hat: anotherOption("hat", REF), species: "octopus" } };

		expect(resolveLook(REF, overrides)).toEqual({ ...defaultLook(REF), hat: overrides[REF].hat });
	});

	it("does not touch any OTHER session's look", () => {
		// Recognisability is the reason the hash exists. One person redecorating one
		// pet must not move anybody else's.
		const overrides = chooseLook({}, REF, "hat", anotherOption("hat", REF));

		expect(resolveLook(OTHER, overrides)).toEqual(defaultLook(OTHER));
		for (let i = 0; i < 50; i++) {
			const ref = `session-${i}`;
			expect(castFor(ref, overrides), ref).toEqual(castForSession(ref));
		}
	});

	it("hands the rig a real cast member, not a look", () => {
		expect(castFor(REF, {})).toEqual(castForSession(REF));
	});
});

describe("changing and clearing a choice", () => {
	it("never mutates the map it was given", () => {
		const before: LookOverrides = {};
		chooseLook(before, REF, "hat", anotherOption("hat", REF));

		expect(before).toEqual({});
	});

	it("puts one axis back on the hash and leaves the other chosen", () => {
		const hat = anotherOption("hat", REF);
		const palette = anotherOption("palette", REF);
		const both = chooseLook(chooseLook({}, REF, "hat", hat), REF, "palette", palette);

		const cleared = clearLookChoice(both, REF, "hat");

		expect(resolveLook(REF, cleared).hat).toBe(defaultLook(REF).hat);
		expect(resolveLook(REF, cleared).palette).toBe(palette);
	});

	it("clears the whole session and leaves no entry behind", () => {
		const both = chooseLook(chooseLook({}, REF, "hat", anotherOption("hat", REF)), REF, "palette", "mint");

		const cleared = clearLookChoice(both, REF);

		expect(resolveLook(REF, cleared)).toEqual(defaultLook(REF));
		expect(Object.keys(cleared)).not.toContain(REF);
	});

	it("drops the session entry when its last axis is cleared", () => {
		// An empty `{}` per session would accumulate for every pet ever looked at.
		const one = chooseLook({}, REF, "hat", anotherOption("hat", REF));

		expect(Object.keys(clearLookChoice(one, REF, "hat"))).toEqual([]);
	});

	it("is a no-op for a session nobody has chosen for", () => {
		const overrides = chooseLook({}, REF, "hat", anotherOption("hat", REF));

		expect(clearLookChoice(overrides, OTHER)).toBe(overrides);
	});
});

describe("persisting", () => {
	it("survives a write and a read, which is what a restart is", () => {
		const overrides = chooseLook(chooseLook({}, REF, "hat", "cone"), OTHER, "palette", "mint");

		const restored = parseLookOverrides(serializeLookOverrides(overrides));

		expect(restored).toEqual(overrides);
		expect(resolveLook(REF, restored).hat).toBe("cone");
		expect(resolveLook(OTHER, restored).palette).toBe("mint");
	});

	it("reads nothing at all as nothing chosen", () => {
		expect(parseLookOverrides(null)).toEqual({});
		expect(parseLookOverrides("")).toEqual({});
	});

	it("treats garbage as nothing chosen rather than throwing", () => {
		// This runs on the OVERLAY, on someone's desktop. A parse failure must lose the
		// decoration, never the pets.
		for (const raw of ["not json", "[]", "42", '"nope"', "null", '{"v":"x"}', '{"sessions":5}']) {
			expect(parseLookOverrides(raw), raw).toEqual({});
		}
	});

	it("drops entries that are not axis-id to option-id strings", () => {
		const raw = JSON.stringify({
			v: 1,
			sessions: { [REF]: { hat: 7, palette: "mint" }, [OTHER]: "beanie", "": { hat: "cone" } },
		});

		expect(parseLookOverrides(raw)).toEqual({ [REF]: { palette: "mint" } });
	});

	it("writes a versioned envelope, so a future shape change is detectable", () => {
		const written = JSON.parse(serializeLookOverrides(chooseLook({}, REF, "hat", "cone")));

		expect(written.v).toBe(1);
		expect(written.sessions[REF]).toEqual({ hat: "cone" });
	});
});

describe("pruning sessions that no longer exist", () => {
	it("forgets a session that is gone and keeps the ones that are not", () => {
		const overrides = chooseLook(chooseLook({}, REF, "hat", "cone"), OTHER, "palette", "mint");

		expect(pruneLookOverrides(overrides, [REF])).toEqual({ [REF]: { hat: "cone" } });
	});

	it("returns the very same map when there is nothing to forget", () => {
		// Identity, not equality: the caller writes to localStorage on a change, and a
		// fresh object every poll would write on every tick for ever.
		const overrides = chooseLook({}, REF, "hat", "cone");

		expect(pruneLookOverrides(overrides, [REF, OTHER])).toBe(overrides);
	});

	it("forgets everything when there are no sessions left", () => {
		expect(pruneLookOverrides(chooseLook({}, REF, "hat", "cone"), [])).toEqual({});
	});
});
