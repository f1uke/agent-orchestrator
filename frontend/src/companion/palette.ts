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
/** Curly, the amber default of the cast. */
export const PROCS_BODY = "#f7c15a";
/** The body's shaded side, used for the lower body and the ear brackets. */
export const PROCS_BODY_SHADE = "#e9a83c";
/** Eye whites / highlights. */
export const PROCS_LIGHT = "#fbfafd";
/** Blush. */
export const PROCS_BLUSH = "#ef8f7a";

/** Rim width in the SVG's own user units, which is its px width at 1× scale. */
export const PROCS_RIM_PX = 2.4;

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
