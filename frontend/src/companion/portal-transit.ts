// The lifecycle transition itself: what a portal COSTS in time, and what a pet's
// place in one is. No art and no React, because the engine reads these too — a pure
// `behaviour.ts` that imported a component to find out how long a jump takes would be
// dragging the whole renderer into the one file that has to stay testable on its own.
//
// The durations are the single source of truth for both halves. `Portal.tsx` writes
// them out as inline `animation-duration`, and the engine ends the transit on the same
// number, so a portal cannot close before its pet has landed and a pet cannot be
// removed while its ring is still open.

/** Whether this portal is letting a pet out or taking one in. */
export type PortalPhase = "arriving" | "leaving";

/**
 * A pet's place in a lifecycle transition.
 *
 * `until` is ABSOLUTE, which is the whole reason this cannot deadlock: every state a
 * pet can get into here has an instant at which it is over, whatever else happens —
 * a feed that flaps, a display that sleeps, a snapshot that repeats.
 */
export type PortalTransit = {
	phase: PortalPhase;
	startedAt: number;
	until: number;
};

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
 * stretch over a second and a half. What remains is a portal and a pet fading through
 * it, and holding a still ring open for 1500ms to say that would be worse than the
 * pop it replaced.
 */
export const PORTAL_REDUCED_MS = 260;

/** How long a phase runs, with reduced motion collapsing both to the short one. */
export function portalDurationMs(phase: PortalPhase, reducedMotion = false): number {
	if (reducedMotion) return PORTAL_REDUCED_MS;
	return phase === "arriving" ? PORTAL_IN_MS : PORTAL_OUT_MS;
}

/** Start one. The only place a transit is constructed, so `until` is never hand-rolled. */
export function beginTransit(phase: PortalPhase, now: number, reducedMotion = false): PortalTransit {
	return { phase, startedAt: now, until: now + portalDurationMs(phase, reducedMotion) };
}

/**
 * How visible a pet is this far through its transition, for the reduced-motion path.
 *
 * Written inline on the pet by the renderer. Under normal motion the keyframes
 * override it, as a running animation outranks a normal declaration; under reduced
 * motion the keyframes are dead and this IS the effect. One code path, so the reduced
 * case cannot be forgotten — it is the same line.
 */
export function transitOpacity(phase: PortalPhase, elapsedMs: number, durationMs: number): number {
	const fade = Math.max(1, durationMs * 0.6);
	const progress = Math.max(0, Math.min(1, elapsedMs / fade));
	return phase === "arriving" ? progress : 1 - progress;
}
