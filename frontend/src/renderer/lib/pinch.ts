import type { DragGrip, DragPoint } from "./drag-stream";

/**
 * Where the two fingers of a hand-driven pinch are.
 *
 * The rule is Simulator.app's own, and it was measured rather than remembered:
 * a page in the simulator's Safari logged every touch while synthetic
 * Option+mouse events were posted to the Simulator window, and the coordinates
 * were read back through `ao sim ax`. Every frame of every Option-drag had its
 * two contacts' midpoint at exactly the centre of the device screen -
 * `(220, 478)` on a 440x956 device, to the pixel, however far the pointer had
 * travelled.
 *
 * So: **one finger is AT the pointer, and the other is the pointer reflected
 * through an anchor** which starts at the middle of the screen. Moving the
 * pointer away from the anchor spreads the fingers; moving it towards the
 * anchor brings them together. There is no separate "spread" number to invent a
 * default for, which is the same reason `ao sim pinch` takes two spans rather
 * than a scale.
 *
 * The one thing Simulator.app can do that this file also has to is move that
 * anchor - otherwise a pinch can only ever zoom about the middle of the screen,
 * and a map or a photo is usually not interesting in the middle. Holding Shift
 * mid-pinch translates both fingers together, which is Simulator.app's
 * behaviour exactly (measured: with Shift down both contacts moved by the same
 * delta and the midpoint left the centre).
 */

/**
 * Where a pinch is centred before anybody moves it: the middle of the screen,
 * which is where Simulator.app puts it and where it goes back to at the start
 * of every pinch.
 */
export const PINCH_ANCHOR: DragPoint = { x: 0.5, y: 0.5 };

/**
 * The closest the two contacts may be and still land as two.
 *
 * ⚠ This is `simbridge.MinPinchSpan` (backend/internal/simbridge/gesture.go),
 * copied because the pane has to know it and the API does not publish it. It is
 * the same number, not a nearby one: a lower copy would send pairs the daemon
 * refuses and a higher one would hold the fingers further apart than they need
 * to be. If that constant moves, this one has to move with it - and the symptom
 * of it not moving is a refusal a human sees, which no test here fails on.
 * (The margin that keeps a placed pair clear of the threshold is separate, and
 * lives in HELD_APART_RADIUS below.)
 *
 * What it is FOR: the anchor starts at the middle of the screen, so the two
 * contacts meet when the pointer reaches the middle - on a press that lands
 * there, and again on a pinch dragged all the way closed through it. The daemon
 * refuses a pair that close, correctly, because a pinch landing as one touch
 * sends events, changes nothing and reads exactly like one that worked. But a
 * refusal there is a red message and a device that answers nothing until the
 * watchdog lifts it, for a human who was simply zooming out as far as it goes.
 *
 * So this is a floor on the gesture rather than a gate on it: the fingers stop
 * coming together, which is what real fingers do, and the overlay draws them
 * where they actually are - so what stops is visible rather than silent.
 */
export const MIN_PINCH_SPAN = 0.02;

/**
 * Half the span the contacts are held at once the pointer has closed them.
 *
 * It is a fifth over half of MIN_PINCH_SPAN rather than exactly half, and the
 * margin is the point: the daemon refuses a pair whose distance comes out below
 * MIN_PINCH_SPAN, and a pair placed at exactly the threshold along a diagonal
 * can round to either side of it. A tenth of a percent of the screen buys
 * immunity from that and is not a distance anybody can see.
 */
const HELD_APART_RADIUS = MIN_PINCH_SPAN * 0.6;

/** How far apart a grip's contacts are, in the units MIN_PINCH_SPAN is in. */
export function pinchSpan(grip: DragGrip): number {
	if (!grip.b) return 0;
	return Math.hypot(grip.b.x - grip.a.x, grip.b.y - grip.a.y);
}

/**
 * pinchGrip is the pair of contacts for a pointer at `at`, about `anchor`.
 *
 * Reflection is done in normalized coordinates, and that is exact rather than
 * approximate: reflecting through a point commutes with scaling each axis, so
 * the pair this describes is the same pair as reflecting in device points and
 * normalizing afterwards. (It is only ROTATION that normalized coordinates
 * would distort on a screen that is not square, which is why `ao sim pinch`
 * keeps its fingers on one axis and this has no rotation at all.)
 */
export function pinchGrip(at: DragPoint, anchor: DragPoint): DragGrip {
	const a = heldApartFrom(anchor, at);
	return { a, b: mirror(a, anchor) };
}

/**
 * heldApartFrom is the pointer's own contact, held out to HELD_APART_RADIUS from
 * the anchor once it comes closer than that - which, since the other contact is
 * the same distance the other way, is what keeps the pair far enough apart to
 * land as two. See MIN_PINCH_SPAN for why the fingers stop rather than the
 * gesture being refused.
 *
 * With the pointer exactly ON the anchor there is no direction to hold it out
 * along, so it picks one. Vertical, because a phone is taller than it is wide
 * and the fingers of a pinch that has closed to nothing still have to point
 * somewhere.
 */
function heldApartFrom(anchor: DragPoint, at: DragPoint): DragPoint {
	const dx = at.x - anchor.x;
	const dy = at.y - anchor.y;
	const away = Math.hypot(dx, dy);
	if (away >= HELD_APART_RADIUS) return at;
	if (away === 0) return { x: anchor.x, y: anchor.y + HELD_APART_RADIUS };
	const out = HELD_APART_RADIUS / away;
	return { x: anchor.x + dx * out, y: anchor.y + dy * out };
}

/**
 * mirror is a point reflected through the anchor, pulled back onto the screen.
 *
 * With the anchor at the middle, a pointer on the screen always reflects onto
 * the screen and the clamp never does anything. It exists for a MOVED anchor,
 * where the far finger can be pushed off the edge - and there the clamp is the
 * honest answer rather than a compensation: a finger cannot be outside the
 * screen, the overlay draws the contact where it actually is so the dot
 * visibly stops at the edge, and the alternative is a coordinate the daemon
 * refuses, which would lift the pinch mid-gesture.
 */
function mirror(at: DragPoint, anchor: DragPoint): DragPoint {
	return { x: clamp01(2 * anchor.x - at.x), y: clamp01(2 * anchor.y - at.y) };
}

/**
 * pannedAnchor moves the anchor by however far the pointer moved, which is what
 * makes both fingers travel together instead of apart. Shift held during a
 * pinch is what asks for it.
 */
export function pannedAnchor(anchor: DragPoint, from: DragPoint, to: DragPoint): DragPoint {
	return { x: onScreenAnchor(anchor.x + (to.x - from.x)), y: onScreenAnchor(anchor.y + (to.y - from.y)) };
}

function clamp01(n: number): number {
	return Math.min(1, Math.max(0, n));
}

/**
 * onScreenAnchor keeps the anchor far enough in from every edge for the held
 * pair to fit, which is what makes the floor above safe: the contacts sit
 * HELD_APART_RADIUS either side of the anchor, so an anchor closer to an edge
 * than that would push one of them off the screen - and an off-screen contact is
 * a coordinate the daemon refuses, which drops the pinch mid-gesture.
 *
 * It costs nothing anybody can see: that radius is a little over one percent of
 * the screen, and a pinch centred inside the outermost one percent of it is not
 * a gesture anybody was making.
 */
function onScreenAnchor(n: number): number {
	return Math.min(1 - HELD_APART_RADIUS, Math.max(HELD_APART_RADIUS, n));
}
