import { useId } from "react";
import { WALK_CYCLE_MS, type Facing } from "./behaviour";
import { PROCS_BLUSH, PROCS_BODY, PROCS_BODY_SHADE, PROCS_INK, PROCS_LIGHT, PROCS_RIM_PX } from "./palette";

// Procs — a little running process, and the placeholder member of the cast this
// PR ships (Curly, the amber default). The full six land with the art PR.
//
// The shape language, from the design:
//   - a big soft head over a small squashy body on stubby legs: solid and GROUNDED,
//     standing on the desktop, which is what keeps it from being a floating sheet
//   - oversized eyes set LOW and WIDE APART (baby schema), each with a highlight so
//     it reads as an eye and not a punched dot; blush; a small mouth
//   - ears are a code-punctuation bracket pair — `{}` for Curly — which is what
//     makes a cast member individual AND code-native instead of "a different animal"
//   - a cord that always leaves from the RIGHT and ends in a plug. It is the
//     signature, and it is kept physically opposite held props (which sit LEFT) so
//     the link and the task can never double-encode each other.
//   - a 2.4px ink rim on every silhouette shape, baked into the art rather than
//     applied as a CSS filter, so an animating Proc pays no per-frame paint cost.
//
// The walk is a real four-frame strip: the four leg poses are drawn side by side in
// one row and the row is stepped through one cell at a time with `steps(4, end)`.
// That is why the poses genuinely differ instead of the whole Proc being nudged.

/** One strip cell. Also the viewBox width, so cell N sits at x = N × CELL. */
const CELL = 96;
const VIEW_HEIGHT = 132;
/** Default drawn height. `full` tier per the design's size rules (≥120px). */
const DEFAULT_SIZE = 128;

export type ProcsProps = {
	facing: Facing;
	walking: boolean;
	/** Drawn height in px. */
	size?: number;
	className?: string;
};

export function Procs({ facing, walking, size = DEFAULT_SIZE, className }: ProcsProps) {
	const uid = useId().replace(/[^a-zA-Z0-9-]/g, "");
	const headClip = `procs-head-${uid}`;
	const cellClip = `procs-cell-${uid}`;

	return (
		<svg
			role="img"
			aria-label="Proc"
			className={className}
			width={(size / VIEW_HEIGHT) * CELL}
			height={size}
			viewBox={`0 0 ${CELL} ${VIEW_HEIGHT}`}
			style={{
				// A discrete flip at the turn — the sprite mirrors, it does not rotate.
				transform: facing === "left" ? "scaleX(-1)" : undefined,
				overflow: "visible",
			}}
		>
			<defs>
				<clipPath id={headClip}>
					<rect x="14" y="6" width="68" height="72" rx="26" />
				</clipPath>
				{/* Shows exactly one cell of the leg strip. */}
				<clipPath id={cellClip}>
					<rect x="0" y="94" width={CELL} height={VIEW_HEIGHT - 94} />
				</clipPath>
			</defs>

			{/* The cord: out to the RIGHT, down to a plug resting on the floor. */}
			<g data-cord-group>
				<CasedStroke part="cord" d={CORD_PATH} core={3.2} />
				<rect data-rim x="81" y="111" width="14" height="9" rx="3" fill={PROCS_BODY_SHADE} {...RIM} />
				<rect x="84" y="119.5" width="2.6" height="4" rx="1.2" fill={PROCS_INK} />
				<rect x="89" y="119.5" width="2.6" height="4" rx="1.2" fill={PROCS_INK} />
			</g>

			{/* Legs, drawn first so the body's rim covers where they meet it. */}
			<g clipPath={`url(#${cellClip})`}>
				<g
					data-walk-strip
					style={walking ? { animation: `procs-walk ${WALK_CYCLE_MS}ms steps(4, end) infinite` } : undefined}
				>
					{LEG_POSES.map((pose, index) => (
						<g key={pose.key} data-walk-pose transform={`translate(${index * CELL} 0)`}>
							<Leg x={38 + pose.left.dx} lift={pose.left.lift} />
							<Leg x={50 + pose.right.dx} lift={pose.right.lift} />
						</g>
					))}
				</g>
			</g>

			{/* Body + ears bob on their own eased track, separate from the leg steps. */}
			<g style={walking ? { animation: `procs-bob ${WALK_CYCLE_MS}ms ease-in-out infinite alternate` } : undefined}>
				<rect data-rim x="29" y="74" width="38" height="30" rx="14" fill={PROCS_BODY_SHADE} {...RIM} />

				{/* Ears: the `{}` bracket pair that makes this one Curly. */}
				<CasedStroke part="ear-left" d={EAR_LEFT_PATH} core={4} />
				<CasedStroke part="ear-right" d={EAR_RIGHT_PATH} core={4} />

				{/* Head. */}
				<rect data-rim x="14" y="6" width="68" height="72" rx="26" fill={PROCS_BODY} {...RIM} />

				{/* Blush, clipped to the head so it can never spill and read as a smudge. */}
				<g clipPath={`url(#${headClip})`}>
					<ellipse data-blush clipPath={`url(#${headClip})`} cx="26" cy="63" rx="7" ry="4.5" fill={PROCS_BLUSH} />
					<ellipse data-blush clipPath={`url(#${headClip})`} cx="70" cy="63" rx="7" ry="4.5" fill={PROCS_BLUSH} />
				</g>

				{/* Eyes: low, wide apart, each with a highlight. */}
				<circle cx="33" cy="53" r="10" fill={PROCS_INK} />
				<circle cx="63" cy="53" r="10" fill={PROCS_INK} />
				<circle cx="29.5" cy="49" r="3.4" fill={PROCS_LIGHT} />
				<circle cx="59.5" cy="49" r="3.4" fill={PROCS_LIGHT} />

				{/* Mouth. */}
				<path d="M43 67 q 5 5 10 0" fill="none" stroke={PROCS_INK} strokeWidth="2.6" strokeLinecap="round" />
			</g>
		</svg>
	);
}

const RIM = { stroke: PROCS_INK, strokeWidth: String(PROCS_RIM_PX) } as const;

const EAR_LEFT_PATH = "M18 26 C 6 28 14 40 2 42 C 14 44 6 56 18 58";
const EAR_RIGHT_PATH = "M78 26 C 90 28 82 40 94 42 C 82 44 90 56 78 58";
const CORD_PATH = "M67 92 C 84 92 91 100 88 111";

/**
 * A stroked line that carries BOTH legibility channels: an ink casing under a
 * body-coloured core, exactly the rim rule applied to a line instead of a shape.
 * Flat-ink ears and a flat-ink cord disappeared entirely on a dark wallpaper —
 * and those two are the character's whole signature, so losing them loses the
 * Proc's identity, not just a detail.
 */
function CasedStroke({ part, d, core }: { part: string; d: string; core: number }) {
	const shared = { d, fill: "none", strokeLinecap: "round", strokeLinejoin: "round" } as const;
	return (
		<>
			<path data-casing={part} {...shared} stroke={PROCS_INK} strokeWidth={String(core + 2 * PROCS_RIM_PX)} />
			<path data-core={part} {...shared} stroke={PROCS_BODY_SHADE} strokeWidth={String(core)} />
		</>
	);
}

function Leg({ x, lift }: { x: number; lift: number }) {
	return <rect data-rim x={x} y={100 - lift} width="9" height="20" rx="4.5" fill={PROCS_BODY_SHADE} {...RIM} />;
}

// The four beats: wide stance → left foot up → feet together → right foot up.
//
// A Proc faces YOU even while it travels sideways, so the legs must never swap
// sides the way a side-view walk cycle's would — the left leg stays left in every
// frame. The cycle reads as a waddle, which is also the right register for the
// character. Pose 0 doubles as the rest pose, so a standing Proc shows cell 0 and
// never freezes mid-stride.
const LEG_POSES = [
	{ key: "contact-a", left: { dx: -4, lift: 0 }, right: { dx: 4, lift: 0 } },
	{ key: "passing-a", left: { dx: -1, lift: 5 }, right: { dx: 2, lift: 0 } },
	{ key: "contact-b", left: { dx: 2, lift: 0 }, right: { dx: -1, lift: 0 } },
	{ key: "passing-b", left: { dx: -1, lift: 0 }, right: { dx: 2, lift: 5 } },
] as const;
