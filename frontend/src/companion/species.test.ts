import { describe, expect, it } from "vitest";
import { APPEARANCE_AXES, composeCast, HATS, PALETTES } from "./cast";
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

	it("tints every creature, and hats only the ones with a head to put one on", () => {
		// Colour is a PARAMETER and applies to everything — a creature that could not be
		// tinted would put the whole band back to one look per body. A hat is a LAYER cut
		// for the Proc's tall head, and three of these do not have one: a ghost is a drape
		// (the drape is the silhouette), a slime's head is its whole self, and a
		// toadstool's cap is already a hat.
		for (const entry of SPECIES) expect(entry.axes, entry.id).toContain("palette");

		expect(SPECIES.filter((entry) => entry.axes.includes("hat")).map((entry) => entry.id)).toEqual([
			"proc",
			"cat",
			"chick",
		]);
	});

	it("answers which axes a creature wears, for the picker", () => {
		expect(speciesWears("proc", "hat")).toBe(true);
		expect(speciesWears("ghost", "hat")).toBe(false);
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

	it("names a creature, and does not name a hat it cannot wear", () => {
		expect(composeCast(PALETTES[1], HATS[5], "cat").name).toBe("Teal Cat, bucket hat");
		expect(composeCast(PALETTES[1], HATS[5], "ghost").name).toBe("Teal Ghost");
		expect(composeCast(PALETTES[1], HATS[5], "ghost").id).toBe("ghost-teal-bucket");
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
