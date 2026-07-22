import { useId } from "react";
import type { SessionStatus } from "../renderer/types/workspace";
import { RUN_CYCLE_MS, WALK_CYCLE_MS, type Facing } from "./behaviour";
import type { CastMember } from "./cast";
import { PROCS_RIM_PX } from "./palette";
import { FIGURE_ATTRIBUTE } from "./pointer-region";
import { CordLayer, DustPuff, EmitLayer, GroundProp, HeldProp, RIM } from "./props";
import { sceneFor } from "./scene";
import { SPECIES_ART, type SpeciesParts } from "./species-art";

// Procs — a little running process. ONE rig, parameterised by a cast member and a
// scene, which is why a seventh character is a row in cast.ts and a sixteenth
// state is a row in scene.ts, never a new component.
//
// The shape language, from the design:
//   - a big soft head over a small squashy body on stubby legs: solid and GROUNDED,
//     standing on the desktop, which is what keeps it from being a floating sheet
//   - oversized eyes set LOW and WIDE APART (baby schema), each with a highlight so
//     it reads as an eye and not a punched dot; blush; a small mouth
//   - ears are a code-punctuation bracket pair, and they are what makes the
//     SILHOUETTE differ between characters — the thing you can still tell apart out
//     of the corner of your eye, which colour alone cannot do
//   - a cord that always leaves from the RIGHT and ends in a plug
//   - a 2.4px ink rim on every silhouette shape, baked into the art rather than
//     applied as a CSS filter, so an animating Proc pays no per-frame paint cost
//
// The walk is a real four-frame strip: the four leg poses are drawn side by side in
// one row and the row is stepped through one cell at a time with `steps(4, end)`.

/** One strip cell. Also the figure's own width, so cell N sits at x = N × CELL. */
const CELL = 96;
/** The figure's box. `size` is measured against this, so props never shrink the Proc. */
export const PROCS_BOX = { width: CELL, height: 132 };
/**
 * The drawn area, which is wider than the figure because scenes put a ground to the
 * RIGHT and a held prop to the LEFT, and taller at the top for the EMIT layer. The
 * bottom stays at the figure's 132 so a Proc still stands on the band's floor line.
 */
export const PROCS_VIEW = { x: -8, y: -24, width: 152, height: 156 };
/** Default drawn height. `full` tier per the design's size rules (≥120px). */
const DEFAULT_SIZE = 128;

/** Layout of the drawn SVG for a given Proc height, in px. */
export function procsFrame(size: number) {
	const scale = size / PROCS_BOX.height;
	return {
		width: PROCS_VIEW.width * scale,
		height: PROCS_VIEW.height * scale,
		/** How far LEFT of the figure the drawing starts. Negative. */
		offsetX: PROCS_VIEW.x * scale,
		/** How far the drawing overhangs to the RIGHT of the figure. */
		overhangRight: (PROCS_VIEW.x + PROCS_VIEW.width - PROCS_BOX.width) * scale,
		figureWidth: PROCS_BOX.width * scale,
	};
}

export type ProcsProps = {
	cast: CastMember;
	status: SessionStatus;
	facing: Facing;
	walking: boolean;
	/** Picked up by the human: dangling, startled, and flailing a bit. */
	held?: boolean;
	/** On its way to meet another Proc: the same walk, stepped faster. */
	running?: boolean;
	/** Face to face with the Proc it came to meet: a couple of hops. */
	greeting?: boolean;
	/** The landing it has just made, if any: `seq` counts them, `strength` sizes the puff. */
	bounce?: { seq: number; strength: number };
	/** Drawn height of the FIGURE in px; props extend beyond it. */
	size?: number;
	className?: string;
};

export function Procs({
	cast,
	status,
	facing,
	walking,
	held = false,
	running = false,
	greeting = false,
	bounce,
	size = DEFAULT_SIZE,
	className,
}: ProcsProps) {
	// A run is the same four-beat strip stepped faster — not a second animation, so
	// the legs can never disagree with themselves about which pose comes next.
	const cycleMs = running ? RUN_CYCLE_MS : WALK_CYCLE_MS;
	const uid = useId().replace(/[^a-zA-Z0-9-]/g, "");
	const headClip = `procs-head-${uid}`;
	const cellClip = `procs-cell-${uid}`;
	const scene = sceneFor(status);
	const frame = procsFrame(size);
	// Which CHARACTER. A species is a head shape, a face and up to three extra
	// layers slotted into this one rig — never a second component, which is why the
	// legs, the walk, the cord, the props and the pointer region below know nothing
	// about it. The species' own tell reads `scene.cord` and nothing else, so it can
	// never disagree with the cord it is drawn beside.
	const art = SPECIES_ART[cast.species];
	const parts: SpeciesParts = { cast, cord: scene.cord, held, headClip };

	return (
		<svg
			role="img"
			aria-label={`${cast.name}, ${held ? "being picked up" : status.replace(/_/g, " ")}`}
			className={className}
			width={frame.width}
			height={frame.height}
			viewBox={`${PROCS_VIEW.x} ${PROCS_VIEW.y} ${PROCS_VIEW.width} ${PROCS_VIEW.height}`}
			style={{
				// A discrete flip at the turn — the sprite mirrors, it does not rotate.
				transform: facing === "left" ? "scaleX(-1)" : undefined,
				// Clipped to the frame, deliberately: an attached cord is drawn running
				// PAST the edge, and the clean cut at the boundary is what reads as
				// "it goes off to something". Left visible, it trailed a long diagonal
				// across whichever Proc happened to be standing to the right.
				overflow: "hidden",
			}}
		>
			<defs>
				<clipPath id={headClip}>
					<rect {...art.head} />
				</clipPath>
				{/* Shows exactly one cell of the leg strip. */}
				<clipPath id={cellClip}>
					<rect x="0" y="94" width={CELL} height={PROCS_BOX.height - 94} />
				</clipPath>
			</defs>

			<GroundProp ground={scene.ground} />
			<CordLayer cord={scene.cord} ground={scene.ground} cast={cast} />

			{/* The character itself. Everything inside this group takes the pointer;
			    everything outside it — the scenery — passes clicks to the desktop. */}
			<g {...{ [FIGURE_ATTRIBUTE]: "" }}>
				<g
					data-teased={held || undefined}
					data-greeting={greeting || undefined}
					style={
						held
							? { animation: "procs-flail 620ms ease-in-out infinite alternate", transformOrigin: "48px 8px" }
							: greeting
								? { animation: "procs-hop 760ms ease-out 2" }
								: undefined
					}
				>
					{/* Legs, drawn before the body so the body's rim covers where they meet it. */}
					<g clipPath={`url(#${cellClip})`}>
						<g
							data-walk-strip
							style={walking ? { animation: `procs-walk ${cycleMs}ms steps(4, end) infinite` } : undefined}
						>
							{(held ? DANGLE_POSES : LEG_POSES).map((pose, index) => (
								<g key={pose.key} data-walk-pose transform={`translate(${index * CELL} 0)`}>
									<Leg x={38 + pose.left.dx} lift={pose.left.lift} colour={cast.shade} />
									<Leg x={50 + pose.right.dx} lift={pose.right.lift} colour={cast.shade} />
								</g>
							))}
						</g>
					</g>

					{/* Body and hat bob on their own eased track, separate from the leg steps. */}
					<g style={walking ? { animation: `procs-bob ${cycleMs}ms ease-in-out infinite alternate` } : undefined}>
						{/* BEHIND everything: a Sprite's wings. Nothing else uses this slot. */}
						{art.Back?.(parts)}

						<rect data-rim data-part="body" x="29" y="74" width="38" height="30" rx="14" fill={cast.shade} {...RIM} />

						{/* On the body: a Kitsu's ruff, a Unit's link lamp. */}
						{art.Chest?.(parts)}

						<rect data-rim data-part="head" {...art.head} fill={cast.body} {...RIM} />

						{/* Blush, eyes and mouth — the blush clipped to the head so it can never
						    spill and read as a smudge. */}
						{art.Face(parts)}

						{/* The hat, over the head so it sits ON it rather than behind it. Drawn
				    after the face so a low brim shades the eyes rather than the reverse. */}
						<g data-slot="hat" data-hat={cast.hatId} data-palette={cast.palette}>
							{cast.hat.map((piece) => (
								<path
									key={piece.d}
									data-rim
									data-hat-piece
									d={piece.d}
									fill={piece.role === "trim" ? cast.hatTrim : cast.hatFill}
									strokeLinejoin="round"
									{...RIM}
								/>
							))}
						</g>

						{/* OVER the hat: ears and an ahoge. The only way a species stays itself
						    while wearing one of the six hats — and it is the anime look anyway. */}
						{art.Crown?.(parts)}

						{/* Held inside the bob group, because a carried thing moves with its carrier. */}
						<HeldProp held={scene.held} mirrored={facing === "left"} />
					</g>
				</g>
			</g>

			{/* Emitted last so zzz and confetti sit in front of the head. */}
			<EmitLayer emit={scene.emit} cast={cast} />

			{/* Dust in FRONT of the feet — it is kicked up between you and the Proc, and
			    behind the legs half of it was hidden by the Proc that raised it. Keyed by
			    the landing COUNT so a second bounce is a second element and its animation
			    starts again instead of being skipped as unchanged. */}
			{bounce ? <DustPuff key={bounce.seq} strength={bounce.strength} /> : null}
		</svg>
	);
}

function Leg({ x, lift, colour }: { x: number; lift: number; colour: string }) {
	return <rect data-rim x={x} y={100 - lift} width="9" height="20" rx="4.5" fill={colour} {...RIM} />;
}

// The four beats: wide stance → left foot up → feet together → right foot up.
//
// A Proc faces YOU even while it travels sideways, so the legs must never swap
// sides the way a side-view walk cycle's would — the left leg stays left in every
// frame. The cycle reads as a waddle, which is also the right register for the
// character. Pose 0 doubles as the rest pose, so a standing Proc shows cell 0 and
// never freezes mid-stride.
// Held: both legs hang, slightly apart and uneven, which is what "dangling" looks
// like. Four identical-shaped entries keep the strip machinery unchanged; the flail
// comes from the swing above, not from the legs.
const DANGLE_POSES = [
	{ key: "dangle-a", left: { dx: -3, lift: -4 }, right: { dx: 3, lift: -6 } },
	{ key: "dangle-b", left: { dx: -3, lift: -5 }, right: { dx: 3, lift: -4 } },
	{ key: "dangle-c", left: { dx: -3, lift: -6 }, right: { dx: 3, lift: -5 } },
	{ key: "dangle-d", left: { dx: -3, lift: -4 }, right: { dx: 3, lift: -6 } },
] as const;

const LEG_POSES = [
	{ key: "contact-a", left: { dx: -4, lift: 0 }, right: { dx: 4, lift: 0 } },
	{ key: "passing-a", left: { dx: -1, lift: 5 }, right: { dx: 2, lift: 0 } },
	{ key: "contact-b", left: { dx: 2, lift: 0 }, right: { dx: -1, lift: 0 } },
	{ key: "passing-b", left: { dx: -1, lift: 0 }, right: { dx: 2, lift: 5 } },
] as const;

export { PROCS_RIM_PX };
