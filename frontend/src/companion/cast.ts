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

import { SPECIES_ACCESSORIES, type Accessory } from "./species-accessories";
import { SPECIES_PALETTES } from "./species-palettes";
import { speciesById, type SpeciesId } from "./species";

/** Ear paths are authored for the LEFT side and mirrored, so the rig owns symmetry. */
const EAR_MIRROR_AXIS = 48;

export type PaletteId = "amber" | "teal" | "violet" | "rose" | "mint" | "sky";
export type HatId = "beanie" | "cap" | "hardhat" | "cone" | "flatcap" | "bucket";

/** One filled piece of a hat. `trim` picks the accent colour instead of the main one. */
export type HatPiece = { d: string; role?: "trim" };

/** The COLOUR axis: everything a Proc's own body is tinted with. */
export type Palette = {
	/**
	 * ⚠ A plain string, not `PaletteId`. The Proc's six ids are still the union below,
	 * but every creature now brings its OWN six — a ghost has no amber and a cat has no
	 * teal — so the id space is per-species and cannot be one closed union.
	 */
	id: string;
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
	/** `<palette>-<hat>`, e.g. `teal-bucket`, prefixed by the creature when it is not a Proc. */
	id: string;
	/** Human-readable, for the accessible label: "Teal bucket hat", "Teal Cat, bucket hat". */
	name: string;
	palette: string;
	/**
	 * What this creature is WEARING, from its own set: one of the six hats on a Proc, a
	 * collar on a cat, a cherry suspended in a slime.
	 *
	 * ⚠ A plain string, and the field keeps the name `hatId` on purpose — it is the axis
	 * id `hat`, which is what is written in everybody's localStorage. Renaming the axis
	 * would silently reset every choice anybody has made; renaming only the field would
	 * leave the two out of step, which is worse than a name that has outgrown itself.
	 */
	hatId: string;
	/**
	 * WHICH CREATURE. Defaults to `proc` everywhere the caller does not say, so every
	 * session in existence keeps the body it already had.
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
 * Salt for the hat's hash. See `AppearanceAxis.salt` for why every axis needs one.
 */
const HAT_SALT = "\u0000hat";

// ---- the axis registry ------------------------------------------------------
//
// Everything above is DATA about one axis or the other. This is the list of the
// axes themselves, and it exists so that nothing downstream has to know how many
// there are or what they are called.
//
// ⚠ It is NOT a picker any more. The one thing a human chooses is the creature, and
// that is chosen per PROJECT and lives in `look-store.ts`. What survives here is the
// registry's real job: these are the INDEPENDENT HASH DIMENSIONS a session's own look
// is drawn from, one `salt` each, and `species.ts` types its `axes` off the same ids.
// A session's colour and accessory are automatic and nobody overrides them.

/** Which axis. A new axis widens this union and adds a row to `APPEARANCE_AXES`. */
export type AxisId = "palette" | "hat";

/** One option on an axis: what the hash picks between. */
export type AxisOption = { id: string; name: string };

/** A whole look: one option id per axis. */
export type Look = Readonly<Record<AxisId, string>>;

export type AppearanceAxis = {
	id: AxisId;
	/**
	 * This axis' own hash dimension.
	 *
	 * The axes MUST be drawn from independent bits, or they are not axes: one hash
	 * used twice would tie hat to colour again, just less obviously - every amber
	 * Proc in a beanie, for ever. Hashing a salted ref gives a genuinely separate
	 * dimension while staying a pure function of the session. Every axis' salt is
	 * therefore distinct, and `cast.test.ts` pins that.
	 *
	 * The colour's salt is "" because that is what shipped: it is the bare
	 * `hash(ref)` the original six characters were picked with. Changing it would
	 * re-roll the colour of every session in existence.
	 */
	salt: string;
	options: readonly AxisOption[];
};

export const APPEARANCE_AXES: readonly AppearanceAxis[] = [
	{
		id: "palette",
		salt: "",
		options: PALETTES.map((palette) => ({ id: palette.id, name: palette.name })),
	},
	{
		id: "hat",
		salt: HAT_SALT,
		// ⚠ The Proc's, as the SLOTS. The real options are per CREATURE and come from
		// `accessoriesFor` — six hats mean nothing to a jelly cube. `withSpecies` maps a
		// slot onto whatever body turns up, which is why the hash can be taken once here
		// and still land on a collar, a cherry or a beanie.
		options: HATS.map((hat) => ({ id: hat.id, name: hat.name })),
	},
];

/** What the hash gives this session on one axis. */
export function defaultOption(axis: AppearanceAxis, sessionRef: string): string {
	return axis.options[hash(sessionRef + axis.salt) % axis.options.length].id;
}

/** The look a session gets with no choices made: the hash, on every axis. */
export function defaultLook(sessionRef: string): Look {
	return Object.fromEntries(APPEARANCE_AXES.map((axis) => [axis.id, defaultOption(axis, sessionRef)])) as Look;
}

/**
 * Flatten a look into what the rig draws.
 *
 * This is the seam a new CHARACTER TYPE lands on: it arrives as a third axis, and
 * this function grows a dispatch on it. The store, the persistence, the pruning and
 * the picker are all axis-generic and would not change.
 *
 * Defensive on both lookups because a look can come out of localStorage, where the
 * option ids are whatever was written by whichever version wrote them. An id this
 * build does not have falls back rather than throwing. `resolveLook` already
 * substitutes the default for exactly that case, so reaching the fallback here
 * means something handed us a look it never resolved.
 */
export function castFromLook(look: Look): CastMember {
	return composeCast(
		PALETTES.find((palette) => palette.id === look.palette) ?? PALETTES[0],
		HATS.find((hat) => hat.id === look.hat) ?? HATS[0],
	);
}

/** Every look a creature has: its own colours × its own accessories. */
export function looksOf(species: SpeciesId): readonly CastMember[] {
	return palettesFor(species).flatMap((palette) =>
		accessoriesFor(species).map((worn) => composeCast(palette, HATS[0], species, worn.id)),
	);
}

/**
 * The look a session always gets. Pure, stable across restarts, both axes.
 *
 * Still the DEFAULT, and still the whole assignment for a session nobody has picked
 * for, which is every session until someone opens the Pet library.
 */
export function castForSession(sessionRef: string): CastMember {
	return castFromLook(defaultLook(sessionRef));
}

/**
 * Assemble a look from a chosen colour, a chosen hat and — once the human has picked
 * them — a chosen creature.
 *
 * The species argument is OPTIONAL and defaults to the Proc, which is what keeps the
 * five new bodies out of the live cast until they are registered as an axis: every
 * existing caller composes exactly the Proc it composed before, id and name included.
 */
export function composeCast(
	palette: Palette,
	hat: Hat,
	species: SpeciesId = "proc",
	/** Which of the CREATURE's own accessories. Defaults to the hat's id, which is the Proc's. */
	accessory: string = hat.id,
): CastMember {
	const creature = speciesById(species);
	const worn = accessoryOf(species, accessory);
	const wornName = accessoriesFor(species).find((entry) => entry.id === worn)?.name ?? worn;
	return {
		id: species === "proc" ? `${palette.id}-${hat.id}` : `${species}-${palette.id}-${worn}`,
		name: species === "proc" ? `${palette.name} ${hat.name}` : `${palette.name} ${creature.name}, ${wornName}`,
		palette: palette.id,
		hatId: worn,
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
 * The six colours a creature is tinted from.
 *
 * ⚠ Per-species, and the Proc's list is the one every session in existence has already
 * been hashed against — so it is returned by identity here rather than copied anywhere.
 * A creature with no list of its own falls back to it, which is also what an id from a
 * LATER build resolves to.
 */
export function palettesFor(species: SpeciesId): readonly Palette[] {
	return SPECIES_PALETTES[species] ?? PALETTES;
}

/**
 * What a creature can WEAR: hats on a Proc, a collar on a cat, a cherry suspended in a
 * slime. The axis is the same one; the options belong to the creature.
 */
export function accessoriesFor(species: SpeciesId): readonly Accessory[] {
	return SPECIES_ACCESSORIES[species] ?? HATS.map((hat) => ({ id: hat.id, name: hat.name }));
}

/** One accessory of a creature's own set, by id, falling back to its first. */
export function accessoryOf(species: SpeciesId, accessoryId: string): string {
	const set = accessoriesFor(species);
	return (set.find((entry) => entry.id === accessoryId) ?? set[0]).id;
}

/** One colour of a creature's own set, by id, falling back to its first. */
export function paletteOf(species: SpeciesId, paletteId: string): Palette {
	const set = palettesFor(species);
	return set.find((palette) => palette.id === paletteId) ?? set[0];
}

/**
 * The same look, on a different creature.
 *
 * What the Procs lab drives its species switcher with, and the shape the third axis
 * resolves to once the Pet library registers one: the colour and the hat are already
 * decided per session and are not this axis' business to re-roll.
 */
export function withSpecies(cast: CastMember, species: SpeciesId): CastMember {
	// By SLOT, not by id, on BOTH axes. Each creature has its own colours and its own
	// accessories with their own names, so "the same choice" across bodies is the same
	// position in the list — there is no ginger Proc to carry over, and no beanie a
	// slime could put on.
	const slotIn = <T extends { id: string }>(set: readonly T[], id: string) =>
		Math.max(
			0,
			set.findIndex((entry) => entry.id === id),
		);
	const colours = palettesFor(species);
	const worn = accessoriesFor(species);
	const colourSlot = slotIn(palettesFor(cast.species), cast.palette);
	const wornSlot = slotIn(accessoriesFor(cast.species), cast.hatId);
	return composeCast(
		colours[colourSlot % colours.length],
		HATS.find((hat) => hat.id === worn[wornSlot % worn.length].id) ?? HATS[0],
		species,
		worn[wornSlot % worn.length].id,
	);
}

/** Every look there is: one per (colour, hat) pair. Used by tests and the demo roster. */
export const ALL_LOOKS: readonly CastMember[] = PALETTES.flatMap((palette) =>
	HATS.map((hat) => composeCast(palette, hat)),
);
