import type { CastMember } from "./cast";
import { PROCS_INK, PROCS_LIGHT, PROP_COLOURS } from "./palette";
import { RIM } from "./props";
import type { Scene } from "./scene";
import { GLOW, glowColour, HOVER, LIMB_POSE, TELL_MOTION, type SpeciesId } from "./species";

// One RIG per creature.
//
// This is the part @agent-orchestrator-159 flagged as the one thing that could not be
// made generic, and they were right: a colour is a parameter and a hat is a layer,
// but a different SILHOUETTE is a different drawing. So each creature owns its body,
// its face, its legs-or-none, and the part of it that reports the link.
//
// What a rig does NOT own is everything that makes a pet a pet, and that is the
// point of the seam. The scene layer (ground prop, held prop, emitted zzz), the
// cord and its plug, the drawn frame, the pointer region, the name chip and the
// bubble are all drawn by `Procs.tsx` around whichever rig it picked. A rig is a
// body; the machinery is the same machinery.
//
// Three rules every rig here obeys:
//
//   1. TWO CHANNELS on anything facing the wallpaper — a swept fill plus the 2.4px
//      ink rim — because these live on someone's desktop and there is no theme.
//   2. PATHS IN M/L/C/Z ONLY, so `mirrorPathX` and the measuring tests can read them.
//   3. POSITION AND MOTION ON SEPARATE GROUPS, because a CSS transform keyframe
//      REPLACES an element's SVG transform attribute rather than composing with it.

export type RigProps = {
	cast: CastMember;
	scene: Scene;
	/** Being picked up: startled face, and whatever "dangling" means for this body. */
	held: boolean;
	walking: boolean;
	/** The walk/hop/drift cycle, already chosen by the caller (a run is the same beat, faster). */
	cycleMs: number;
	/** Unique per instance, for clip path ids. */
	uid: string;
	/** The task prop — a page, a laptop, a sign — already mirrored for the facing. */
	heldProp: React.ReactNode;
	/**
	 * The Proc's hat, as the shell assembles it from `HATS`.
	 *
	 * ⚠ ONLY the Proc uses it. Every other creature draws its own accessory from
	 * `cast.hatId` — a collar, a halo, a cherry suspended in jelly — because those are
	 * not one shape in one slot and no shared table could have placed them.
	 */
	hat: React.ReactNode;
};

/** One strip cell, and the figure's own width: cell N sits at x = N × CELL. */
export const CELL = 96;

// ---------------------------------------------------------------- shared bits

function round(value: number): number {
	return Math.round(value * 10) / 10;
}

/** Mirror about a creature's own centre line. Not always 48 — a cat's head is off to one side. */
function mirror(axis: number): string {
	return `translate(${axis * 2} 0) scale(-1 1)`;
}

/**
 * Rotate and shrink a part about its own root.
 *
 * Nested into one transform string rather than an SVG `rotate(a cx cy)` because the
 * folded poses need to FORESHORTEN as well as swing: swing a 40-unit ear down through
 * 70° at full length and its tip goes clear out of the frame, which is clipped. A real
 * ear folds away from you, and in flat art that is a shorter ear.
 */
export function pivoted(root: readonly [number, number], angle: number, scale: number): string {
	return `translate(${root[0]} ${root[1]}) rotate(${angle}) scale(${scale}) translate(${-root[0]} ${-root[1]})`;
}

/**
 * The CSS `transform-origin` for a tell's motion group, given the point it turns about.
 *
 * ⚠ Not the rig coordinate itself, and this is the trap. An SVG element's
 * `transform-box` is `view-box`, so a CSS `transform-origin` is measured from the VIEW
 * BOX's corner — which is at (-8, -24) here, not (0, 0). Written as the raw pivot,
 * every swing turns about a point 25 units up and left of where it should and the part
 * pumps instead of pivoting. `rigs.test.tsx` pins these against `PROCS_VIEW`.
 */
export const TELL_VIEW_ORIGIN = { x: -8, y: -24 } as const;

export function tellOrigin(pivot: readonly [number, number]): string {
	return `${round(pivot[0] - TELL_VIEW_ORIGIN.x)}px ${round(pivot[1] - TELL_VIEW_ORIGIN.y)}px`;
}

/** Blend two `#rrggbb` colours, so a glow DIMS to grey rather than fading out. */
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

/**
 * A lit tell's colour for a cord state. Exported because it is the one colour on
 * these bodies that is COMPUTED, and a computed colour is one nothing can enumerate.
 */
export function tellGlow(cord: Scene["cord"]): string {
	return mix(PROP_COLOURS.quiet, glowColour(cord), GLOW[cord]);
}

/**
 * Eyes, and each creature gets its OWN.
 *
 * ⚠ The first pass gave all six the Proc's round eye with one highlight, and the human
 * called it immediately: six different bodies wearing one face is six of the same
 * character again, which is the exact thing this whole direction exists to fix. The
 * eyes are the first thing anybody looks at, so they are the last place to save effort.
 *
 * - `round`   the Proc's: a dark mass with a highlight riding on it
 * - `tall`    a sheet-ghost's: taller than wide, the folk shape of a hole in a cloth
 * - `slit`    a cat's: a bright vertical slit rather than a round catchlight
 * - `glassy`  a slime's: two highlights, because it is made of the same stuff as its gloss
 * - `bead`    a bird's: small, round and hard, with a rim of white around it
 * - `arc`     a toadstool's: a happy closed curve, no pupil at all
 */
export type EyeStyle = "round" | "tall" | "slit" | "glassy" | "bead" | "arc";

/**
 * How long a blink cycle is, per creature, and when in it the eyes shut.
 *
 * Staggered on purpose: a band of pets blinking in unison reads as a screen refresh
 * rather than as a room full of living things. The eyes are SHUT for about 4% of the
 * cycle, which is roughly what a real blink is, and the whole thing is a scaleY —
 * transform only, so it stays on the compositor and costs nothing.
 *
 * Blinking says nothing about status and is not a tell. It is the same class of thing
 * as a ghost's hover or a Proc's walk: how a drawing is alive rather than printed.
 */
const BLINK_MS: Record<SpeciesId, number> = {
	proc: 5200,
	ghost: 6100,
	cat: 4300,
	slime: 5700,
	chick: 3700,
	toadstool: 6700,
};

/**
 * Where in its blink cycle THIS pet starts.
 *
 * ⚠ Not decoration. Every pet on the band mounts at the same moment, so without this
 * they all run the same keyframes from the same instant and the whole cast blinks in
 * time — caught on the real band, where three cats shut their eyes together and it read
 * as the screen refreshing rather than as three animals. Staggering the two eyes of one
 * face was never going to fix that; the pets have to be out of phase with each other.
 *
 * Derived from the instance's own id, so it is stable across re-renders: a phase that
 * was re-rolled every render would restart the animation and the eye would never shut.
 *
 * ⚠ FNV-1a with murmur3's avalanche finalizer, and it is the SAME lesson `cast.ts`
 * already learned the hard way: taking `% n` of a weak hash uses its low bits, and
 * React hands out sequential ids (`:r0:`, `:r1:`, `:r2:`). A plain rolling hash of
 * those came out 1ms apart across eight pets — measured on the real band — which is
 * still in unison as far as anybody watching is concerned. The finalizer mixes the high
 * bits down and spreads them across the whole cycle.
 */
export function blinkPhase(uid: string, cycle: number): number {
	let value = 0x811c9dc5;
	for (let i = 0; i < uid.length; i++) {
		value ^= uid.charCodeAt(i);
		value = Math.imul(value, 0x01000193);
	}
	value ^= value >>> 16;
	value = Math.imul(value, 0x85ebca6b);
	value ^= value >>> 13;
	value = Math.imul(value, 0xc2b2ae35);
	value ^= value >>> 16;
	return (value >>> 0) % cycle;
}

function Eyes({
	at,
	r,
	held,
	style,
	blink,
	uid,
}: {
	at: ReadonlyArray<readonly [number, number]>;
	r: number;
	held: boolean;
	style: EyeStyle;
	/** The blink cycle for this creature, or 0 for eyes that are already closed. */
	blink: number;
	/** This instance's id, so this pet blinks out of step with the one beside it. */
	uid: string;
}) {
	const radius = held ? r * 1.16 : r;
	const phase = blink ? blinkPhase(uid, blink) : 0;
	return (
		<>
			{at.map(([cx, cy], index) => (
				<g
					key={`${cx}-${cy}`}
					data-eye
					data-eye-style={style}
					style={
						blink
							? {
									// ⚠ `fill-box`, not the view box. An SVG element's transform-origin
									// is measured from the VIEW BOX's corner by default, which is at
									// (-8, -24) here — get that wrong and the eye pivots about a point
									// well above itself and appears to SLIDE down its own face instead
									// of closing. Against its own bounding box, "center" is the eye's
									// centre whatever shape or size the eye is, and there is no
									// arithmetic left to get wrong.
									transformBox: "fill-box",
									transformOrigin: "center",
									animation: `procs-blink ${blink}ms ease-in-out ${phase + index * 60}ms infinite`,
								}
							: undefined
					}
				>
					{style === "arc" ? (
						<path
							d={`M${round(cx - radius)} ${round(cy + radius * 0.3)} C ${round(cx - radius * 0.4)} ${round(cy - radius * 0.8)} ${round(cx + radius * 0.4)} ${round(cy - radius * 0.8)} ${round(cx + radius)} ${round(cy + radius * 0.3)}`}
							fill="none"
							stroke={PROCS_INK}
							strokeWidth={round(radius * 0.42)}
							strokeLinecap="round"
						/>
					) : (
						<>
							{style === "bead" && (
								// A thin ring of white around a small hard pupil, which is what a
								// bird's eye is. ⚠ Kept THIN: at 1.24× with a heavy stroke the ring
								// read as a pair of goggles strapped to the bird's face.
								<circle
									cx={cx}
									cy={cy}
									r={round(radius * 1.06)}
									fill={PROCS_LIGHT}
									stroke={PROCS_INK}
									strokeWidth="1.3"
								/>
							)}
							<ellipse
								cx={cx}
								cy={cy}
								rx={round(style === "tall" ? radius * 0.82 : style === "bead" ? radius * 0.78 : radius)}
								ry={round(style === "tall" ? radius * 1.24 : style === "bead" ? radius * 0.78 : radius)}
								fill={PROCS_INK}
							/>
							{style === "slit" ? (
								// A cat's catchlight is a SLIT, standing where a round one would sit.
								// Nothing else in the cast has a vertical highlight, and at 30px that
								// is the whole difference between an animal's eye and a dot.
								<ellipse
									cx={round(cx - radius * 0.22)}
									cy={round(cy - radius * 0.1)}
									rx={round(radius * 0.2)}
									ry={round(radius * 0.62)}
									fill={PROCS_LIGHT}
								/>
							) : (
								<circle
									cx={round(cx - radius * 0.34)}
									cy={round(cy - radius * (held ? 0.5 : 0.4))}
									r={round(radius * (held ? 0.42 : style === "bead" ? 0.24 : 0.36))}
									fill={PROCS_LIGHT}
								/>
							)}
							{style === "glassy" && (
								// A second, smaller catchlight low and opposite: the same trick the
								// gloss on its body uses, so the eye is made of the creature.
								<circle
									cx={round(cx + radius * 0.36)}
									cy={round(cy + radius * 0.42)}
									r={round(radius * 0.18)}
									fill={PROCS_LIGHT}
								/>
							)}
						</>
					)}
				</g>
			))}
		</>
	);
}

/** The small smile every creature wears, and the open mouth it wears while being held. */
function Mouth({ cx, cy, width, held }: { cx: number; cy: number; width: number; held: boolean }) {
	if (held)
		return <ellipse data-mouth cx={cx} cy={cy + 1} rx={round(width * 0.4)} ry={round(width * 0.48)} fill={PROCS_INK} />;
	const half = width / 2;
	return (
		<path
			data-mouth
			d={`M${round(cx - half)} ${cy} C ${round(cx - half * 0.4)} ${round(cy + width * 0.42)} ${round(cx + half * 0.4)} ${round(cy + width * 0.42)} ${round(cx + half)} ${cy}`}
			fill="none"
			stroke={PROCS_INK}
			strokeWidth="2.6"
			strokeLinecap="round"
		/>
	);
}

/**
 * Cheeks, and each creature gets its own of these too.
 *
 * `soft` is the Proc's flat oval. `hatch` is two diagonal ticks. `dot` is a small round
 * one for a face too small for anything else. `whisker` is a cheek with three whiskers
 * across it, which no other creature has and which says CAT before the ears do.
 *
 * Always clipped to the body they sit on, whatever the shape: a cheek that spilled past
 * the outline would be a colour facing the wallpaper that nothing has measured there,
 * and it would read as a smudge besides.
 */
export type CheekStyle = "soft" | "hatch" | "dot" | "whisker";

function Blush({
	at,
	rx,
	ry,
	colour,
	clip,
	style,
}: {
	at: ReadonlyArray<readonly [number, number]>;
	rx: number;
	ry: number;
	colour: string;
	clip: string;
	style: CheekStyle;
}) {
	return (
		<g clipPath={`url(#${clip})`}>
			{at.map(([cx, cy], index) => (
				<g key={`${cx}-${cy}`} data-cheek={style}>
					{style === "hatch" ? (
						[-1, 0, 1].map((step) => (
							<path
								key={step}
								data-blush
								d={`M${round(cx + step * rx * 0.5 - rx * 0.22)} ${round(cy + ry)} L${round(cx + step * rx * 0.5 + rx * 0.22)} ${round(cy - ry)}`}
								stroke={colour}
								strokeWidth="2.4"
								strokeLinecap="round"
								fill="none"
							/>
						))
					) : (
						<ellipse
							data-blush
							cx={cx}
							cy={cy}
							rx={round(style === "dot" ? Math.min(rx, ry) : rx)}
							ry={round(style === "dot" ? Math.min(rx, ry) : ry)}
							fill={colour}
						/>
					)}
					{style === "whisker" &&
						[-1, 0, 1].map((step) => (
							<path
								key={step}
								data-whisker
								// Away from the muzzle, so they read as whiskers and not as a scratch:
								// the outer end is always the far side of the face.
								d={`M${round(cx + (index === 0 ? rx * 0.6 : -rx * 0.6))} ${round(cy + step * 3.4)} L${round(cx + (index === 0 ? -rx * 1.9 : rx * 1.9))} ${round(cy + step * 5.2 - 1.4)}`}
								stroke={PROCS_INK}
								strokeWidth="1.5"
								strokeLinecap="round"
								opacity="0.75"
								fill="none"
							/>
						))}
				</g>
			))}
		</g>
	);
}

/**
 * A pair of limbs that swing to report the link: a cat's ears, a ghost's sleeves, a
 * chick's wings.
 *
 * One component for all three because they are the same mechanism — authored once for
 * the left, mirrored for the right, posed by `LIMB_POSE`, and animated on a separate
 * group so the pose and the twitch never share a node.
 */
function SwingPair({
	part,
	cord,
	root,
	axis,
	speedMs,
	single = false,
	children,
}: {
	part: string;
	cord: Scene["cord"];
	root: readonly [number, number];
	axis: number;
	speedMs: number;
	/** One copy rather than a mirrored pair — a crest, of which there is only ever one. */
	single?: boolean;
	children: React.ReactNode;
}) {
	const pose = LIMB_POSE[cord];
	const motion = TELL_MOTION[cord];
	return (
		<g data-slot="tell" data-tell={part} data-cord={cord}>
			{(single ? [false] : [false, true]).map((mirrored) => (
				<g key={String(mirrored)} transform={mirrored ? mirror(axis) : undefined}>
					<g transform={pivoted(root, pose.angle, pose.scale)}>
						<g
							style={
								motion
									? {
											animation: `procs-swing-${motion} ${speedMs}ms ease-in-out infinite`,
											transformOrigin: tellOrigin(root),
										}
									: undefined
							}
						>
							{children}
						</g>
					</g>
				</g>
			))}
		</g>
	);
}

/** A glowing tell: a slime's nucleus, a toadstool's spots. Brightness, plus a hue at failure. */
function GlowPart({
	part,
	cord,
	origin,
	children,
}: {
	part: string;
	cord: Scene["cord"];
	origin: readonly [number, number];
	children: React.ReactNode;
}) {
	const motion = TELL_MOTION[cord];
	return (
		<g data-slot="tell" data-tell={part} data-cord={cord}>
			<g
				style={
					motion
						? { animation: `procs-lamp-${motion} 1200ms ease-in-out infinite`, transformOrigin: tellOrigin(origin) }
						: undefined
				}
			>
				{children}
			</g>
			{GLOW[cord] === 0 && (
				// Dark is not a reading on its own at 30px — a pale glow and an unlit one
				// measure the same. The slash is what says OUT.
				<path
					data-tell-off
					d={`M${round(origin[0] - 5)} ${round(origin[1] - 5)} L${round(origin[0] + 5)} ${round(origin[1] + 5)}`}
					stroke={PROCS_INK}
					strokeWidth="2.2"
					strokeLinecap="round"
					fill="none"
				/>
			)}
		</g>
	);
}

/**
 * The four-beat leg strip: four poses drawn side by side in one row, stepped through
 * one cell at a time. A run is the same strip stepped faster, never a second
 * animation, so the legs cannot disagree with themselves about which pose comes next.
 */
function Strip({
	uid,
	top,
	walking,
	cycleMs,
	children,
}: {
	uid: string;
	top: number;
	walking: boolean;
	cycleMs: number;
	children: React.ReactNode;
}) {
	const clip = `procs-cell-${uid}`;
	return (
		<>
			<defs>
				<clipPath id={clip}>
					<rect x="0" y={top} width={CELL} height={132 - top} />
				</clipPath>
			</defs>
			<g clipPath={`url(#${clip})`}>
				<g
					data-walk-strip
					style={walking ? { animation: `procs-walk ${cycleMs}ms steps(4, end) infinite` } : undefined}
				>
					{children}
				</g>
			</g>
		</>
	);
}

// ---------------------------------------------------------------- accessories
//
// Each creature wears its OWN, and the shared ones are here because a bow is a bow
// wherever it is pinned. What is NOT shared is where each creature puts it — a ghost's
// halo floats above it, a cat's collar goes round its neck, a slime's cherry is
// SUSPENDED INSIDE IT, and a toadstool's is the pattern on its cap. None of those four
// is one shape drawn in one slot, which is why the rigs place them rather than a table.
//
// All of them obey the same two rules the bodies do: a swept fill plus the ink rim on
// anything facing the wallpaper, and paths in M/L/C/Z only.

/** A bow: two loops and a knot. Symmetric, so it survives the sprite turning round. */
function Bow({ cx, cy, size, colour }: { cx: number; cy: number; size: number; colour: string }) {
	const w = size;
	return (
		<g data-worn="bow">
			<path
				data-rim
				d={`M${round(cx)} ${round(cy)} L${round(cx - w)} ${round(cy - w * 0.7)} L${round(cx - w)} ${round(cy + w * 0.7)} Z`}
				fill={colour}
				strokeLinejoin="round"
				{...RIM}
			/>
			<path
				data-rim
				d={`M${round(cx)} ${round(cy)} L${round(cx + w)} ${round(cy - w * 0.7)} L${round(cx + w)} ${round(cy + w * 0.7)} Z`}
				fill={colour}
				strokeLinejoin="round"
				{...RIM}
			/>
			<circle data-rim cx={cx} cy={cy} r={round(w * 0.34)} fill={colour} {...RIM} />
		</g>
	);
}

/** A scarf: a band round the neck with one end hanging. */
function Scarf({ cx, cy, width, colour }: { cx: number; cy: number; width: number; colour: string }) {
	const half = width / 2;
	return (
		<g data-worn="scarf">
			<path
				data-rim
				d={`M${round(cx - half)} ${round(cy)} C ${round(cx - half)} ${round(cy + 8)} ${round(cx + half)} ${round(cy + 8)} ${round(cx + half)} ${round(cy)} C ${round(cx + half)} ${round(cy - 5)} ${round(cx - half)} ${round(cy - 5)} ${round(cx - half)} ${round(cy)} Z`}
				fill={colour}
				strokeLinejoin="round"
				{...RIM}
			/>
			<path
				data-rim
				d={`M${round(cx + half - 3)} ${round(cy + 2)} L${round(cx + half + 4)} ${round(cy + 14)} L${round(cx + half - 4)} ${round(cy + 15)} L${round(cx + half - 7)} ${round(cy + 4)} Z`}
				fill={colour}
				strokeLinejoin="round"
				{...RIM}
			/>
		</g>
	);
}

/** A little flower: five petals round a middle. */
function Flower({ cx, cy, r, colour }: { cx: number; cy: number; r: number; colour: string }) {
	return (
		<g data-worn="flower">
			{[0, 1, 2, 3, 4].map((i) => {
				const angle = (i / 5) * Math.PI * 2 - Math.PI / 2;
				return (
					<circle
						key={i}
						data-rim
						cx={round(cx + Math.cos(angle) * r * 0.72)}
						cy={round(cy + Math.sin(angle) * r * 0.72)}
						r={round(r * 0.52)}
						fill={PROP_COLOURS.paper}
						{...RIM}
					/>
				);
			})}
			<circle data-rim cx={cx} cy={cy} r={round(r * 0.42)} fill={colour} {...RIM} />
		</g>
	);
}

/** One stubby leg. */
function Leg({
	x,
	y,
	lift,
	width,
	height,
	colour,
}: {
	x: number;
	y: number;
	lift: number;
	width: number;
	height: number;
	colour: string;
}) {
	return (
		<rect data-rim x={x} y={y - lift} width={width} height={height} rx={round(width / 2)} fill={colour} {...RIM} />
	);
}

// The four beats: wide stance → left foot up → feet together → right foot up.
//
// A pet faces YOU even while it travels sideways, so the legs must never swap sides
// the way a side-view walk cycle's would. Pose 0 doubles as the rest pose, so a
// standing pet shows cell 0 and never freezes mid-stride.
const LEG_POSES = [
	{ key: "contact-a", left: { dx: -4, lift: 0 }, right: { dx: 4, lift: 0 } },
	{ key: "passing-a", left: { dx: -1, lift: 5 }, right: { dx: 2, lift: 0 } },
	{ key: "contact-b", left: { dx: 2, lift: 0 }, right: { dx: -1, lift: 0 } },
	{ key: "passing-b", left: { dx: -1, lift: 0 }, right: { dx: 2, lift: 5 } },
] as const;

// Held: both legs hang, slightly apart and uneven, which is what dangling looks like.
const DANGLE_POSES = [
	{ key: "dangle-a", left: { dx: -3, lift: -4 }, right: { dx: 3, lift: -6 } },
	{ key: "dangle-b", left: { dx: -3, lift: -5 }, right: { dx: 3, lift: -4 } },
	{ key: "dangle-c", left: { dx: -3, lift: -6 }, right: { dx: 3, lift: -5 } },
	{ key: "dangle-d", left: { dx: -3, lift: -4 }, right: { dx: 3, lift: -6 } },
] as const;

// A quadruped's diagonal gait: front-left with back-right, then the other pair. Same
// four cells, so the strip machinery is untouched — only which legs lift when.
const CAT_POSES = [
	{ key: "cat-a", front: [0, 5], back: [5, 0] },
	{ key: "cat-b", front: [0, 0], back: [0, 0] },
	{ key: "cat-c", front: [5, 0], back: [0, 5] },
	{ key: "cat-d", front: [0, 0], back: [0, 0] },
] as const;

// ---------------------------------------------------------------- Proc
//
// The original, drawn exactly as it was before there was a species axis. Every
// session in existence is one of these, so this is a move rather than a redraw.

function ProcRig({ cast, held, walking, cycleMs, uid, heldProp, hat }: RigProps) {
	const headClip = `procs-head-${uid}`;
	return (
		<>
			<defs>
				<clipPath id={headClip}>
					<rect x="14" y="6" width="68" height="72" rx="26" />
				</clipPath>
			</defs>

			<Strip uid={uid} top={94} walking={walking} cycleMs={cycleMs}>
				{(held ? DANGLE_POSES : LEG_POSES).map((pose, index) => (
					<g key={pose.key} data-walk-pose transform={`translate(${index * CELL} 0)`}>
						<Leg x={38 + pose.left.dx} y={100} lift={pose.left.lift} width={9} height={20} colour={cast.shade} />
						<Leg x={50 + pose.right.dx} y={100} lift={pose.right.lift} width={9} height={20} colour={cast.shade} />
					</g>
				))}
			</Strip>

			<g style={walking ? { animation: `procs-bob ${cycleMs}ms ease-in-out infinite alternate` } : undefined}>
				<rect data-rim data-part="body" x="29" y="74" width="38" height="30" rx="14" fill={cast.shade} {...RIM} />
				<rect data-rim data-part="head" x="14" y="6" width="68" height="72" rx="26" fill={cast.body} {...RIM} />
				<Blush
					at={[
						[26, 63],
						[70, 63],
					]}
					rx={7}
					ry={4.5}
					colour={cast.blush}
					clip={headClip}
					style="soft"
				/>
				<Eyes
					at={[
						[33, 53],
						[63, 53],
					]}
					r={10}
					held={held}
					style="round"
					blink={BLINK_MS.proc}
					uid={uid}
				/>
				<Mouth cx={48} cy={67} width={10} held={held} />
				<g transform={hatTransform("proc")}>{hat}</g>
				{heldProp}
			</g>
		</>
	);
}

// ---------------------------------------------------------------- Ghost
//
// A cloth draped over nothing, floating a hand's width off the floor.
//
// Ours, and deliberately not the rounded blob with dot eyes that everyone else's
// cute ghost is: this one has a PEAKED crown where the cloth is pulled up, a
// three-lobe scalloped hem, and two sleeve-corners where its arms would be if it had
// any. The wisp that trails off it to the plug is the power lead — the same cord,
// the same six states, grown out of different anatomy.
//
// It is the one creature with no hat: the drape IS the silhouette, and a beanie on
// top of it turns a ghost into a bag.

const GHOST_BODY =
	"M16 106 C 20 118 33 118 37.3 106 C 41.6 118 54.4 118 58.7 106 C 63 118 76 118 80 106 L 80 62 C 80 32 66 18 48 18 C 30 18 16 32 16 62 Z";
// ⚠ Lifted and lengthened after the first render. Sleeves at the drape's waist were
// two things at once: too small to read, and directly behind the HELD PROP — which
// always hangs on the left — so eight of the fifteen states hid one of them entirely.
// Anything that reports the link has to live where a page cannot cover it.
const GHOST_SLEEVE = "M24 54 C 10 54 0 62 0 72 C 10 74 21 68 28 60 Z";
const GHOST_SLEEVE_ROOT = [26, 56] as const;

function GhostRig({ cast, scene, held, walking, cycleMs, uid, heldProp }: RigProps) {
	const bodyClip = `procs-body-${uid}`;
	// A ghost that has lost its lead SINKS. Nothing else in the cast can say "gone" by
	// changing its height, because nothing else in the cast is holding itself up — and
	// it costs no ink at all, which at 30px is worth more than another prop.
	const lift = HOVER[scene.cord];
	return (
		<>
			<defs>
				<clipPath id={bodyClip}>
					<path d={GHOST_BODY} />
				</clipPath>
			</defs>

			<g transform={`translate(0 ${-lift})`}>
				{/* The float is its resting state, not its walk: a ghost is never NOT
				    hovering. Walking only widens the drift. */}
				<g
					style={{
						animation: `procs-float ${walking ? cycleMs : 2600}ms ease-in-out infinite alternate`,
					}}
				>
					<SwingPair part="sleeves" cord={scene.cord} root={GHOST_SLEEVE_ROOT} axis={48} speedMs={1500}>
						<path data-rim data-sleeve d={GHOST_SLEEVE} fill={cast.body} strokeLinejoin="round" {...RIM} />
					</SwingPair>

					<path data-rim data-part="body" d={GHOST_BODY} fill={cast.body} strokeLinejoin="round" {...RIM} />
					<Blush
						at={[
							[27, 76],
							[69, 76],
						]}
						rx={6.5}
						ry={4}
						colour={cast.blush}
						clip={bodyClip}
						style="soft"
					/>
					<Eyes
						at={[
							[35, 64],
							[61, 64],
						]}
						r={8.5}
						held={held}
						style="tall"
						blink={BLINK_MS.ghost}
						uid={uid}
					/>
					<Mouth cx={48} cy={78} width={9} held={held} />
					<GhostWorn worn={cast.hatId} colour={cast.shade} />
					{heldProp}
				</g>
			</g>
		</>
	);
}

/** What a ghost haunts with. Placed at the peak of the drape, where a head would be. */
function GhostWorn({ worn, colour }: { worn: string; colour: string }) {
	if (worn === "halo") {
		return (
			<g data-worn="halo">
				{/* Above it and not touching, which is the only way a halo reads as one. */}
				<ellipse data-rim cx="48" cy="6" rx="17" ry="5.5" fill="none" {...RIM} />
				<ellipse cx="48" cy="6" rx="17" ry="5.5" fill="none" stroke={PROP_COLOURS.spark} strokeWidth="2.6" />
			</g>
		);
	}
	if (worn === "candle") {
		return (
			<g data-worn="candle">
				<rect data-rim x="43" y="6" width="10" height="16" rx="2" fill={PROP_COLOURS.linen} {...RIM} />
				<path data-rim d="M48 -6 C 53 0 52 6 48 6 C 44 6 43 0 48 -6 Z" fill={PROP_COLOURS.spark} {...RIM} />
			</g>
		);
	}
	if (worn === "patch") {
		return (
			<g data-worn="patch">
				{/* A square of cloth stitched on. A ghost is a sheet, and a sheet gets mended. */}
				<rect data-rim x="58" y="70" width="17" height="15" rx="2" fill={colour} {...RIM} />
				<path
					d="M58 74 L75 74 M58 81 L75 81 M62 70 L62 85 M70 70 L70 85"
					stroke={PROCS_INK}
					strokeWidth="1.4"
					opacity="0.5"
					fill="none"
				/>
			</g>
		);
	}
	return <Bow cx={48} cy={22} size={11} colour={colour} />;
}

// ---------------------------------------------------------------- Cat
//
// TWO POSES, and that is the whole design.
//
// ⚠ Three attempts failed here before the human named the fix, and the reason they
// failed is worth keeping: all three drew ONE figure with the head from the front and
// the body from the side. That is not a rendering bug, it is the children's-drawing
// convention — face from the front so you can see it, body from the side so you can see
// the legs — and no amount of moving legs about was going to rescue it.
//
// A cat sitting and a cat walking are different SHAPES, so they are drawn as different
// shapes, each from one viewpoint:
//
//   STILL   a fat round cat sitting head-on, paws together, tail curled round its side.
//           This is the pose it is in nearly all the time and the one that has to be
//           charming.
//   WALKING it gets up and turns side-on, and walks. Head leads, tail trails, four legs
//           step. Drawn facing RIGHT, because the rig mirrors the whole sprite to walk
//           left — so head-right is the one drawing that is correct both ways.
//
// A cat that sits down when it stops is also just what a cat does.

const CAT_AXIS = 48;

// ---- sitting, head-on
const CAT_SIT_HEAD = { cx: 48, cy: 44, rx: 31, ry: 28 };
/** Fat and round: the body is wider than the head and sits flat on the floor. */
const CAT_SIT_BODY = "M48 62 C 20 62 12 84 13 100 C 14 112 26 117 48 117 C 70 117 82 112 83 100 C 84 84 76 62 48 62 Z";
const CAT_SIT_BIB = "M40 112 C 35 100 37 84 48 78 C 59 84 61 100 56 112 Z";
/** Curled round its own side and out, where the cord takes over. */
const CAT_SIT_TAIL = "M76 112 C 88 111 94 103 93 92 L 85 90 C 86 99 83 104 73 105 Z";
const CAT_EAR = "M23 28 L 15 -2 L 45 18 Z";
// ⚠ Scaled about the outer ear's own CENTROID rather than nudged by eye — by eye, the
// first one poked out past the ear's lower edge by two units and the containment test
// caught it. A centroid-scaled triangle is inside its parent by construction.
const CAT_EAR_INNER = "M24.8 22.9 L 19.8 4.3 L 38.4 16.7 Z";
const CAT_SIT_EAR_ROOT = [30, 24] as const;

// ---- walking, side-on, facing right
const CAT_WALK_HEAD = { cx: 70, cy: 52, rx: 24, ry: 22 };
const CAT_WALK_BODY = "M22 66 C 10 66 6 78 8 88 C 10 98 22 102 40 102 C 58 102 72 98 74 88 C 76 76 66 64 50 64 Z";
/** The far ear, drawn behind the head so the near one reads as the near one. */
const CAT_WALK_EAR_FAR = "M58 34 L 58 16 L 74 30 Z";
const CAT_WALK_EAR = "M68 34 L 76 12 L 88 32 Z";
const CAT_WALK_EAR_INNER = "M71.5 31 L 76.5 17.3 L 83.9 29.7 Z";
const CAT_WALK_EAR_ROOT = [78, 33] as const;
/** Up and back, the way a cat carries it while it trots. */
const CAT_WALK_TAIL = "M14 74 C 4 72 0 62 2 50 L 10 48 C 9 58 10 64 18 66 Z";

/** A paw: a rounded foot with two toe notches, which says paw rather than peg. */
function CatPaw({ x, y, wide, fill }: { x: number; y: number; wide: number; fill: string }) {
	return (
		<>
			<ellipse data-rim data-paw cx={x} cy={y} rx={wide} ry={round(wide * 0.72)} fill={fill} {...RIM} />
			<path
				d={`M${round(x - wide * 0.3)} ${round(y - wide * 0.5)} L${round(x - wide * 0.3)} ${round(y - wide * 0.1)} M${round(x + wide * 0.3)} ${round(y - wide * 0.5)} L${round(x + wide * 0.3)} ${round(y - wide * 0.1)}`}
				stroke={PROCS_INK}
				strokeWidth="1.4"
				strokeLinecap="round"
				opacity="0.65"
				fill="none"
			/>
		</>
	);
}

/** The muzzle, nose and mouth, wherever the face happens to be. */
function CatFace({ cx, cy, held, ink = PROCS_INK }: { cx: number; cy: number; held: boolean; ink?: string }) {
	if (held) return <Mouth cx={cx} cy={cy + 3} width={10} held />;
	return (
		<>
			<path data-nose d={`M${cx - 3.6} ${cy - 3} L${cx + 3.6} ${cy - 3} L${cx} ${cy + 1} Z`} fill={ink} />
			<path
				data-mouth
				d={`M${cx - 6} ${cy + 4} C ${cx - 4} ${cy + 8.5} ${cx - 1} ${cy + 8.5} ${cx} ${cy + 4.8} C ${cx + 1} ${cy + 8.5} ${cx + 4} ${cy + 8.5} ${cx + 6} ${cy + 4}`}
				fill="none"
				stroke={ink}
				strokeWidth="2.4"
				strokeLinecap="round"
				strokeLinejoin="round"
			/>
		</>
	);
}

function CatRig({ cast, scene, held, walking, cycleMs, uid, heldProp }: RigProps) {
	const headClip = `procs-head-${uid}`;
	// Standing still, it sits. That is both the better drawing and what a cat does.
	if (!walking) {
		return (
			<>
				<defs>
					<clipPath id={headClip}>
						<ellipse {...CAT_SIT_HEAD} />
					</clipPath>
				</defs>

				<path data-rim data-part="tail" d={CAT_SIT_TAIL} fill={cast.shade} strokeLinejoin="round" {...RIM} />
				<path data-rim data-part="body" d={CAT_SIT_BODY} fill={cast.shade} strokeLinejoin="round" {...RIM} />
				<path data-bib d={CAT_SIT_BIB} fill={PROP_COLOURS.linen} />
				<g data-cat-paw="front">
					<CatPaw x={38} y={112} wide={7.6} fill={cast.body} />
					<CatPaw x={58} y={112} wide={7.6} fill={cast.body} />
				</g>

				<ellipse data-rim data-part="head" {...CAT_SIT_HEAD} fill={cast.body} {...RIM} />
				<g clipPath={`url(#${headClip})`}>
					<ellipse data-muzzle cx="41" cy="59" rx="10" ry="7.5" fill={PROP_COLOURS.linen} />
					<ellipse data-muzzle cx="55" cy="59" rx="10" ry="7.5" fill={PROP_COLOURS.linen} />
				</g>
				<Blush
					at={[
						[20, 52],
						[76, 52],
					]}
					rx={7}
					ry={4.5}
					colour={cast.blush}
					clip={headClip}
					style="whisker"
				/>
				<Eyes
					at={[
						[33, 41],
						[63, 41],
					]}
					r={9}
					held={held}
					style="slit"
					blink={BLINK_MS.cat}
					uid={uid}
				/>
				<CatFace cx={48} cy={56} held={held} />
				<CatWorn worn={cast.hatId} colour={cast.shade} at={[48, 72]} ear={[74, 30]} />
				<SwingPair part="ears" cord={scene.cord} root={CAT_SIT_EAR_ROOT} axis={CAT_AXIS} speedMs={1400}>
					<path data-rim data-ear d={CAT_EAR} fill={cast.body} strokeLinejoin="round" {...RIM} />
					<path data-ear-lining d={CAT_EAR_INNER} fill={cast.blush} />
				</SwingPair>
				{heldProp}
			</>
		);
	}

	// Walking: up on all fours and side-on, head leading.
	return (
		<>
			<defs>
				<clipPath id={headClip}>
					<ellipse {...CAT_WALK_HEAD} />
				</clipPath>
			</defs>

			<Strip uid={uid} top={94} walking cycleMs={cycleMs}>
				{CAT_POSES.map((pose, index) => (
					<g key={pose.key} data-walk-pose transform={`translate(${index * CELL} 0)`}>
						{/* Far side first, in the shade: a trotting cat shows two pairs of legs
						    and the near pair has to win. */}
						<CatLeg x={20} lift={pose.back[1]} colour={cast.shade} />
						<CatLeg x={54} lift={pose.front[1]} colour={cast.shade} />
						<CatLeg x={30} lift={pose.back[0]} colour={cast.body} />
						<CatLeg x={62} lift={pose.front[0]} colour={cast.body} />
					</g>
				))}
			</Strip>

			<g style={{ animation: `procs-bob ${cycleMs}ms ease-in-out infinite alternate` }}>
				<path data-rim data-part="tail" d={CAT_WALK_TAIL} fill={cast.shade} strokeLinejoin="round" {...RIM} />
				<path data-rim data-ear d={CAT_WALK_EAR_FAR} fill={cast.shade} strokeLinejoin="round" {...RIM} />
				<path data-rim data-part="body" d={CAT_WALK_BODY} fill={cast.shade} strokeLinejoin="round" {...RIM} />
				<ellipse data-rim data-part="head" {...CAT_WALK_HEAD} fill={cast.body} {...RIM} />
				<g clipPath={`url(#${headClip})`}>
					<ellipse data-muzzle cx="82" cy="60" rx="11" ry="8" fill={PROP_COLOURS.linen} />
				</g>
				<Blush
					at={[
						[66, 58],
						[66, 58],
					]}
					rx={6.5}
					ry={4}
					colour={cast.blush}
					clip={headClip}
					style="soft"
				/>
				{/* One eye, because this is a profile. Two would be the same mistake again. */}
				<Eyes at={[[72, 46]]} r={8} held={held} style="slit" blink={BLINK_MS.cat} uid={uid} />
				<CatFace cx={84} cy={57} held={held} />
				<CatWorn worn={cast.hatId} colour={cast.shade} at={[62, 70]} ear={[88, 34]} />
				<SwingPair part="ears" cord={scene.cord} root={CAT_WALK_EAR_ROOT} axis={CAT_AXIS} speedMs={1400} single>
					<path data-rim data-ear d={CAT_WALK_EAR} fill={cast.body} strokeLinejoin="round" {...RIM} />
					<path data-ear-lining d={CAT_WALK_EAR_INNER} fill={cast.blush} />
				</SwingPair>
				{heldProp}
			</g>
		</>
	);
}

/** One leg of a trotting cat: a tapered limb with a paw, in profile. */
function CatLeg({ x, lift, colour }: { x: number; lift: number; colour: string }) {
	const top = 92 - lift;
	return (
		<g data-cat-leg>
			<path
				data-rim
				d={`M${x} ${top} L${round(x + 9)} ${top} L${round(x + 7.4)} ${round(112 - lift)} L${round(x + 1.6)} ${round(112 - lift)} Z`}
				fill={colour}
				strokeLinejoin="round"
				{...RIM}
			/>
			<CatPaw x={round(x + 4.5)} y={round(114 - lift)} wide={6.4} fill={colour} />
		</g>
	);
}

/**
 * What a cat is given. Round the neck for three of them, behind the ear for the flower.
 *
 * Placed by the caller because this cat has TWO poses with the neck in two places — the
 * one thing a shared accessory table could not have known.
 */
function CatWorn({
	worn,
	colour,
	at,
	ear,
}: {
	worn: string;
	colour: string;
	at: readonly [number, number];
	ear: readonly [number, number];
}) {
	if (worn === "bowtie") return <Bow cx={at[0]} cy={at[1]} size={9} colour={colour} />;
	if (worn === "scarf") return <Scarf cx={at[0]} cy={at[1]} width={30} colour={colour} />;
	if (worn === "flower") return <Flower cx={ear[0]} cy={ear[1]} r={8} colour={colour} />;
	return (
		<g data-worn="collar">
			<rect data-rim x={round(at[0] - 17)} y={round(at[1] - 4)} width="34" height="8" rx="4" fill={colour} {...RIM} />
			{/* The bell. A collar without one is a strap. */}
			<circle data-rim cx={at[0]} cy={round(at[1] + 6)} r="5.5" fill={PROP_COLOURS.spark} {...RIM} />
			<path
				d={`M${round(at[0] - 3)} ${round(at[1] + 6)} L${round(at[0] + 3)} ${round(at[1] + 6)}`}
				stroke={PROCS_INK}
				strokeWidth="1.6"
				strokeLinecap="round"
				fill="none"
			/>
		</g>
	);
}

// ---------------------------------------------------------------- Slime
//
// A jelly CUBE with soft corners and a flat bottom — squared off on purpose, because
// the teardrop-with-a-grin is somebody's slime and this one is ours. What makes it
// read as jelly rather than as a box is the glass highlight and the nucleus floating
// inside it, and the nucleus is also what reports the link.

// Taller than the first pass, which sat so low in the frame that it read as a
// footstool with a face. A jelly cube still has to be a BODY.
const SLIME_BODY =
	"M20 116 C 15 116 15 111 15 105 L 15 80 C 15 58 29 52 48 52 C 67 52 81 58 81 80 L 81 105 C 81 111 81 116 76 116 Z";
const SLIME_NUCLEUS = [48, 100] as const;

function SlimeRig({ cast, scene, held, walking, cycleMs, uid, heldProp }: RigProps) {
	const bodyClip = `procs-body-${uid}`;
	return (
		<>
			<defs>
				<clipPath id={bodyClip}>
					<path d={SLIME_BODY} />
				</clipPath>
			</defs>

			{/* A slime has no legs, so travelling is a HOP: squash on the floor, stretch in
			    the air. Its transform origin is its own base, or it would grow from the
			    middle and read as a balloon rather than a body. */}
			<g
				style={
					walking
						? { animation: `procs-hop ${cycleMs}ms ease-in-out infinite`, transformOrigin: tellOrigin([48, 116]) }
						: undefined
				}
			>
				<path data-rim data-part="body" d={SLIME_BODY} fill={cast.body} strokeLinejoin="round" {...RIM} />

				{/* The glass highlight: what turns a rounded box into something wet. */}
				<g clipPath={`url(#${bodyClip})`}>
					<path data-gloss d="M26 76 C 26 64 33 60 41 60 C 34 65 32 70 32 78 Z" fill={PROCS_LIGHT} />
				</g>

				<Blush
					at={[
						[28, 92],
						[68, 92],
					]}
					rx={6}
					ry={4}
					colour={cast.blush}
					clip={bodyClip}
					style="hatch"
				/>
				<Eyes
					at={[
						[36, 82],
						[60, 82],
					]}
					r={8}
					held={held}
					style="glassy"
					blink={BLINK_MS.slime}
					uid={uid}
				/>
				<Mouth cx={48} cy={89} width={9} held={held} />

				<SlimeWorn worn={cast.hatId} />

				<GlowPart part="nucleus" cord={scene.cord} origin={SLIME_NUCLEUS}>
					<ellipse
						data-rim
						data-nucleus
						cx={SLIME_NUCLEUS[0]}
						cy={SLIME_NUCLEUS[1]}
						rx="8"
						ry="6"
						fill={tellGlow(scene.cord)}
						{...RIM}
					/>
				</GlowPart>
				{heldProp}
			</g>
		</>
	);
}

/**
 * What is SUSPENDED IN a slime.
 *
 * Nothing else in the cast can wear a thing inside itself, and it is the most slime-ish
 * idea available — a hat on a jelly cube is a hat on a box. Placed high and left, clear
 * of both the face and the nucleus, which is the tell and must never be crowded.
 */
function SlimeWorn({ worn }: { worn: string }) {
	if (worn === "star") {
		return (
			<path
				data-rim
				data-worn="star"
				d="M28 60 L31 67 L38.5 67.5 L32.5 72 L34.5 79 L28 74.5 L21.5 79 L23.5 72 L17.5 67.5 L25 67 Z"
				fill={PROP_COLOURS.spark}
				strokeLinejoin="round"
				{...RIM}
			/>
		);
	}
	if (worn === "coin") {
		return (
			<g data-worn="coin">
				<circle data-rim cx="28" cy="69" r="9" fill={PROP_COLOURS.spark} {...RIM} />
				<circle cx="28" cy="69" r="4.6" fill="none" stroke={PROCS_INK} strokeWidth="1.6" opacity="0.55" />
			</g>
		);
	}
	if (worn === "leaf") {
		return (
			<g data-worn="leaf">
				<path
					data-rim
					d="M19 76 C 19 64 27 58 37 58 C 37 70 29 76 19 76 Z"
					fill={PROP_COLOURS.sprig}
					strokeLinejoin="round"
					{...RIM}
				/>
				<path d="M21 74 L35 60" stroke={PROCS_INK} strokeWidth="1.4" strokeLinecap="round" opacity="0.5" fill="none" />
			</g>
		);
	}
	return (
		<g data-worn="cherry">
			<path
				d="M28 58 C 31 63 33 66 33 70"
				stroke={PROP_COLOURS.sprig}
				strokeWidth="2.4"
				strokeLinecap="round"
				fill="none"
			/>
			<circle data-rim cx="26" cy="72" r="7" fill={PROP_COLOURS.cherry} {...RIM} />
			<circle data-rim cx="37" cy="74" r="6" fill={PROP_COLOURS.cherry} {...RIM} />
		</g>
	);
}

// ---------------------------------------------------------------- Chick
//
// A round bird on two stick legs. One shape for head and body together, which is
// what makes a chick a chick rather than a bird — plus the beak, which is the single
// most legible feature in the whole cast at 30px, and a three-feather crest that is
// drawn OVER the hat so this one is still itself in a hard hat.

const CHICK_BODY = "M48 22 C 24 22 16 50 16 74 C 16 94 30 104 48 104 C 66 104 80 94 80 74 C 80 50 72 22 48 22 Z";
const CHICK_WING = "M22 62 C 8 64 2 76 5 86 C 14 86 21 76 26 68 Z";
const CHICK_CREST = "M42 26 L 40 4 L 50 16 L 54 0 L 58 18 L 66 8 Z";
const CHICK_CREST_ROOT = [48, 26] as const;

function ChickRig({ cast, scene, held, walking, cycleMs, uid, heldProp }: RigProps) {
	const bodyClip = `procs-body-${uid}`;
	return (
		<>
			<defs>
				<clipPath id={bodyClip}>
					<path d={CHICK_BODY} />
				</clipPath>
			</defs>

			<Strip uid={uid} top={100} walking={walking} cycleMs={cycleMs}>
				{(held ? DANGLE_POSES : LEG_POSES).map((pose, index) => (
					<g key={pose.key} data-walk-pose transform={`translate(${index * CELL} 0)`}>
						<Leg
							x={39 + pose.left.dx}
							y={100}
							lift={pose.left.lift}
							width={6}
							height={20}
							colour={PROP_COLOURS.spark}
						/>
						<Leg
							x={51 + pose.right.dx}
							y={100}
							lift={pose.right.lift}
							width={6}
							height={20}
							colour={PROP_COLOURS.spark}
						/>
					</g>
				))}
			</Strip>

			<g style={walking ? { animation: `procs-bob ${cycleMs}ms ease-in-out infinite alternate` } : undefined}>
				{/* Wings, static: they are the shape of the bird, not the state of it. */}
				{[false, true].map((mirrored) => (
					<g key={String(mirrored)} transform={mirrored ? undefined : undefined}>
						<path
							data-rim
							data-wing
							transform={mirrored ? "translate(96 0) scale(-1 1)" : undefined}
							d={CHICK_WING}
							fill={cast.shade}
							strokeLinejoin="round"
							{...RIM}
						/>
					</g>
				))}

				<path data-rim data-part="body" d={CHICK_BODY} fill={cast.body} strokeLinejoin="round" {...RIM} />
				<Blush
					at={[
						[26, 72],
						[70, 72],
					]}
					rx={6}
					ry={6}
					colour={cast.blush}
					clip={bodyClip}
					style="dot"
				/>
				<Eyes
					at={[
						[37, 58],
						[59, 58],
					]}
					r={6.5}
					held={held}
					style="bead"
					blink={BLINK_MS.chick}
					uid={uid}
				/>
				{held ? (
					<Mouth cx={48} cy={74} width={9} held />
				) : (
					<path
						data-rim
						data-beak
						d="M41 70 L55 70 L48 80 Z"
						fill={PROP_COLOURS.spark}
						strokeLinejoin="round"
						{...RIM}
					/>
				)}
				<ChickWorn worn={cast.hatId} colour={cast.shade} />
				{/* Over the hat, deliberately, and it is also the TELL: a crest a beanie
				    swallowed would leave this one indistinguishable from any other round
				    body, and a tell on the left would be behind the held prop in eight of
				    the fifteen states. The top of a head is the one place nothing covers. */}
				<SwingPair part="crest" cord={scene.cord} root={CHICK_CREST_ROOT} axis={48} speedMs={1100} single>
					<path data-rim data-crest d={CHICK_CREST} fill={cast.shade} strokeLinejoin="round" {...RIM} />
				</SwingPair>
				{heldProp}
			</g>
		</>
	);
}

/** What a chick has. The eggshell is the one it hatched out of, which is the joke. */
function ChickWorn({ worn, colour }: { worn: string; colour: string }) {
	if (worn === "bow") return <Bow cx={48} cy={30} size={10} colour={colour} />;
	if (worn === "scarf") return <Scarf cx={48} cy={92} width={30} colour={colour} />;
	if (worn === "seed") {
		return (
			<ellipse
				data-rim
				data-worn="seed"
				cx="48"
				cy="86"
				rx="5"
				ry="7"
				fill={PROP_COLOURS.wood}
				transform="rotate(14 48 86)"
				{...RIM}
			/>
		);
	}
	return (
		<path
			data-rim
			data-worn="shell"
			// ⚠ A DOME with a jagged bottom edge, in `paper` rather than `linen`. Drawn as a
			// jagged band in linen it vanished into a pale chick and left nothing on screen
			// but the zigzag of its own outline, which read as a spiky crown. Half an
			// eggshell is a shape, and the shape is what has to be there.
			//
			// ⚠ And it sits DOWN ON the skull, not above it. Drawn with its broken edge at
			// the body's own top line it floated clear of the bird and read as a hat being
			// held over its head; the jagged edge has to cut ACROSS the crown, the way half
			// an eggshell actually sits on the chick that just came out of it.
			d="M17 45 L24 35 L31 44 L38 32 L45 42 L52 31 L59 42 L66 33 L73 43 L79 34 L79 29 C 74 17 62 11 48 11 C 34 11 22 17 17 29 Z"
			fill={PROP_COLOURS.paper}
			strokeLinejoin="round"
			{...RIM}
		/>
	);
}

// ---------------------------------------------------------------- Toadstool
//
// A walking mushroom: a wide spotted cap over a stubby stem with the face on it. The
// widest silhouette in the cast and the only one that is wider than it is tall, which
// is what makes it tell apart from everything else at a glance.
//
// The second creature with no hat, and for the opposite reason to the ghost: the cap
// IS a hat. A beanie over it would read as a mistake rather than as a choice.

const TOAD_CAP = "M4 58 C 4 26 22 12 48 12 C 74 12 92 26 92 58 C 74 66 22 66 4 58 Z";
const TOAD_STEM = "M28 58 L 28 102 C 28 114 36 118 48 118 C 60 118 68 114 68 102 L 68 58 Z";
const TOAD_SPOTS = [
	{ cx: 24, cy: 40, rx: 8, ry: 6.5 },
	{ cx: 48, cy: 28, rx: 9, ry: 7.5 },
	{ cx: 72, cy: 40, rx: 8, ry: 6.5 },
	{ cx: 48, cy: 52, rx: 6, ry: 4.5 },
];

function ToadstoolRig({ cast, scene, held, walking, cycleMs, uid, heldProp }: RigProps) {
	const stemClip = `procs-body-${uid}`;
	const glow = tellGlow(scene.cord);
	return (
		<>
			<defs>
				<clipPath id={stemClip}>
					<path d={TOAD_STEM} />
				</clipPath>
			</defs>

			<g
				style={
					walking
						? { animation: `procs-hop ${cycleMs}ms ease-in-out infinite`, transformOrigin: tellOrigin([48, 118]) }
						: undefined
				}
			>
				<path data-rim data-part="stem" d={TOAD_STEM} fill={PROP_COLOURS.linen} strokeLinejoin="round" {...RIM} />
				<path data-rim data-part="cap" d={TOAD_CAP} fill={cast.body} strokeLinejoin="round" {...RIM} />

				{/* ⚠ The cap's pattern is BOTH the accessory and the tell, and they do not
				    collide: the accessory chooses the SHAPE, the cord chooses how brightly
				    it burns. A snail is the odd one out and keeps a single spot to light. */}
				<GlowPart part="spots" cord={scene.cord} origin={[48, 34]}>
					<ToadstoolWorn worn={cast.hatId} glow={glow} />
				</GlowPart>

				<Blush
					at={[
						[32, 92],
						[64, 92],
					]}
					rx={5}
					ry={5}
					colour={cast.blush}
					clip={stemClip}
					style="dot"
				/>
				{/* Arcs are a closed, happy eye — there is nothing left for a blink to do,
				    so it does not get one rather than getting a broken one. */}
				<Eyes
					at={[
						[38, 82],
						[58, 82],
					]}
					r={7}
					held={held}
					style={held ? "round" : "arc"}
					blink={held ? BLINK_MS.toadstool : 0}
					uid={uid}
				/>
				<Mouth cx={48} cy={95} width={8} held={held} />
				{heldProp}
			</g>
		</>
	);
}

/** The pattern on a toadstool's cap — which is the only place a toadstool could wear one. */
function ToadstoolWorn({ worn, glow }: { worn: string; glow: string }) {
	if (worn === "rings") {
		return (
			<g data-worn="rings">
				{[
					{ rx: 30, ry: 13 },
					{ rx: 20, ry: 9 },
					{ rx: 10, ry: 5 },
				].map((ring) => (
					<ellipse key={ring.rx} data-spot cx="48" cy="38" {...ring} fill="none" stroke={glow} strokeWidth="4.5" />
				))}
			</g>
		);
	}
	if (worn === "dew") {
		return (
			<g data-worn="dew">
				{[
					{ cx: 26, cy: 42 },
					{ cx: 44, cy: 30 },
					{ cx: 62, cy: 44 },
					{ cx: 70, cy: 30 },
				].map((drop) => (
					<path
						key={drop.cx}
						data-spot
						d={`M${drop.cx} ${drop.cy - 7} C ${drop.cx + 5} ${drop.cy} ${drop.cx + 4} ${drop.cy + 5} ${drop.cx} ${drop.cy + 5} C ${drop.cx - 4} ${drop.cy + 5} ${drop.cx - 5} ${drop.cy} ${drop.cx} ${drop.cy - 7} Z`}
						fill={glow}
					/>
				))}
			</g>
		);
	}
	if (worn === "snail") {
		return (
			<g data-worn="snail">
				<ellipse data-spot cx="62" cy="30" rx="11" ry="9.5" fill={glow} />
				<path
					d="M62 30 C 66 30 67 26 63 25 C 58 24 56 30 61 33 C 67 36 70 29 66 24"
					stroke={PROCS_INK}
					strokeWidth="1.8"
					fill="none"
					strokeLinecap="round"
					opacity="0.6"
				/>
				<path
					data-rim
					d="M52 36 C 44 36 40 32 38 26 L44 24 C 46 28 48 30 53 30 Z"
					fill={PROP_COLOURS.linen}
					strokeLinejoin="round"
					{...RIM}
				/>
			</g>
		);
	}
	return (
		<g data-worn="spots">
			{TOAD_SPOTS.map((spot) => (
				<ellipse key={spot.cx + spot.cy} data-spot {...spot} fill={glow} />
			))}
		</g>
	);
}

export const RIGS: Record<SpeciesId, (props: RigProps) => React.ReactNode> = {
	proc: ProcRig,
	ghost: GhostRig,
	cat: CatRig,
	slime: SlimeRig,
	chick: ChickRig,
	toadstool: ToadstoolRig,
};

/**
 * Where each creature's hat sits, given as the line its BAND should land on.
 *
 * The six hats are authored once, in rig coordinates, against the PROC's head. Rather
 * than redraw six hats per creature — 36 drawings and 36 chances to get one wrong —
 * each rig says where its own brim line is and the hat is mapped onto it.
 *
 * ⚠ The BRIM, not the crown, and the first pass got this wrong in a way only rendering
 * showed. Anchoring the crown puts the band a fixed distance BELOW the anchor, which is
 * right for a Proc's tall head and lands across the eyes of anything shorter — the slime
 * came out wearing its beanie over its face. The brim line is the part that has to be in
 * the right place; the crown can go wherever it likes above it.
 */
const CANONICAL_BRIM = 34;

export const HAT_FIT: Partial<Record<SpeciesId, { centre: number; brim: number; scale: number }>> = {
	proc: { centre: 48, brim: CANONICAL_BRIM, scale: 1 },
	chick: { centre: 48, brim: 46, scale: 0.85 },
};

/**
 * ⚠ Each RIG applies this, not the shell. Only the rig knows where its own head is —
 * and the cat's moves, because a sitting cat and a walking cat are two drawings with
 * the head in two places. A hat positioned once outside the rig would sit on the cat's
 * shoulder for half its life.
 */
export function hatTransform(species: SpeciesId): string | undefined {
	const fit = HAT_FIT[species];
	if (!fit || (fit.centre === 48 && fit.brim === CANONICAL_BRIM && fit.scale === 1)) return undefined;
	return `translate(${fit.centre} ${fit.brim}) scale(${fit.scale}) translate(${-48} ${-CANONICAL_BRIM})`;
}
