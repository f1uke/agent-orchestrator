import { describe, expect, it } from "vitest";
import { accessoriesFor, APPEARANCE_AXES, composeCast, HATS, PALETTES } from "./cast";
import { ALL_CORDS, type Cord } from "./scene";
import {
	GLOW,
	glowColour,
	HOVER,
	LIMB_POSE,
	NEW_SPECIES,
	SPECIES,
	speciesForProject,
	speciesById,
	speciesWears,
	TELL_MOTION,
} from "./species";

// Every cord state there is, written out a SECOND time on purpose. `ALL_CORDS` is what
// the tell tables are walked with; this list is what says `ALL_CORDS` itself is
// complete. Derive both from one place and a new cord value quietly acquires no pose on
// any creature, and nothing goes red.
const CORDS: Cord[] = ["attached", "streaming", "tugging", "sparking", "coiled", "unplugged"];

function pairs<T>(items: readonly T[]): Array<[T, T]> {
	return items.flatMap((a, i) => items.slice(i + 1).map((b) => [a, b] as [T, T]));
}

describe("the cast of creatures", () => {
	it("has one row per creature, with distinct ids and names", () => {
		expect(new Set(SPECIES.map((entry) => entry.id)).size).toBe(SPECIES.length);
		expect(new Set(SPECIES.map((entry) => entry.name)).size).toBe(SPECIES.length);
		for (const entry of SPECIES) expect(entry.identity.length, entry.id).toBeGreaterThan(20);
	});

	it("keeps the Proc first and the default", () => {
		expect(SPECIES[0].id).toBe("proc");
		expect(NEW_SPECIES).not.toContain("proc");
		expect(NEW_SPECIES.length).toBe(SPECIES.length - 1);
	});

	it("throws on a creature it does not have, because that is a typo not input", () => {
		expect(() => speciesById("dragon" as never)).toThrow(/unknown species/);
	});

	it("names only axes that actually exist, so the library cannot be asked for a section it has no data for", () => {
		const known = new Set(APPEARANCE_AXES.map((axis) => axis.id));
		for (const entry of SPECIES) {
			for (const axis of entry.axes) expect(known, `${entry.id}: ${axis}`).toContain(axis);
		}
	});

	it("gives every creature both axes, now the second one is its OWN", () => {
		// ⚠ Three of them used to wear only a colour, because the second axis was six HATS
		// cut for the Proc's tall head — and a ghost has no head, a slime's head is its
		// whole self, and a toadstool's cap is already a hat. Three creatures with one
		// look each was the result. The axis is now the creature's own accessory set, so
		// the answer stopped being "no second axis" and became "its own second axis".
		for (const entry of SPECIES) {
			expect(entry.axes, entry.id).toContain("palette");
			expect(entry.axes, entry.id).toContain("hat");
		}
	});

	it("gives each creature its own SET, with no two creatures offering the same one", () => {
		// A shared set is what made this axis useless for half the cast: a cat gets a
		// collar and a bell, a slime gets a cherry suspended inside it, and neither could
		// be the other's. Two creatures may both offer a bow — a bow is a bow wherever it
		// is pinned — but no two may offer the same LIST, and no id may repeat inside one.
		const sets = new Map<string, string>();
		for (const entry of SPECIES) {
			const ids = accessoriesFor(entry.id).map((worn) => worn.id);

			expect(ids.length, entry.id).toBeGreaterThanOrEqual(4);
			expect(new Set(ids).size, `${entry.id} repeats an id`).toBe(ids.length);

			const key = ids.join("|");
			const clash = sets.get(key);
			expect(clash, `${entry.id} offers exactly what ${clash} offers`).toBeUndefined();
			sets.set(key, entry.id);
		}
	});

	it("answers which axes a creature wears, for the picker", () => {
		expect(speciesWears("proc", "hat")).toBe(true);
		expect(speciesWears("ghost", "hat")).toBe(true);
		expect(speciesWears("ghost", "palette")).toBe(true);
	});

	it("gives more than one way of getting about, and says which per creature", () => {
		// A ghost has no legs to walk on in any state and a slime has none either, so
		// locomotion is anatomy rather than status. If every creature walked, five of them
		// would be a Proc in a costume.
		expect(new Set(SPECIES.map((entry) => entry.locomotion)).size).toBeGreaterThan(2);
		expect(speciesById("ghost").locomotion).toBe("float");
		expect(speciesById("cat").locomotion).toBe("walk");
	});

	it("anchors every creature's lead on the creature, at both ends of a pose change", () => {
		// Only the START of the cord moves per creature; every route still ends at the same
		// socket, so what has to be true of an anchor is that it is ON THE BODY and inside
		// the drawn frame. Distance from the Proc's own anchor is NOT the rule — the cat
		// deliberately moves its anchor the whole width of itself when it turns side-on,
		// because its tail goes from its right flank to the back of the animal.
		for (const entry of SPECIES) {
			for (const anchor of [entry.cordFrom, entry.cordFromWalking].filter(Boolean) as Array<
				readonly [number, number]
			>) {
				expect(anchor[0], `${entry.id} x`).toBeGreaterThan(0);
				expect(anchor[0], `${entry.id} x`).toBeLessThan(96);
				expect(anchor[1], `${entry.id} y`).toBeGreaterThan(40);
				expect(anchor[1], `${entry.id} y`).toBeLessThan(118);
			}
		}
	});
});

describe("the creature a PROJECT is drawn as", () => {
	// The axis that replaced the coloured mark on the name chip. Colour and hat answer
	// "which session is this?"; the creature answers the question above it — WHICH
	// PROJECT — so a band groups itself by shape with nothing to decode.
	const PROJECTS = ["demo-app", "demo-api", "demo-web", "demo-infra", "demo-tools", "starlight"];

	it("gives one project one creature, every time", () => {
		for (const project of PROJECTS) {
			expect(speciesForProject(project)).toBe(speciesForProject(project));
		}
	});

	it("spreads near-identical project names across the cast", () => {
		// ⚠ The real input, and the reason the hash needs an avalanche finalizer: projects
		// in one workspace are near-identical strings that differ in their last few
		// characters, which is exactly where a weak hash puts them all on one creature.
		const dealt = new Set(PROJECTS.map(speciesForProject));

		expect(dealt.size, [...dealt].join(", ")).toBeGreaterThanOrEqual(4);
	});

	it("draws a session with no project as the default", () => {
		// No project, no creature identity to carry. A Proc is what everything started as.
		expect(speciesForProject(undefined)).toBe("proc");
		expect(speciesForProject("")).toBe("proc");
	});

	it("can only ever name a creature that exists", () => {
		for (const project of [...PROJECTS, "x", "a-very-long-project-name-indeed", "🙂"]) {
			expect(SPECIES.map((entry) => entry.id)).toContain(speciesForProject(project));
		}
	});
});

describe("composing a look with a creature on it", () => {
	it("leaves every existing look untouched when no creature is named", () => {
		// The five new bodies must not reach a single live session until they are
		// registered as an axis. Every caller that composed a Proc before still does,
		// down to the id string other code keys on.
		for (const palette of PALETTES) {
			for (const hat of HATS) {
				const look = composeCast(palette, hat);

				expect(look.species).toBe("proc");
				expect(look.id).toBe(`${palette.id}-${hat.id}`);
				expect(look.name).toBe(`${palette.name} ${hat.name}`);
			}
		}
	});

	it("names a creature by what IT wears, not by a hat it has never seen", () => {
		expect(composeCast(PALETTES[1], HATS[5], "cat", "collar").name).toBe("Teal Cat, Collar and bell");
		expect(composeCast(PALETTES[1], HATS[5], "ghost", "halo").name).toBe("Teal Ghost, Halo");
		expect(composeCast(PALETTES[1], HATS[5], "ghost", "halo").id).toBe("ghost-teal-halo");
	});

	it("falls back to a creature's first accessory when handed one it does not have", () => {
		// A stored id can be anything: another creature's, or a later build's.
		expect(composeCast(PALETTES[0], HATS[0], "slime", "beanie").hatId).toBe(accessoriesFor("slime")[0].id);
	});
});

describe("the tell — what a creature says the LINK is doing", () => {
	it("knows about every cord state the scene layer has", () => {
		expect([...ALL_CORDS].sort()).toEqual([...CORDS].sort());
	});

	it("has a pose, a glow and a hover height for every cord state", () => {
		for (const cord of CORDS) {
			expect(LIMB_POSE[cord], cord).toBeDefined();
			expect(GLOW[cord], cord).toBeDefined();
			expect(HOVER[cord], cord).toBeDefined();
			expect(TELL_MOTION, cord).toHaveProperty(cord);
		}
	});

	it("draws no two cord states the same, on any of the three channels", () => {
		// The rule the whole art obeys: two states drawn alike are two states the overlay
		// cannot tell you apart. A tell that collapsed two of them would be worse than no
		// tell — it would be a confident wrong answer.
		for (const [a, b] of pairs(CORDS)) {
			expect(LIMB_POSE[a], `limb: ${a} vs ${b}`).not.toEqual(LIMB_POSE[b]);
			expect([GLOW[a], glowColour(a)], `glow: ${a} vs ${b}`).not.toEqual([GLOW[b], glowColour(b)]);
			expect(HOVER[a], `hover: ${a} vs ${b}`).not.toEqual(HOVER[b]);
		}
	});

	it("separates the limb poses by enough angle to be seen, not just enough to differ", () => {
		// 12° apart at the ~30px these are really drawn at. Values that merely differed
		// passed the test above and were indistinguishable on the contact sheet.
		for (const [a, b] of pairs(CORDS)) {
			expect(Math.abs(LIMB_POSE[a].angle - LIMB_POSE[b].angle), `${a} vs ${b}`).toBeGreaterThanOrEqual(12);
		}
	});

	it("holds a fold shorter than an upright pose, so a folded part stays in frame", () => {
		for (const cord of CORDS) {
			if (LIMB_POSE[cord].angle < -40) expect(LIMB_POSE[cord].scale, cord).toBeLessThan(0.9);
			expect(LIMB_POSE[cord].scale, cord).toBeGreaterThan(0.6);
		}
	});

	it("moves only where the link is live, and never where it is gone", () => {
		// Motion asserts liveness. A session we have lost contact with, one that has ended
		// and one at rest must all be STILL — the rule the quiet dots already follow.
		expect(TELL_MOTION.streaming).not.toBeNull();
		expect(TELL_MOTION.tugging).not.toBeNull();
		expect(TELL_MOTION.sparking).not.toBeNull();
		expect(TELL_MOTION.attached).toBeNull();
		expect(TELL_MOTION.coiled).toBeNull();
		expect(TELL_MOTION.unplugged).toBeNull();
	});

	it("puts a glow out completely, and sinks a floater to the floor, when the cord is out", () => {
		expect(GLOW.unplugged).toBe(0);
		expect(GLOW.streaming).toBe(1);
		expect(Math.min(...CORDS.map((cord) => HOVER[cord]))).toBe(HOVER.unplugged);
		expect(HOVER.unplugged).toBeLessThan(HOVER.coiled);
	});

	it("gives a failed run its own glow colour, not just a dimmer one", () => {
		// Brightness alone cannot carry a failure: `sparking` at 0.62 and `attached` at
		// 0.72 are the same lamp as far as a glance is concerned. Hue is what makes it a
		// different reading.
		expect(glowColour("sparking")).not.toBe(glowColour("attached"));
		for (const cord of CORDS) {
			if (cord !== "sparking") expect(glowColour(cord), cord).toBe(glowColour("attached"));
		}
	});
});
