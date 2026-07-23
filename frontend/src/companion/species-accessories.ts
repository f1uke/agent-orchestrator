import type { SpeciesId } from "./species";

// What each creature WEARS — and it is a different kind of thing per creature.
//
// ⚠ This replaced one shared set of six hats, and the reason is the same one that made
// the colours per-species: a hat is cut for the Proc's tall head and says nothing about
// anything else. A ghost is a cloth with no head to perch one on. A slime's "head" is
// its whole self. A toadstool's cap IS a hat. Three of the six therefore had no second
// axis at all, which meant three creatures with one look each.
//
// So the axis stays — one option per creature, chosen per session, so two workers on the
// same project are still told apart up close — but WHAT it offers is the creature's own:
//
//   Proc       hats. They were drawn for this head and they still fit it.
//   Ghost      what haunts with it: a bow at the peak, a halo, a candle, a mended patch
//   Cat        what a cat is given: a collar and bell, a bow tie, a scarf, a flower
//   Slime      what is SUSPENDED IN IT — a cherry, a star, a coin, a leaf. Nothing else
//              in the cast can wear a thing inside itself, and it is the single most
//              slime-ish idea available.
//   Chick      a broken eggshell cap, a bow, a seed in the beak, a scarf
//   Toadstool  the pattern on its cap: spots, rings, dew, or a snail sitting on it
//
// ⚠ The axis ID is still `hat`, deliberately. Nobody CHOOSES an accessory any more — it
// is a hash dimension of the session ref — but the id is the one `CastMember.hatId` and
// `speciesWears` are written in, and a rename would be churn across every rig for a word
// no user ever sees.

export type Accessory = { id: string; name: string };

const GHOST_ACCESSORIES: readonly Accessory[] = [
	{ id: "bow", name: "Bow" },
	{ id: "halo", name: "Halo" },
	{ id: "candle", name: "Candle" },
	{ id: "patch", name: "Mended patch" },
];

const CAT_ACCESSORIES: readonly Accessory[] = [
	{ id: "collar", name: "Collar and bell" },
	{ id: "bowtie", name: "Bow tie" },
	{ id: "scarf", name: "Scarf" },
	{ id: "flower", name: "Flower" },
];

const SLIME_ACCESSORIES: readonly Accessory[] = [
	{ id: "cherry", name: "Cherry" },
	{ id: "star", name: "Star" },
	{ id: "coin", name: "Coin" },
	{ id: "leaf", name: "Leaf" },
];

const CHICK_ACCESSORIES: readonly Accessory[] = [
	{ id: "shell", name: "Eggshell" },
	{ id: "bow", name: "Bow" },
	{ id: "seed", name: "Seed" },
	{ id: "scarf", name: "Scarf" },
];

const TOADSTOOL_ACCESSORIES: readonly Accessory[] = [
	{ id: "spots", name: "Spots" },
	{ id: "rings", name: "Rings" },
	{ id: "dew", name: "Dewdrops" },
	{ id: "snail", name: "Snail" },
];

/**
 * What a creature can wear.
 *
 * `proc` is absent on purpose rather than listed: its set is `HATS` itself, and a copy
 * here would be a second list of the six every existing session has already been hashed
 * against. `accessoriesFor` falls back to it.
 */
export const SPECIES_ACCESSORIES: Partial<Record<SpeciesId, readonly Accessory[]>> = {
	ghost: GHOST_ACCESSORIES,
	cat: CAT_ACCESSORIES,
	slime: SLIME_ACCESSORIES,
	chick: CHICK_ACCESSORIES,
	toadstool: TOADSTOOL_ACCESSORIES,
};
