import { useEffect, useRef, useState } from "react";
import { composeCast, HATS, palettesFor, accessoriesFor, type CastMember } from "./cast";
import { figureLeft, NAME_TAG_ALLOWANCE, PET_HEIGHT, petFrame } from "./layout";
import { NameTag } from "./NameTag";
import { Portal, PortalLabel, PortalTransit } from "./Portal";
import {
	PORTAL_IN_MS,
	PORTAL_OUT_MS,
	PORTAL_REDUCED_MS,
	portalDurationMs,
	transitOpacity,
	type PortalPhase,
} from "./portal-transit";
import { Procs } from "./Procs";
import type { SessionStatus } from "../renderer/types/workspace";
import type { SpeciesId } from "./species";

// The portal sheet: the two lifecycle moments, played over and over, so they can be
// judged the only way an animation can be — by watching it.
//
// Three shapes were drawn at first — a bracket gate, a socket and a rift — on the
// theory that the choice could not be argued on paper. It could not, and it was not:
// the human looked at the sheet and asked for the circle on its own, bigger, with more
// happening inside it. So there is one portal now, and this sheet exists to show it
// moving rather than to offer a menu.
//
// Lab only, like the rest of the concept sheet. Nothing here is mounted on a desktop.

/** One replay every this often, so a cell is watchable without clicking anything. */
const LOOP_MS = Math.max(PORTAL_IN_MS, PORTAL_OUT_MS) + 900;

const PHASES: Array<{ phase: PortalPhase; caption: string }> = [
	{ phase: "arriving", caption: "portal-in · a worker was spawned" },
	{ phase: "leaving", caption: "portal-out · the work is finished" },
];

/**
 * A walker, a floater and a hopper — the three ways this cast gets about — and one of
 * them ANCHORED, because a pet sitting on its crate is the case where an exit has
 * something to leave behind.
 */
const CAST_ON_SHOW: Array<{ species: SpeciesId; status: SessionStatus }> = [
	{ species: "proc", status: "working" },
	{ species: "ghost", status: "needs_input" },
	{ species: "slime", status: "idle" },
];

/** Where the sequence is sampled, as fractions of the whole move. */
const FILMSTRIP = [0.04, 0.18, 0.32, 0.44, 0.56, 0.7, 0.9];

/** Two ends of the wallpaper axis, side by side — the pair that catches contrast bugs. */
const TONE_PAIR = [
	{ id: "light", label: "light wallpaper", background: "#f2f0f5" },
	{ id: "dark", label: "dark wallpaper", background: "#1b1a22" },
];

/**
 * The portals on their own, as a page, with the wallpaper under them switchable.
 *
 * `companion.html#portals`. It exists because a review of ONE thing should not open
 * onto six creatures across fifteen states — and because this art cannot be reviewed
 * by reading a diff or a still. Somebody has to watch it.
 */
export function PortalReview({ onClose }: { onClose: () => void }) {
	const [tone, setTone] = useState(REVIEW_TONES[0]);
	return (
		<div style={PAGE}>
			<header style={BAR}>
				<strong>Portals</strong>
				<span style={MUTED}>how a session arrives, and how it leaves</span>
				<span style={{ flex: 1 }} />
				{REVIEW_TONES.map((entry) => (
					<button
						key={entry.id}
						type="button"
						style={{ ...BUTTON, ...(entry.id === tone.id ? BUTTON_ON : null) }}
						onClick={() => setTone(entry)}
					>
						{entry.label}
					</button>
				))}
				<button type="button" style={BUTTON} onClick={onClose}>
					close
				</button>
			</header>
			<PortalSheet tone={tone} />
		</div>
	);
}

/**
 * The wallpaper range, as tones rather than pictures. Contrast depends only on relative
 * luminance, so a light, a mid and a dark grey — plus one busy photo-ish gradient — is
 * the whole axis rather than a sample of it.
 */
const REVIEW_TONES = [
	{ id: "light", label: "light wallpaper", background: "#f2f0f5" },
	{ id: "mid", label: "mid wallpaper", background: "#8d8a97" },
	{ id: "dark", label: "dark wallpaper", background: "#1b1a22" },
	{
		id: "busy",
		label: "busy wallpaper",
		background: "linear-gradient(115deg, #2b1c3f 0%, #d8c7a2 38%, #16394a 62%, #f4f1ea 100%)",
	},
];

const PAGE: React.CSSProperties = {
	position: "fixed",
	inset: 0,
	overflowY: "auto",
	pointerEvents: "auto",
	background: "#100d18",
	color: "#f3f0f8",
	font: "500 12px/1.5 ui-sans-serif, system-ui, sans-serif",
	zIndex: 10000,
};

const BAR: React.CSSProperties = {
	position: "sticky",
	top: 0,
	display: "flex",
	gap: 6,
	alignItems: "center",
	flexWrap: "wrap",
	padding: "8px 12px",
	background: "#16131f",
	borderBottom: "1px solid #3a3448",
	zIndex: 2,
};

const BUTTON_ON: React.CSSProperties = { background: "#3c2f5c", borderColor: "#7c5cf0" };

export function PortalSheet({ tone }: { tone: { label: string; background: string } }) {
	const { beat, elapsed, replay } = usePortalLoop();

	return (
		<section style={{ ...SHEET, background: "#141020" }}>
			<header style={HEAD}>
				<h2 style={TITLE}>Portals — how a session arrives, and how it leaves</h2>
				<span style={MUTED}>
					in {PORTAL_IN_MS}ms · out {PORTAL_OUT_MS}ms · replaying every {(LOOP_MS / 1000).toFixed(1)}s
				</span>
				<span style={{ flex: 1 }} />
				<button type="button" style={BUTTON} onClick={replay}>
					replay now
				</button>
			</header>

			<h3 style={SUBTITLE}>1 · A whole session's life, on a loop. It arrives, it works, its work finishes, it goes.</h3>
			<div style={{ ...STRIP, background: tone.background, justifyContent: "center", gap: 24 }}>
				{LIFECYCLE_CAST.map((entry, index) => (
					<Lifecycle key={entry.species} cast={look(entry.species, index + 1)} status={entry.status} />
				))}
			</div>
			<p style={NOTE}>
				This is the real thing at its real size — the same components and the same stylesheet the overlay uses, at{" "}
				{PORTAL_IN_MS}ms in and {PORTAL_OUT_MS}ms out. Between the two it just stands there being a pet; on a desktop
				that stretch is however long the session lasts.
			</p>

			<h3 style={SUBTITLE}>2 · The same two moves, frozen. Read left to right.</h3>
			{PHASES.map(({ phase, caption }) => (
				<div key={phase} style={{ ...STRIP, background: tone.background }}>
					<figcaption style={{ ...CAPTION, alignSelf: "center", width: 96, textAlign: "left" }}>{caption}</figcaption>
					{FILMSTRIP.map((at) => (
						<figure key={at} style={FIGURE}>
							<PortalStage
								cast={look(phase === "arriving" ? "proc" : "cat", 1)}
								phase={phase}
								beat={0}
								freezeAt={at}
								narrow
							/>
							<figcaption style={CAPTION}>{Math.round(at * portalDurationMs(phase))}ms</figcaption>
						</figure>
					))}
				</div>
			))}
			<p style={NOTE}>
				The ring opens on its own for the first third, the pet is only on the desktop for the middle third, and it
				collapses over the last. An <b>anchored</b> pet leaves its bed or its crate behind: a place is the one thing it
				cannot take with it.
			</p>

			<h3 style={SUBTITLE}>3 · Playing, across the cast and both ends of the wallpaper axis.</h3>
			{TONE_PAIR.map((paper) => (
				<div key={paper.id} style={{ ...STRIP, background: paper.background }}>
					{CAST_ON_SHOW.map(({ species, status }, index) =>
						PHASES.map(({ phase }) => (
							<figure key={`${species}-${phase}`} style={FIGURE}>
								<PortalStage cast={look(species, index)} phase={phase} status={status} beat={beat} />
								<figcaption style={CAPTION}>
									{species} · {status} · {phase === "arriving" ? "in" : "out"}
								</figcaption>
							</figure>
						)),
					)}
				</div>
			))}

			<h3 style={SUBTITLE}>4 · Reduced motion. The same event, with nothing that moves for its own sake.</h3>
			<div data-reduced-motion style={{ ...STRIP, background: tone.background }}>
				{PHASES.map(({ phase, caption }) => (
					<figure key={phase} style={FIGURE}>
						<PortalStage
							cast={look("proc", 3)}
							phase={phase}
							beat={beat}
							durationMs={PORTAL_REDUCED_MS}
							opacity={transitOpacity(phase, elapsed, PORTAL_REDUCED_MS)}
						/>
						<figcaption style={CAPTION}>{caption}</figcaption>
					</figure>
				))}
				<figcaption style={{ ...CAPTION, alignSelf: "center", maxWidth: 260, textAlign: "left" }}>
					The portal still opens — it is simply <b>already open</b>, because the resting style is the portal rather than
					the first frame of one. Nothing spins, nothing leaps, nothing overshoots. The pet fades through in{" "}
					{PORTAL_REDUCED_MS}ms.
				</figcaption>
			</div>
		</section>
	);
}

/**
 * One pet living a whole session, over and over: it arrives through a portal, stands
 * there working, its work finishes, it leaves by one, and after a beat of empty band
 * it starts again.
 *
 * The two moments are only half the question anybody actually has, which is "what will
 * this look like on my desktop". Side-by-side strips answer "is the move good"; this
 * answers "is the whole thing good", including the part where nothing is happening —
 * and it is the part a reviewer cannot picture from a filmstrip.
 */
function Lifecycle({ cast, status }: { cast: CastMember; status: SessionStatus }) {
	const { phase, cycle } = useLifecycle();
	const transit = phase === "arriving" || phase === "leaving" ? phase : null;
	const runFor = transit ? portalDurationMs(transit) : 0;

	return (
		<figure style={FIGURE}>
			<div style={{ ...STAGE, width: 200 }}>
				<div className="companion-proc" style={{ ...STAND_VARS, position: "relative" }}>
					{/* Keyed by the cycle AND the phase, so every repeat is a new element whose
					    animations start again rather than being skipped as unchanged. */}
					{transit ? <Portal key={`${cycle}-${transit}`} phase={transit} durationMs={runFor} /> : null}
					{phase === "gone" ? null : transit ? (
						<PortalTransit key={`${cycle}-${transit}-leap`} phase={transit} durationMs={runFor}>
							<Procs
								cast={cast}
								status={status}
								facing="front"
								walking={false}
								travelling
								size={PET_HEIGHT}
								className="companion-proc-art"
							/>
						</PortalTransit>
					) : (
						<Procs
							cast={cast}
							status={status}
							facing="front"
							walking={false}
							size={PET_HEIGHT}
							className="companion-proc-art"
						/>
					)}
					{phase === "gone" ? null : (
						<div className="companion-proc-name">
							<NameTag name="fix the flaky test" />
						</div>
					)}
				</div>
			</div>
			<figcaption style={CAPTION}>{LIFECYCLE_CAPTIONS[phase]}</figcaption>
		</figure>
	);
}

type LifecyclePhase = "arriving" | "working" | "leaving" | "gone";

const LIFECYCLE_CAPTIONS: Record<LifecyclePhase, string> = {
	arriving: "▸ the worker was spawned",
	working: "· running, like any other pet",
	leaving: "▸ the work is finished",
	gone: "· the band, without it",
};

/** Two creatures, so the loop is not one body's quirk. One of them anchored. */
const LIFECYCLE_CAST: Array<{ species: SpeciesId; status: SessionStatus }> = [
	{ species: "proc", status: "working" },
	{ species: "cat", status: "idle" },
];

/** How long the pet just stands there between the two moments. */
const LIFECYCLE_WORK_MS = 2_600;
/** And how long the band stays empty before it starts over, so the loop has a seam. */
const LIFECYCLE_GAP_MS = 1_100;

/** The loop's clock, as a phase and which repeat we are on. */
function useLifecycle(): { phase: LifecyclePhase; cycle: number } {
	const [state, setState] = useState<{ phase: LifecyclePhase; cycle: number }>({ phase: "arriving", cycle: 0 });

	useEffect(() => {
		const next: Record<LifecyclePhase, { phase: LifecyclePhase; after: number }> = {
			arriving: { phase: "working", after: PORTAL_IN_MS },
			working: { phase: "leaving", after: LIFECYCLE_WORK_MS },
			leaving: { phase: "gone", after: PORTAL_OUT_MS },
			gone: { phase: "arriving", after: LIFECYCLE_GAP_MS },
		};
		const step = next[state.phase];
		const timer = setTimeout(
			() => setState((current) => ({ phase: step.phase, cycle: current.cycle + (step.phase === "arriving" ? 1 : 0) })),
			step.after,
		);
		return () => clearTimeout(timer);
	}, [state]);

	return state;
}

/** One pet arriving or leaving, drawn exactly as the overlay lays a pet out. */
function PortalStage({
	cast,
	phase,
	beat,
	durationMs,
	opacity,
	freezeAt,
	narrow,
	status = "working",
}: {
	cast: CastMember;
	phase: PortalPhase;
	beat: number;
	durationMs?: number;
	opacity?: number;
	/** 0-1 through the move: hold every animation at that instant instead of playing. */
	freezeAt?: number;
	narrow?: boolean;
	status?: SessionStatus;
}) {
	const runFor = durationMs ?? portalDurationMs(phase);
	const frozen =
		freezeAt === undefined ? null : { ["--procs-portal-freeze" as string]: `-${Math.round(freezeAt * runFor)}ms` };
	return (
		<div style={{ ...STAGE, ...(narrow ? NARROW : null), ...frozen }} data-portal-freeze={frozen ? "" : undefined}>
			{/* Keyed by the beat, so each replay is a NEW element and its animations start
			    again rather than being skipped as unchanged — the same trick the dust puff
			    and the rally ring are keyed by. */}
			<div key={beat} className="companion-proc" style={{ ...STAND_VARS, position: "relative" }}>
				<Portal phase={phase} durationMs={runFor} />
				<PortalTransit phase={phase} durationMs={runFor} opacity={opacity}>
					<Procs
						cast={cast}
						status={status}
						facing="front"
						walking={false}
						travelling
						size={PET_HEIGHT}
						className="companion-proc-art"
					/>
				</PortalTransit>
				<PortalLabel phase={phase} durationMs={runFor} opacity={opacity}>
					<div className="companion-proc-name">
						<NameTag name="login rate limit" />
					</div>
				</PortalLabel>
			</div>
		</div>
	);
}

/**
 * The replay clock.
 *
 * `beat` re-keys every cell together — every portal on the sheet plays in step, which
 * is what makes three variants comparable at all. `elapsed` is how far into the
 * current beat we are, and exists for one reason: the reduced-motion strip has no
 * animation to drive it, so its fade is computed from progress exactly the way the
 * engine will compute it.
 */
function usePortalLoop() {
	const [beat, setBeat] = useState(0);
	const [elapsed, setElapsed] = useState(0);
	const startedAt = useRef(0);

	useEffect(() => {
		let frame = 0;
		let start = performance.now();
		startedAt.current = start;
		const step = (now: number) => {
			if (now - start >= LOOP_MS) {
				start = now;
				startedAt.current = now;
				setBeat((n) => n + 1);
			}
			setElapsed(now - start);
			frame = requestAnimationFrame(step);
		};
		frame = requestAnimationFrame(step);
		return () => cancelAnimationFrame(frame);
	}, []);

	return {
		beat,
		elapsed,
		replay: () => {
			startedAt.current = performance.now();
			setBeat((n) => n + 1);
		},
	};
}

/** A demo look: this creature, one of its own colours, one of its own accessories. */
function look(species: SpeciesId, index: number): CastMember {
	const colours = palettesFor(species);
	const worn = accessoriesFor(species);
	return composeCast(colours[(index * 2 + 1) % colours.length], HATS[0], species, worn[index % worn.length].id);
}

/**
 * The four measurements the overlay hands every pet.
 *
 * Set here too, and from the SAME functions, so a portal on the concept sheet sits on
 * its pet exactly as it will on a desktop — a sheet that lays its own art out by eye
 * proves nothing about the thing it is standing in for.
 */
const FRAME = petFrame(PET_HEIGHT);
const STAND_VARS: React.CSSProperties = {
	["--procs-offset-x" as string]: `${FRAME.offsetX}px`,
	["--procs-figure-width" as string]: `${FRAME.figureWidth}px`,
	["--procs-figure-left" as string]: `${figureLeft(false)}px`,
	["--procs-name-room" as string]: `${NAME_TAG_ALLOWANCE}px`,
	["--procs-figure-height" as string]: `${PET_HEIGHT}px`,
};

const SHEET: React.CSSProperties = { display: "grid", gap: 10, padding: 14 };
const HEAD: React.CSSProperties = { display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap" };
const TITLE: React.CSSProperties = { font: "700 15px/1.3 ui-sans-serif, system-ui", margin: 0 };
const SUBTITLE: React.CSSProperties = {
	font: "600 12px/1.4 ui-sans-serif, system-ui",
	margin: "6px 0 0",
	opacity: 0.8,
};
const STRIP: React.CSSProperties = {
	display: "flex",
	flexWrap: "wrap",
	gap: 8,
	padding: 10,
	borderRadius: 10,
	alignItems: "flex-end",
};
const FIGURE: React.CSSProperties = { margin: 0, display: "grid", justifyItems: "center" };
/** A filmstrip frame: as tall as a stage, only as wide as the figure needs. */
const NARROW: React.CSSProperties = { width: 148 };
/** Tall enough that the leap's apex is inside the cell rather than clipped by it. */
const STAGE: React.CSSProperties = {
	position: "relative",
	width: 196,
	height: 236,
	display: "flex",
	alignItems: "flex-end",
	justifyContent: "center",
};
const CAPTION: React.CSSProperties = {
	marginTop: 6,
	padding: "2px 6px",
	borderRadius: 5,
	background: "rgba(16,13,24,0.78)",
	color: "#f3f0f8",
	font: "600 10px/1.4 ui-sans-serif, system-ui",
	letterSpacing: "0.03em",
	textAlign: "center",
};
const NOTE: React.CSSProperties = { margin: 0, opacity: 0.66 };
const MUTED: React.CSSProperties = { opacity: 0.6 };
const BUTTON: React.CSSProperties = {
	background: "#241f31",
	color: "inherit",
	border: "1px solid #3a3448",
	borderRadius: 6,
	padding: "3px 8px",
	font: "inherit",
	cursor: "pointer",
};
