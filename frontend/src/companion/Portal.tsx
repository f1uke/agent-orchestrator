import { PROCS_INK, PROCS_RIM_PX, PROP_COLOURS } from "./palette";

// The portal a session arrives through and leaves by.
//
// A pet used to POP into existence when its session appeared and blink out when it
// ended. Both are real events — a worker was spawned, a worker finished — and both
// were shown as nothing at all. The portal is the moment made visible: a ring opens
// wide, the pet leaps out of it (or into it), and it collapses.
//
// It is a RING, and a big one — wider than the pet is tall. The first pass flanked it
// with two brackets, on the reasoning that the cast wears bracket ears; the human
// looked at it and asked for the circle on its own, larger, and with more going on
// inside it. That is what this is.
//
// The rules it is drawn under, all of which hold at this size:
//   - TWO CHANNELS. The mouth is a HOLE — ink inside, a lit blue edge around it — so
//     the blue carries a dark wallpaper and the ink carries a light one, and there is
//     no desktop where both are lost. Every colour comes out of `PROP_COLOURS`, which
//     `palette.test.ts` sweeps across the whole grey axis.
//   - TRANSFORM AND OPACITY ONLY, so six portals at once stay on the compositor. No
//     blur, no filter, no shadow: the spectacle is built out of things that move.
//   - The RESTING style is the portal fully OPEN. `prefers-reduced-motion` kills every
//     animation inside a pet, and what is left has to be a portal rather than the
//     first frame of one — the same rule the rally ring is drawn under.

/**
 * How long the whole entrance runs: the ring opens, the pet leaps out, it collapses.
 *
 * It was 900ms and the human's note on it was that the ring "appears and is gone too
 * fast" — which it was: the leap took the middle half of it and left the ring itself
 * barely a quarter of a second at full size either side. The extra time is spent
 * HOLDING it open, not on a slower jump: the leap is still about 600ms.
 */
export const PORTAL_IN_MS = 1500;
/** The exit. A shade quicker — the pet is already on its way out. */
export const PORTAL_OUT_MS = 1400;
/**
 * Both, under `prefers-reduced-motion`.
 *
 * The gesture is unchanged — a portal still opens and the session still arrives
 * through it — but nothing leaps, spins or overshoots, so there is nothing left to
 * stretch over 900ms. What remains is a portal and a pet fading through it.
 */
export const PORTAL_REDUCED_MS = 260;

/** Whether this portal is letting a pet out or taking one in. */
export type PortalPhase = "arriving" | "leaving";

/** The default envelope for a phase, when the caller has nothing else to say. */
export function portalDurationMs(phase: PortalPhase): number {
	return phase === "arriving" ? PORTAL_IN_MS : PORTAL_OUT_MS;
}

const CORE = PROP_COLOURS.portalCore;
const GLOW = PROP_COLOURS.portalGlow;

/** Rotate/scale an SVG group about ITSELF, not about the view box's corner. */
const SPIN_BOX = { transformBox: "fill-box", transformOrigin: "center" } as const;

/** The middle of the ring, in the art's own units. Set high, so it stands on the floor. */
const EYE = { x: 48, y: 66 };
/** The hole. Wider than the pet is tall once drawn — this is the "bigger" the human asked for. */
const MOUTH_R = 45;
/** How thick the lit edge of the hole is. */
const EDGE = 6;
/** The corona, outside the hole: the ring of broken light that makes it an EVENT. */
const CORONA_R = 57;

export type PortalProps = {
	phase: PortalPhase;
	/**
	 * The whole open→collapse envelope.
	 *
	 * Always written as an inline `animation-duration`, never as a number in the
	 * stylesheet: the engine times the pet's transition off the SAME constants, and a
	 * duration written twice is a duration that drifts — a portal that closes before
	 * its pet has landed, or a pet removed while its ring is still open.
	 */
	durationMs?: number;
};

/**
 * One portal, drawn behind its pet and set back from where the pet stands.
 *
 * Behind, because a pet leaping OUT is in front of the ring it came through and one
 * leaping in is swallowed by it. Set back, because a leap needs somewhere to leap
 * FROM — see `--procs-portal-gap`, which the ring and the pet's arc both read.
 */
export function Portal({ phase, durationMs }: PortalProps) {
	const runFor = durationMs ?? portalDurationMs(phase);
	// When the ring gets hit: on the way in, at the burst; on the way out, at the
	// swallow. The flash is the pet crossing the threshold, so it is timed off the
	// same fraction of the same clock the leap is, rather than guessed at in the CSS.
	const crossing = phase === "arriving" ? 0.28 : 0.52;
	return (
		<div className="companion-proc-portal" data-portal-phase={phase} style={{ animationDuration: `${runFor}ms` }}>
			<svg
				aria-hidden="true"
				focusable="false"
				width="100%"
				height="100%"
				viewBox="0 0 96 132"
				// The corona and the shockwaves are drawn OUTSIDE the figure's box on
				// purpose; nothing here is clipped to it.
				overflow="visible"
			>
				<g data-portal-mouth style={SPIN_BOX}>
					{/* The corona: a broken ring of light turning one way, and short ticks
					    turning the other. Two speeds in opposite directions is what reads as
					    something being HELD open rather than as a circle spinning. */}
					<g data-portal-corona style={SPIN_BOX}>
						<circle
							cx={EYE.x}
							cy={EYE.y}
							r={CORONA_R}
							fill="none"
							stroke={CORE}
							strokeWidth={3.4}
							strokeLinecap="round"
							strokeDasharray="10 14"
						/>
					</g>
					<g data-portal-ticks style={SPIN_BOX}>
						{TICKS.map((angle) => (
							<line
								key={angle}
								x1={EYE.x}
								y1={EYE.y - MOUTH_R - 4}
								x2={EYE.x}
								y2={EYE.y - MOUTH_R - 11}
								stroke={CORE}
								strokeWidth={3}
								strokeLinecap="round"
								transform={`rotate(${angle} ${EYE.x} ${EYE.y})`}
							/>
						))}
					</g>

					{/* The hole itself: ink inside, a lit edge around it, an ink casing under
					    that. Drawn as three rings rather than a disc, because a portal you
					    cannot see THROUGH is a blue egg standing on the floor — which is
					    exactly what the first pass looked like. */}
					<circle
						cx={EYE.x}
						cy={EYE.y}
						r={MOUTH_R}
						fill={PROCS_INK}
						stroke={PROCS_INK}
						strokeWidth={EDGE + 2 * PROCS_RIM_PX}
					/>
					<circle cx={EYE.x} cy={EYE.y} r={MOUTH_R} fill={PROCS_INK} stroke={CORE} strokeWidth={EDGE} />

					{/* Light going round inside the dark. Light on INK, never light on blue —
					    the first pass put pale blue arcs on a pale blue fill and at 128px they
					    simply were not there. */}
					<g data-portal-spin style={SPIN_BOX}>
						{SWIRL.map((arc) => (
							<path key={arc.d} d={arc.d} fill="none" stroke={GLOW} strokeWidth={arc.width} strokeLinecap="round" />
						))}
					</g>
					<g data-portal-motes style={SPIN_BOX}>
						{MOTES.map((mote) => (
							<circle
								key={`${mote.angle}-${mote.r}`}
								cx={EYE.x}
								cy={EYE.y - mote.r}
								r={mote.size}
								fill={GLOW}
								transform={`rotate(${mote.angle} ${EYE.x} ${EYE.y})`}
							/>
						))}
					</g>
					<circle data-portal-iris cx={EYE.x} cy={EYE.y} r={9} fill={GLOW} style={SPIN_BOX} />
				</g>

				{/* Two shockwaves off the ring: one when it tears open, one when the pet
				    crosses it. Drawn LAST and outside the mouth's group, so they carry on
				    expanding while the mouth itself is collapsing. */}
				<g
					data-portal-wave
					style={{
						...SPIN_BOX,
						animationDuration: `${Math.round(runFor * 0.5)}ms`,
						animationDelay: `${Math.round(runFor * 0.06)}ms`,
					}}
				>
					<circle cx={EYE.x} cy={EYE.y} r={MOUTH_R} fill="none" stroke={CORE} strokeWidth={4} />
				</g>
				<g
					data-portal-wave
					style={{
						...SPIN_BOX,
						animationDuration: `${Math.round(runFor * 0.42)}ms`,
						animationDelay: `${Math.round(runFor * crossing)}ms`,
					}}
				>
					<circle cx={EYE.x} cy={EYE.y} r={MOUTH_R} fill="none" stroke={GLOW} strokeWidth={5} />
				</g>
			</svg>
		</div>
	);
}

/** Three arcs at three radii, so the swirl has depth rather than one ring turning. */
const SWIRL = [
	{ d: `M48,24 A42,42 0 0 1 84,58`, width: 3.6 },
	{ d: `M48,108 A42,42 0 0 1 12,74`, width: 3.6 },
	{ d: `M48,38 A28,28 0 0 1 72,74`, width: 2.8 },
	{ d: `M48,94 A28,28 0 0 1 24,58`, width: 2.8 },
];

/** Sparks caught in the current. Different radii, so they do not read as one rigid ring. */
const MOTES = [
	{ angle: 18, r: 36, size: 2.6 },
	{ angle: 96, r: 24, size: 2 },
	{ angle: 158, r: 39, size: 3 },
	{ angle: 232, r: 29, size: 2.2 },
	{ angle: 310, r: 34, size: 2.6 },
];

/** Where the rim ticks sit. Eight, evenly, so the ring reads as machined rather than drawn. */
const TICKS = [0, 45, 90, 135, 180, 225, 270, 315];

/**
 * The wrapper that carries a pet through its own transition: the leap out of the
 * portal, or the leap into it.
 *
 * Separate from the portal because the two are different objects moving differently
 * — and because the pet's art must keep its own animations (walk, bob, blink) while
 * this one runs, which it does: a transform on an ancestor composes with them rather
 * than replacing them.
 *
 * `opacity` is set INLINE from the transition's own progress. Under normal motion the
 * keyframes override it, as a running animation outranks a normal declaration. Under
 * `prefers-reduced-motion` the keyframes are dead and this inline value is the whole
 * effect: a pet fading through a portal that is simply open. One code path, and the
 * reduced case cannot be forgotten because it is the same line.
 */
export function PortalTransit({
	phase,
	durationMs,
	opacity,
	children,
}: {
	phase: PortalPhase;
	durationMs?: number;
	opacity?: number;
	children: React.ReactNode;
}) {
	return (
		<div
			className="companion-proc-transit"
			data-portal-phase={phase}
			style={{ opacity, animationDuration: `${durationMs ?? portalDurationMs(phase)}ms` }}
		>
			{children}
		</div>
	);
}

/**
 * The pet's name chip while it is in transit.
 *
 * Outside {@link PortalTransit} on purpose: a name chip squashed and stretched by the
 * leap reads as a rendering fault rather than as a jump. It only has to agree about
 * WHEN the pet is on the desktop, which is all this does.
 */
export function PortalLabel({
	phase,
	durationMs,
	opacity,
	children,
}: {
	phase: PortalPhase;
	durationMs?: number;
	opacity?: number;
	children: React.ReactNode;
}) {
	return (
		<div
			className="companion-proc-transit-chrome"
			data-portal-phase={phase}
			style={{ opacity, animationDuration: `${durationMs ?? portalDurationMs(phase)}ms` }}
		>
			{children}
		</div>
	);
}

/**
 * How visible a pet is this far through its transition, for the reduced-motion path.
 *
 * A fade rather than a cut, and quick: at {@link PORTAL_REDUCED_MS} the whole
 * transition is a quarter of a second, so this is the pet appearing or leaving rather
 * than a long dissolve.
 */
export function transitOpacity(phase: PortalPhase, elapsedMs: number, durationMs: number): number {
	const fade = Math.max(1, durationMs * 0.6);
	const progress = Math.max(0, Math.min(1, elapsedMs / fade));
	return phase === "arriving" ? progress : 1 - progress;
}
