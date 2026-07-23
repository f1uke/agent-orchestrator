// The SPECIES axis: WHICH CREATURE a pet is.
//
// The cast was one body — `Proc`, a running process with a power lead — varied by
// colour and by hat. That is the axis structure the Pet library was built on, and
// it worked; what it could not do was make the band look like more than one
// character. Six tints and six hats on one silhouette is still one silhouette, and
// the human's word for the result was that they all feel the same.
//
// So this axis is different in kind from the other two. A colour is a parameter and
// a hat is a layer, but a species is A DIFFERENT DRAWING: its own rig, its own
// proportions, its own way of getting about. `rigs.tsx` holds one per creature and
// `Procs.tsx` chooses between them.
//
// What every creature still owes the system, because these are agents and not
// desktop toys:
//
//   1. THE CORD. Every creature is on a lead, and every creature's lead reports the
//      same six `Cord` states. It is EXPRESSED per body — a ghost's trailing wisp
//      IS the cord, a cat's tail carries it to the plug — but the state comes from
//      `scene.cord` and nowhere else, so no creature can be sparking at one end and
//      dozing at the other.
//   2. A TELL, also driven by `scene.cord`, in a part of its own body: the ghost's
//      arms, the cat's ears, the slime's nucleus, the chick's wings, the toadstool's
//      cap. Only the three live states move — motion asserts liveness we may not
//      have — and every pose is legible with the animation switched off.
//   3. THE FIFTEEN STATES, no two drawn alike. The scene layer (ground, held, emit)
//      is shared, so this comes free as long as a rig does not hide it.
//
// ⚠ ORIGINAL AND GENERIC. A sheet-ghost and a cat are folk shapes nobody owns, and
// they are drawn here from the archetype: a cloth draped over nothing, a cat with
// ears and a tail. No specific character, mascot or franchise creature is
// referenced, and the ghost in particular is drawn deliberately away from the
// rounded-blob-with-dot-eyes ghost a competitor uses — ours is a PEAKED drape with a
// scalloped hem, sleeve-corners for arms, and a wisp that is its power lead.

import type { AxisId } from "./cast";
import type { Cord } from "./scene";

/** Which creature. A new one is a row in {@link SPECIES} plus a rig in `rigs.tsx`. */
export type SpeciesId = "proc" | "ghost" | "cat" | "slime" | "chick" | "toadstool";

/**
 * How a creature gets about.
 *
 * The engine decides WHETHER a pet is moving; this decides what moving looks like.
 * It is per-species and not per-status, because it is anatomy: a ghost has no legs
 * to walk on in any state, and a slime has no legs in any state either.
 */
export type Locomotion = "walk" | "float" | "hop";

export type Species = {
	id: SpeciesId;
	/** As the Pet library lists it. */
	name: string;
	/** What this one IS, in one line, for the library and the concept sheet. */
	identity: string;
	/** The part of it that reports the link, in words. */
	tell: string;
	locomotion: Locomotion;
	/**
	 * Which appearance axes this body can wear.
	 *
	 * ⚠ All six wear both now, and the reason is worth keeping: three of them USED to
	 * wear only a colour, because the second axis was six HATS cut for the Proc's tall
	 * head — and a ghost has no head to perch one on, a slime's head is its whole self,
	 * and a toadstool's cap is already a hat. Three creatures with one look each was the
	 * result. The axis is now the creature's OWN accessory set (`species-accessories.ts`),
	 * so the answer stopped being "no second axis" and became "its own second axis".
	 */
	axes: readonly AxisId[];
	/**
	 * Where the cord leaves the body, in rig coordinates.
	 *
	 * The cord's own path and its plug are shared — same four routes, same socket,
	 * same states — and only the point it grows FROM moves, so a tail and a wisp and
	 * a lead are one piece of machinery drawn out of different anatomy.
	 */
	cordFrom: readonly [number, number];
	/**
	 * Where the cord leaves the body WHILE MOVING, when that is somewhere else.
	 *
	 * Only the cat needs it, and it needs it badly: sitting, its tail curls round its
	 * right side and the lead carries on from the tip; walking, it turns side-on and the
	 * tail is at the BACK, which is the other end of the animal. One anchor for both
	 * would have the lead growing out of its face half the time.
	 */
	cordFromWalking?: readonly [number, number];
	/** Where the held prop hangs, relative to where a Proc holds it. */
	heldOffset: readonly [number, number];
	/** Where the emitted zzz/sparks/confetti start, relative to a Proc's. */
	emitOffset: readonly [number, number];
};

export const SPECIES: readonly Species[] = [
	{
		id: "proc",
		name: "Proc",
		identity: "A running process with a power lead. The original, and still what every session gets.",
		tell: "the cord alone",
		locomotion: "walk",
		axes: ["palette", "hat"],
		cordFrom: [67, 92],
		heldOffset: [0, 0],
		emitOffset: [0, 0],
	},
	{
		id: "ghost",
		name: "Ghost",
		identity:
			"A little sheet-ghost: a peaked drape with a scalloped hem, floating a hand's width off the floor. Its trailing wisp IS the power lead.",
		tell: "its sleeves, and how high it floats",
		locomotion: "float",
		axes: ["palette", "hat"],
		cordFrom: [72, 96],
		heldOffset: [0, 4],
		emitOffset: [0, 0],
	},
	{
		id: "cat",
		name: "Cat",
		identity:
			"A cat head-on, standing on all fours — the hind haunches either side are what say four legs from the front. Its tail runs out to the plug, so the lead is part of the animal.",
		tell: "its ears",
		locomotion: "walk",
		axes: ["palette", "hat"],
		cordFrom: [90, 92],
		cordFromWalking: [10, 64],
		heldOffset: [-4, 2],
		emitOffset: [0, 8],
	},
	{
		id: "slime",
		name: "Slime",
		identity:
			"A jelly cube with soft corners and a bright nucleus, sitting flat on the floor. It hops rather than walks.",
		tell: "its nucleus",
		locomotion: "hop",
		axes: ["palette", "hat"],
		cordFrom: [72, 100],
		heldOffset: [0, 6],
		emitOffset: [0, 34],
	},
	{
		id: "chick",
		name: "Chick",
		identity:
			"A round bird on two stick legs, with a beak and a three-feather crest that comes through any hat — and the crest is what reports the link.",
		tell: "its crest",
		locomotion: "walk",
		axes: ["palette", "hat"],
		cordFrom: [70, 92],
		heldOffset: [0, 2],
		emitOffset: [0, 10],
	},
	{
		id: "toadstool",
		name: "Toadstool",
		identity: "A walking mushroom: a wide spotted cap over a stubby stem with the face on it. The cap is its own hat.",
		tell: "its cap",
		locomotion: "hop",
		axes: ["palette", "hat"],
		cordFrom: [64, 96],
		heldOffset: [0, 10],
		emitOffset: [0, 6],
	},
];

/** One species by id. Throws on an unknown id, which is a typo rather than input. */
export function speciesById(id: SpeciesId): Species {
	const found = SPECIES.find((entry) => entry.id === id);
	if (!found) throw new Error(`unknown species: ${id}`);
	return found;
}

/**
 * Whether this creature can wear anything on that axis.
 *
 * What the Pet library asks before it draws a section: a picker that offered a hat
 * to a toadstool would be offering a choice with no effect, which is worse than not
 * offering it — the user picks, nothing changes, and the feature looks broken.
 */
export function speciesWears(id: SpeciesId, axis: AxisId): boolean {
	return speciesById(id).axes.includes(axis);
}

/**
 * FNV-1a with murmur3's avalanche finalizer.
 *
 * The same hash `cast.ts` uses and for the same reason: `% n` takes the low bits, which
 * FNV's are weakest, and project names in one workspace are near-identical strings
 * (`demo-app`, `demo-api`, `demo-web`). Without the finalizer half of them land on one
 * creature, which is the exact failure this axis exists to prevent.
 */
function hash(value: string): number {
	let result = 0x811c9dc5;
	for (let i = 0; i < value.length; i++) {
		result ^= value.charCodeAt(i);
		result = Math.imul(result, 0x01000193);
	}
	result ^= result >>> 16;
	result = Math.imul(result, 0x85ebca6b);
	result ^= result >>> 13;
	result = Math.imul(result, 0xc2b2ae35);
	result ^= result >>> 16;
	return result >>> 0;
}

/**
 * The creature a PROJECT is drawn as.
 *
 * ⚠ Keyed on the project, not the session, and that is the whole point of it. The other
 * two axes answer "which session is this?" — a colour and a hat vary within a project so
 * you can tell two workers apart. The species answers the question above that one:
 * WHICH PROJECT, and every session on a project is the same creature.
 *
 * It replaces the coloured mark that used to sit after the name on the chip. A mark is
 * something you have to look at and decode; a creature is something you already know by
 * the time you have noticed it is there, which on a band you see out of the corner of
 * your eye is the whole difference.
 *
 * ⚠ Six creatures means six projects told apart by shape alone. A seventh collides, and
 * the answer to that is the library rather than a seventh body nobody wanted: pick the
 * creature for the project by hand and the collision is gone.
 *
 * No project, no creature: a session that belongs to nothing is a Proc, which is the
 * default everything started as.
 */
export function speciesForProject(project: string | undefined): SpeciesId {
	if (!project) return "proc";
	return SPECIES[hash(project) % SPECIES.length].id;
}

/** The five that are not the default. `proc` is what a session with no project gets. */
export const NEW_SPECIES: readonly SpeciesId[] = SPECIES.filter((entry) => entry.id !== "proc").map(
	(entry) => entry.id,
);

// ---------------------------------------------------------------- the tell
//
// Every creature's tell is keyed on `Cord` DIRECTLY rather than on a vocabulary of
// its own. A second enum in between would be a second table to keep in sync, and the
// first time it fell behind, a pet would be sparking at one end and dozing at the
// other. The cord's six values ARE the six poses.

/**
 * How a part that hangs off a body is held: swung about its own root, and
 * foreshortened as it folds.
 *
 * POSITIVE is up and alert, NEGATIVE is folded down — for the LEFT-hand copy, which
 * is what is authored; the right one is the same drawing mirrored, so it negates
 * itself and the pair stays symmetric for free.
 *
 * `scale` is not decoration. A part 40 units long swung down through 70° at full
 * length throws its tip clear out of the drawn frame, which is clipped; a real ear
 * folds AWAY from you, and in flat art that is a shorter ear.
 */
export type Pose = { angle: number; scale: number };

/**
 * The one pose table, shared by every creature that has a pair of things to swing:
 * the cat's ears, the ghost's sleeves, the chick's wings.
 *
 * Shared on purpose. These are the same six readings — reaching for you, busy,
 * steady, alarmed, resting, gone — and giving each creature its own numbers would
 * be three tables saying one thing, differing only by whoever typed them last.
 * What differs per creature is the PART, which is the whole point.
 */
export const LIMB_POSE: Record<Cord, Pose> = {
	// ⚠ The upright poses are capped by the DRAWN FRAME, not by taste: the frame is
	// clipped at y = -24 and a cat's ear rotated past about 32° puts its point through
	// the top of it. `rigs.test.tsx` measures the tip for every pose here.
	tugging: { angle: 32, scale: 1 },
	streaming: { angle: 18, scale: 1 },
	attached: { angle: 0, scale: 1 },
	sparking: { angle: -32, scale: 0.88 },
	coiled: { angle: -50, scale: 0.8 },
	unplugged: { angle: -74, scale: 0.66 },
};

/**
 * How brightly a lit tell burns, 0 (out) … 1 (full): the slime's nucleus, the
 * toadstool's cap spots.
 */
export const GLOW: Record<Cord, number> = {
	streaming: 1,
	tugging: 0.86,
	attached: 0.72,
	sparking: 0.62,
	coiled: 0.4,
	unplugged: 0,
};

/**
 * How far a floating body sits off the floor, in rig units.
 *
 * The ghost's own reading, and the one that needs no second part: a ghost with its
 * lead pulled out SINKS. Nothing else in the set can say "gone" by changing its
 * height, because nothing else in the set is holding itself up.
 */
export const HOVER: Record<Cord, number> = {
	tugging: 16,
	streaming: 13,
	attached: 11,
	sparking: 12,
	coiled: 6,
	unplugged: 1,
};

/**
 * How a tell MOVES, per cord state, or `null` for a still pose.
 *
 * A register rather than a keyframe name, because the same three registers are drawn
 * two different ways: a limb swings and a glow cannot — rotating a circle does
 * nothing. Each rig turns the register into `procs-swing-<reg>` or `procs-lamp-<reg>`,
 * which `companion.css` owns and which are transform/opacity only.
 *
 * Only the three LIVE states move at all. Nothing that has lost its link may twitch:
 * motion would assert liveness we do not have, which is the rule the quiet dots
 * already follow. And every pose is legible WITHOUT its animation — the angle, the
 * glow and the height do the work, so reduced motion loses the garnish and none of
 * the meaning.
 */
export type TellMotion = "live" | "urgent" | "alarm";

export const TELL_MOTION: Record<Cord, TellMotion | null> = {
	streaming: "live",
	tugging: "urgent",
	sparking: "alarm",
	attached: null,
	coiled: null,
	unplugged: null,
};

/**
 * The lit colour of a glowing tell. Failure changes HUE rather than brightness,
 * because a dimmer glow is not a different reading at 30px and `sparking` has to be.
 */
export function glowColour(cord: Cord): string {
	return cord === "sparking" ? "#ff9166" : "#ffd166";
}
