// The cast: six Procs, and the rule that decides which one a session gets.
//
// A Proc is PARAMETERS ON ONE RIG, not six drawings — so a seventh cast member is
// a data row here, never a new component. What varies is exactly two things, and
// both of them are doing work:
//
//   1. the HAT, which is what makes the SILHOUETTE differ. Six tints of one face
//      would still read as one character; a beanie next to a hard hat next to a
//      party cone reads as three, even in peripheral vision — which is the whole
//      point on a desktop you are not looking straight at.
//   2. the COLOUR, which carries at a glance what the hat carries up close.
//
// Hats replaced code-bracket ears, for two reasons the human found by living with
// it. The heads read as bald, and — the bug — an asymmetric glyph pair MIRRORS: a
// Proc that turned to walk left wore `><` instead of `<>`, which just reads as
// broken. A hat is still a hat in a mirror.
//
// ⚠ The identity stays in the BODY and the CORD. A Proc is a running process with a
// power lead, and that is what makes it ours rather than a ghost with accessories.
// Hats are variety ON a character, never the character itself — the moment the hat
// is doing the identifying, this has drifted into a blob in a costume.
//
// Assignment is a stable hash of the session ref, per the design. The human asked
// for "random" faces and colours; stable-per-session gives the same visible
// variety on a board of many sessions AND lets someone learn that the teal `<>` is
// the worker fixing the flaky test. Re-randomising every launch would throw that
// away for nothing — the pets would stop being anybody.

/** Ear paths are authored for the LEFT side and mirrored, so the rig owns symmetry. */
const EAR_MIRROR_AXIS = 48;

export type CastId = "curly" | "angle" | "brack" | "glob" | "hash" | "tilde";

export type CastMember = {
	id: CastId;
	name: string;
	/** The punctuation pair this character is named for. */
	glyph: string;
	/** Head fill. */
	body: string;
	/** Lower body, legs, and the ear/cord stroke cores. */
	shade: string;
	/** Cheeks. Sits on `body`, never on the wallpaper. */
	blush: string;
	/** Hat fill. Measured against the wallpaper like every other exposed colour. */
	hatFill: string;
	/** The hat's band, brim or trim. */
	hatTrim: string;
	/** The hat itself, as filled shapes in rig coordinates, drawn back to front. */
	hat: HatPiece[];
};

/** One filled piece of a hat. `trim` picks the accent colour instead of the main one. */
export type HatPiece = { d: string; role?: "trim" };

// Hat colours sit in the same narrow luminance band as the bodies, and for the same
// reason: a hat's crown rises ABOVE the head, so its outline is against the
// wallpaper, and a dark hat on a dark desktop loses both channels at once and the
// Proc appears to have a flat top. Hat and head are told apart by their own ink
// rims, exactly as the body and the head already are.
//
// Colours are not eyeballed: every `body` and `shade` here is checked by
// palette.test.ts across the entire wallpaper luminance range, and the worst case
// is ~3.1:1 — above the 3:1 decorative floor. They sit at a deliberately narrow
// luminance band (~0.50-0.60) because that is what the two-channel rule costs: too
// dark and the ink rim stops separating them from a dark wallpaper, since both
// channels would be dark at once.
export const CAST: readonly CastMember[] = [
	{
		id: "curly",
		name: "Curly",
		glyph: "{}",
		body: "#f4c558",
		shade: "#ecb22a",
		blush: "#e8735c",
		hatFill: "#f6b6ac",
		hatTrim: "#dcdcdc",
		hat: [
			// A slouchy beanie, deliberately wider than the head — an oversize hat reads
			// as a hat, while one cut to the skull just reads as a differently shaped head.
			{ d: "M10 28 C 10 -16 86 -16 86 28 L 10 28 Z" },
			{ d: "M14 26 L 82 26 C 90 26 90 40 82 40 L 14 40 C 6 40 6 26 14 26 Z", role: "trim" },
		],
	},
	{
		id: "angle",
		name: "Angle",
		glyph: "<>",
		body: "#92d8dc",
		shade: "#71c9d0",
		blush: "#e8735c",
		hatFill: "#d6c499",
		hatTrim: "#e2d9c8",
		hat: [
			// A cap. The peak reads as a peak whichever way the sprite is facing.
			{ d: "M12 29 C 12 -12 84 -12 84 29 L 12 29 Z" },
			{ d: "M12 29 C 0 29 -6 36 -2 41 C 8 36 14 34 22 34 L 12 29 Z", role: "trim" },
		],
	},
	{
		id: "brack",
		name: "Brack",
		glyph: "[]",
		body: "#dcc1f1",
		shade: "#d1aeeb",
		blush: "#e8735c",
		hatFill: "#eec41e",
		hatTrim: "#e9ddbc",
		hat: [
			// A site hard hat: high crown, wide brim, one ridge.
			{ d: "M16 30 C 16 -12 78 -12 78 30 L 16 30 Z" },
			{ d: "M10 30 L 84 30 C 92 30 92 40 84 40 L 10 40 C 2 40 2 30 10 30 Z", role: "trim" },
			{ d: "M43 -10 L 51 -10 L 51 30 L 43 30 Z", role: "trim" },
		],
	},
	{
		id: "glob",
		name: "Glob",
		glyph: "**",
		body: "#fabad1",
		shade: "#f6a3c2",
		blush: "#d95f7a",
		hatFill: "#d4beec",
		hatTrim: "#f2db63",
		hat: [
			// A party cone, because Glob is the odd one of the six.
			{ d: "M48 -30 L 82 31 L 14 31 Z" },
			{ d: "M18 31 C 36 38 60 38 78 31 L 78 36 C 60 42 36 42 18 36 Z", role: "trim" },
		],
	},
	{
		id: "hash",
		name: "Hash",
		glyph: "##",
		body: "#9fd9aa",
		shade: "#82cc91",
		blush: "#e8735c",
		hatFill: "#adcabc",
		hatTrim: "#d0ddd6",
		hat: [
			// A flat cap, brim forward.
			{ d: "M10 31 C 6 -12 90 -16 84 31 L 10 31 Z" },
			{ d: "M10 31 C -2 32 -5 39 0 42 C 10 37 16 35 22 35 L 10 31 Z", role: "trim" },
		],
	},
	{
		id: "tilde",
		name: "Tilde",
		glyph: "~~",
		body: "#b1cef5",
		shade: "#99bef0",
		blush: "#e8735c",
		hatFill: "#efbd9b",
		hatTrim: "#efd8c7",
		hat: [
			// A bucket hat. The brim is drawn AFTER the crown and overlaps its base, or
			// the two read as a box balanced on a wire rather than as one hat.
			{ d: "M18 29 L 26 -12 L 70 -12 L 78 29 Z" },
			{ d: "M2 24 C 18 42 78 42 94 24 C 78 33 18 33 2 24 Z", role: "trim" },
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

/** The character a session always gets. Pure, stable across restarts. */
export function castForSession(sessionRef: string): CastMember {
	return CAST[hash(sessionRef) % CAST.length];
}
