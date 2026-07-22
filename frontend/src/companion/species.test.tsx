import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { composeCast, HATS, PALETTES } from "./cast";
import { contrastRatio, PROCS_INK } from "./palette";
import { ALL_CORDS, type Cord } from "./scene";
import {
	ANIME_SPECIES,
	CORE_GLOW,
	coreColour,
	EAR_POSE,
	IRIS_BY_PALETTE,
	SPECIES,
	speciesById,
	TELL_MOTION,
	WING_POSE,
} from "./species";
import { Procs } from "./Procs";
import { lampColour } from "./species-art";

// Every cord state there is, written out a SECOND time on purpose. `ALL_CORDS` is
// what the tell tables are walked with; this list is what says `ALL_CORDS` itself is
// complete. Derive both from one place and a new cord value quietly acquires no pose
// on any character and nothing goes red.
const CORDS: Cord[] = ["attached", "streaming", "tugging", "sparking", "coiled", "unplugged"];

/** Plain RGB distance. Crude on purpose: a "these are not the same swatch" guard. */
function distance(a: string, b: string): number {
	const parse = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
	const [ar, ag, ab] = parse(a);
	const [br, bg, bb] = parse(b);
	return Math.sqrt((ar - br) ** 2 + (ag - bg) ** 2 + (ab - bb) ** 2);
}

function pairs<T>(items: readonly T[]): Array<[T, T]> {
	return items.flatMap((a, i) => items.slice(i + 1).map((b) => [a, b] as [T, T]));
}

describe("the cast of characters", () => {
	it("has one row per character, with distinct ids and names", () => {
		expect(new Set(SPECIES.map((entry) => entry.id)).size).toBe(SPECIES.length);
		expect(new Set(SPECIES.map((entry) => entry.name)).size).toBe(SPECIES.length);
		for (const entry of SPECIES) expect(entry.identity.length, entry.id).toBeGreaterThan(20);
	});

	it("keeps the Proc as the incumbent and the other three as the anime ones", () => {
		expect(SPECIES[0].id).toBe("proc");
		expect(ANIME_SPECIES).not.toContain("proc");
		expect([...ANIME_SPECIES].sort()).toEqual(
			SPECIES.filter((entry) => entry.id !== "proc")
				.map((entry) => entry.id)
				.sort(),
		);
	});

	it("throws on a species id it does not have, because that is a typo not input", () => {
		expect(() => speciesById("dragon" as never)).toThrow(/unknown species/);
	});
});

describe("composing a look with a character on it", () => {
	it("leaves every existing look untouched when no character is named", () => {
		// The three new bodies must not reach a single live session until they are
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

	it("gives a named character its own id and a name that says which it is", () => {
		const look = composeCast(PALETTES[1], HATS[5], "kitsu");

		expect(look.id).toBe("kitsu-teal-bucket");
		expect(look.name).toBe("Teal Kitsu, bucket hat");
		expect(look.species).toBe("kitsu");
	});

	it("multiplies the cast rather than adding to it", () => {
		// 4 characters × 6 colours × 6 hats. The point of a species being an AXIS: a
		// new body is 36 new looks, and the colour and hat a session already has are
		// not re-rolled by it.
		const ids = new Set(
			SPECIES.flatMap((species) =>
				PALETTES.flatMap((palette) => HATS.map((hat) => composeCast(palette, hat, species.id).id)),
			),
		);

		expect(ids.size).toBe(SPECIES.length * PALETTES.length * HATS.length);
	});
});

describe("the tell — what a character says the LINK is doing", () => {
	it("knows about every cord state the scene layer has", () => {
		expect([...ALL_CORDS].sort()).toEqual([...CORDS].sort());
	});

	it("has a pose for every cord state, on every character that has one", () => {
		for (const cord of CORDS) {
			expect(EAR_POSE[cord], cord).toBeDefined();
			expect(WING_POSE[cord], cord).toBeDefined();
			expect(CORE_GLOW[cord], cord).toBeDefined();
			expect(TELL_MOTION, cord).toHaveProperty(cord);
		}
	});

	it("draws no two cord states the same, on any character", () => {
		// The rule the whole art obeys: two states drawn alike are two states the
		// overlay cannot tell you apart. A tell that collapsed two of them would be
		// worse than no tell — it would be a confident wrong answer.
		for (const [a, b] of pairs(CORDS)) {
			expect(EAR_POSE[a], `ears: ${a} vs ${b}`).not.toEqual(EAR_POSE[b]);
			expect(WING_POSE[a], `wings: ${a} vs ${b}`).not.toEqual(WING_POSE[b]);
			expect([CORE_GLOW[a], coreColour(a)], `lamp: ${a} vs ${b}`).not.toEqual([CORE_GLOW[b], coreColour(b)]);
		}
	});

	it("separates the poses by enough angle to be seen, not just by enough to differ", () => {
		// 12° apart at the 30px a Proc is really drawn at. Values that merely differed
		// passed the test above and were indistinguishable on the contact sheet — the
		// first pass had `sparking` and `coiled` 20° apart and they read as one pose.
		for (const [a, b] of pairs(CORDS)) {
			expect(Math.abs(EAR_POSE[a].angle - EAR_POSE[b].angle), `ears: ${a} vs ${b}`).toBeGreaterThanOrEqual(12);
			expect(Math.abs(WING_POSE[a].angle - WING_POSE[b].angle), `wings: ${a} vs ${b}`).toBeGreaterThanOrEqual(12);
		}
	});

	it("holds a fold shorter than an upright pose, so a folded part stays in frame", () => {
		for (const cord of CORDS) {
			if (EAR_POSE[cord].angle < -40) expect(EAR_POSE[cord].scale, cord).toBeLessThan(0.9);
			expect(EAR_POSE[cord].scale, cord).toBeGreaterThan(0.6);
			expect(WING_POSE[cord].scale, cord).toBeGreaterThan(0.6);
		}
	});

	it("moves only where the link is live, and never where it is gone", () => {
		// Motion asserts liveness. A session we have lost contact with, one that has
		// ended, and one sitting at rest must all be STILL — the same rule that keeps
		// the "nothing is coming" dots from twitching.
		expect(TELL_MOTION.streaming).not.toBeNull();
		expect(TELL_MOTION.tugging).not.toBeNull();
		expect(TELL_MOTION.sparking).not.toBeNull();
		expect(TELL_MOTION.attached).toBeNull();
		expect(TELL_MOTION.coiled).toBeNull();
		expect(TELL_MOTION.unplugged).toBeNull();
	});

	it("gives a failed run its own lamp colour, not just a dimmer one", () => {
		// Brightness alone cannot carry a failure: `sparking` at 0.62 and `attached` at
		// 0.72 are the same lamp as far as a glance is concerned. Hue is what makes it
		// a different reading.
		expect(coreColour("sparking")).not.toBe(coreColour("attached"));
		for (const cord of CORDS) {
			if (cord !== "sparking") expect(coreColour(cord), cord).toBe(coreColour("attached"));
		}
	});

	it("puts the lamp out completely when the cord is out", () => {
		expect(CORE_GLOW.unplugged).toBe(0);
		expect(CORE_GLOW.streaming).toBe(1);
	});
});

describe("what sits ON a character rather than on the wallpaper", () => {
	it("lines a Kitsu's ear with the character's OWN blush, not a colour of its own", () => {
		// ⚠ Found by measuring, after the eye had signed it off on one character. A
		// fixed warm lining (#f4bbae) sat at 1.03:1 on the amber head — an ear with no
		// inside at all — and looked fine on the rose one it was drawn against. The
		// blush is the colour already guaranteed to read on its own head and nowhere
		// else (1.35…4.5:1, pinned in `palette.test.ts`), which is exactly the property
		// an ear lining needs, so the lining IS the blush and inherits that guarantee.
		const { container } = render(
			<Procs cast={composeCast(PALETTES[0], HATS[0], "kitsu")} status="working" facing="front" walking={false} />,
		);

		expect(container.querySelector("[data-ear-lining]")?.getAttribute("fill")).toBe(PALETTES[0].blush);
	});

	it("makes a Unit's lamp obviously lit or obviously out", () => {
		// ⚠ Measured by HUE, not by luminance, and the difference is the finding: a gold
		// lamp and the grey it dims to are 124 apart in RGB and only 1.11:1 in contrast,
		// because gold and light grey have nearly the same luminance. Contrast is the
		// wrong instrument here — both sit inside an ink bezel, so neither is being read
		// against the other, they are being TOLD APART. Which is also why "off" does not
		// rest on the colour alone: it gets the slash as well, pinned in `species-art`.
		expect(distance(lampColour("streaming"), lampColour("unplugged"))).toBeGreaterThan(60);
		expect(distance(lampColour("sparking"), lampColour("attached"))).toBeGreaterThan(40);
	});

	it("gives the lamp a colour of its own on every body it is mounted on", () => {
		for (const palette of PALETTES) {
			expect(distance(lampColour("streaming"), palette.shade), palette.name).toBeGreaterThan(40);
		}
	});
});

describe("the anime eye", () => {
	it("gives every colour an iris that reads against the eye's own ink", () => {
		// The iris sits INSIDE the eye's ink mass, so what it must clear is the ink and
		// not the wallpaper — which is exactly why it is allowed to be a second hue at
		// all, and why it is not in the wallpaper sweep.
		for (const palette of PALETTES) {
			expect(contrastRatio(IRIS_BY_PALETTE[palette.id], PROCS_INK), palette.name).toBeGreaterThanOrEqual(4.5);
		}
	});

	it("gives no two colours near-identical eyes", () => {
		for (const [a, b] of pairs(PALETTES)) {
			expect(distance(IRIS_BY_PALETTE[a.id], IRIS_BY_PALETTE[b.id]), `${a.name} vs ${b.name}`).toBeGreaterThan(40);
		}
	});

	it("never tints an eye with the head it sits on", () => {
		// An amber pet with amber eyes is a monotone blob at 30px. The iris is the one
		// place a contrasting hue can go, so it is spent on contrast.
		for (const palette of PALETTES) {
			expect(distance(IRIS_BY_PALETTE[palette.id], palette.body), palette.name).toBeGreaterThan(60);
		}
	});
});
