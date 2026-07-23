import { APPEARANCE_AXES, castFromLook, defaultLook, type AxisId, type CastMember, type Look } from "./cast";

// A session's CHOSEN look, one axis at a time, over the hash default.
//
// Pure and storage-free on purpose: `look-store-live.ts` binds this to
// localStorage, the Pet library drives it, and the overlay reads the result. Every
// function here takes the whole map and returns a new one, so a caller can render
// a preview of a choice without committing it.
//
// The shape is `{ sessionRef: { axisId: optionId } }` and it is deliberately SPARSE:
// an axis nobody has picked has no key, which is what makes "back to the default"
// a deletion rather than a second kind of value. Absent means the hash decides, and
// there is exactly one way to say that.
//
// ⚠ Written narrow, read wide. The write side (`chooseLook`) takes a typed `AxisId`,
// so this build can only record axes it knows. The read side accepts any string key,
// because the file on disk may have been written by a LATER build that has more
// axes than this one - and the right response to an axis we have never heard of is
// to draw the Proc anyway, not to fall over.

/** What one session has chosen: axis id to option id. Sparse; absent means "the hash". */
export type LookChoices = Readonly<Record<string, string>>;

/** Every session anybody has chosen for. Sessions absent from it are pure hash. */
export type LookOverrides = Readonly<Record<string, LookChoices>>;

/** Where the choices live. Same key in both windows; they share an origin. */
export const LOOKS_STORAGE_KEY = "ao.companion.looks";

/** Bumped only if the stored SHAPE changes; an added axis is not a shape change. */
const STORAGE_VERSION = 1;

function axisFor(axisId: string) {
	return APPEARANCE_AXES.find((axis) => axis.id === axisId);
}

/** True when `optionId` is something this build can actually draw on that axis. */
function isKnownOption(axisId: string, optionId: unknown): optionId is string {
	return typeof optionId === "string" && (axisFor(axisId)?.options.some((option) => option.id === optionId) ?? false);
}

/**
 * The look this session shows: the chosen option per axis, the hash for the rest.
 *
 * Resolution is PER AXIS and independent, so a chosen hat cannot disturb a colour
 * and an unreadable stored value costs only its own axis.
 */
export function resolveLook(sessionRef: string, overrides: LookOverrides): Look {
	const chosen = overrides[sessionRef];
	if (!chosen) return defaultLook(sessionRef);
	const fallback = defaultLook(sessionRef);
	return Object.fromEntries(
		APPEARANCE_AXES.map((axis) => [
			axis.id,
			isKnownOption(axis.id, chosen[axis.id]) ? chosen[axis.id] : fallback[axis.id],
		]),
	) as Look;
}

/** The resolved look, flattened into what the rig paints. */
export function castFor(sessionRef: string, overrides: LookOverrides): CastMember {
	return castFromLook(resolveLook(sessionRef, overrides));
}

/** True when this axis is showing a human's choice rather than the hash. */
export function isAxisChosen(overrides: LookOverrides, sessionRef: string, axisId: AxisId): boolean {
	return isKnownOption(axisId, overrides[sessionRef]?.[axisId]);
}

/** Record a choice on one axis. Every other axis is left exactly as it was. */
export function chooseLook(
	overrides: LookOverrides,
	sessionRef: string,
	axisId: AxisId,
	optionId: string,
): LookOverrides {
	return { ...overrides, [sessionRef]: { ...overrides[sessionRef], [axisId]: optionId } };
}

/**
 * Put one axis - or, with no axis, the whole session - back on the hash.
 *
 * A session with no choices left is REMOVED rather than left as an empty object,
 * so the store does not grow a row for every pet anyone has ever looked at.
 */
export function clearLookChoice(overrides: LookOverrides, sessionRef: string, axisId?: AxisId): LookOverrides {
	const chosen = overrides[sessionRef];
	if (!chosen) return overrides;

	const next = { ...overrides };
	if (axisId === undefined) {
		delete next[sessionRef];
		return next;
	}

	const remaining = { ...chosen };
	delete remaining[axisId];
	if (Object.keys(remaining).length === 0) delete next[sessionRef];
	else next[sessionRef] = remaining;
	return next;
}

/**
 * Forget the sessions that are gone.
 *
 * ⚠ Feed this the AUTHORITATIVE session list, which only the main app has. The
 * overlay's roster is capped at `MAX_PETS`, so pruning against it would throw away
 * the saved look of a session that exists and is merely off-screen.
 *
 * Returns the same object when nothing is dropped: the caller persists on change,
 * and a fresh object every poll would rewrite localStorage for ever.
 */
export function pruneLookOverrides(overrides: LookOverrides, liveRefs: Iterable<string>): LookOverrides {
	const live = new Set(liveRefs);
	const kept = Object.keys(overrides).filter((ref) => live.has(ref));
	if (kept.length === Object.keys(overrides).length) return overrides;
	return Object.fromEntries(kept.map((ref) => [ref, overrides[ref]]));
}

function readChoices(raw: unknown): LookChoices | null {
	if (typeof raw !== "object" || raw === null || Array.isArray(raw)) return null;
	const entries = Object.entries(raw as Record<string, unknown>).filter(
		([axisId, optionId]) => typeof axisId === "string" && axisId !== "" && typeof optionId === "string",
	) as Array<[string, string]>;
	return entries.length > 0 ? Object.fromEntries(entries) : null;
}

/**
 * Read the stored choices, tolerating anything.
 *
 * This runs on the OVERLAY, on someone's desktop, before a single Proc is drawn. A
 * corrupt or half-written value must cost the decoration and never the pets, so
 * every failure path returns "nobody has chosen anything" - which is a perfectly
 * good state that the whole feature is designed around.
 */
export function parseLookOverrides(raw: string | null | undefined): LookOverrides {
	if (!raw) return {};
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return {};
	}
	if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
	const sessions = (parsed as { sessions?: unknown }).sessions;
	if (typeof sessions !== "object" || sessions === null || Array.isArray(sessions)) return {};

	const out: Record<string, LookChoices> = {};
	for (const [ref, value] of Object.entries(sessions as Record<string, unknown>)) {
		if (ref === "") continue;
		const choices = readChoices(value);
		if (choices) out[ref] = choices;
	}
	return out;
}

/** The stored form: a versioned envelope, so a future shape change is detectable. */
export function serializeLookOverrides(overrides: LookOverrides): string {
	return JSON.stringify({ v: STORAGE_VERSION, sessions: overrides });
}
