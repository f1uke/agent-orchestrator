import type { components } from "../api/schema";

// The two-slot decay ladder from the feed's contract, and the reason the bubble
// can be trusted.
//
// The feed emits observed facts with an expiry attached; it never says "and it is
// still true now". So the consumer keeps exactly two slots per session and
// RE-EVALUATES THEM ON A TIMER. Silence is not "unchanged" — silence is how a claim
// runs out. Without that, a Proc goes on saying "Running the test suite" two minutes
// after the run finished, which is precisely the lie the whole design exists to
// avoid.
//
//   detail  →  coarse  →  unknown
//
// `backend/internal/domain/activityevent.go` is the reference implementation of the
// same ladder; this mirrors it on the consumer side.

/** The wire frame, straight from the generated OpenAPI types — never hand-rolled. */
export type ActivityFrame = components["schemas"]["ActivityEvent"];

export type CoarseLevel = NonNullable<ActivityFrame["coarse"]>;

export type DetailSlot = {
	tool?: string;
	target?: string;
	text?: string;
	kind: ActivityFrame["kind"];
	atMs: number;
	ttlMs: number;
};

export type CoarseSlot = {
	coarse: CoarseLevel;
	atMs: number;
	ttlMs: number;
};

export type ActivitySlots = {
	detail?: DetailSlot;
	coarse?: CoarseSlot;
};

export type Resolved = {
	level: "detail" | "coarse" | "unknown";
	detail?: DetailSlot;
	coarse?: CoarseLevel;
};

export function emptySlots(): ActivitySlots {
	return {};
}

/**
 * Fold a frame into the slots.
 *
 * Two absences are meaningful and are NOT the same as a clear:
 *   - `ttlMs === 0` means "this event carries no detail", so the existing detail
 *     stands until its own TTL runs out.
 *   - an absent `coarse` means "this event does not change the coarse level".
 */
export function applyEvent(slots: ActivitySlots, frame: ActivityFrame): ActivitySlots {
	const atMs = Date.parse(frame.at);
	const next: ActivitySlots = { ...slots };

	if (frame.ttlMs > 0) {
		next.detail = {
			tool: frame.tool,
			target: frame.target,
			text: frame.text,
			kind: frame.kind,
			atMs,
			ttlMs: frame.ttlMs,
		};
	}
	if (frame.coarse) {
		next.coarse = { coarse: frame.coarse, atMs, ttlMs: frame.coarseTtlMs };
	}
	return next;
}

/** What may honestly be shown at `now`. */
export function resolveAt(slots: ActivitySlots, now: number): Resolved {
	const { detail, coarse } = slots;
	if (detail && now < detail.atMs + detail.ttlMs) return { level: "detail", detail, coarse: coarse?.coarse };
	// coarseTtlMs 0 is sticky: a pending prompt is pending until answered.
	if (coarse && (coarse.ttlMs === 0 || now < coarse.atMs + coarse.ttlMs)) {
		return { level: "coarse", coarse: coarse.coarse };
	}
	return { level: "unknown" };
}
