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
	/** The hat, if this creature wears one. Positioned by the rig, which knows where its head is. */
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

function Eyes({
	at,
	r,
	held,
	style,
	blink,
	lid,
	uid,
}: {
	at: ReadonlyArray<readonly [number, number]>;
	r: number;
	held: boolean;
	style: EyeStyle;
	/** The blink cycle for this creature, or 0 for eyes that are already closed. */
	blink: number;
	/** The colour of the face the eye sits on: what the eyelid is made of. */
	lid: string;
	uid: string;
}) {
	const radius = held ? r * 1.16 : r;
	const tall = style === "tall" ? radius * 1.24 : style === "bead" ? radius * 0.78 : radius;
	const wide = style === "tall" ? radius * 0.82 : style === "bead" ? radius * 0.78 : radius;
	// A bead's white ring is part of the EYE, so the lid has to cover that too — sized to
	// the pupil alone, a closed bird was still staring out of two white circles.
	const lidRx = style === "bead" ? radius * 1.12 : wide;
	const lidRy = style === "bead" ? radius * 1.12 : tall;
	return (
		<>
			{at.map(([cx, cy], index) => (
				<g key={`${cx}-${cy}`} data-eye data-eye-style={style}>
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
							{blink > 0 && (
								<Eyelid
									cx={cx}
									cy={cy}
									rx={lidRx}
									ry={lidRy}
									colour={lid}
									ms={blink}
									delay={index * 60}
									uid={`${uid}-${index}`}
								/>
							)}
						</>
					)}
				</g>
			))}
		</>
	);
}

/**
 * A blink: an EYELID that comes down over the eye.
 *
 * ⚠ The first pass squashed the whole eye with `scaleY`, and the human's word for it was
 * that the eye looked like it was sliding down — which is exactly what a vertical squash
 * about a centre point IS. An eye does not shrink when it closes; something comes down
 * over it, and the lid is the thing that has to move.
 *
 * So the lid is a rectangle in the colour of the face, CLIPPED to the eye's own outline,
 * parked just above it and translated down across it. Clipped, it can only ever appear
 * inside the eye, so it closes the eye and touches nothing else — no matter what shape
 * the eye is, which is what lets one lid serve a tall ghost eye, a cat's slit and a
 * bird's bead. Transform only, so it stays on the compositor.
 */
function Eyelid({
	cx,
	cy,
	rx,
	ry,
	colour,
	ms,
	delay,
	uid,
}: {
	cx: number;
	cy: number;
	rx: number;
	ry: number;
	colour: string;
	ms: number;
	delay: number;
	uid: string;
}) {
	const clip = `procs-lid-${uid}`;
	const height = round(ry * 2 + 2);
	return (
		<>
			<defs>
				<clipPath id={clip}>
					<ellipse cx={cx} cy={cy} rx={round(rx + 0.6)} ry={round(ry + 0.6)} />
				</clipPath>
			</defs>
			<g clipPath={`url(#${clip})`}>
				<rect
					data-eyelid
					x={round(cx - rx - 1)}
					y={round(cy - ry - 1)}
					width={round(rx * 2 + 2)}
					height={height}
					fill={colour}
					style={{
						// Parked clear of the eye and swept down over it. The distance is the
						// eye's own height, handed to the keyframe as a variable so one set of
						// keyframes serves every eye in the cast whatever size it is.
						["--procs-lid" as string]: `${height}px`,
						animation: `procs-blink ${ms}ms ease-in ${delay}ms infinite`,
					}}
				/>
			</g>
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

/**
 * A cat's leg: a tapered limb with a PAW on the end.
 *
 * ⚠ Four identical rounded posts is what the first pass had, and the human's word for
 * it was ugly — which is right, because posts are what furniture stands on. What makes
 * a leg an animal's is that it is thicker at the top than at the bottom and finishes in
 * a foot, and at 30px the foot is the part that does the work.
 *
 * The back pair get a haunch and the front pair do not, because that is the difference
 * a cat actually has and it is what stops four legs reading as a table.
 */
function CatLeg({
	x,
	lift,
	back,
	colour,
	shade,
}: {
	x: number;
	lift: number;
	back: boolean;
	colour: string;
	shade: string;
}) {
	const top = 92 - lift;
	const foot = 118 - lift;
	return (
		<g data-cat-leg={back ? "back" : "front"}>
			{back && <ellipse data-rim cx={round(x + 4)} cy={round(top + 7)} rx="9.5" ry="10" fill={shade} {...RIM} />}
			<path
				data-rim
				d={`M${round(x)} ${round(top)} L${round(x + 8)} ${round(top)} L${round(x + 6.6)} ${round(foot - 4)} L${round(x + 1.4)} ${round(foot - 4)} Z`}
				fill={colour}
				strokeLinejoin="round"
				{...RIM}
			/>
			<ellipse data-rim data-paw cx={round(x + 4)} cy={round(foot - 3)} rx="6" ry="4.4" fill={colour} {...RIM} />
		</g>
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
					lid={cast.body}
					uid={uid}
				/>
				<Mouth cx={48} cy={67} width={10} held={held} />
				{hat}
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
						lid={cast.body}
						uid={uid}
					/>
					<Mouth cx={48} cy={78} width={9} held={held} />
					{heldProp}
				</g>
			</g>
		</>
	);
}

// ---------------------------------------------------------------- Cat
//
// A generic cat on all fours, head turned to face you — which is the pose that keeps
// the face readable while the body stays unmistakably four-legged. Its TAIL runs off
// to the right and becomes the cord, so the lead is part of the animal rather than
// something attached to it.

// ⚠ Redrawn after the first render, where a small head and a plain oval behind it read
// as TWO OBJECTS rather than as one animal. What fixes it is chibi proportions — a head
// big enough to dominate — plus a body that overlaps it instead of sitting beside it.
const CAT_HEAD = { x: 6, y: 22, width: 60, height: 56, rx: 26 };
const CAT_AXIS = 36;
const CAT_EAR = "M16 34 L 10 6 L 34 25 Z";
const CAT_EAR_ROOT = [24, 29] as const;
/** Where the tail leaves the body and the cord takes over. */
// A short thick stub where the tail leaves the body, which the CORD then continues.
// The join is the whole idea: the lead is part of the animal rather than clipped to it.
const CAT_TAIL = "M66 88 C 74 88 79 84 82 76 L 74 72 C 71 79 70 81 64 82 Z";

function CatRig({ cast, scene, held, walking, cycleMs, uid, heldProp, hat }: RigProps) {
	const headClip = `procs-head-${uid}`;
	return (
		<>
			<defs>
				<clipPath id={headClip}>
					<rect {...CAT_HEAD} />
				</clipPath>
			</defs>

			<Strip uid={uid} top={88} walking={walking} cycleMs={cycleMs}>
				{CAT_POSES.map((pose, index) => (
					<g key={pose.key} data-walk-pose transform={`translate(${index * CELL} 0)`}>
						{/* Back legs first: they are behind the body, and the body's rim should
						    cover where they meet it. */}
						<CatLeg x={57} lift={held ? -5 : pose.back[0]} back colour={cast.body} shade={cast.shade} />
						<CatLeg x={69} lift={held ? -6 : pose.back[1]} back colour={cast.body} shade={cast.shade} />
						<CatLeg x={20} lift={held ? -4 : pose.front[0]} back={false} colour={cast.body} shade={cast.shade} />
						<CatLeg x={32} lift={held ? -6 : pose.front[1]} back={false} colour={cast.body} shade={cast.shade} />
					</g>
				))}
			</Strip>

			<g style={walking ? { animation: `procs-bob ${cycleMs}ms ease-in-out infinite alternate` } : undefined}>
				<path data-rim data-part="tail" d={CAT_TAIL} fill={cast.shade} strokeLinejoin="round" {...RIM} />
				<rect data-rim data-part="body" x="26" y="66" width="54" height="34" rx="17" fill={cast.shade} {...RIM} />
				<rect data-rim data-part="head" {...CAT_HEAD} fill={cast.body} {...RIM} />
				<Blush
					at={[
						[19, 60],
						[55, 60],
					]}
					rx={6}
					ry={4}
					colour={cast.blush}
					clip={headClip}
					style="whisker"
				/>
				<Eyes
					at={[
						[25, 50],
						[49, 50],
					]}
					r={8}
					held={held}
					style="slit"
					blink={BLINK_MS.cat}
					lid={cast.body}
					uid={uid}
				/>
				{held ? (
					<Mouth cx={36} cy={63} width={9} held />
				) : (
					<>
						<path data-nose d="M33 63 L39 63 L36 66.4 Z" fill={PROCS_INK} />
						<path
							data-mouth
							d="M30 68 C 32 72 35 72 36 68.6 C 37 72 40 72 42 68"
							fill="none"
							stroke={PROCS_INK}
							strokeWidth="2.4"
							strokeLinecap="round"
							strokeLinejoin="round"
						/>
					</>
				)}
				{hat}
				<SwingPair part="ears" cord={scene.cord} root={CAT_EAR_ROOT} axis={CAT_AXIS} speedMs={1400}>
					<path data-rim data-ear d={CAT_EAR} fill={cast.body} strokeLinejoin="round" {...RIM} />
					<path data-ear-lining d="M20 31 L 14 11 L 30 25 Z" fill={cast.blush} />
				</SwingPair>
				{heldProp}
			</g>
		</>
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

function SlimeRig({ cast, scene, held, walking, cycleMs, uid, heldProp, hat }: RigProps) {
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
					lid={cast.body}
					uid={uid}
				/>
				<Mouth cx={48} cy={89} width={9} held={held} />

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
				{hat}
				{heldProp}
			</g>
		</>
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

function ChickRig({ cast, scene, held, walking, cycleMs, uid, heldProp, hat }: RigProps) {
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
					lid={cast.body}
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
				{hat}
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

				<GlowPart part="spots" cord={scene.cord} origin={[48, 34]}>
					{TOAD_SPOTS.map((spot) => (
						<ellipse key={spot.cx + spot.cy} data-spot {...spot} fill={glow} />
					))}
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
					lid={cast.body}
					uid={uid}
				/>
				<Mouth cx={48} cy={95} width={8} held={held} />
				{heldProp}
			</g>
		</>
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
	cat: { centre: 36, brim: 33, scale: 0.78 },
	chick: { centre: 48, brim: 46, scale: 0.85 },
};

export function hatTransform(species: SpeciesId): string | undefined {
	const fit = HAT_FIT[species];
	if (!fit || (fit.centre === 48 && fit.brim === CANONICAL_BRIM && fit.scale === 1)) return undefined;
	return `translate(${fit.centre} ${fit.brim}) scale(${fit.scale}) translate(${-48} ${-CANONICAL_BRIM})`;
}
