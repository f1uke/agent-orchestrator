// The SPECIES axis: which CHARACTER a Proc is, as opposed to what colour it is
// tinted or what hat it has on.
//
// The cast used to be one body — `Proc`, a running process with a power lead —
// varied along colour and hat. This is the third axis, and it is a different KIND
// of axis to those two: colour and hat are variety ON a character, this one is the
// character. It exists because the human asked for anime-styled companions, and a
// beanie cannot make a Proc anime.
//
// Three rules kept the new three from becoming a different feature:
//
//   1. ONE RIG STILL. A species is a head shape, a face and up to three extra
//      layers — not a second component. The body, the legs, the four-beat walk,
//      the cord, the held prop, the ground prop, the emit layer, the name chip and
//      the pointer region are shared, and none of them knows a species exists.
//   2. THE HAT AXIS SURVIVES. Every hat is authored in rig coordinates against the
//      same head box (x 14…82, top y 6), so all six hats fit all four species and
//      the cast is 4 × 6 × 6 = 144 looks rather than 3 new drawings. What is drawn
//      OVER the hat — ears, an ahoge — is what keeps the species readable while
//      wearing one, and a beast ear coming through a beanie is the anime look
//      anyway.
//   3. THE CORD IS NOT REPLACED. It is the process signature: the one part of the
//      rig that reports on the SESSION rather than the task, and the whole reason
//      these read as live agents rather than as desktop pets. Each species instead
//      adds a TELL driven by the same `Cord` value (ears, wings, a chest lamp), so
//      liveness is said twice in the same voice and can never be said two ways.
//
// ⚠ ORIGINAL, deliberately. Anime is the STYLE here — chibi proportions, big eyes
// with a lash bar and two catchlights, hatch blush, sparkles — and not a specific
// character. The three are built from our own vocabulary: Kitsu's ears are SIGNAL
// ears that report the link, Sprite's wings are the stacked frames of a sprite
// sheet, Unit's face is a soft helm around a link lamp. If one of them ever starts
// to resemble somebody's character, that is a bug in this file.

import type { PaletteId } from "./cast";
import type { Cord } from "./scene";

/** Which character. A new one is a row in {@link SPECIES} plus a row of art. */
export type SpeciesId = "proc" | "kitsu" | "sprite" | "unit";

export type Species = {
	id: SpeciesId;
	/** As the Pet library lists it. */
	name: string;
	/** What this one IS, in one line, for the library and for the design record. */
	identity: string;
	/** The part of it that reports the link, in words — used by the concept sheet. */
	tell: string;
};

export const SPECIES: readonly Species[] = [
	{
		id: "proc",
		name: "Proc",
		identity: "A running process with a power lead. The original, and still the default.",
		tell: "the cord alone",
	},
	{
		id: "kitsu",
		name: "Kitsu",
		identity:
			"A beast-eared listener whose ears are SIGNAL ears: they perk while data flows, pin back when a run fails, and fold flat when the cord comes out.",
		tell: "the ears",
	},
	{
		id: "sprite",
		name: "Sprite",
		identity:
			"A winged sprite in both senses — its wings are the three stacked frames of a sprite sheet, and they beat while the session streams.",
		tell: "the wings",
	},
	{
		id: "unit",
		name: "Unit",
		identity:
			"A chibi build-unit in a soft helm, with the link lamp on its chest: lit and pulsing while data flows, dark when the session ends.",
		tell: "the chest lamp",
	},
];

/** One species by id. Throws on an unknown id, which is a typo rather than input. */
export function speciesById(id: SpeciesId): Species {
	const found = SPECIES.find((entry) => entry.id === id);
	if (!found) throw new Error(`unknown species: ${id}`);
	return found;
}

/** The three new ones. `proc` is the existing body and is not anime-styled. */
export const ANIME_SPECIES: readonly SpeciesId[] = ["kitsu", "sprite", "unit"];

// ---------------------------------------------------------------- the tell
//
// Every species' tell is keyed on `Cord` DIRECTLY rather than on a vocabulary of
// its own. A second enum in between would be a second table to keep in sync, and
// the first time it fell behind a Proc would be sparking at one end and dozing at
// the other. There is no mapping to get wrong: the cord's six values are the six
// poses, and the tests below pin that every one of them is drawn differently.

/**
 * How a part that hangs off the body is held: swung about its own root, and
 * foreshortened as it folds.
 *
 * POSITIVE is up and alert, NEGATIVE is folded down — for the LEFT-hand copy, which
 * is what is authored; the right one is the same drawing mirrored, so it negates
 * itself and the pair stays symmetric for free.
 *
 * `scale` is not decoration. An ear is 40 units long, and swinging one down through
 * 70° at full length throws the tip clear off the left of the frame; a real ear
 * folds AWAY from you, which in flat art is a shorter ear.
 */
export type Pose = { angle: number; scale: number };

/**
 * A Kitsu's ears, per cord state.
 *
 * The five poses are the ones an ear-reading animal actually has — up, hard up,
 * neutral, pinned back, limp — mapped onto what the link is doing. `sparking` pins
 * them BACK rather than pushing them further up, because a failed run needs a
 * different silhouette from a busy one and not just a bigger one.
 */
export const EAR_POSE: Record<Cord, Pose> = {
	// ⚠ The upright poses are capped by the DRAWN FRAME, not by taste: the frame is
	// clipped at y = -24 and an ear rotated past about 32° puts its point through the
	// top of it. `species-art.test.tsx` measures the tip for every pose here.
	tugging: { angle: 32, scale: 1 },
	streaming: { angle: 18, scale: 1 },
	attached: { angle: 0, scale: 1 },
	sparking: { angle: -32, scale: 0.88 },
	coiled: { angle: -50, scale: 0.8 },
	unplugged: { angle: -74, scale: 0.66 },
};

/** A Sprite's wings, per cord state. Same convention, shallower — wings sweep, ears swing. */
export const WING_POSE: Record<Cord, Pose> = {
	tugging: { angle: 30, scale: 1.04 },
	streaming: { angle: 15, scale: 1 },
	attached: { angle: 0, scale: 1 },
	sparking: { angle: -16, scale: 0.95 },
	coiled: { angle: -34, scale: 0.88 },
	unplugged: { angle: -52, scale: 0.78 },
};

/** How brightly a Unit's chest lamp is lit, 0 (dark) … 1 (full). */
export const CORE_GLOW: Record<Cord, number> = {
	streaming: 1,
	tugging: 0.86,
	sparking: 0.62,
	attached: 0.72,
	coiled: 0.4,
	unplugged: 0,
};

/**
 * How a tell MOVES, per cord state, or `null` for a still pose.
 *
 * A register rather than a keyframe name, because the same three registers have to
 * be drawn two different ways: an ear or a wing swings, and a lamp cannot — rotating
 * a circle does nothing. Each species turns the register into `procs-swing-<reg>` or
 * `procs-lamp-<reg>`, which `companion.css` owns and which are transform/opacity
 * only, so a strolling cast still costs nothing to composite.
 *
 * Only the three LIVE states move at all. Nothing that has lost its link may twitch:
 * motion would assert liveness we do not have, which is the same rule the quiet dots
 * follow. And every pose is legible WITHOUT its animation — the angle and the glow
 * do the work, so reduced motion loses the garnish and none of the meaning.
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
 * The lamp colour for a cord state. Failure is the one that changes HUE rather
 * than brightness, because a dimmer lamp is not a different reading at 30px and
 * `sparking` has to be one.
 */
export function coreColour(cord: Cord): string {
	return cord === "sparking" ? "#ff9166" : "#ffd166";
}

// ---------------------------------------------------------------- the eyes
//
// The anime eye is the STYLE, and it is shared by all three new species: one ink
// mass, a jewel iris, an ink pupil, two catchlights and a lash bar over the top.
// The lash bar is what most says "anime" and it is also the thing with the least
// room — hat brims come down to y≈40 in rig coordinates, so the whole eye lives
// below that and the face is laid out from the brim down rather than from the
// crown down.

/** Where an anime eye sits, in rig coordinates. Both eyes, one geometry. */
export const ANIME_EYE = { cx: [31, 65], cy: 54, rx: 10, ry: 10.6 } as const;

/**
 * The iris colour per palette.
 *
 * Deliberately NOT the body colour: an amber pet with amber eyes is a monotone
 * blob at 30px, and the iris is the one place a second hue can go without
 * touching the wallpaper contract at all — it sits inside the eye's ink mass, so
 * what it must clear is the INK, not the desktop. `species.test.ts` measures that.
 */
export const IRIS_BY_PALETTE: Record<PaletteId, string> = {
	amber: "#5fc9f8",
	teal: "#ffb74d",
	violet: "#7ee0b8",
	rose: "#a89bf5",
	mint: "#ff9aa8",
	sky: "#c77dff",
};
