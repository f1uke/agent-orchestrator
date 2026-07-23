import { useEffect, useMemo, useRef, useState } from "react";
import {
	createWorld,
	dragPet,
	drawnX,
	grabPet,
	releasePet,
	startConversation,
	startRally,
	advanceFlight,
	advanceTransits,
	syncActivities,
	tick,
	type Band,
	type Pet,
	type World,
} from "./behaviour";
import { isShaking, newShakeTrack, trackShake } from "./shake";
import { Bubble } from "./Bubble";
import type { ComposedBubble } from "./bubble-compose";
import { castForSession, withSpecies, type CastMember } from "./cast";
import { resolveSpecies } from "./look-store";
import { useProjectLooks } from "./look-store-live";
import { hoverAt, HOVER_TOOLTIP_DELAY_MS, idleHover, tooltipTarget, type HoverState } from "./hover";
import { NameTag, PetTooltip } from "./NameTag";
import { Portal, PortalLabel, PortalTransit } from "./Portal";
import { transitOpacity } from "./portal-transit";
import { createInteractionTracker, isOverPet } from "./pointer-region";
import type { CompanionFeed } from "./feed";
import { createMockFeed } from "./mock-feed";
import { stackBubbles, type BubbleBox } from "./bubble-lanes";
import { BUBBLE_MAX_HEIGHT, BUBBLE_MAX_WIDTH } from "./Bubble";
import { bubbleOpensLeft, figureLeft, NAME_TAG_ALLOWANCE, PET_HEIGHT, petFrame } from "./layout";
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

/**
 * How far a press may travel and still count as a CLICK rather than a drag.
 *
 * 5px is the app's existing answer — the split view's drag layer uses the same
 * number to stop a click on a pane header turning into a drag — and it is about
 * the wobble a hand puts into a deliberate click on a 30px figure.
 */
export const CLICK_SLOP_PX = 5;

/**
 * How long a press may be HELD and still count as a click.
 *
 * Distance alone is not enough here, because a Proc is a thing you pick up: press
 * and hold without moving and the pet is in your hand, being held, which is a
 * gesture with its own meaning. Letting go of that after a second should put the
 * pet down, not open a terminal. 400ms is the usual "this was a tap, not a hold"
 * boundary (macOS's own press-and-hold threshold is in this range).
 */
export const CLICK_HOLD_MS = 400;

/**
 * How often the overlay re-asks who owns the pointer while the pointer is still.
 *
 * Ten times a second: a hit test is cheap, the answer only crosses IPC when it
 * changes, and a Proc that has walked under a resting cursor becomes clickable
 * within a frame or two rather than "when you jiggle the mouse".
 */
export const POINTER_REVALIDATE_MS = 100;

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
	 * Right-click on a Proc: "let me change how this one looks".
	 *
	 * A RIGHT click, specifically. Press-drag is `grabPet` and hover opens the name
	 * card, so the two obvious alternatives are both taken: a double-click is two
	 * whole press/release pairs, which grabs, drops, grabs and drops the pet before
	 * the gesture is even recognised, and a control drawn on the sprite is a new hit
	 * target on a 30px figure sitting on a band that must stay click-through. A
	 * different mouse BUTTON cannot race the drag at all, and it is what the platform
	 * already means by "options for this thing".
	 */
	onRequestLook?: (sessionId: string) => void;
	/**
	 * PROTOTYPE (terminal bubble). A CLEAN LEFT CLICK on a Proc: "let me talk to
	 * this session". Fired on release, and only when the press never became a drag.
	 *
	 * The split has to be made here rather than by a `click` handler, because a
	 * press on a Proc is ALREADY a gesture: it picks the Proc up, and letting go
	 * throws it at the speed of the hand. `click` fires after a throw too — the DOM
	 * has no idea a drag happened — so the two would collide on every flick. The
	 * rule is the same one the drag layer of the split view settled on: a press that
	 * moved less than a few pixels was never a drag, and a press that moved is never
	 * a click. A shake (which calls a rally) is a drag by construction and so can
	 * never end in a click either.
	 */
	onActivate?: (sessionId: string, at: { x: number; y: number }) => void;
	/**
	 * PROTOTYPE (terminal bubble): whose terminal is open.
	 *
	 * The terminal is a WINDOW, not something drawn here — putting it in the
	 * overlay meant making the overlay focusable, and that cost the band its mouse
	 * events and made every Proc blink out for a beat (main/terminal-window.ts).
	 * What the stage still owns is where that window belongs: only it knows where a
	 * Proc actually IS, because a walking Proc's `x` is where it set off from and
	 * the compositor is carrying the rest.
	 */
	attachedSession?: string;
	/** Where the attached Proc is now, in this window's coordinates. */
	onAttachedAnchorMove?: (anchor: { x: number; y: number }) => void;
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
	/**
	 * What a session LOOKS like, overriding the stored-choices-over-hash resolution.
	 *
	 * Only the Procs lab passes it, to switch the whole band between creatures so the
	 * new bodies can be watched walking, talking and being picked up rather than only
	 * standing on a contact sheet. With it absent — which is every real overlay — the
	 * look is the human's choice over the session's hash, exactly as before.
	 */
	castFor?: (sessionId: string, project?: string) => CastMember;
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

export function CompanionStage({
	feed,
	bubbleFor,
	onInteractiveChange,
	onRequestLook,
	onActivate,
	attachedSession,
	onAttachedAnchorMove,
	reducedMotion,
	onStage,
	castFor: castForOverride,
}: CompanionStageProps) {
	const source = useMemo(() => feed ?? createMockFeed(), [feed]);
	// The CREATURE is the only part anybody chooses, and it is chosen per PROJECT. Both
	// windows read the same localStorage key, so a choice made in Settings lands here on
	// the `storage` event with nothing in between.
	const projectLooks = useProjectLooks();
	// Two questions, two answers. The COLOUR and the accessory are the hash of the session
	// ref — automatic, stable across restarts, and varied enough that two workers on one
	// project are tellable apart. The CREATURE comes from the PROJECT, so every session on
	// it is the same animal and the band groups itself by shape. That is what took the
	// coloured mark off the name chip — a mark has to be decoded, a creature is known by
	// the time you have noticed it.
	const resolveCast =
		castForOverride ??
		((sessionId: string, project?: string) =>
			withSpecies(castForSession(sessionId), resolveSpecies(project, projectLooks)));
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
		// The figure ALONE, which is what "shoulder to shoulder" is a fact about. The
		// gathered photo is the one pose that gives the scenery's room up on purpose.
		figureWidth: FRAME.figureWidth,
	}));

	useEffect(() => {
		// UN-SEEDED for this feed, so its first snapshot is a baseline rather than a
		// lifecycle event. The overlay swaps the mock cast for the live roster by handing
		// this component a different feed, and without this the swap reads as every mock
		// pet's session ending and every real one's beginning — a dozen portals for
		// nothing that happened. A reload gets the same protection from a fresh world.
		setWorld((current) => (current.seeded ? { ...current, seeded: false } : current));
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
		// A quarter of the hover delay, so the card opens within a frame or two of the
		// moment it is due rather than up to a poll late — at half a second's wait, a
		// 250ms poll was half the delay again.
		const timer = setInterval(() => setOpenTooltip(tooltipTarget(hover, Date.now())), HOVER_TOOLTIP_DELAY_MS / 4);
		return () => clearInterval(timer);
	}, [hover]);

	useEffect(() => {
		const timer = setInterval(() => {
			setWorld((current) => tick(current, Date.now(), Math.random));
			setBubbleTick((n) => n + 1);
		}, TICK_MS);
		return () => clearInterval(timer);
	}, []);

	// A thrown Proc travels an ARC, and a CSS transition draws a straight line
	// between two points — so the one thing on this desktop the compositor cannot
	// interpolate for us is drawn frame by frame instead. The loop runs only while
	// something is actually in the air, so an idle desktop still costs nothing.
	const flying = world.pets.some((pet) => pet.motion.kind === "flying");
	useEffect(() => {
		if (!flying) return;
		let frame = 0;
		let last = performance.now();
		const advance = (now: number) => {
			const dt = Math.min(48, now - last);
			last = now;
			setWorld((current) => advanceFlight(current, dt));
			frame = requestAnimationFrame(advance);
		};
		frame = requestAnimationFrame(advance);
		return () => cancelAnimationFrame(frame);
	}, [flying]);

	// A portal is short and it is on a clock, so it cannot be drawn on the engine's
	// 500ms tick: a reduced-motion transition is 260ms and would fall between two of
	// them entirely, and a pet whose exit had finished would linger up to half a second
	// after it. While anything is in transit the frame loop retires them instead — and
	// re-renders, which is what gives the reduced-motion fade a clock to read.
	const transiting = world.pets.some((pet) => pet.transit !== undefined);
	useEffect(() => {
		if (!transiting) return;
		let frame = 0;
		const advance = () => {
			setWorld((current) => advanceTransits(current, Date.now()));
			frame = requestAnimationFrame(advance);
		};
		frame = requestAnimationFrame(advance);
		return () => cancelAnimationFrame(frame);
	}, [transiting]);

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
		// The pointer's position in band coordinates: the stage fills the window, and a
		// Proc's transform is its own left edge, so a grab keeps the offset it was
		// grabbed at instead of snapping the Proc's corner to the cursor.
		let grabOffset = 0;
		let grabOffsetY = 0;
		// The last couple of pointer samples, so letting go of a FLICK can throw the
		// Proc at the speed the hand was actually moving.
		let sample: { x: number; y: number; at: number } | null = null;
		let throwSpeed = { vx: 0, vy: 0 };
		/**
		 * Which Proc the press went down on, and the shape the pointer has traced
		 * since. Reset on every press, so one gesture can never finish another's shake.
		 *
		 * Read from the DOM rather than from the world, and deliberately: a `setWorld`
		 * updater does not run until React renders, so anything learned inside one is
		 * not available to the pointer moves that arrive in the meantime — which is
		 * exactly the window a shake happens in. Whether this Proc may actually call a
		 * rally is the ENGINE's question, and `startRally` answers it.
		 */
		let grabbed: string | null = null;
		let shake = newShakeTrack();
		/**
		 * Where the pointer was last seen, so the window's click-through state can be
		 * re-decided WITHOUT a pointer event.
		 *
		 * It has to be re-decidable, because the pointer is not the only thing that
		 * moves: a Proc walks under a resting cursor, and a card closes out from under
		 * one. Deciding only on pointer events leaves the window in whatever state the
		 * last MOVE left it in — which is how a click after closing a terminal fell
		 * straight through to the desktop, and how a Proc that walked under a still
		 * cursor could not be clicked at all.
		 */
		let lastPointer: { x: number; y: number } | null = null;
		/**
		 * The press that is still eligible to be a CLICK, and stops being one the
		 * moment the hand moves past the slop. Kept beside `grabbed` rather than
		 * inside it because a press that has become a drag must still finish its
		 * drag — only its right to end in a click is withdrawn.
		 */
		let press: { id: string; x: number; y: number; at: number } | null = null;
		/** Height above the floor line, which is the bottom of the window. */
		const heightOf = (clientY: number) => window.innerHeight - clientY;

		const petIdAt = (target: EventTarget | null): string | null => {
			if (!(target instanceof Element)) return null;
			return target.closest("[data-proc]")?.getAttribute("data-session") ?? null;
		};

		/** Re-decide who owns the pointer from where it actually is right now. */
		const revalidate = () => {
			// jsdom has no layout and so no hit testing; there is nothing to re-decide
			// there, and the pointer-event paths above are what its tests drive.
			if (!lastPointer || typeof document.elementFromPoint !== "function") return;
			tracker.update(document.elementFromPoint(lastPointer.x, lastPointer.y));
		};

		const onMove = (event: PointerEvent) => {
			lastPointer = { x: event.clientX, y: event.clientY };
			tracker.update(event.target);
			// Press-and-shake is a COMMAND laid over the drag rather than instead of it:
			// the Proc goes on following the pointer throughout, because taking it out of
			// the hand mid-gesture reads as the app dropping the thing you are holding.
			if (press && Math.hypot(event.clientX - press.x, event.clientY - press.y) > CLICK_SLOP_PX) {
				press = null;
			}
			if (grabbed) {
				const leaderId = grabbed;
				shake = trackShake(shake, { x: event.clientX, y: event.clientY, at: event.timeStamp || performance.now() });
				if (isShaking(shake)) {
					// Cleared so the same wiggle cannot be counted twice. Shaking a Proc that
					// is not the coordinator simply asks and is refused, which costs nothing.
					shake = newShakeTrack();
					setWorld((current) => startRally(current, leaderId, Date.now()));
				}
			}
			setWorld((current) => {
				const holding = current.pets.find((pet) => pet.motion.kind === "held");
				if (!holding) return current;
				const now = performance.now();
				if (sample) {
					const dt = Math.max(1, now - sample.at);
					// Only a RECENT sample is a flick; an old one is where the hand paused.
					const weight = dt > 120 ? 0 : 1;
					throwSpeed = {
						vx: (weight * (event.clientX - sample.x)) / dt,
						vy: (weight * (sample.y - event.clientY)) / dt,
					};
				}
				sample = { x: event.clientX, y: event.clientY, at: now };
				return dragPet(current, holding.id, event.clientX - grabOffset, heightOf(event.clientY) - grabOffsetY);
			});
			const over = isOverPet(event.target) ? petIdAt(event.target) : null;
			setHover((current) => hoverAt(current, over, Date.now()));
		};

		const onDown = (event: PointerEvent) => {
			console.log(
				"[proto] pointerdown overPet=",
				isOverPet(event.target),
				"target=",
				(event.target as Element)?.tagName,
				Date.now() % 100000,
			);
			tracker.update(event.target);
			if (!isOverPet(event.target)) return;
			// LEFT button only. A right-press was never meant to pick a Proc up, and it
			// has to be genuinely free for the look gesture below: a right-click that
			// also grabbed would fling the pet while its menu opened.
			if (event.button !== 0) return;
			const id = petIdAt(event.target);
			if (!id) return;
			// Keep the pointer for the whole gesture: a drag pulls it off the Proc
			// constantly, and reverting to click-through mid-drag would hand the rest of
			// it to the desktop.
			tracker.hold(true);
			grabbed = id;
			press = { id, x: event.clientX, y: event.clientY, at: performance.now() };
			shake = trackShake(newShakeTrack(), {
				x: event.clientX,
				y: event.clientY,
				at: event.timeStamp || performance.now(),
			});
			setWorld((current) => {
				const pet = current.pets.find((entry) => entry.id === id);
				grabOffset = pet ? event.clientX - pet.x : 0;
				grabOffsetY = pet ? heightOf(event.clientY) - pet.y : 0;
				sample = { x: event.clientX, y: event.clientY, at: performance.now() };
				throwSpeed = { vx: 0, vy: 0 };
				return grabPet(current, id, Date.now());
			});
			setHover(idleHover());
		};

		const onUp = (event: PointerEvent) => {
			tracker.hold(false);
			const thrown = sample && performance.now() - sample.at < 120 ? throwSpeed : { vx: 0, vy: 0 };
			// A press that never moved and never lingered: the human clicked this Proc.
			// Read BEFORE the release below, so the pet is still the one that was held.
			const clicked = press && performance.now() - press.at <= CLICK_HOLD_MS ? press : null;
			press = null;
			sample = null;
			grabbed = null;
			shake = newShakeTrack();
			if (clicked) {
				onActivate?.(clicked.id, { x: clicked.x, y: clicked.y });
			}
			setWorld((current) => {
				const holding = current.pets.find((pet) => pet.motion.kind === "held");
				if (!holding) return current;
				// A Proc that has just been shaken into calling a rally is NOT being thrown.
				// The wrist speed that fired the rally is the same speed that would fling
				// it, so read literally every successful shake would end in a throw — and
				// then the two gestures are not distinct at all, whatever the detector says.
				const speed = holding.rally ? { vx: 0, vy: 0 } : thrown;
				return releasePet(current, holding.id, Date.now(), Math.random, speed);
			});
			tracker.update(event.target);
		};

		/**
		 * The pointer genuinely left the WINDOW: nothing here owns it any more.
		 *
		 * Bound WITHOUT capture, and that is the whole of it. `pointerleave` does not
		 * bubble, so a plain listener on the document fires only when the pointer
		 * leaves the document — which is the question being asked. With capture, the
		 * same listener also caught every leave from every element inside the page:
		 * each Proc walking out from under the cursor, each card closing, hundreds a
		 * minute. Every one of them said "the pointer has gone" and handed the desktop
		 * back, so the window's click-through state flapped constantly and a click
		 * landing in the wrong half of that flap fell through to the desktop.
		 */
		const onOut = () => {
			lastPointer = null;
			tracker.release();
			setHover(idleHover());
		};

		/**
		 * The WINDOW lost the keyboard — which says nothing about where the mouse is.
		 *
		 * This used to release the pointer outright, which was harmless while the
		 * overlay could never be focused at all. Now that a terminal borrows the
		 * keyboard and gives it back, `blur` fires in the middle of ordinary use, with
		 * the pointer sitting on a Proc — and releasing there made the window
		 * click-through under the cursor, so the next click went to the desktop behind
		 * instead of to the pet. Close the tooltip (it asserts a hover we can no longer
		 * be sure of) and re-decide the pointer from where the pointer actually is.
		 */
		const onWindowBlur = () => {
			setHover(idleHover());
			revalidate();
		};

		// The look gesture. It opens the library in the MAIN WINDOW rather than drawing
		// a menu here: this page is a transparent always-on-top band whose click-through
		// is decided per pointer move, and a popover living on it would have to pin the
		// window interactive for as long as it stayed open. One missed close and the
		// bottom of the user's screen stops taking clicks, which is the exact failure
		// `pointer-region.ts` exists to prevent.
		const onMenu = (event: MouseEvent) => {
			if (!isOverPet(event.target)) return;
			const id = petIdAt(event.target);
			if (!id) return;
			// Only once we know it landed on a Proc: elsewhere on the band the desktop
			// underneath owns the click, and eating its context menu would be theft.
			event.preventDefault();
			onRequestLook?.(id);
		};

		// The scene moves on its own — Procs walk, cards open and close — so the
		// question "is the pointer on something of ours" has to be asked on a clock as
		// well as on pointer events. It only crosses IPC when the ANSWER changes.
		const revalidateTimer = setInterval(revalidate, POINTER_REVALIDATE_MS);

		document.addEventListener("pointermove", onMove, true);
		document.addEventListener("pointerdown", onDown, true);
		document.addEventListener("pointerup", onUp, true);
		document.addEventListener("pointercancel", onUp, true);
		document.addEventListener("pointerleave", onOut);
		document.addEventListener("contextmenu", onMenu, true);
		window.addEventListener("blur", onWindowBlur);
		return () => {
			clearInterval(revalidateTimer);
			document.removeEventListener("pointermove", onMove, true);
			document.removeEventListener("pointerdown", onDown, true);
			document.removeEventListener("pointerup", onUp, true);
			document.removeEventListener("pointercancel", onUp, true);
			document.removeEventListener("pointerleave", onOut);
			document.removeEventListener("contextmenu", onMenu, true);
			window.removeEventListener("blur", onWindowBlur);
			tracker.release();
		};
	}, [onInteractiveChange, onRequestLook, onActivate]);

	const painted = paintOrder(world.pets);
	// Everything a pet SAYS, and everything you can ask it, belongs to a pet that is
	// actually on the desktop. One that is arriving has not got here and one that is
	// leaving has finished — a session narrating its way into a portal would be the
	// flourish inventing status, which is the one thing it must never do.
	const speaking = painted.filter((pet) => pet.transit === undefined);
	// The clock the transitions are drawn against. Read here rather than kept in state
	// because the frame loop above already re-renders on every frame a transition is
	// running, and a second copy of "what time is it" is a second thing to keep in step.
	const frameNow = transiting ? Date.now() : 0;

	// The cards' DRAWN sizes, read back off the layer after it renders.
	//
	// Guessing them from the maximum a card could be lifted a 145px card clear of a
	// neighbour it never touched, and put three lines of air above a one-line card.
	// The measurement is safe to feed back: a card's width and height are unaffected
	// by the offset it produces, so this settles in one extra pass and cannot
	// oscillate.
	const chromeLayer = useRef<HTMLDivElement>(null);
	const [cardSizes, setCardSizes] = useState<Record<string, CardBox>>({});
	// On the TICK, deliberately — NOT after every render.
	//
	// A card's left edge is read from the live rect, which the compositor is moving
	// continuously while a Proc walks. Measured after every render, each measurement
	// returned a slightly later position, which set state, which rendered, which
	// measured again: a synchronous loop that locked the page solid. Keyed to the
	// tick, the re-render it causes cannot re-enter it.
	useEffect(() => {
		const layer = chromeLayer.current;
		if (!layer) return;
		const measured: Record<string, CardBox> = {};
		for (const card of layer.querySelectorAll<HTMLElement>("[data-bubble-of]")) {
			const id = card.dataset.bubbleOf;
			if (!id) continue;
			// SIZE in layout units — the transformed rect is multiplied by every
			// transform above it. POSITION from the rect, because that is the only
			// thing that knows where the card has got to: a walking Proc's `x` is
			// still where it set off from, and the compositor is carrying its card
			// across the screen. Reading the destination instead lifted bubbles that
			// were nowhere near each other yet and left colliding ones flat.
			measured[id] = {
				width: card.offsetWidth,
				height: card.offsetHeight,
				left: Math.round(card.getBoundingClientRect().left),
			};
		}
		setCardSizes((current) => (sameSizes(current, measured) ? current : measured));
	}, [bubbleTick]);

	// The Proc whose terminal is open, if it is still on the band. A session that
	// ended while its terminal was up simply has no Proc to follow.
	const attachedPet = attachedSession ? (world.pets.find((pet) => pet.id === attachedSession) ?? null) : null;

	// A Proc you are TALKING to stands still.
	//
	// The card rides its Proc, so it would otherwise slide across the desktop mid
	// sentence while you were typing into it — and a terminal that wanders is a
	// terminal you cannot use. It stops where it is (`drawnX`, so there is no jump
	// from wherever the walk had actually got to) and holds off on strolling until
	// the terminal closes. It can still be picked up and thrown; the card follows.
	const attachedId = attachedSession;
	useEffect(() => {
		if (!attachedId) return;
		const stop = (current: World): World => ({
			...current,
			pets: current.pets.map((pet) =>
				pet.id !== attachedId || pet.motion.kind !== "walking"
					? pet
					: { ...pet, x: drawnX(pet, Date.now()), motion: { kind: "standing" }, restUntil: Number.MAX_SAFE_INTEGER },
			),
		});
		setWorld(stop);
		// A walk can begin between renders; hold the Proc for as long as its terminal
		// is open rather than only at the moment it opened.
		const timer = setInterval(() => setWorld(stop), TICK_MS);
		return () => {
			clearInterval(timer);
			setWorld((current) => ({
				...current,
				pets: current.pets.map((pet) => (pet.id === attachedId ? { ...pet, restUntil: Date.now() } : pet)),
			}));
		};
	}, [attachedId]);

	// Tell the shell where the attached Proc is, so its terminal window travels with
	// it. Reported from the DRAWN position, and only when it has actually moved:
	// this crosses a process boundary, and a Proc that is standing still (which one
	// with an open terminal does) must not generate any traffic at all.
	const anchorX = attachedPet
		? drawnX(attachedPet, frameNow) + figureLeft(attachedPet.facing === "left") + FRAME.figureWidth / 2
		: null;
	const anchorY = attachedPet ? window.innerHeight - attachedPet.y : null;
	const lastAnchor = useRef<{ x: number; y: number } | null>(null);
	useEffect(() => {
		if (anchorX === null || anchorY === null) {
			lastAnchor.current = null;
			return;
		}
		const next = { x: Math.round(anchorX), y: Math.round(anchorY) };
		const previous = lastAnchor.current;
		if (previous && Math.abs(previous.x - next.x) < 2 && Math.abs(previous.y - next.y) < 2) return;
		lastAnchor.current = next;
		onAttachedAnchorMove?.(next);
	}, [anchorX, anchorY, onAttachedAnchorMove]);

	// How high each bubble has to sit to clear the ones before it. Computed for the
	// whole band at once — a bubble cannot know on its own whether it is in the way.
	const offsets = stackBubbles(
		speaking
			.filter((pet) => spokenLine(pet, bubbleFor?.(pet.id) ?? null) !== null)
			.map((pet) => bubbleBox(pet, cardSizes[pet.id])),
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
					<ProcArt key={pet.id} pet={pet} cast={resolveCast(pet.id, pet.project)} now={frameNow} />
				))}
			</div>
			<div className="companion-chrome" ref={chromeLayer}>
				{speaking.map((pet) => (
					<ProcChrome
						key={pet.id}
						pet={pet}
						tooltip={openTooltip === pet.id}
						bubble={bubbleFor?.(pet.id) ?? null}
						offsetY={offsets.get(pet.id) ?? 0}
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
	// A held Proc must track the pointer exactly, and a thrown one is being drawn
	// frame by frame — neither may have a transition smoothing over it.
	const durationMs = pet.motion.kind === "walking" ? pet.motion.endsAt - pet.motion.startedAt : 0;
	return {
		// Y is height above the floor, so up the screen is NEGATIVE.
		transform: `translate3d(${targetX}px, ${-pet.y}px, 0px)`,
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
		// The FIGURE's own box, which the call cue's ring is centred on. The frame is
		// taller and wider than it — it carries the scenery — so a ring drawn on the
		// frame sits off the Proc it is coming out of.
		["--procs-figure-height" as string]: `${PET_HEIGHT}px`,
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

// The look is resolved once for the whole band and handed down, rather than each
// Proc subscribing to the store for itself: twelve subscriptions to one localStorage
// key would all fire on the same change and re-render the same frame twelve times.
function ProcArt({ pet, cast, now }: { pet: Pet; cast: CastMember; now: number }) {
	const walking = pet.motion.kind === "walking";
	const held = pet.motion.kind === "held";
	const greeting = pet.meeting?.phase === "greeting";
	// A Proc on its way to (or back from) a meeting — or answering a roll-call — is
	// running, not strolling. The same event pace, because it is the same kind of
	// event: the human asked for it.
	const running = walking && (pet.meeting !== undefined || pet.rally !== undefined);
	// The one that was shaken. It carries a rally naming ITSELF as the leader, which
	// is the same fact the engine works from rather than a second copy of it.
	const calling = pet.rally?.leaderId === pet.id;

	// Coming through a portal, or going into one. The transition owns the pet's frame
	// for as long as it runs: the art still draws whatever the session is doing, but a
	// wrapper carries the leap and the ring is drawn behind it.
	const transit = pet.transit;
	const runFor = transit ? transit.until - transit.startedAt : 0;
	// Only ever SEEN under reduced motion, where the keyframes are dead and this is the
	// whole effect. Computed unconditionally so there is one code path rather than a
	// reduced-motion branch somebody can forget to keep working.
	const fade = transit ? transitOpacity(transit.phase, now - transit.startedAt, runFor) : undefined;

	const figure = (
		<Procs
			cast={cast}
			status={pet.status}
			facing={pet.facing}
			walking={walking}
			held={held}
			running={running}
			greeting={greeting}
			travelling={transit !== undefined}
			bounce={pet.bounce}
			size={PET_HEIGHT}
			className="companion-proc-art"
		/>
	);
	const label = (
		<div className="companion-proc-name">
			<NameTag name={pet.name} lead={pet.kind === "orchestrator"} />
		</div>
	);

	return (
		<div
			data-proc
			data-session={pet.id}
			className="companion-proc"
			style={{ ...placement(pet), zIndex: stackOrder(pet) }}
		>
			{/* Keyed by WHEN the transition started, so a pet that leaves and comes
			    straight back gets a second element and plays its arrival rather than
			    inheriting the exit's progress. */}
			{transit ? <Portal key={transit.startedAt} phase={transit.phase} durationMs={runFor} /> : null}
			{transit ? (
				<PortalTransit key={`leap-${transit.startedAt}`} phase={transit.phase} durationMs={runFor} opacity={fade}>
					{figure}
				</PortalTransit>
			) : (
				figure
			)}
			{/* Keyed by WHEN the shake landed, so a second rally is a second element and
			    its rings play again instead of being skipped as unchanged. */}
			{calling ? (
				<div className="companion-proc-rally" data-rally-call key={pet.rally?.startedAt}>
					<span />
					<span />
				</div>
			) : null}
			{transit ? (
				<PortalLabel key={`name-${transit.startedAt}`} phase={transit.phase} durationMs={runFor} opacity={fade}>
					{label}
				</PortalLabel>
			) : (
				label
			)}
		</div>
	);
}

/** A card as it is actually drawn: its size in layout units, its left edge on screen. */
type CardBox = { width: number; height: number; left: number };

/** Two measurement maps are the same when every card in both is the same box. */
function sameSizes(a: Record<string, CardBox>, b: Record<string, CardBox>): boolean {
	const keys = Object.keys(a);
	if (keys.length !== Object.keys(b).length) return false;
	return keys.every(
		(key) => b[key] && a[key].width === b[key].width && a[key].height === b[key].height && a[key].left === b[key].left,
	);
}

/**
 * Where this Proc's card actually sits, in screen px.
 *
 * `measured` is absent on the very first pass, before the layer has been read
 * back. The widest and tallest a card can be is the safe assumption for one
 * frame: it over-separates rather than letting two cards land on each other.
 */
function bubbleBox(pet: Pet, measured?: CardBox): BubbleBox {
	const width = measured?.width ?? BUBBLE_MAX_WIDTH;
	const height = measured?.height ?? BUBBLE_MAX_HEIGHT;
	if (measured) return { id: pet.id, left: measured.left, right: measured.left + width, height };

	// Nothing measured yet — the very first pass. Fall back to where the card would
	// be if its Proc were standing still, at the widest and tallest it can be: it
	// over-separates for one frame rather than letting two cards land on each other.
	const figureX = pet.x + figureLeft(pet.facing === "left");
	const opensLeft = bubbleOpensLeft({
		figureX,
		figureWidth: FRAME.figureWidth,
		screenWidth: window.innerWidth,
		preferLeft: pet.meeting?.phase === "greeting" && pet.facing === "right",
	});
	const left = opensLeft ? figureX + FRAME.figureWidth - width : figureX;
	return { id: pet.id, left, right: left + width, height };
}

function ProcChrome({
	pet,
	tooltip,
	bubble,
	offsetY,
}: {
	pet: Pet;
	tooltip: boolean;
	bubble: ComposedBubble | null;
	/** How far above its Proc this bubble sits, in px, to clear the others. */
	offsetY: number;
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
					data-bubble-of={pet.id}
					data-lifted={offsetY ? "" : undefined}
					style={{ ["--procs-bubble-lane" as string]: `${offsetY}px` }}
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
