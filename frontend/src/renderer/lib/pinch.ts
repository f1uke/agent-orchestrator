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
 * copied because the pane has to know it and the API does not publish it. If
 * that constant moves, this one has to move with it - and the symptom of it not
 * moving is a refusal a human sees rather than anything a test fails on. It is
 * duplicated rather than approximated on purpose: the two are compared against
 * the same doubles, so an equal value is exactly safe and a nearby one is not.
 *
 * What it is FOR here: the anchor starts at the middle of the screen, so a press
 * ON the middle puts both fingers on the same spot. The daemon refuses that -
 * correctly, because a pinch that lands as one touch sends events, changes
 * nothing and reads exactly like one that worked - but "you pressed too close to
 * the middle" is a poor thing to say to somebody who has simply started a zoom
 * where a zoom naturally starts. So the touch waits, by the same instinct the
 * one-finger path waits to tell a tap from a drag: it goes down the moment there
 * are two contacts to put down, which for every press but a bullseye is at once.
 */
export const MIN_PINCH_SPAN = 0.02;

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
	return { a: at, b: mirror(at, anchor) };
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
	return { x: clamp01(anchor.x + (to.x - from.x)), y: clamp01(anchor.y + (to.y - from.y)) };
}

function clamp01(n: number): number {
	return Math.min(1, Math.max(0, n));
}
