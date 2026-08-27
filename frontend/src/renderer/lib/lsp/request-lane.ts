/**
 * One request on the wire per open document, shared by every position-based
 * feature this app asks a language server about.
 *
 * ## Why serialise, and why NOT cancel
 *
 * #258 measured this for completion, before any of it was written, on the real
 * iOS app. A burst of 8 keystrokes 60 ms apart into a cold Swift file:
 *
 * | policy | requests | last keystroke → answer |
 * |---|---|---|
 * | per-keystroke + `$/cancelRequest` | 8 (7 cancelled) | **2 172–2 422 ms** |
 * | debounce 120 ms | 1 | 522 ms |
 * | **serialise, never cancel** | **3** | **2 ms** |
 *
 * sourcekit-lsp honours `$/cancelRequest`, and each cancellation throws away
 * the in-progress type-check the next request has to redo.
 *
 * ## …and why HOVER shares the lane rather than getting its own
 *
 * Measured for this slice, same machine, same two workspaces:
 *
 * | | first request in the file | warm repeats |
 * |---|---|---|
 * | sourcekit-lsp `textDocument/hover` | **1 919 ms** | 56–70 ms |
 * | gopls `textDocument/hover` | **587 ms** | 0–17 ms |
 *
 * 🗝 The expensive thing is not the request, it is **the file's first
 * type-check** — and hover, completion and references all wait on the same one.
 * Two independent lanes would let a hover's 1 919 ms type-check run beside a
 * completion's, so opening a file and touching the mouse would pay the cold cost
 * twice. One lane per document means whichever asks first pays it and the other
 * gets it warm.
 *
 * Supersession is deliberately NOT in here: it is per FEATURE. A newer hover
 * supersedes an older hover; it must not drop a completion that is queued behind
 * it. Each caller passes its own `isStale`.
 */

/** Per document: the request on the wire. Nothing else belongs in this map. */
const lanes = new Map<string, Promise<unknown> | null>();

export type LaneOutcome<T> = { ok: true; value: T } | { ok: false; stale: true };

/**
 * Run `send` with at most one request per `modelUri` on the wire.
 *
 * Waits for whatever is in flight rather than cancelling it, re-checks
 * `isStale()` after every wait, and releases the slot unconditionally — a
 * settled promise left in the lane spins every later waiter forever, which is
 * the single strongest failure this module can produce and the one its test
 * mutates for.
 */
export async function runInLane<T>(
	modelUri: string,
	isStale: () => boolean,
	send: () => Promise<T>,
): Promise<LaneOutcome<T>> {
	// 🗝 WAIT for whatever is on the wire; do not cancel it. On a cold file that
	// request is doing the type-check this one would otherwise have to redo from
	// scratch, and cancelling it measured 34x slower.
	for (;;) {
		const inFlight = lanes.get(modelUri);
		if (!inFlight) break;
		try {
			await inFlight;
		} catch {
			// Its own caller reports it. All this one needs is the slot. The loop
			// repeats because another queued call may have taken the slot while this
			// one was waiting.
		}
		// Superseded while queued — by Monaco cancelling this call for a newer
		// keystroke or a newer pointer position, or by a newer call arriving.
		// Either way this answer is for a context that is no longer on screen and
		// must never reach the widget. Silently: being superseded is the ordinary
		// case while somebody types or moves a mouse.
		if (isStale()) return { ok: false, stale: true };
	}
	// Nothing was in flight at all, so the check above never ran.
	if (isStale()) return { ok: false, stale: true };

	const answer = send();
	// The slot is released by a rejection too — including a timeout, after which
	// the server is presumed wedged. Holding it would make every later request
	// wait on one that is never coming back.
	const slot = answer.catch(() => undefined);
	lanes.set(modelUri, slot);
	try {
		return { ok: true, value: await answer };
	} finally {
		// Unconditionally, and only if it is still OURS.
		if (lanes.get(modelUri) === slot) lanes.set(modelUri, null);
	}
}

/** A pane closing. Leaving the entry behind would hold one promise per file ever opened. */
export function forgetLane(modelUri: string): void {
	lanes.delete(modelUri);
}

/**
 * Drop every lane.
 *
 * For tests only, and they need it: this module holds process-global state, so
 * a test that leaves a request unresolved would otherwise wedge every later test
 * on the same document — the same thing that would happen in the app if a
 * request could never settle, which is what {@link REQUEST_TIMEOUT_MS} exists to
 * make impossible.
 */
export function resetLanes(): void {
	lanes.clear();
}

/** For tests: is anything on the wire for this document right now? */
export function laneBusy(modelUri: string): boolean {
	return Boolean(lanes.get(modelUri));
}

/**
 * How long to wait for one answer before giving up on it.
 *
 * Well above the slowest thing measured — a cold global-scope completion at
 * 1 367 ms and a cold Swift hover at 1 919 ms — and well below "forever",
 * because the failure this whole area is written against is a server that
 * answers nothing at all.
 */
export const REQUEST_TIMEOUT_MS = 8_000;

/** `send`, but rejecting after {@link REQUEST_TIMEOUT_MS} rather than hanging. */
export function withTimeout<T>(send: () => Promise<T>): Promise<T> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	const timeout = new Promise<never>((_, reject) => {
		timer = setTimeout(() => reject(new Error("the language server did not answer")), REQUEST_TIMEOUT_MS);
	});
	return Promise.race([send(), timeout]).finally(() => clearTimeout(timer));
}
