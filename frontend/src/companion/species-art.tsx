import type { CastMember } from "./cast";
import { PROCS_INK, PROCS_LIGHT, PROP_COLOURS } from "./palette";
import { CasedStroke, RIM } from "./props";
import type { Cord } from "./scene";
import {
	ANIME_EYE,
	CORE_GLOW,
	coreColour,
	EAR_POSE,
	IRIS_BY_PALETTE,
	TELL_MOTION,
	WING_POSE,
	type SpeciesId,
} from "./species";

// The art for each species: a head box, a face, and up to three extra layers slotted
// into the ONE rig in `Procs.tsx`.
//
// The slots exist because the order the rig draws in is load-bearing and a species
// has to land INSIDE it, not around it:
//
//   BACK   before the body        — wings, so they sit behind the whole figure
//   CHEST  over the body          — a ruff, a link lamp
//   HEAD   the head silhouette    — the shape that carries the hat
//   FACE   inside the head        — blush, eyes, mouth (clipped to the head)
//   CROWN  after the hat          — ears and an ahoge, which is the only way a
//                                   species stays recognisable while wearing one
//                                   of the six hats, and is the anime look anyway
//
// Everything here obeys the rig's own rules rather than having its own: two
// channels on anything facing the wallpaper (a swept fill plus the 2.4px ink rim),
// paths in M/L/C/Z only so `mirrorPathX` and the measuring tests can read them, and
// position and motion on separate groups so a keyframe never replaces a transform.

export type SpeciesParts = {
	cast: CastMember;
	/** What the link is doing. The species TELL is a pure function of this and nothing else. */
	cord: Cord;
	/** Picked up by the human: startled eyes, open mouth. */
	held: boolean;
	/** The head's clip path id, for anything that must not spill off the face. */
	headClip: string;
};

export type SpeciesArt = {
	/** The head silhouette. Same top and same width for every species, so all six hats fit. */
	head: { x: number; y: number; width: number; height: number; rx: number };
	Back?: (parts: SpeciesParts) => React.ReactNode;
	Chest?: (parts: SpeciesParts) => React.ReactNode;
	Face: (parts: SpeciesParts) => React.ReactNode;
	Crown?: (parts: SpeciesParts) => React.ReactNode;
};

/** The head box the hats were authored against. A species may only change its `rx`. */
const HEAD = { x: 14, y: 6, width: 68, height: 72 } as const;

/** The rig's mirror axis, so a left-authored part can be flipped to the right. */
const MIRROR = "translate(96 0) scale(-1 1)";

function round(value: number): number {
	return Math.round(value * 10) / 10;
}

/**
 * Rotate and shrink a part about its own root.
 *
 * Nested into one transform string rather than an SVG `rotate(a cx cy)` because the
 * folded poses need to FORESHORTEN as well as swing: an ear is 40 units long, and
 * swinging one down through 70° with its length intact throws the tip clear off the
 * left of the frame. Real ears fold away from you; in flat art that is a shorter ear.
 */
function pivoted(root: readonly [number, number], angle: number, scale: number): string {
	return `translate(${root[0]} ${root[1]}) rotate(${angle}) scale(${scale}) translate(${-root[0]} ${-root[1]})`;
}

/**
 * A leaf from `root` to `tip`, bowed `bulge` units either side of the line between
 * them. Generated rather than hand-authored so the three wing plates are the same
 * shape at three sizes — hand-drawing them produced three unrelated blobs.
 */
function leaf(root: readonly [number, number], tip: readonly [number, number], bulge: number): string {
	const [rx, ry] = root;
	const [tx, ty] = tip;
	const dx = tx - rx;
	const dy = ty - ry;
	const length = Math.hypot(dx, dy) || 1;
	const nx = -dy / length;
	const ny = dx / length;
	const at = (t: number, side: number) =>
		`${round(rx + dx * t + nx * bulge * side)} ${round(ry + dy * t + ny * bulge * side)}`;
	return `M${rx} ${ry} C ${at(0.32, 1)} ${at(0.72, 1)} ${tx} ${ty} C ${at(0.72, -1)} ${at(0.32, -1)} ${rx} ${ry} Z`;
}

/**
 * The CSS `transform-origin` for a tell's motion group, given the point it must turn
 * about in rig coordinates.
 *
 * ⚠ Not the rig coordinate itself, and this is the trap. An SVG element's
 * `transform-box` is `view-box`, so a CSS `transform-origin` is measured from the
 * VIEW BOX's own corner — which is at (-8, -24) here, not (0, 0). Written as the raw
 * pivot, every swing turned about a point 25 units up and left of the ear's base and
 * the ear pumped instead of pivoting. `species-art.test.tsx` pins these against
 * `PROCS_VIEW` so a change to the drawn frame cannot silently move them again.
 */
const TELL_VIEW_ORIGIN = { x: -8, y: -24 } as const;

export function tellOrigin(pivot: readonly [number, number]): string {
	return `${round(pivot[0] - TELL_VIEW_ORIGIN.x)}px ${round(pivot[1] - TELL_VIEW_ORIGIN.y)}px`;
}

/** A straight cut across a leaf at `t` along it: the frame boundary of a wing plate. */
function cellLine(root: readonly [number, number], tip: readonly [number, number], bulge: number, t: number): string {
	const dx = tip[0] - root[0];
	const dy = tip[1] - root[1];
	const length = Math.hypot(dx, dy) || 1;
	const nx = (-dy / length) * bulge * 0.62;
	const ny = (dx / length) * bulge * 0.62;
	const cx = root[0] + dx * t;
	const cy = root[1] + dy * t;
	return `M${round(cx - nx)} ${round(cy - ny)} L${round(cx + nx)} ${round(cy + ny)}`;
}

/**
 * The Unit's chest lamp, for a cord state.
 *
 * A BLEND rather than an opacity, so an unlit lamp dims to the same grey the quiet
 * dots use instead of fading the body through it. Exported because it is the one
 * colour on these bodies that is computed, and a computed colour is one nothing can
 * enumerate — `species-art.test.tsx` enumerates it through here.
 */
export function lampColour(cord: Cord): string {
	return mix(PROP_COLOURS.quiet, coreColour(cord), CORE_GLOW[cord]);
}

/** Blend two `#rrggbb` colours. Used so a lamp DIMS to grey rather than fading out. */
function mix(from: string, to: string, amount: number): string {
	const parse = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
	const [ar, ag, ab] = parse(from);
	const [br, bg, bb] = parse(to);
	const channel = (a: number, b: number) =>
		Math.round(a + (b - a) * amount)
			.toString(16)
			.padStart(2, "0");
	return `#${channel(ar, br)}${channel(ag, bg)}${channel(ab, bb)}`;
}

// ---------------------------------------------------------------- the anime face
//
// The eye IS the anime style, and it is shared by all three new species rather than
// drawn per character: one ink mass, a jewel iris, an ink pupil, a big catchlight
// up-left and a small one down-right, and a lash bar over the top. The lash is what
// most says "anime" and it is also what has the least room — the six hats come down
// to y≈40, so the whole face is laid out from that brim DOWN.

function AnimeEyes({ iris, held }: { iris: string; held: boolean }) {
	const rx = held ? ANIME_EYE.rx + 1.1 : ANIME_EYE.rx;
	const ry = held ? ANIME_EYE.ry + 1.6 : ANIME_EYE.ry;
	const cy = ANIME_EYE.cy;
	return (
		<>
			{ANIME_EYE.cx.map((cx) => (
				<g key={cx} data-eye data-anime-eye>
					<ellipse cx={cx} cy={cy} rx={rx} ry={ry} fill={PROCS_INK} />
					<ellipse cx={cx} cy={cy + 1.4} rx={round(rx - 2.7)} ry={round(ry - 2.9)} fill={iris} />
					<ellipse cx={cx} cy={cy + 2.6} rx={round(rx - 6.1)} ry={round(ry - 5.9)} fill={PROCS_INK} />
					<circle cx={cx - 3.1} cy={cy - (held ? 6.2 : 5)} r={held ? 4.3 : 3.6} fill={PROCS_LIGHT} />
					<circle cx={cx + 3.4} cy={cy + 4.8} r="1.9" fill={PROCS_LIGHT} />
					<path
						d={`M${round(cx - rx - 1.2)} ${round(cy - ry + 3.4)} C ${round(cx - rx * 0.5)} ${round(cy - ry - 3.6)} ${round(cx + rx * 0.5)} ${round(cy - ry - 3.6)} ${round(cx + rx + 1.2)} ${round(cy - ry + 3.4)}`}
						fill="none"
						stroke={PROCS_INK}
						strokeWidth="3.4"
						strokeLinecap="round"
					/>
				</g>
			))}
		</>
	);
}

/**
 * Hatch blush: two short diagonal ticks per cheek instead of the Proc's soft
 * ellipse. It is the second-loudest anime signal after the eyes, it costs two
 * strokes, and it sits BELOW the eyes because at this head size there is no cheek
 * left beside them.
 */
function HatchBlush({ colour, headClip }: { colour: string; headClip: string }) {
	const ticks = [
		[25, 72, 29, 66],
		[30.5, 73, 34.5, 67],
	];
	return (
		<g clipPath={`url(#${headClip})`}>
			{[false, true].map((mirrored) => (
				<g key={String(mirrored)} transform={mirrored ? MIRROR : undefined}>
					{ticks.map(([x1, y1, x2, y2]) => (
						<path
							key={`${x1}-${y1}`}
							data-blush
							d={`M${x1} ${y1} L${x2} ${y2}`}
							stroke={colour}
							strokeWidth="2.6"
							strokeLinecap="round"
							fill="none"
						/>
					))}
				</g>
			))}
		</g>
	);
}

/** The open, surprised mouth every species wears while it is being held. */
function StartledMouth() {
	return <ellipse data-mouth cx="48" cy="71" rx="4.6" ry="5.4" fill={PROCS_INK} />;
}

// ---------------------------------------------------------------- Proc
//
// The original, reproduced EXACTLY as it was drawn before there was a species axis.
// A Proc that changed by one unit when this file arrived would be a regression on
// every session in existence, so this is a move rather than a redraw.

const PROC: SpeciesArt = {
	head: { ...HEAD, rx: 26 },
	Face: ({ cast, held, headClip }) => (
		<>
			<g clipPath={`url(#${headClip})`}>
				<ellipse data-blush cx="26" cy="63" rx="7" ry="4.5" fill={cast.blush} />
				<ellipse data-blush cx="70" cy="63" rx="7" ry="4.5" fill={cast.blush} />
			</g>
			<circle data-eye cx="33" cy="53" r={held ? 11.5 : 10} fill={PROCS_INK} />
			<circle data-eye cx="63" cy="53" r={held ? 11.5 : 10} fill={PROCS_INK} />
			<circle cx={held ? 30 : 29.5} cy={held ? 47 : 49} r={held ? 4.2 : 3.4} fill={PROCS_LIGHT} />
			<circle cx={held ? 60 : 59.5} cy={held ? 47 : 49} r={held ? 4.2 : 3.4} fill={PROCS_LIGHT} />
			{held ? (
				<ellipse data-mouth cx="48" cy="69" rx="4.6" ry="5.4" fill={PROCS_INK} />
			) : (
				<path
					data-mouth
					d="M43 67 C 45 71 51 71 53 67"
					fill="none"
					stroke={PROCS_INK}
					strokeWidth="2.6"
					strokeLinecap="round"
				/>
			)}
		</>
	),
};

// ---------------------------------------------------------------- Kitsu
//
// The listener. Ours because the ears are not an animal's: they are SIGNAL ears
// with a notched inner shell, and their pose is a pure function of what the cord is
// doing — perked while data flows, straight up when it is tugging at you, pinned
// back at a failed run, drooped at rest, limp when it is out. The link is therefore
// legible from behind a hat, from the corner of an eye, and at 30px, which the cord
// alone is not.

/** Authored for the LEFT ear. Rotated about the base's midpoint; the right one mirrors. */
const EAR_ROOT = [30, 19.5] as const;
/** The ear's point, the one part of any character that can reach the top of the frame. */
export const EAR_TIP = [9, -14] as const;
const EAR_OUTER = `M20 27 L ${EAR_TIP[0]} ${EAR_TIP[1]} L 40 12 Z`;
const EAR_INNER = "M24 24 L 17 -3 L 34 13 Z";

/**
 * Where an ear's point lands for a given pose, in rig coordinates.
 *
 * Worth a function of its own because it is the one measurement that can go wrong
 * silently: the drawn frame is clipped (`overflow: hidden`, so an attached cord ends
 * in a clean cut rather than a diagonal across the neighbour), and an ear perked one
 * degree too far is a Proc with a flat top and no error anywhere. Pinned in
 * `species-art.test.tsx` against `PROCS_VIEW` for every cord state.
 */
export function earTip(pose: { angle: number; scale: number }): { x: number; y: number } {
	const radians = (pose.angle * Math.PI) / 180;
	const dx = EAR_TIP[0] - EAR_ROOT[0];
	const dy = EAR_TIP[1] - EAR_ROOT[1];
	return {
		x: round(EAR_ROOT[0] + (dx * Math.cos(radians) - dy * Math.sin(radians)) * pose.scale),
		y: round(EAR_ROOT[1] + (dx * Math.sin(radians) + dy * Math.cos(radians)) * pose.scale),
	};
}

const KITSU: SpeciesArt = {
	head: { ...HEAD, rx: 28 },
	Chest: ({ cord }) => (
		<g data-slot="ruff" data-cord={cord}>
			{/* A fluff ruff between head and body: the species mark that survives every
			    held prop, because the props hang to the LEFT and this sits centre. */}
			{[
				[30, 81, 9.5],
				[66, 81, 9.5],
				[48, 84, 11],
			].map(([cx, cy, r]) => (
				<circle key={cx} data-rim cx={cx} cy={cy} r={r} fill={PROP_COLOURS.linen} {...RIM} />
			))}
		</g>
	),
	Face: ({ cast, held, headClip }) => (
		<>
			<HatchBlush colour={cast.blush} headClip={headClip} />
			<AnimeEyes iris={IRIS_BY_PALETTE[cast.palette]} held={held} />
			{held ? (
				<StartledMouth />
			) : (
				<>
					{/* A small ink nose over a cat mouth — the ω that says "anime" and
					    "not a person" in the same two strokes. */}
					<path d="M44.6 66 L51.4 66 L48 69.6 Z" fill={PROCS_INK} />
					<path
						data-mouth
						d="M42 71 C 44 75 47 75 48 71.6 C 49 75 52 75 54 71"
						fill="none"
						stroke={PROCS_INK}
						strokeWidth="2.6"
						strokeLinecap="round"
						strokeLinejoin="round"
					/>
				</>
			)}
		</>
	),
	Crown: ({ cast, cord }) => {
		const pose = EAR_POSE[cord];
		const motion = TELL_MOTION[cord];
		return (
			<g data-slot="tell" data-tell="ears" data-cord={cord}>
				{[false, true].map((mirrored) => (
					<g key={String(mirrored)} transform={mirrored ? MIRROR : undefined}>
						<g transform={pivoted(EAR_ROOT, pose.angle, pose.scale)}>
							{/* Motion on its own group: a keyframe REPLACES a transform attribute
							    rather than composing with it, so the pose and the twitch cannot
							    share a node. */}
							<g
								style={
									motion
										? {
												animation: `procs-swing-${motion} 1.4s ease-in-out infinite`,
												transformOrigin: tellOrigin(EAR_ROOT),
											}
										: undefined
								}
							>
								<path data-rim data-ear d={EAR_OUTER} fill={cast.body} strokeLinejoin="round" {...RIM} />
								<path data-ear-lining d={EAR_INNER} fill={cast.blush} />
							</g>
						</g>
					</g>
				))}
			</g>
		);
	},
};

// ---------------------------------------------------------------- Sprite
//
// A sprite in both senses at once: the folklore one and the 2D one. Ours because
// the wings are not insect wings — they are the three stacked FRAMES of a sprite
// sheet, each with the scanline of its own cell across it, and they beat while the
// session streams. The ahoge is drawn over the hat, so this one is recognisable in
// a hard hat.

// TWO broad lobes per side, and the reason is a measurement rather than a taste.
//
// A wing drawn BEHIND the figure is 90% hidden: the head alone is 68 of the rig's 96
// units wide, so all a wing can ever show is the ~22 units of margin outside it. The
// first pass answered that with three narrow plates, and three narrow slivers either
// side of a head do not read as wings — they read as LAUREL LEAVES, and worse, as the
// beast ears Kitsu already owns. What survives the margin is one WIDE lobe: at the
// head's edge its cross-section is broad enough to be a wing tip rather than a frond.
const WING_ROOT = [33, 78] as const;
const WING_PLATES = [
	{ root: [33, 78] as const, tip: [-5, 29] as const, bulge: 13 },
	{ root: [33, 81] as const, tip: [3, 65] as const, bulge: 8 },
];

const SPRITE: SpeciesArt = {
	head: { ...HEAD, rx: 30 },
	Back: ({ cord }) => {
		const pose = WING_POSE[cord];
		const motion = TELL_MOTION[cord];
		return (
			<g data-slot="tell" data-tell="wings" data-cord={cord}>
				{[false, true].map((mirrored) => (
					<g key={String(mirrored)} transform={mirrored ? MIRROR : undefined}>
						<g transform={pivoted(WING_ROOT, pose.angle, pose.scale)}>
							<g
								style={
									motion
										? {
												animation: `procs-swing-${motion} 1.1s ease-in-out infinite`,
												transformOrigin: tellOrigin(WING_ROOT),
											}
										: undefined
								}
							>
								{WING_PLATES.map((plate) => (
									<path
										key={plate.tip[0]}
										data-rim
										data-wing
										d={leaf(plate.root, plate.tip, plate.bulge)}
										fill={PROP_COLOURS.linen}
										strokeLinejoin="round"
										{...RIM}
									/>
								))}
								{/* STRAIGHT cell-lines across each lobe, not the branching veins a
								    real wing has. That is the sprite-sheet in the sprite: the wing
								    is cut into frames, and it is the one detail here that is ours
								    rather than every fairy's. */}
								{WING_PLATES.flatMap((plate) =>
									[0.42, 0.68].map((t) => (
										<path
											key={`cell-${plate.tip[0]}-${t}`}
											d={cellLine(plate.root, plate.tip, plate.bulge, t)}
											stroke={PROCS_INK}
											strokeWidth="1.5"
											strokeLinecap="round"
											opacity="0.42"
											fill="none"
										/>
									)),
								)}
							</g>
						</g>
					</g>
				))}
			</g>
		);
	},
	Face: ({ cast, held, headClip }) => (
		<>
			{/* Sidelocks, BELOW the hat line so they are not a thing only a bare head has.
			    They hang past the head's own edge, so they face the wallpaper and carry
			    the rim like everything else out there. */}
			{[false, true].map((mirrored) => (
				<path
					key={String(mirrored)}
					data-rim
					data-sidelock
					transform={mirrored ? MIRROR : undefined}
					d="M17 30 C 10 44 11 58 16.5 66 C 22.5 58 22 42 25 33 Z"
					fill={cast.shade}
					strokeLinejoin="round"
					{...RIM}
				/>
			))}
			<HatchBlush colour={cast.blush} headClip={headClip} />
			<AnimeEyes iris={IRIS_BY_PALETTE[cast.palette]} held={held} />
			{held ? (
				<StartledMouth />
			) : (
				<path data-mouth d="M43.5 69 C 45 74.5 51 74.5 52.5 69 Z" fill={PROCS_INK} strokeLinejoin="round" />
			)}
		</>
	),
	Crown: ({ cast }) => (
		<g data-slot="ahoge">
			{/* The one stray strand. Drawn over the hat on purpose — an ahoge that a
			    beanie swallowed would leave this one indistinguishable from a Proc. */}
			<CasedStroke part="ahoge" d="M47 11 C 44 4 51 1 54 -6" core={2.6} colour={cast.shade} />
		</g>
	),
};

// ---------------------------------------------------------------- Unit
//
// The build unit. Ours because its face is a soft HELM — square-shouldered where
// the others are round — and because the link is on its chest rather than its head:
// one lamp in a hex bezel that is bright and pulsing while data flows, amber-steady
// when a PR is up, hot orange and flickering when a run fails, and dark with a slash
// through it when the cord comes out.

const UNIT_CORE = [48, 88] as const;

const UNIT: SpeciesArt = {
	// Squarer than the other three and no squarer than that. At rx 14 the helm read
	// as a toaster: the corners, the guards and a grille mouth together left the face
	// nowhere to be, and the one thing that has to survive is that this is a CHARACTER
	// in a helm rather than a machine with eyes painted on.
	head: { ...HEAD, rx: 19 },
	Chest: ({ cord }) => {
		const glow = CORE_GLOW[cord];
		const motion = TELL_MOTION[cord];
		const lit = lampColour(cord);
		return (
			<g data-slot="tell" data-tell="core" data-cord={cord}>
				<path
					data-rim
					data-core-bezel
					d={`M${UNIT_CORE[0]} ${UNIT_CORE[1] - 9.5} L${UNIT_CORE[0] + 8.5} ${UNIT_CORE[1] - 4.8} L${UNIT_CORE[0] + 8.5} ${UNIT_CORE[1] + 4.8} L${UNIT_CORE[0]} ${UNIT_CORE[1] + 9.5} L${UNIT_CORE[0] - 8.5} ${UNIT_CORE[1] + 4.8} L${UNIT_CORE[0] - 8.5} ${UNIT_CORE[1] - 4.8} Z`}
					fill={PROCS_INK}
					strokeLinejoin="round"
					{...RIM}
				/>
				<g
					style={
						motion
							? { animation: `procs-lamp-${motion} 1.2s ease-in-out infinite`, transformOrigin: tellOrigin(UNIT_CORE) }
							: undefined
					}
				>
					<circle data-core-lamp cx={UNIT_CORE[0]} cy={UNIT_CORE[1]} r="5.6" fill={lit} />
				</g>
				{glow === 0 && (
					// Dark is not a reading on its own at 30px — a pale lamp and an unlit one
					// measure the same. The slash is what says OFF.
					<path
						data-core-off
						d={`M${UNIT_CORE[0] - 5.6} ${UNIT_CORE[1] - 5.6} L${UNIT_CORE[0] + 5.6} ${UNIT_CORE[1] + 5.6}`}
						stroke={PROCS_INK}
						strokeWidth="2.2"
						strokeLinecap="round"
						fill="none"
					/>
				)}
			</g>
		);
	},
	Face: ({ cast, held, headClip }) => (
		<>
			{/* Ear guards, at the sides where no hat reaches. Hex, bolted, and the widest
			    part of the silhouette — which is what tells a Unit from a Proc across a room. */}
			{[false, true].map((mirrored) => (
				<g key={String(mirrored)} transform={mirrored ? MIRROR : undefined}>
					<path
						data-rim
						data-guard
						d="M12 44 L19.5 48 L19.5 58 L12 62 L4.5 58 L4.5 48 Z"
						fill={cast.shade}
						strokeLinejoin="round"
						{...RIM}
					/>
					<circle cx="12" cy="53" r="2.2" fill={PROCS_INK} />
				</g>
			))}
			<HatchBlush colour={cast.blush} headClip={headClip} />
			<AnimeEyes iris={IRIS_BY_PALETTE[cast.palette]} held={held} />
			{held ? (
				<StartledMouth />
			) : (
				// A visor slit, not a grille. The grille it replaced put eight ink strokes
				// directly under the eyes and buried the face in hardware — the mechanical
				// reading has to come from the helm and the guards, never from the mouth.
				<g data-mouth>
					{/* No rim on it: the rim is for shapes that face the WALLPAPER, and a
					    mouth never does. Rimmed, this measured 10 units of solid ink across
					    the chin and read as a gag rather than a visor. */}
					<rect x="42" y="69.6" width="12" height="4.6" rx="2.3" fill={PROCS_INK} />
					<path d="M44.6 71.9 L51.4 71.9" stroke={PROCS_LIGHT} strokeWidth="1.3" strokeLinecap="round" fill="none" />
				</g>
			)}
		</>
	),
	Crown: ({ cast }) => (
		<g data-slot="antenna">
			{/* Short, straight and beaded — a mecha aerial. Sprite's ahoge is long and
			    curls; keeping the two top silhouettes different is what stops the pair
			    reading as one character at the edge of vision. */}
			<CasedStroke part="antenna" d="M48 10 L 48 -8" core={2.8} colour={cast.shade} />
			<circle data-rim cx="48" cy="-11" r="3.6" fill={cast.shade} {...RIM} />
		</g>
	),
};

export const SPECIES_ART: Record<SpeciesId, SpeciesArt> = {
	proc: PROC,
	kitsu: KITSU,
	sprite: SPRITE,
	unit: UNIT,
};
