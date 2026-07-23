import { useId } from "react";
import type { SessionStatus } from "../renderer/types/workspace";
import { RUN_CYCLE_MS, WALK_CYCLE_MS, type Facing } from "./behaviour";
import type { CastMember } from "./cast";
import { PROCS_RIM_PX } from "./palette";
import { FIGURE_ATTRIBUTE } from "./pointer-region";
import { CordLayer, DustPuff, EmitLayer, GroundProp, HeldProp, RIM } from "./props";
import { sceneFor } from "./scene";
import { CELL, RIGS } from "./rigs";
import { speciesById, speciesWears } from "./species";

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
	const scene = sceneFor(status);
	const frame = procsFrame(size);
	const species = speciesById(cast.species);
	const Rig = RIGS[cast.species];

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
			<GroundProp ground={scene.ground} />
			<CordLayer
				cord={scene.cord}
				ground={scene.ground}
				cast={cast}
				from={(walking && species.cordFromWalking) || species.cordFrom}
			/>

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
								? { animation: "procs-hop-greet 760ms ease-out 2" }
								: undefined
					}
				>
					{/* WHICH CREATURE. A rig owns the body, the face, the legs-or-none and the
					    part that reports the link; everything around it — ground, cord, held
					    prop, emitted zzz, the frame, the pointer region — is this shell, and
					    is the same for all six. */}
					<Rig
						cast={cast}
						scene={scene}
						held={held}
						walking={walking}
						cycleMs={cycleMs}
						uid={uid}
						hat={
							speciesWears(cast.species, "hat") ? (
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
							) : null
						}
						heldProp={
							<g transform={offset(species.heldOffset)}>
								<HeldProp held={scene.held} mirrored={facing === "left"} />
							</g>
						}
					/>
				</g>
			</g>

			{/* Emitted last so zzz and confetti sit in front of the body. Offset per
			    creature, because "just above its head" is a different height on a cat
			    than on a Proc. */}
			<g transform={offset(species.emitOffset)}>
				<EmitLayer emit={scene.emit} cast={cast} />
			</g>

			{/* Dust in FRONT of the feet — it is kicked up between you and the Proc, and
			    behind the legs half of it was hidden by the Proc that raised it. Keyed by
			    the landing COUNT so a second bounce is a second element and its animation
			    starts again instead of being skipped as unchanged. */}
			{bounce ? <DustPuff key={bounce.seq} strength={bounce.strength} /> : null}
		</svg>
	);
}

/** A per-creature nudge, as a transform. `[0, 0]` produces none, so the Proc is untouched. */
function offset(by: readonly [number, number]): string | undefined {
	return by[0] === 0 && by[1] === 0 ? undefined : `translate(${by[0]} ${by[1]})`;
}

export { PROCS_RIM_PX };
