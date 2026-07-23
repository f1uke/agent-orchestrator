import type { Palette } from "./cast";
import type { SpeciesId } from "./species";

// A palette set PER CREATURE.
//
// ⚠ The human's complaint, and it was the right one: six creatures drawn from one set
// of six tints is one colour template wearing six shapes. Amber suits a chick and says
// nothing about a ghost; a ghost wants to be pale and spectral and a slime wants to be
// vivid and wet, and neither of those is a thing you can reach by tinting the same six
// swatches. So the COLOUR axis is per-species too, and each creature's six are chosen
// for what that creature IS.
//
// It costs nothing structurally, because the axis registry already resolves options per
// axis — this makes the option LIST depend on the creature, which is exactly what the
// per-species `axes` list already does for hats, one level down.
//
// ⚠ THE PROC'S SIX ARE UNTOUCHED, ids included. Every session in existence is a Proc
// with a colour picked by hashing its ref against that list; change one value and every
// pet on every desk changes colour. They are re-exported from `cast.ts`, not copied.
//
// ⚠ Every colour here is SOLVED, not chosen by eye, and the difference is not pedantry.
// The wallpaper floor works out at a relative luminance of about 0.48 for anything that
// faces the desktop — below that a fill loses to a mid-grey wallpaper AND its ink rim
// loses to a dark one, so both channels go at once. That is why the Proc's six have
// always been pastels, and it is why the first hand-picked pass here failed the sweep on
// eleven colours: a deep ginger cat and a hot-red toadstool are simply not available.
//
// So each entry is a HUE and a saturation, and the body and the shade are the points on
// that hue where the luminance lands on target (0.68 and 0.53; a ghost's cloth is paler
// at 0.78 and carries its colour in the fold). The blush is then walked until it sits
// inside the 1.35…4.5 window against its own body. `palette.test.ts` re-measures the lot.

/**
 * A ghost is a bedsheet: almost white, with the colour in the shadow it folds into
 * rather than in the cloth. Named for what light does through it.
 */
const GHOST_PALETTES: readonly Palette[] = [
	{ id: "linen", name: "Linen", body: "#ebe2ed", shade: "#d6b6e0", blush: "#db7a67" },
	{ id: "moonlight", name: "Moonlight", body: "#dee5ef", shade: "#a8c3e5", blush: "#df7583" },
	{ id: "seafoam", name: "Seafoam", body: "#d6e9e3", shade: "#76d1b6", blush: "#dc7a6a" },
	{ id: "candle", name: "Candle", body: "#ece5d2", shade: "#e0bc69", blush: "#d87d57" },
	{ id: "rosewater", name: "Rosewater", body: "#efe2e5", shade: "#e4b4c1", blush: "#de70a7" },
	{ id: "fern", name: "Fern", body: "#dae8d8", shade: "#91d187", blush: "#dd796d" },
];

/** A cat is a coat: the colours cats actually come in, plus one nobody's cat is. */
const CAT_PALETTES: readonly Palette[] = [
	{ id: "ginger", name: "Ginger", body: "#fcceb0", shade: "#fab080", blush: "#de7874" },
	{ id: "cream", name: "Cream", body: "#ded8b3", shade: "#cbc285", blush: "#db7a67" },
	{ id: "smoke", name: "Smoke", body: "#d0d7e4", shade: "#b7c1d4", blush: "#df7580" },
	{ id: "lilacpoint", name: "Lilac point", body: "#e7d0eb", shade: "#d9b4df", blush: "#df7676" },
	{ id: "rosewood", name: "Rosewood", body: "#eed0d6", shade: "#e3b4be", blush: "#de7397" },
	{ id: "sage", name: "Sage", body: "#bcdfc7", shade: "#96cea7", blush: "#dd796d" },
];

/** A slime is a boiled sweet: saturated, wet, and the only creature allowed to be loud. */
const SLIME_PALETTES: readonly Palette[] = [
	{ id: "lime", name: "Lime", body: "#abe875", shade: "#77d723", blush: "#de7874" },
	{ id: "raspberry", name: "Raspberry", body: "#f9cadd", shade: "#f5acc9", blush: "#dd6ebb" },
	{ id: "lagoon", name: "Lagoon", body: "#9de3e8", shade: "#5ed1d9", blush: "#dd796d" },
	{ id: "grape", name: "Grape", body: "#dfd1f4", shade: "#cdb6ee", blush: "#dd70ae" },
	{ id: "tangerine", name: "Tangerine", body: "#fbd0a4", shade: "#f9b26a", blush: "#df7676" },
	{ id: "bubblegum", name: "Bubblegum", body: "#b8dcf6", shade: "#8fc6f1", blush: "#de7397" },
];

/** A chick is down: yolk yellows and the soft browns of a nest, nothing cold. */
const CHICK_PALETTES: readonly Palette[] = [
	{ id: "yolk", name: "Yolk", body: "#f7d54b", shade: "#e8bb0a", blush: "#de7770" },
	{ id: "duckling", name: "Duckling", body: "#e2da71", shade: "#d0c52b", blush: "#db7a67" },
	{ id: "apricot", name: "Apricot", body: "#f9cfb0", shade: "#f5b27f", blush: "#df7676" },
	{ id: "dove", name: "Dove", body: "#ded6cb", shade: "#cbbfad", blush: "#dc7a6a" },
	{ id: "robin", name: "Robin", body: "#bbdce8", shade: "#94c9db", blush: "#de7770" },
	{ id: "mallard", name: "Mallard", body: "#b6e1cb", shade: "#89cfac", blush: "#df7583" },
];

/** A toadstool is a cap: the colours mushrooms come in, which is more than people think. */
const TOADSTOOL_PALETTES: readonly Palette[] = [
	{ id: "fly", name: "Fly agaric", body: "#fbcccd", shade: "#f9acaf", blush: "#df7583" },
	{ id: "chanterelle", name: "Chanterelle", body: "#f8d28f", shade: "#f3b64a", blush: "#dd796d" },
	{ id: "blewit", name: "Blewit", body: "#e9cef6", shade: "#dcb0f1", blush: "#de70a7" },
	{ id: "morel", name: "Morel", body: "#e1d6c9", shade: "#cfbeaa", blush: "#de7770" },
	{ id: "verdigris", name: "Verdigris", body: "#a8e3ce", shade: "#72d2af", blush: "#de7770" },
	{ id: "inkcap", name: "Ink cap", body: "#d5d6e1", shade: "#bdc0cf", blush: "#df7586" },
];

/**
 * Which six a creature is tinted from.
 *
 * `proc` is absent on purpose rather than listed: the Proc's set is `PALETTES` itself,
 * and writing it out here would be a second copy of the one list every existing session
 * has already been hashed against. `palettesFor` falls back to it.
 */
export const SPECIES_PALETTES: Partial<Record<SpeciesId, readonly Palette[]>> = {
	ghost: GHOST_PALETTES,
	cat: CAT_PALETTES,
	slime: SLIME_PALETTES,
	chick: CHICK_PALETTES,
	toadstool: TOADSTOOL_PALETTES,
};
