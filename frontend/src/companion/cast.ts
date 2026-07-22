// The cast: six Procs, and the rule that decides which one a session gets.
//
// A Proc is PARAMETERS ON ONE RIG, not six drawings — so a seventh cast member is
// a data row here, never a new component. What varies is exactly two things, and
// both of them are doing work:
//
//   1. the EARS, a code-punctuation bracket pair, which is what makes the
//      SILHOUETTE differ. Six tints of one face would still read as one character;
//      a `{}` next to a `<>` next to a `##` reads as three characters even in
//      peripheral vision, which is the whole point on a desktop you are not
//      looking straight at.
//   2. the COLOUR, which carries at a glance what the ears carry up close.
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
	/** The LEFT ear, in rig coordinates. The right one is this, mirrored. */
	ear: string;
};

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
		ear: "M18 26 C 6 28 14 40 2 42 C 14 44 6 56 18 58",
	},
	{
		id: "angle",
		name: "Angle",
		glyph: "<>",
		body: "#92d8dc",
		shade: "#71c9d0",
		blush: "#e8735c",
		ear: "M18 26 L 3 42 L 18 58",
	},
	{
		id: "brack",
		name: "Brack",
		glyph: "[]",
		body: "#dcc1f1",
		shade: "#d1aeeb",
		blush: "#e8735c",
		ear: "M18 26 L 5 26 L 5 58 L 18 58",
	},
	{
		id: "glob",
		name: "Glob",
		glyph: "**",
		body: "#fabad1",
		shade: "#f6a3c2",
		blush: "#d95f7a",
		// An asterisk: three crossing strokes rather than a bracket, which is what
		// makes Glob the odd silhouette of the six.
		ear: "M4 33 L 15 51 M4 51 L 15 33 M1 42 L 18 42",
	},
	{
		id: "hash",
		name: "Hash",
		glyph: "##",
		body: "#9fd9aa",
		shade: "#82cc91",
		blush: "#e8735c",
		ear: "M8 28 L 5 56 M15 28 L 12 56 M2 37 L 18 37 M1 47 L 17 47",
	},
	{
		id: "tilde",
		name: "Tilde",
		glyph: "~~",
		body: "#b1cef5",
		shade: "#99bef0",
		blush: "#e8735c",
		ear: "M2 36 C 6 30 11 42 16 36 M2 50 C 6 44 11 56 16 50",
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
