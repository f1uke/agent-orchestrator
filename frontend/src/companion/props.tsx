import type { CastMember } from "./cast";
import { PROCS_INK, PROCS_LIGHT, PROCS_RIM_PX, PROP_COLOURS } from "./palette";
import type { Cord, Emit, Ground, Held } from "./scene";

// The PROP layers: GROUND, HELD, EMIT and the CORD, drawn in the same SVG as the
// Proc so they scale and mirror with it.
//
// Every prop obeys the same two-channel rule the character does, because a prop
// sits on the same wallpaper: a body colour that carries dark desktops plus a 2.4px
// ink rim that carries light ones. That is why there is no `fill` here that is not
// either from PROP_COLOURS (swept by palette.test.ts) or the ink itself.
//
// Sides are fixed and load-bearing: the CORD always leaves RIGHT and the GROUND
// sits to its right so the cord can plug straight into it; HELD props always sit
// LEFT. The cord is the LINK, props are the TASK, and keeping them physically
// apart is what stops them double-encoding the same fact.

export const RIM = { stroke: PROCS_INK, strokeWidth: String(PROCS_RIM_PX) } as const;

/**
 * Where the cord meets a ground prop. Every ground exposes the same socket, low and
 * on its near side, so one cord path serves the desk, the bed and the crate.
 */
const SOCKET = { x: 86, y: 112 };

/**
 * A stroked line carrying BOTH channels: an ink casing under a coloured core, i.e.
 * the rim rule applied to a line. Flat-ink ears and cord disappeared entirely on a
 * dark wallpaper in #157, and those two are the character's whole signature.
 */
export function CasedStroke({ part, d, core, colour }: { part: string; d: string; core: number; colour: string }) {
	const shared = { d, fill: "none", strokeLinecap: "round", strokeLinejoin: "round" } as const;
	return (
		<>
			<path data-casing={part} {...shared} stroke={PROCS_INK} strokeWidth={String(core + 2 * PROCS_RIM_PX)} />
			<path data-core={part} {...shared} stroke={colour} strokeWidth={String(core)} />
		</>
	);
}

// ---------------------------------------------------------------- GROUND

export function GroundProp({ ground }: { ground: Ground }) {
	if (ground === "none") return null;
	return (
		<g data-slot="ground" data-ground={ground}>
			{ground === "desk" && <Desk />}
			{ground === "bed" && <Bed />}
			{ground === "crate" && <Crate />}
		</g>
	);
}

// A desk BESIDE the Proc, never behind it: a Proc is nearly all head with a chin
// almost to the floor, so anything it sits behind crosses its face. Beside, with
// the cord plugged into the leg, it still reads as "at the computer".
function Desk() {
	return (
		<>
			<rect data-rim x="102" y="74" width="32" height="24" rx="3" fill={PROP_COLOURS.linen} {...RIM} />
			<rect x="107" y="79" width="22" height="13" rx="1.5" fill={PROCS_INK} />
			<rect data-rim x="86" y="98" width="54" height="7" rx="2.5" fill={PROP_COLOURS.wood} {...RIM} />
			<rect data-rim x="90" y="105" width="7" height="17" rx="2" fill={PROP_COLOURS.wood} {...RIM} />
			<rect data-rim x="130" y="105" width="7" height="17" rx="2" fill={PROP_COLOURS.wood} {...RIM} />
		</>
	);
}

function Bed() {
	return (
		<>
			{/* Headboard first, so the mattress rim reads in front of it. A bed without
			    one measured, on the contact sheet, as "a bench". */}
			<rect data-rim x="130" y="80" width="12" height="36" rx="4" fill={PROP_COLOURS.wood} {...RIM} />
			<rect data-rim x="84" y="99" width="54" height="17" rx="5" fill={PROP_COLOURS.linen} {...RIM} />
			<rect data-rim x="106" y="97" width="28" height="19" rx="5" fill={PROP_COLOURS.blanket} {...RIM} />
			<rect data-rim x="90" y="92" width="20" height="12" rx="5" fill={PROP_COLOURS.linen} {...RIM} />
			<rect data-rim x="86" y="116" width="7" height="6" rx="2" fill={PROP_COLOURS.wood} {...RIM} />
			<rect data-rim x="130" y="116" width="7" height="6" rx="2" fill={PROP_COLOURS.wood} {...RIM} />
		</>
	);
}

function Crate() {
	return (
		<>
			<rect data-rim x="92" y="88" width="44" height="34" rx="3" fill={PROP_COLOURS.wood} {...RIM} />
			<path d="M92 99 L136 99 M92 111 L136 111" stroke={PROCS_INK} strokeWidth="2" fill="none" />
			<path d="M107 88 L107 122 M121 88 L121 122" stroke={PROCS_INK} strokeWidth="1.6" fill="none" opacity="0.55" />
		</>
	);
}

// ---------------------------------------------------------------- HELD

/**
 * The x-extent each held shape occupies in rig coordinates.
 *
 * Needed because the sprite turns around by mirroring on X, and a held prop's
 * CONTENT means its direction — a `?`, a merge arrow, a tick, a clock. Mirrored,
 * they read backwards. So the prop travels to the Proc's other hand with the rest
 * of the sprite (a carried thing moves with its carrier) and flips its own content
 * back about its own footprint, which is the one axis that leaves it in the hand
 * it just moved to.
 */
const HELD_EXTENT = { sign: { min: 1, max: 30 }, page: { min: 4, max: 29 } } as const;

/** `translate(t) scale(-1 1)` maps x to `t - x`; `t = min + max` sends a footprint back onto itself. */
export function counterMirrorX(extent: { min: number; max: number }): string {
	return `translate(${extent.min + extent.max} 0) scale(-1 1)`;
}

export function HeldProp({ held, mirrored = false }: { held: Held; mirrored?: boolean }) {
	if (held === "none") return null;
	const sign = held === "sign-question" || held === "sign-merge";
	return (
		<g
			data-slot="held"
			data-held={held}
			transform={mirrored ? counterMirrorX(sign ? HELD_EXTENT.sign : HELD_EXTENT.page) : undefined}
		>
			{sign ? <Sign kind={held} /> : <Page surface={held} />}
		</g>
	);
}

// One page shape, five surfaces. Held at the Proc's left side, below the ears so it
// never crowds the silhouette that identifies the character.
function Page({ surface }: { surface: Held }) {
	return (
		<>
			<rect data-rim x="4" y="74" width="25" height="31" rx="2.5" fill={PROP_COLOURS.paper} {...RIM} />
			{surface === "page-lines" && (
				<path
					d="M9 82 L24 82 M9 88 L24 88 M9 94 L19 94"
					stroke={PROCS_INK}
					strokeWidth="2"
					strokeLinecap="round"
					fill="none"
				/>
			)}
			{surface === "page-check" && (
				<path
					d="M10 90 L15 96 L24 83"
					stroke={PROCS_INK}
					strokeWidth="3"
					strokeLinecap="round"
					strokeLinejoin="round"
					fill="none"
				/>
			)}
			{surface === "page-cross" && (
				<path d="M10 84 L24 96 M24 84 L10 96" stroke={PROCS_INK} strokeWidth="3" strokeLinecap="round" fill="none" />
			)}
			{surface === "page-clock" && (
				<>
					<circle cx="16.5" cy="90" r="8" fill="none" stroke={PROCS_INK} strokeWidth="2.4" />
					<path d="M16.5 85 L16.5 90 L21 92" stroke={PROCS_INK} strokeWidth="2.4" strokeLinecap="round" fill="none" />
				</>
			)}
		</>
	);
}

// Signs are RATIONED to the two states that are addressed to YOU — "answer me" and
// "press merge". A sign anywhere therefore always means the same thing, which is
// what a page cannot do because pages describe the work instead.
function Sign({ kind }: { kind: Held }) {
	return (
		<>
			<rect data-rim x="9" y="92" width="4" height="16" rx="2" fill={PROP_COLOURS.wood} {...RIM} />
			<rect data-rim x="1" y="68" width="29" height="26" rx="5" fill={PROP_COLOURS.paper} {...RIM} />
			{kind === "sign-question" ? (
				<>
					<path
						d="M10 78 C 10 70 22 70 22 77 C 22 82.5 15.5 82.5 15.5 87"
						stroke={PROCS_INK}
						strokeWidth="3"
						strokeLinecap="round"
						fill="none"
					/>
					<circle cx="15.5" cy="90.5" r="1.9" fill={PROCS_INK} />
				</>
			) : (
				<path
					d="M7 73 L7 82 C 7 86 9.5 87.5 13 87.5 L22 87.5 M18 82.5 L23.5 87.5 L18 92.5"
					stroke={PROCS_INK}
					strokeWidth="3"
					strokeLinecap="round"
					strokeLinejoin="round"
					fill="none"
				/>
			)}
		</>
	);
}

// ---------------------------------------------------------------- EMIT

export function EmitLayer({ emit, cast }: { emit: Emit; cast: CastMember }) {
	if (emit === "none") return null;
	return (
		<g data-slot="emit" data-emit={emit}>
			{emit === "zzz" && <Zzz />}
			{emit === "sparks" && <Sparks />}
			{emit === "confetti" && <Confetti />}
			{emit === "quiet" && <Quiet cast={cast} />}
		</g>
	);
}

// Rising Z's, staggered so they read as a sequence rather than a blink.
function Zzz() {
	const zs = [
		{ x: 78, y: 4, size: 1, delay: 0 },
		{ x: 92, y: -6, size: 0.82, delay: 0.6 },
		{ x: 103, y: -15, size: 0.64, delay: 1.2 },
	];
	return (
		<>
			{zs.map((z) => (
				<g key={z.x} transform={`translate(${z.x} ${z.y}) scale(${z.size})`}>
					<g style={{ animation: `procs-zzz 3s ease-in-out ${z.delay}s infinite` }}>
						<CasedStroke part={`zzz-${z.x}`} d="M0 0 L11 0 L0 12 L11 12" core={3} colour={PROP_COLOURS.linen} />
					</g>
				</g>
			))}
		</>
	);
}

// Sparks off the link, not off the Proc: a failed run is a fact about the work.
function Sparks() {
	const sparks = [
		{ x: 80, y: 58, size: 1.35, delay: 0 },
		{ x: 100, y: 42, size: 1, delay: 0.35 },
		{ x: 116, y: 62, size: 0.8, delay: 0.7 },
	];
	return (
		<>
			{sparks.map((spark) => (
				<g key={`${spark.x}-${spark.y}`} transform={`translate(${spark.x} ${spark.y}) scale(${spark.size})`}>
					<g style={{ animation: `procs-spark 900ms ease-in-out ${spark.delay}s infinite` }}>
						<path
							data-rim
							d="M0 -9 L2.6 -2.6 L9 0 L2.6 2.6 L0 9 L-2.6 2.6 L-9 0 L-2.6 -2.6 Z"
							fill={PROP_COLOURS.spark}
							{...RIM}
						/>
					</g>
				</g>
			))}
		</>
	);
}

function Confetti() {
	const pieces = [
		{ x: 20, y: 2, fill: PROP_COLOURS.confettiA, delay: 0, spin: -18 },
		{ x: 38, y: -8, fill: PROP_COLOURS.confettiB, delay: 0.45, spin: 24 },
		{ x: 56, y: -4, fill: PROP_COLOURS.confettiC, delay: 0.2, spin: -32 },
		{ x: 72, y: 6, fill: PROP_COLOURS.confettiA, delay: 0.75, spin: 14 },
		{ x: 10, y: 14, fill: PROP_COLOURS.confettiB, delay: 1.1, spin: 30 },
		{ x: 84, y: 20, fill: PROP_COLOURS.confettiC, delay: 1.5, spin: -12 },
	];
	return (
		<>
			{pieces.map((piece) => (
				<g key={`${piece.x}-${piece.y}`} transform={`translate(${piece.x} ${piece.y})`}>
					<g style={{ animation: `procs-confetti 2.4s ease-in ${piece.delay}s infinite` }}>
						<rect
							data-rim
							x="-4"
							y="-4"
							width="8"
							height="8"
							rx="1.5"
							fill={piece.fill}
							transform={`rotate(${piece.spin})`}
							{...RIM}
						/>
					</g>
				</g>
			))}
		</>
	);
}

// The "nothing is coming" dots. Static on purpose: a session we have lost contact
// with must not twitch, because motion would assert liveness we do not have.
function Quiet({ cast }: { cast: CastMember }) {
	void cast;
	return (
		<>
			{[36, 48, 60].map((x) => (
				<circle key={x} data-rim cx={x} cy="-6" r="3.4" fill={PROP_COLOURS.quiet} {...RIM} />
			))}
		</>
	);
}

// ---------------------------------------------------------------- CORD

// A simple sag from the body down to the prop's socket. The first attempt ran the
// cord straight across the ~10 units between them, which left the data pips no room
// and rendered them inside the cord's own casing; the second looped it back on
// itself, which read as a tangle and crossed the bed's pillow. The gap the cable
// needs is what decides where the ground props sit, not the other way round.
const CORD_TO_GROUND = "M67 92 C 73 99 74 110 86 112";
// Eight of the fifteen states have no ground prop, so their cord has nothing on
// screen to terminate at — and that is the majority of the cast at any moment,
// which is why "some of them have a long weird tail" was the human's experience of
// it. Two earlier answers were worse: ending it in a plug lying on the floor was
// indistinguishable from UNPLUGGED, and running it off the frame gave it no ending
// at all, which is the tail.
//
// It now coils once and plugs into the floor. Short, finished, and still obviously
// connected — the difference from unplugged is carried by the plug being at the
// cord's end and standing up, rather than lying on its side a gap away.
const CORD_TO_FLOOR = "M67 92 C 79 93 83 101 76 105 C 70 108 72 114 82 112";
const CORD_LOOSE = "M67 92 C 77 96 83 104 85 115";
const CORD_COILED = "M67 92 C 78 92 82 100 74 104 C 68 107 70 112 86 112";

/**
 * The cord: the LINK, and the one part of the rig that reports on the session
 * rather than the task. It always leaves RIGHT — into a ground prop when the scene
 * has one, off the frame towards whatever it is attached to when there is none, and
 * dropped on the floor when the session is over or lost.
 */
export function CordLayer({ cord, ground, cast }: { cord: Cord; ground: Ground; cast: CastMember }) {
	const plugged = ground !== "none";
	const unplugged = cord === "unplugged";
	const d = unplugged ? CORD_LOOSE : plugged ? (cord === "coiled" ? CORD_COILED : CORD_TO_GROUND) : CORD_TO_FLOOR;

	return (
		<g
			data-slot="cord"
			data-cord={cord}
			style={cord === "tugging" ? { animation: "procs-tug 700ms ease-in-out infinite" } : undefined}
		>
			<CasedStroke part="cord" d={d} core={3.2} colour={cast.shade} />
			{plugged && (
				<g transform={`translate(${SOCKET.x} ${SOCKET.y}) rotate(-90)`}>
					<Plug kind="ground" colour={cast.shade} />
				</g>
			)}
			{!plugged && !unplugged && (
				<g transform="translate(82 112)">
					<Plug kind="floor" colour={cast.shade} />
				</g>
			)}
			{unplugged && (
				<g transform="translate(101 117) rotate(-90) scale(1.1)">
					<Plug kind="loose" colour={cast.shade} />
				</g>
			)}
			{cord === "streaming" && <Pips d={d} />}
			{cord === "sparking" && <CordSpark />}
		</g>
	);
}

function Plug({ kind, colour }: { kind: string; colour: string }) {
	return (
		<>
			<rect data-rim data-plug={kind} x="-7" y="0" width="14" height="9" rx="3" fill={colour} {...RIM} />
			<rect x="-4" y="8.5" width="2.6" height="4" rx="1.2" fill={PROCS_INK} />
			<rect x="1.4" y="8.5" width="2.6" height="4" rx="1.2" fill={PROCS_INK} />
		</>
	);
}

// Data moving down the link. Opacity only, staggered: three dots that pulse in
// sequence read as flow, and cost nothing but a composited opacity per frame.
function Pips({ d }: { d: string }) {
	const points = pointsAlong(d);
	return (
		<>
			{points.map((point, index) => (
				<circle
					key={`${point.x}-${point.y}`}
					data-pip
					cx={point.x}
					cy={point.y}
					r="3.4"
					fill={PROCS_LIGHT}
					stroke={PROCS_INK}
					strokeWidth="1.6"
					style={{ animation: `procs-pip 1.2s ease-in-out ${index * 0.28}s infinite` }}
				/>
			))}
		</>
	);
}

// Three points spaced along a cubic, so the pips sit ON the cord whichever cord
// path the scene chose rather than at hand-placed coordinates that would drift.
function pointsAlong(d: string): Array<{ x: number; y: number }> {
	const n = (d.match(/-?\d+(\.\d+)?/g) ?? []).map(Number);
	if (n.length < 8) return [];
	// Walk the chained cubics as one curve so the pips spread over the WHOLE cord,
	// however many segments the scene's route happens to use.
	const segments = Math.max(1, Math.floor((n.length - 2) / 6));
	return [0.25, 0.5, 0.75].map((t) => {
		const scaled = t * segments;
		const index = Math.min(segments - 1, Math.floor(scaled));
		const local = scaled - index;
		const base = index * 6;
		const x0 = index === 0 ? n[0] : n[base - 2];
		const y0 = index === 0 ? n[1] : n[base - 1];
		const [x1, y1, x2, y2, x3, y3] = n.slice(base + 2, base + 8);
		const u = 1 - local;
		return {
			x: round(u ** 3 * x0 + 3 * u * u * local * x1 + 3 * u * local * local * x2 + local ** 3 * x3),
			y: round(u ** 3 * y0 + 3 * u * u * local * y1 + 3 * u * local * local * y2 + local ** 3 * y3),
		};
	});
}

function round(value: number): number {
	return Math.round(value * 10) / 10;
}

function CordSpark() {
	return (
		<g transform="translate(80 100)">
			<g style={{ animation: "procs-spark 700ms ease-in-out infinite" }}>
				<path data-rim d="M0 -7 L2 -2 L7 0 L2 2 L0 7 L-2 2 L-7 0 L-2 -2 Z" fill={PROP_COLOURS.spark} {...RIM} />
			</g>
		</g>
	);
}

// ---------------------------------------------------------------- DUST

/** Where each puff of a landing goes: outward from the feet, and a little up. */
const DUST_PUFFS = [
	{ x: 28, dx: -24, r: 7, delay: 0 },
	{ x: 48, dx: 0, r: 5, delay: 70 },
	{ x: 68, dx: 24, r: 6.5, delay: 35 },
];

/**
 * The puff a landing throws up.
 *
 * A Proc that hits the floor and simply carries on reads as weightless; this is
 * the only thing that says it landed ON something. Same two channels as
 * everything else out here — a self-contained fill plus the ink rim — because it
 * is drawn over the wallpaper like the rest of the scene.
 *
 * `strength` runs 0-1 off the landing speed, so a Proc set down gently barely
 * raises anything and one dropped from the top of the display raises a cloud.
 */
export function DustPuff({ strength }: { strength: number }) {
	const spread = 0.5 + strength;
	return (
		<g data-slot="dust" data-dust-strength={strength.toFixed(2)}>
			{DUST_PUFFS.map((puff) => (
				<circle
					key={puff.x}
					data-rim
					cx={puff.x}
					cy={116}
					r={puff.r * (0.6 + strength * 0.6)}
					fill={PROP_COLOURS.quiet}
					{...RIM}
					style={{
						// About the CIRCLE, not about the corner of the view box. An SVG
						// element's transform origin defaults to the view box's own origin,
						// so `scale(0.35)` at the start of the puff hauled it from the feet
						// up to the Proc's head and then swept it back down — the dust
						// appeared to come off its ears.
						transformBox: "fill-box",
						transformOrigin: "center",
						animation: `procs-dust ${420 + strength * 220}ms ease-out ${puff.delay}ms both`,
						["--procs-dust-dx" as string]: `${puff.dx * spread}px`,
					}}
				/>
			))}
		</g>
	);
}
