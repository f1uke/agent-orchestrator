import { useEffect, useMemo, useState } from "react";
import {
	createWorld,
	dragPet,
	grabPet,
	releasePet,
	startConversation,
	syncActivities,
	tick,
	type Band,
	type Pet,
	type World,
} from "./behaviour";
import { Bubble } from "./Bubble";
import type { ComposedBubble } from "./bubble-compose";
import { castForSession } from "./cast";
import { hoverAt, idleHover, tooltipTarget, type HoverState } from "./hover";
import { NameTag, PetTooltip } from "./NameTag";
import { createInteractionTracker, isOverPet } from "./pointer-region";
import type { CompanionFeed } from "./feed";
import { createMockFeed } from "./mock-feed";
import { assignBubbleLanes, type BubbleSpan } from "./bubble-lanes";
import { BUBBLE_MAX_WIDTH } from "./Bubble";
import { BUBBLE_LANE_HEIGHT, bubbleOpensLeft, figureLeft, NAME_TAG_ALLOWANCE, PET_HEIGHT, petFrame } from "./layout";
import { Procs } from "./Procs";
import { stackOrder } from "./stacking";

// The stage: the only stateful part of the overlay renderer. It owns a World,
// advances it on a slow tick, and paints each Proc with a `transform` — the engine
// hands out destinations, CSS interpolates between them on the compositor. Nothing
// here re-implements a behaviour rule; every decision comes from behaviour.ts.

/** The drawn frame: the figure's own width plus whatever its scene hangs either side. */
const FRAME = petFrame(PET_HEIGHT);
/**
 * How far the turnaround sits in from the screen edge. A Proc turns here rather
 * than at the edge, so it never half-exits the display.
 */
const EDGE_INSET = 28;
/** Breathing room between two Procs standing on the band, on top of their drawn width. */
const PET_GAP = 8;
/**
 * The decision tick. Deliberately slow: walking is a rare event on a 45-150s
 * clock, so polling it faster would burn CPU to learn nothing. Between ticks the
 * compositor is doing all the work.
 */
const TICK_MS = 500;

export type CompanionStageProps = {
	feed?: CompanionFeed;
	/**
	 * What each Proc may honestly say right now. Re-read on every tick rather than
	 * pushed, because a claim expires on the CLOCK — silence from the feed means a
	 * line has run out, not that it is still true.
	 */
	bubbleFor?: (sessionId: string) => ComposedBubble | null;
	/** Called with true while the pointer is over a Proc, so the shell can take clicks. */
	onInteractiveChange?: (interactive: boolean) => void;
	/**
	 * Override `prefers-reduced-motion`. Only the dev playground passes it: the
	 * reduced-motion path is a real behaviour with its own rules, and it is not
	 * reachable by eye without changing an OS setting mid-session.
	 */
	reducedMotion?: boolean;
	/**
	 * Hands the world setter out once, for the dev playground. The overlay itself
	 * never passes this — nothing outside the engine may move a Proc in production,
	 * which is exactly why the seam is explicit rather than a mutable export.
	 */
	onStage?: (api: { setWorld: React.Dispatch<React.SetStateAction<World>> }) => void;
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

export function CompanionStage({ feed, bubbleFor, onInteractiveChange, reducedMotion, onStage }: CompanionStageProps) {
	const source = useMemo(() => feed ?? createMockFeed(), [feed]);
	// Every effect below reaches the latest world through the functional setter, so
	// the interval and listeners are installed once instead of being torn down and
	// rebuilt on every state change.
	// Bumped by the same tick the engine runs on, so an expiring bubble re-renders
	// itself away without needing an event to tell it to.
	const [bubbleTick, setBubbleTick] = useState(0);
	const [world, setWorld] = useState<World>(() => ({
		...createWorld(bandFor(window.innerWidth)),
		// Clearance is the whole DRAWN FRAME, not just the figure. Spacing them by the
		// body alone still let one Proc's crate sit on the next Proc's face, and let a
		// cord drape across a neighbour — the scenery is how a Proc says what it is
		// doing, so it needs its own room as much as the body does.
		spacing: FRAME.width + PET_GAP,
	}));

	useEffect(() => {
		return source.subscribe((activities) => {
			setWorld((current) => syncActivities(current, activities, Date.now(), Math.random));
		});
	}, [source]);

	// An `ao send` between two sessions is the one event on this desktop that is
	// about a RELATIONSHIP rather than about one session, so it is the one time two
	// Procs act together. The engine decides whether it can actually be staged.
	useEffect(() => {
		return source.conversations?.(({ from, to, line }) => {
			setWorld((current) => startConversation(current, { from, to, line, now: Date.now() }));
		});
	}, [source]);

	// Re-read on the same slow tick the engine runs on, so the tooltip opens without
	// a timer of its own.
	const [hover, setHover] = useState<HoverState>(idleHover);
	const [openTooltip, setOpenTooltip] = useState<string | null>(null);
	useEffect(() => {
		const timer = setInterval(() => setOpenTooltip(tooltipTarget(hover, Date.now())), 250);
		return () => clearInterval(timer);
	}, [hover]);

	useEffect(() => {
		const timer = setInterval(() => {
			setWorld((current) => tick(current, Date.now(), Math.random));
			setBubbleTick((n) => n + 1);
		}, TICK_MS);
		return () => clearInterval(timer);
	}, []);

	// Reduced motion and parking are inputs to the engine, not renderer special
	// cases: that is what keeps "no walking" true rather than merely invisible.
	useEffect(() => {
		const apply = () =>
			setWorld((current) => ({
				...current,
				reducedMotion: reducedMotion ?? prefersReducedMotion(),
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
	}, [reducedMotion]);

	useEffect(() => onStage?.({ setWorld }), [onStage]);

	useEffect(() => {
		const onResize = () => setWorld((current) => ({ ...current, band: bandFor(window.innerWidth) }));
		window.addEventListener("resize", onResize);
		return () => window.removeEventListener("resize", onResize);
	}, []);

	// Click-through is decided per POINTER MOVE against what is actually under the
	// pointer, not by enter/leave handlers on each Proc. Enter/leave can be missed
	// entirely — flick the pointer off the bottom of the screen and no leave ever
	// arrives — and a missed leave leaves the whole band swallowing clicks.
	useEffect(() => {
		const tracker = createInteractionTracker(onInteractiveChange ?? (() => {}));
		// The pointer's x in band coordinates: the stage fills the window, and a Proc's
		// transform is its own left edge, so a grab keeps the offset it was grabbed at
		// instead of snapping the Proc's corner to the cursor.
		let grabOffset = 0;

		const petIdAt = (target: EventTarget | null): string | null => {
			if (!(target instanceof Element)) return null;
			return target.closest("[data-proc]")?.getAttribute("data-session") ?? null;
		};

		const onMove = (event: PointerEvent) => {
			tracker.update(event.target);
			setWorld((current) => {
				const holding = current.pets.find((pet) => pet.motion.kind === "held");
				if (holding) return dragPet(current, holding.id, event.clientX - grabOffset);
				return current;
			});
			const over = isOverPet(event.target) ? petIdAt(event.target) : null;
			setHover((current) => hoverAt(current, over, Date.now()));
		};

		const onDown = (event: PointerEvent) => {
			tracker.update(event.target);
			if (!isOverPet(event.target)) return;
			const id = petIdAt(event.target);
			if (!id) return;
			// Keep the pointer for the whole gesture: a drag pulls it off the Proc
			// constantly, and reverting to click-through mid-drag would hand the rest of
			// it to the desktop.
			tracker.hold(true);
			setWorld((current) => {
				const pet = current.pets.find((entry) => entry.id === id);
				grabOffset = pet ? event.clientX - pet.x : 0;
				return grabPet(current, id, Date.now());
			});
			setHover(idleHover());
		};

		const onUp = (event: PointerEvent) => {
			tracker.hold(false);
			setWorld((current) => {
				const holding = current.pets.find((pet) => pet.motion.kind === "held");
				return holding ? releasePet(current, holding.id, Date.now(), Math.random) : current;
			});
			tracker.update(event.target);
		};

		const onOut = () => {
			tracker.release();
			setHover(idleHover());
		};

		document.addEventListener("pointermove", onMove, true);
		document.addEventListener("pointerdown", onDown, true);
		document.addEventListener("pointerup", onUp, true);
		document.addEventListener("pointercancel", onUp, true);
		document.addEventListener("pointerleave", onOut, true);
		window.addEventListener("blur", onOut);
		return () => {
			document.removeEventListener("pointermove", onMove, true);
			document.removeEventListener("pointerdown", onDown, true);
			document.removeEventListener("pointerup", onUp, true);
			document.removeEventListener("pointercancel", onUp, true);
			document.removeEventListener("pointerleave", onOut, true);
			window.removeEventListener("blur", onOut);
			tracker.release();
		};
	}, [onInteractiveChange]);

	const painted = paintOrder(world.pets);
	// Which bubbles would collide, and how high each has to sit to clear the ones
	// before it. Computed once for the whole band — a bubble cannot know on its own
	// whether it is in anybody's way.
	const lanes = assignBubbleLanes(
		painted.filter((pet) => spokenLine(pet, bubbleFor?.(pet.id) ?? null) !== null).map((pet) => bubbleSpan(pet)),
	);

	// TWO layers, deliberately. Every Proc carries a `transform`, and a transform
	// makes an element a stacking context — so a bubble drawn INSIDE its Proc can
	// never rise above the Proc next door however its z-index is set, and a
	// neighbour standing to the right was painting straight over what it was
	// saying. Characters below, the things they are SAYING above all of them.
	return (
		<div className="companion-stage">
			<div className="companion-cast">
				{painted.map((pet) => (
					<ProcArt key={pet.id} pet={pet} />
				))}
			</div>
			<div className="companion-chrome">
				{painted.map((pet) => (
					<ProcChrome
						key={pet.id}
						pet={pet}
						tooltip={openTooltip === pet.id}
						bubble={bubbleFor?.(pet.id) ?? null}
						lane={lanes.get(pet.id) ?? 0}
						bubbleTick={bubbleTick}
					/>
				))}
			</div>
		</div>
	);
}

/**
 * Right to left, so the LEFTMOST Proc is painted last and therefore on top.
 *
 * Only decides character-over-character now — the speech is in its own layer — but
 * it still matters: a Proc's ground prop and cord hang to its right, so the one on
 * the left is the one that should be in front of them.
 */
function paintOrder(pets: Pet[]): Pet[] {
	return [...pets].sort((a, b) => b.x - a.x);
}

/** Everything about where a Proc IS: the same transform for its art and its chrome. */
function placement(pet: Pet): React.CSSProperties {
	// While walking, paint at the DESTINATION and let the transition carry the Proc
	// there over exactly the walk's duration; the engine sets `x` to the same value
	// when the walk ends, so there is never a jump at the hand-off.
	const targetX = pet.motion.kind === "walking" ? pet.motion.toX : pet.x;
	// A held Proc must track the pointer exactly, so no transition on the drag.
	const durationMs = pet.motion.kind === "walking" ? pet.motion.endsAt - pet.motion.startedAt : 0;
	return {
		transform: `translate3d(${targetX}px, 0px, 0px)`,
		transitionProperty: "transform",
		transitionTimingFunction: "linear",
		transitionDuration: `${durationMs}ms`,
		["--procs-offset-x" as string]: `${FRAME.offsetX}px`,
		["--procs-figure-width" as string]: `${FRAME.figureWidth}px`,
		// Mirroring the sprite to walk left moves the figure across its own frame, and
		// the chrome pinned to it does not mirror — so it is told where the figure went.
		["--procs-figure-left" as string]: `${figureLeft(pet.facing === "left")}px`,
		["--procs-name-room" as string]: `${NAME_TAG_ALLOWANCE}px`,
		["--procs-frame-height" as string]: `${FRAME.height}px`,
	};
}

/**
 * What a Proc is saying right now, or null.
 *
 * A Proc that is mid-greeting and HAS a line speaks it — that is the sender,
 * dramatising the `ao send`. Everyone else, including the Proc being told, shows
 * whatever the feed says it has, which for the recipient is the message itself,
 * attributed to whoever sent it. Both ends keep a card; where two cards collide
 * they stack (see `bubble-lanes.ts`) rather than one of them being hidden.
 */
function spokenLine(pet: Pet, bubble: ComposedBubble | null): ComposedBubble | null {
	if (pet.meeting?.phase === "greeting" && pet.meeting.line) {
		return { text: pet.meeting.line, tone: "normal", decay: "fresh" };
	}
	return bubble;
}

function ProcArt({ pet }: { pet: Pet }) {
	// The character is a stable function of the session ref, so the same worker is
	// always the same Proc — that is what lets someone learn to recognise it.
	const cast = castForSession(pet.id);
	const walking = pet.motion.kind === "walking";
	const held = pet.motion.kind === "held";
	const greeting = pet.meeting?.phase === "greeting";
	// A Proc on its way to (or back from) a meeting is running, not strolling.
	const running = walking && pet.meeting !== undefined;

	return (
		<div
			data-proc
			data-session={pet.id}
			className="companion-proc"
			style={{ ...placement(pet), zIndex: stackOrder(pet) }}
		>
			<Procs
				cast={cast}
				status={pet.status}
				facing={pet.facing}
				walking={walking}
				held={held}
				running={running}
				greeting={greeting}
				size={PET_HEIGHT}
				className="companion-proc-art"
			/>
			<div className="companion-proc-name">
				<NameTag name={pet.name} lead={pet.kind === "orchestrator"} />
			</div>
		</div>
	);
}

/** Where this Proc's widest possible card would sit, in screen px. */
function bubbleSpan(pet: Pet): BubbleSpan {
	const figureX = (pet.motion.kind === "walking" ? pet.motion.toX : pet.x) + figureLeft(pet.facing === "left");
	const opensLeft = bubbleOpensLeft({
		figureX,
		figureWidth: FRAME.figureWidth,
		screenWidth: window.innerWidth,
		preferLeft: pet.meeting?.phase === "greeting" && pet.facing === "right",
	});
	const left = opensLeft ? figureX + FRAME.figureWidth - BUBBLE_MAX_WIDTH : figureX;
	return { id: pet.id, left, right: left + BUBBLE_MAX_WIDTH };
}

function ProcChrome({
	pet,
	tooltip,
	bubble,
	lane,
}: {
	pet: Pet;
	tooltip: boolean;
	bubble: ComposedBubble | null;
	/** How many card-heights above its Proc this bubble sits, to clear the others. */
	lane: number;
	/** Only here to re-render the bubble as its claim ages. */
	bubbleTick: number;
}) {
	const held = pet.motion.kind === "held";
	const greeting = pet.meeting?.phase === "greeting";
	const said = spokenLine(pet, bubble);

	const targetX = pet.motion.kind === "walking" ? pet.motion.toX : pet.x;
	// Face to face, the two cards would open the same way and the one on the left
	// would be laid straight across the one on the right. Each opens AWAY from the
	// Proc it is talking to instead — unless that would put it off the screen.
	const opensLeft = bubbleOpensLeft({
		figureX: targetX + figureLeft(pet.facing === "left"),
		figureWidth: FRAME.figureWidth,
		screenWidth: window.innerWidth,
		preferLeft: greeting && pet.facing === "right",
	});

	// The wrapper is ALWAYS mounted, even with nothing in it. Mounting it only when
	// there is something to say meant it appeared already at the destination of a
	// walk already in progress — no previous transform to transition FROM — so a
	// bubble hung in the air at the meeting spot while its Proc was still running
	// towards it. Always mounted, it carries exactly the same transform history as
	// the art and travels with it.
	return (
		<div className="companion-proc-chrome" style={placement(pet)}>
			{/* The tooltip wins the space when it is open: it is a deliberate request
			    for detail, and two cards over one Proc would collide. */}
			{said && !held && !tooltip ? (
				<div
					className="companion-proc-bubble"
					data-opens={opensLeft ? "left" : undefined}
					data-lane={lane || undefined}
					style={{ ["--procs-bubble-lane" as string]: `${lane * BUBBLE_LANE_HEIGHT}px` }}
				>
					<Bubble text={said.text} tone={said.tone} decay={said.decay} tail={opensLeft ? "right" : "left"} />
				</div>
			) : null}
			{tooltip && !held ? (
				<div className="companion-proc-tooltip">
					<PetTooltip
						name={pet.name}
						sessionId={pet.id}
						project={pet.project}
						status={pet.status}
						lead={pet.kind === "orchestrator"}
					/>
				</div>
			) : null}
		</div>
	);
}
