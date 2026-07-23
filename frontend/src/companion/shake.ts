// Telling a SHAKE apart from a throw and from a drag. Pure: pointer samples in,
// a verdict out, no DOM and no clock of its own.
//
// This is the whole crux of the rally gesture. All three gestures start the same
// way — press a Proc and move the pointer — so the only thing that separates them
// is the SHAPE of the movement:
//
//   throw  → one long directional run, sometimes with a wind-up behind it: 1-2 legs
//   drag   → any shape at all, but slow, or short, or both
//   shake  → several fast, substantial reversals inside one short window
//
// So the detector does not look at speed alone (a throw is faster than a shake) and
// does not look at reversals alone (a drag reverses whenever the hand changes its
// mind). It looks for BOTH at once, and requires them to be consecutive: a slow or
// stubby leg in the middle breaks the run and the gesture is not a shake.

/** One pointer sample, in client px. */
export type ShakePoint = { x: number; y: number; at: number };

/**
 * A leg: one run of travel in a single direction, and how long it took.
 *
 * `endedAt` is when the hand turned round, which is what the window is measured
 * against — a leg that finished a second ago belongs to a previous gesture.
 */
type Leg = { dx: number; ms: number; endedAt: number };

/** One axis' worth of tracking. x and y are watched independently; see {@link isShaking}. */
type AxisTrack = {
	/** Finished legs, oldest first, already pruned to the window. */
	legs: Leg[];
	/** Where the leg in progress started, and when. */
	fromV: number;
	fromAt: number;
	/** The latest sample on this axis. */
	v: number;
	at: number;
	/** Which way the leg in progress is going. 0 before anything has moved. */
	dir: -1 | 0 | 1;
	/**
	 * A turn that has not been believed yet.
	 *
	 * A pointer stream is noisy, and one 2px hiccup inside a 40px flick would
	 * otherwise cut that flick into two stubby legs and disqualify a real shake. A
	 * reversal is only committed once the hand has actually travelled
	 * {@link JITTER_PX} back the other way — and when it is, the leg ends at the
	 * EXTREME it reached, not at the sample that proved the turn.
	 */
	pivotV: number | null;
	pivotAt: number;
};

export type ShakeTrack = { x: AxisTrack; y: AxisTrack };

/** Reversals older than this belong to a previous gesture, not to this one. */
export const SHAKE_WINDOW_MS = 700;
/**
 * How many consecutive qualifying legs make a shake. Four legs is three reversals
 * — → ← → ← — which is a deliberate wiggle and is three more than a throw has.
 */
export const SHAKE_LEGS = 4;
/** A leg has to cover ground. Below this it is positioning, not shaking. */
export const SHAKE_MIN_LEG_PX = 22;
/** …and cover it fast. 0.45px/ms is a flick of the wrist; a carried Proc is far slower. */
export const SHAKE_MIN_LEG_SPEED = 0.45;
/** How far back the hand must go before a turn is believed rather than read as noise. */
const JITTER_PX = 3;
/** Below this a sample has not moved at all, and cannot start or end anything. */
const STILL_PX = 0.5;
/**
 * How much of a gap before a leg can be blamed on the hand actually travelling.
 *
 * The gesture is press-and-HOLD and then shake, and a hand holding still emits no
 * pointer events at all — so the sample before the opening flick is the PRESS,
 * possibly half a second earlier. Measured from there the opening flick looks as
 * slow as the pause in front of it and the whole shake is thrown away. About two
 * frames is the most that gap can honestly be travel; the rest was waiting.
 */
const RESUME_GAP_MS = 32;

function newAxis(): AxisTrack {
	return { legs: [], fromV: 0, fromAt: 0, v: 0, at: 0, dir: 0, pivotV: null, pivotAt: 0 };
}

/** A clean slate. Taken at the start of every press, so one gesture cannot finish another. */
export function newShakeTrack(): ShakeTrack {
	return { x: newAxis(), y: newAxis() };
}

/** Fold one pointer sample into the track. Pure — the track handed in is untouched. */
export function trackShake(track: ShakeTrack, point: ShakePoint): ShakeTrack {
	return {
		x: trackAxis(track.x, point.x, point.at),
		y: trackAxis(track.y, point.y, point.at),
	};
}

function trackAxis(axis: AxisTrack, v: number, at: number): AxisTrack {
	// The very first sample only says where the hand IS. There is no travel yet.
	if (axis.dir === 0 && axis.legs.length === 0 && axis.at === 0) {
		return { ...axis, fromV: v, fromAt: at, v, at };
	}

	const delta = v - axis.v;
	if (Math.abs(delta) < STILL_PX) {
		// Standing still. Recorded, because a leg that spent a second going nowhere is
		// a slow leg and must be scored as one, not silently skipped.
		return { ...axis, v, at, legs: prune(axis.legs, at) };
	}

	const heading: -1 | 1 = delta < 0 ? -1 : 1;
	if (axis.dir === 0) {
		// The hand has started moving, so the first leg starts at the position it was
		// resting at — and its clock starts at the last sample, or at most
		// {@link RESUME_GAP_MS} ago, whichever is later. A continuous pointer stream is
		// sampled far faster than that, so this is exact while the hand is moving and
		// only bites when it has been sitting still.
		return {
			...axis,
			fromV: axis.v,
			fromAt: Math.max(axis.at, at - RESUME_GAP_MS),
			dir: heading,
			v,
			at,
			legs: prune(axis.legs, at),
		};
	}

	if (heading === axis.dir) {
		// Carrying on. Any turn we were half-believing was noise after all.
		return { ...axis, v, at, pivotV: null, legs: prune(axis.legs, at) };
	}

	// Going the other way. Remember the extreme we turned at, and wait to be sure.
	const pivotV = axis.pivotV ?? axis.v;
	const pivotAt = axis.pivotV === null ? axis.at : axis.pivotAt;
	if (Math.abs(v - pivotV) < JITTER_PX) {
		return { ...axis, v, at, pivotV, pivotAt, legs: prune(axis.legs, at) };
	}

	// A real turn. The leg that just ended ran from where it started to the extreme.
	const leg: Leg = { dx: pivotV - axis.fromV, ms: Math.max(1, pivotAt - axis.fromAt), endedAt: pivotAt };
	return {
		legs: prune([...axis.legs, leg], at),
		fromV: pivotV,
		fromAt: pivotAt,
		v,
		at,
		dir: heading,
		pivotV: null,
		pivotAt: 0,
	};
}

function prune(legs: Leg[], now: number): Leg[] {
	const oldest = now - SHAKE_WINDOW_MS;
	return legs.every((leg) => leg.endedAt >= oldest) ? legs : legs.filter((leg) => leg.endedAt >= oldest);
}

/** A leg fast enough and long enough to be part of a shake rather than a carry. */
function qualifies(leg: Leg): boolean {
	const distance = Math.abs(leg.dx);
	return distance >= SHAKE_MIN_LEG_PX && distance / leg.ms >= SHAKE_MIN_LEG_SPEED;
}

/**
 * Is the hand shaking right now?
 *
 * True when either axis shows {@link SHAKE_LEGS} consecutive qualifying legs inside
 * the window. Both axes, because a hand told to shake something does not check which
 * way the band runs — a vertical or diagonal wiggle is the same gesture, and its
 * dominant axis is the one that registers.
 *
 * The leg IN PROGRESS counts as soon as it qualifies, so the rally fires on the beat
 * the fourth flick lands rather than waiting for a fifth reversal to close it.
 */
export function isShaking(track: ShakeTrack): boolean {
	return axisShaking(track.x) || axisShaking(track.y);
}

function axisShaking(axis: AxisTrack): boolean {
	const running: Leg = { dx: axis.v - axis.fromV, ms: Math.max(1, axis.at - axis.fromAt), endedAt: axis.at };
	const legs = qualifies(running) ? [...axis.legs, running] : axis.legs;
	if (legs.length < SHAKE_LEGS) return false;
	return legs.slice(-SHAKE_LEGS).every(qualifies);
}
