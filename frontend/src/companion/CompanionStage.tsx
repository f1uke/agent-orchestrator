import { useEffect, useMemo, useState } from "react";
import { createWorld, syncActivities, tick, type Band, type Pet, type World } from "./behaviour";
import { castForSession } from "./cast";
import type { CompanionFeed } from "./feed";
import { createMockFeed } from "./mock-feed";
import { procsFrame, Procs } from "./Procs";

// The stage: the only stateful part of the overlay renderer. It owns a World,
// advances it on a slow tick, and paints each Proc with a `transform` — the engine
// hands out destinations, CSS interpolates between them on the compositor. Nothing
// here re-implements a behaviour rule; every decision comes from behaviour.ts.

/** Drawn Proc height. `full` tier from the design's size rules. */
const PET_HEIGHT = 128;
/** The drawn frame: the figure's own width plus whatever its scene hangs either side. */
const FRAME = procsFrame(PET_HEIGHT);
/**
 * How far the turnaround sits in from the screen edge. A Proc turns here rather
 * than at the edge, so it never half-exits the display.
 */
const EDGE_INSET = 28;
/**
 * The decision tick. Deliberately slow: walking is a rare event on a 45-150s
 * clock, so polling it faster would burn CPU to learn nothing. Between ticks the
 * compositor is doing all the work.
 */
const TICK_MS = 500;

export type CompanionStageProps = {
	feed?: CompanionFeed;
	/** Called with true while the pointer is over a Proc, so the shell can take clicks. */
	onInteractiveChange?: (interactive: boolean) => void;
};

// The band is inset by the SCENE's overhang, not just the figure's: a Proc parked
// at the right edge with a desk beside it would otherwise have half its desk off
// the display, and the desk is how that Proc says what it is doing.
function bandFor(width: number): Band {
	const left = EDGE_INSET - FRAME.offsetX;
	const right = width - EDGE_INSET - FRAME.figureWidth - FRAME.overhangRight;
	const maxX = Math.max(left, right);
	return { minX: Math.min(left, maxX), maxX };
}

function prefersReducedMotion(): boolean {
	return typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

export function CompanionStage({ feed, onInteractiveChange }: CompanionStageProps) {
	const source = useMemo(() => feed ?? createMockFeed(), [feed]);
	// Every effect below reaches the latest world through the functional setter, so
	// the interval and listeners are installed once instead of being torn down and
	// rebuilt on every state change.
	const [world, setWorld] = useState<World>(() => createWorld(bandFor(window.innerWidth)));

	useEffect(() => {
		return source.subscribe((activities) => {
			setWorld((current) => syncActivities(current, activities, Date.now(), Math.random));
		});
	}, [source]);

	useEffect(() => {
		const timer = setInterval(() => {
			setWorld((current) => tick(current, Date.now(), Math.random));
		}, TICK_MS);
		return () => clearInterval(timer);
	}, []);

	// Reduced motion and parking are inputs to the engine, not renderer special
	// cases: that is what keeps "no walking" true rather than merely invisible.
	useEffect(() => {
		const apply = () =>
			setWorld((current) => ({
				...current,
				reducedMotion: prefersReducedMotion(),
				parked: document.visibilityState === "hidden",
			}));
		apply();
		const media = window.matchMedia?.("(prefers-reduced-motion: reduce)");
		media?.addEventListener?.("change", apply);
		document.addEventListener("visibilitychange", apply);
		return () => {
			media?.removeEventListener?.("change", apply);
			document.removeEventListener("visibilitychange", apply);
		};
	}, []);

	useEffect(() => {
		const onResize = () => setWorld((current) => ({ ...current, band: bandFor(window.innerWidth) }));
		window.addEventListener("resize", onResize);
		return () => window.removeEventListener("resize", onResize);
	}, []);

	return (
		<div className="companion-stage">
			{world.pets.map((pet) => (
				<ProcOnStage key={pet.id} pet={pet} onInteractiveChange={onInteractiveChange} />
			))}
		</div>
	);
}

function ProcOnStage({ pet, onInteractiveChange }: { pet: Pet; onInteractiveChange?: (interactive: boolean) => void }) {
	// The character is a stable function of the session ref, so the same worker is
	// always the same Proc — that is what lets someone learn to recognise it.
	const cast = castForSession(pet.id);
	const walking = pet.motion.kind === "walking";
	// While walking, paint at the DESTINATION and let the transition carry the Proc
	// there over exactly the walk's duration; the engine sets `x` to the same value
	// when the walk ends, so there is never a jump at the hand-off.
	const targetX = pet.motion.kind === "walking" ? pet.motion.toX : pet.x;
	const durationMs = pet.motion.kind === "walking" ? pet.motion.endsAt - pet.motion.startedAt : 0;

	return (
		<div
			data-proc
			data-session={pet.id}
			className="companion-proc"
			style={{
				transform: `translate3d(${targetX}px, 0px, 0px)`,
				transitionProperty: "transform",
				transitionTimingFunction: "linear",
				transitionDuration: `${durationMs}ms`,
				["--procs-offset-x" as string]: `${FRAME.offsetX}px`,
			}}
			onPointerEnter={() => onInteractiveChange?.(true)}
			onPointerLeave={() => onInteractiveChange?.(false)}
		>
			<Procs
				cast={cast}
				status={pet.status}
				facing={pet.facing}
				walking={walking}
				size={PET_HEIGHT}
				className="companion-proc-art"
			/>
		</div>
	);
}
