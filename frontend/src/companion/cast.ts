// What a Proc looks like: two INDEPENDENT axes on one rig.
//
// A Proc is PARAMETERS ON ONE RIG, not a set of drawings — so a new colour or a
// new hat is a data row here, never a new component. What varies is exactly two
// things, and both of them are doing work:
//
//   1. the HAT, which is what makes the SILHOUETTE differ. Six tints of one face
//      would still read as one character; a beanie next to a hard hat next to a
//      party cone reads as three, even in peripheral vision — which is the whole
//      point on a desktop you are not looking straight at.
//   2. the COLOUR, which carries at a glance what the hat carries up close.
//
// They used to be BUNDLED: six fixed characters, each a (hat, colour) pair, so
// every colour had exactly one hat and there were six looks in the world. They are
// now drawn from SEPARATE hash dimensions of the session ref, which multiplies the
// looks (6 × 6) instead of adding them — the human could not tell sessions apart
// on a busy desktop, and six was not enough to.
//
// Hats replaced code-bracket ears, for two reasons the human found by living with
// it. The heads read as bald, and — the bug — an asymmetric glyph pair MIRRORS: a
// Proc that turned to walk left wore `><` instead of `<>`, which just reads as
// broken. A hat is still a hat in a mirror.
//
// ⚠ The identity stays in the BODY and the CORD. A Proc is a running process with a
// power lead, and that is what makes it ours rather than a ghost with accessories.
// Hats and colours are variety ON a character, never the character itself — the
// moment the hat is doing the identifying, this has drifted into a blob in a
// costume.
//
// Both axes are a stable hash of the session ref, per the design. The human asked
// for "random" faces and colours; stable-per-session gives the same visible
// variety on a board of many sessions AND lets someone learn that the teal one in
// the bucket hat is the worker fixing the flaky test. Re-randomising every launch
// would throw that away for nothing — the pets would stop being anybody.

import { speciesById, type SpeciesId } from "./species";

/** Ear paths are authored for the LEFT side and mirrored, so the rig owns symmetry. */
const EAR_MIRROR_AXIS = 48;

export type PaletteId = "amber" | "teal" | "violet" | "rose" | "mint" | "sky";
export type HatId = "beanie" | "cap" | "hardhat" | "cone" | "flatcap" | "bucket";

/** One filled piece of a hat. `trim` picks the accent colour instead of the main one. */
export type HatPiece = { d: string; role?: "trim" };

/** The COLOUR axis: everything a Proc's own body is tinted with. */
export type Palette = {
	id: PaletteId;
	name: string;
	/** Head fill. */
	body: string;
	/** Lower body, legs, and the cord's stroke core. */
	shade: string;
	/** Cheeks. Sits on `body`, never on the wallpaper. */
	blush: string;
};

/** The SILHOUETTE axis: the shape on top, and the two colours it is drawn in. */
export type Hat = {
	id: HatId;
	name: string;
	/** Hat fill. Measured against the wallpaper like every other exposed colour. */
	fill: string;
	/** The hat's band, brim or trim. */
	trim: string;
	/** The hat itself, as filled shapes in rig coordinates, drawn back to front. */
	pieces: HatPiece[];
};

/**
 * One Proc's whole appearance: a palette and a hat, chosen independently.
 *
 * Flattened rather than nested because the rig draws it — `Procs.tsx` should not
 * have to know that a look is assembled from two axes, only what to paint.
 */
export type CastMember = {
	/** `<palette>-<hat>`, e.g. `teal-bucket`, prefixed by the species when it is not a Proc. */
	id: string;
	/** Human-readable, for the accessible label: "Teal bucket hat", "Teal Kitsu, bucket hat". */
	name: string;
	palette: PaletteId;
	hatId: HatId;
	/**
	 * Which CHARACTER this is. Defaults to `proc` everywhere the caller does not say,
	 * so every session in existence keeps the body it already had.
	 */
	species: SpeciesId;
	body: string;
	shade: string;
	blush: string;
	hatFill: string;
	hatTrim: string;
	hat: HatPiece[];
};

export const PALETTES: readonly Palette[] = [
	{ id: "amber", name: "Amber", body: "#f4c558", shade: "#ecb22a", blush: "#e8735c" },
	{ id: "teal", name: "Teal", body: "#92d8dc", shade: "#71c9d0", blush: "#e8735c" },
	{ id: "violet", name: "Violet", body: "#dcc1f1", shade: "#d1aeeb", blush: "#e8735c" },
	{ id: "rose", name: "Rose", body: "#fabad1", shade: "#f6a3c2", blush: "#d95f7a" },
	{ id: "mint", name: "Mint", body: "#9fd9aa", shade: "#82cc91", blush: "#e8735c" },
	{ id: "sky", name: "Sky", body: "#b1cef5", shade: "#99bef0", blush: "#e8735c" },
];

export const HATS: readonly Hat[] = [
	{
		id: "beanie",
		name: "beanie",
		fill: "#f6b6ac",
		trim: "#dcdcdc",
		pieces: [
			// A slouchy beanie, deliberately wider than the head — an oversize hat reads
			// as a hat, while one cut to the skull just reads as a differently shaped head.
			{ d: "M10 28 C 10 -16 86 -16 86 28 L 10 28 Z" },
			{ d: "M14 26 L 82 26 C 90 26 90 40 82 40 L 14 40 C 6 40 6 26 14 26 Z", role: "trim" },
		],
	},
	{
		id: "cap",
		name: "cap",
		fill: "#d6c499",
		trim: "#e2d9c8",
		pieces: [
			// A cap. The peak reads as a peak whichever way the sprite is facing.
			{ d: "M7 29 C 7 -12 89 -12 89 29 L 7 29 Z" },
			{ d: "M9 29 C -1 29 -6 36 -2 41 C 8 36 14 34 22 34 L 9 29 Z", role: "trim" },
		],
	},
	{
		id: "hardhat",
		name: "hard hat",
		fill: "#eec41e",
		trim: "#e9ddbc",
		pieces: [
			// A site hard hat: high crown, wide brim, one ridge.
			{ d: "M7 30 C 7 -12 89 -12 89 30 L 7 30 Z" },
			{ d: "M8 30 L 88 30 C 96 30 96 40 88 40 L 8 40 C 0 40 0 30 8 30 Z", role: "trim" },
			{ d: "M43 -10 L 51 -10 L 51 30 L 43 30 Z", role: "trim" },
		],
	},
	{
		id: "cone",
		name: "party cone",
		fill: "#d4beec",
		trim: "#f2db63",
		pieces: [
			// A party cone, for the odd one out.
			{ d: "M48 -30 L 89 31 L 7 31 Z" },
			{ d: "M12 31 C 34 38 62 38 84 31 L 84 36 C 62 42 34 42 12 36 Z", role: "trim" },
		],
	},
	{
		id: "flatcap",
		name: "flat cap",
		fill: "#adcabc",
		trim: "#d0ddd6",
		pieces: [
			// A flat cap, brim forward.
			{ d: "M10 31 C 6 -12 90 -16 84 31 L 10 31 Z" },
			{ d: "M10 31 C -2 32 -5 39 0 42 C 10 37 16 35 22 35 L 10 31 Z", role: "trim" },
		],
	},
	{
		id: "bucket",
		name: "bucket hat",
		fill: "#efbd9b",
		trim: "#efd8c7",
		pieces: [
			// A bucket hat. The brim is drawn AFTER the crown and overlaps its base, or
			// the two read as a box balanced on a wire rather than as one hat.
			{ d: "M7 29 L 24 -12 L 72 -12 L 89 29 Z" },
			{ d: "M0 24 C 18 42 78 42 96 24 C 78 33 18 33 0 24 Z", role: "trim" },
		],
	},
];

/**
 * Mirror an absolute M/L/C path about the rig's centre line. Ears are authored
 * once for the left side; this is how the right one exists without a second string
 * to keep in sync.
 */
export function mirrorPathX(d: string): string {
	return d
		.replace(/([MLC])\s*([-\d.\s,]+)/g, (_match, command: string, rest: string) => {
			const numbers = rest
				.trim()
				.split(/[\s,]+/)
				.map(Number);
			const flipped = numbers.map((value, index) => (index % 2 === 0 ? 2 * EAR_MIRROR_AXIS - value : value));
			return `${command}${flipped.join(" ")} `;
		})
		.trim();
}

// FNV-1a, then an avalanche finalizer.
//
// FNV alone was not enough, and the failure was visible rather than theoretical:
// picking a character with `hash % 6` uses the LOW bits, which are FNV's weakest,
// and the eight demo session refs landed FIVE on the same character — the
// all-pets-look-identical complaint, reproduced by the fix for it. The murmur3
// fmix32 finalizer mixes high bits down into low ones and takes that to three.
//
// Sequential refs (`…-156`, `…-157`, `…-158`) matter most, because those are the
// ones a person sees side by side.
function hash(ref: string): number {
	let value = 0x811c9dc5;
	for (let i = 0; i < ref.length; i++) {
		value ^= ref.charCodeAt(i);
		value = Math.imul(value, 0x01000193);
	}
	value ^= value >>> 16;
	value = Math.imul(value, 0x85ebca6b);
	value ^= value >>> 13;
	value = Math.imul(value, 0xc2b2ae35);
	value ^= value >>> 16;
	return value >>> 0;
}

/**
 * Salt for the hat's hash.
 *
 * The two axes MUST be drawn from independent bits, or they are not two axes: one
 * hash used twice would tie hat to colour again, just less obviously — every amber
 * Proc in a beanie, for ever. Hashing a salted ref gives a genuinely separate
 * dimension while staying a pure function of the session.
 */
const HAT_SALT = "\u0000hat";

/** The look a session always gets. Pure, stable across restarts, both axes. */
export function castForSession(sessionRef: string): CastMember {
	return composeCast(PALETTES[hash(sessionRef) % PALETTES.length], HATS[hash(sessionRef + HAT_SALT) % HATS.length]);
}

/**
 * Assemble a look from a chosen colour, a chosen hat and — once the human has picked
 * them — a chosen character.
 *
 * The species argument is OPTIONAL and defaults to the Proc, which is what keeps the
 * three new bodies out of the live cast until they are registered as an axis: every
 * existing caller composes exactly the Proc it composed before, down to the id.
 */
export function composeCast(palette: Palette, hat: Hat, species: SpeciesId = "proc"): CastMember {
	const name = speciesById(species).name;
	return {
		id: species === "proc" ? `${palette.id}-${hat.id}` : `${species}-${palette.id}-${hat.id}`,
		name: species === "proc" ? `${palette.name} ${hat.name}` : `${palette.name} ${name}, ${hat.name}`,
		palette: palette.id,
		hatId: hat.id,
		species,
		body: palette.body,
		shade: palette.shade,
		blush: palette.blush,
		hatFill: hat.fill,
		hatTrim: hat.trim,
		hat: hat.pieces,
	};
}

/**
 * The same look, on a different character.
 *
 * What the Procs lab drives its species switcher with, and the shape the third axis
 * will resolve to once the Pet library registers one: the colour and the hat are
 * already decided per session and are not this axis' business to re-roll.
 */
export function withSpecies(cast: CastMember, species: SpeciesId): CastMember {
	return composeCast(
		PALETTES.find((palette) => palette.id === cast.palette) ?? PALETTES[0],
		HATS.find((hat) => hat.id === cast.hatId) ?? HATS[0],
		species,
	);
}

/** Every look there is: one per (colour, hat) pair. Used by tests and the demo roster. */
export const ALL_LOOKS: readonly CastMember[] = PALETTES.flatMap((palette) =>
	HATS.map((hat) => composeCast(palette, hat)),
);
