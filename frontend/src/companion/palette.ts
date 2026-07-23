// Procs colours, and the arithmetic that proves they work.
//
// A Proc is an object on someone's WALLPAPER, not on an app surface, so it has no
// light/dark variant to switch between — there is nothing to theme against. What
// replaces theming is a two-channel rule the design derived by measurement: a
// solid body measures as little as 1.02:1 on a mid-bright wallpaper, i.e.
// invisible, so separation is carried by the saturated body on dark wallpapers AND
// by a 2.4px ink rim on light ones. The rim is load-bearing, not decoration, and it
// is baked into the SVG rather than applied as a CSS `filter`, so an animating Proc
// never pays a per-frame paint cost for it.
//
// palette.test.ts sweeps the whole luminance axis and asserts the floor, so these
// values cannot be changed to something that fails without the suite going red.

/** Rim + face ink. The second channel. */
export const PROCS_INK = "#181422";
/** Eye whites / highlights. Never measured against the wallpaper — it sits on ink. */
export const PROCS_LIGHT = "#fbfafd";

/** Rim width in the SVG's own user units, which is its px width at 1× scale. */
export const PROCS_RIM_PX = 2.4;

/**
 * Every colour the PROP layers put against the wallpaper. Named rather than
 * inlined in the art precisely so the sweep in palette.test.ts can enumerate them:
 * a prop colour that was only ever written into a `fill=` would be invisible to the
 * test, which is exactly how #157 shipped a 2.96:1 shade.
 */
export const PROP_COLOURS = {
	/** Desk and crate. */
	wood: "#dcb98c",
	/** Bed frame, mattress, pillow. */
	linen: "#ece8f0",
	/** The blanket on the bed. */
	blanket: "#a9bde4",
	/** Held pages and signs. */
	paper: "#fbfafd",
	/** CI sparks, a chick's beak and legs, a cat's bell, a slime's coin. */
	spark: "#ffd166",
	/** A stalk and a leaf: the green a slime's cherry and leaf are grown on. */
	sprig: "#8fd18f",
	/** A slime's cherry. */
	cherry: "#f3a3b0",
	/** The three confetti colours a merge throws. */
	confettiA: "#f4c558",
	confettiB: "#92d8dc",
	confettiC: "#fabad1",
	/** The muted "nothing is coming" dots. Deliberately low-energy, still legible. */
	quiet: "#cfcad8",
	/**
	 * The orchestrator's gold: its name chip's fill and the crown on it.
	 *
	 * A wallpaper colour like any other prop, so it is swept the same way — 3.62:1
	 * at its worst, which clears the bar on every grey between black and white.
	 * It marks the ONE session on a project that coordinates the others; the
	 * character keeps its own hat, so who a Proc is and what job it has stay two
	 * separate readings.
	 */
	lead: "#ffd75e",
	/**
	 * Bubble text colours. Measured against the bubble's OWN `paper` fill, never
	 * against the wallpaper — that is the point of it being a self-contained card.
	 */
	bubbleMuted: "#5b5566",
	bubbleAlert: "#a3341f",
} as const;

// The per-project MARKER's colours used to live here. They are gone with the marker:
// the project is the CREATURE now, so there is nothing on the name chip to colour.

function channel(value: number): number {
	const c = value / 255;
	return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

/** WCAG relative luminance of a `#rrggbb` colour. */
export function relativeLuminance(hex: string): number {
	const value = hex.replace("#", "");
	const r = parseInt(value.slice(0, 2), 16);
	const g = parseInt(value.slice(2, 4), 16);
	const b = parseInt(value.slice(4, 6), 16);
	return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

/** WCAG contrast ratio between two `#rrggbb` colours, 1:1 … 21:1. */
export function contrastRatio(a: string, b: string): number {
	const la = relativeLuminance(a);
	const lb = relativeLuminance(b);
	const [hi, lo] = la > lb ? [la, lb] : [lb, la];
	return (hi + 0.05) / (lo + 0.05);
}

/**
 * The worst separation a fill achieves against ANY wallpaper, counting both
 * channels: the fill itself (which carries dark wallpapers) and the ink rim beside
 * it (which carries light ones). This is the number every drawn colour must clear,
 * and the reason the palette sits in a narrow luminance band — a colour too dark
 * loses BOTH channels at once against a dark desktop.
 *
 * Contrast depends only on relative luminance, so stepping the grey axis covers
 * every possible wallpaper colour rather than sampling a few.
 */
export function worstSeparation(fill: string, steps = 400): number {
	let worst = Number.POSITIVE_INFINITY;
	for (let i = 0; i <= steps; i++) {
		const level = Math.round((i / steps) * 255)
			.toString(16)
			.padStart(2, "0");
		const wallpaper = `#${level}${level}${level}`;
		worst = Math.min(worst, Math.max(contrastRatio(fill, wallpaper), contrastRatio(PROCS_INK, wallpaper)));
	}
	return worst;
}
