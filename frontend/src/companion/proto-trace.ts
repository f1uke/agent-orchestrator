// PROTOTYPE instrument. A rolling record of everything that decides whether a
// click lands on a Proc, so a human can reproduce a bug with their own hand and
// the answer can be read back afterwards.
//
// The thing it exists to settle: when a click "goes through" to the desktop, did
// the page receive a pointerdown at all? If it did, the overlay's own state was
// wrong. If it did not, macOS never delivered the click to us — which is a
// different bug entirely, and no amount of renderer code can fix it.
//
// Only loaded when the prototype harness asks for it (see companion/main.tsx).

export type TraceEntry = { t: number; what: string; detail?: string };

const MAX_ENTRIES = 400;

export function startProtoTrace(): void {
	const entries: TraceEntry[] = [];
	const started = performance.now();
	const push = (what: string, detail?: string) => {
		entries.push({ t: Math.round(performance.now() - started), what, detail });
		if (entries.length > MAX_ENTRIES) entries.shift();
	};

	const where = (event: PointerEvent | MouseEvent): string => {
		const target = event.target as Element | null;
		const onPet = Boolean(target?.closest?.("[data-figure]"));
		const onCard = Boolean(target?.closest?.("[data-companion-interactive]"));
		return `${Math.round(event.clientX)},${Math.round(event.clientY)} ${onPet ? "PET" : onCard ? "CARD" : "band"}${event.isTrusted ? "" : " (synthetic)"}`;
	};

	document.addEventListener("pointerdown", (event) => push("pointerdown", where(event)), true);
	document.addEventListener("pointerup", (event) => push("pointerup", where(event)), true);
	document.addEventListener("pointerleave", () => push("pointerleave"), true);
	// Moves are the busiest event by far; record only that they are still arriving,
	// once every 250ms, because "did any move arrive at all" is the question.
	let lastMove = 0;
	document.addEventListener(
		"pointermove",
		(event) => {
			const now = performance.now();
			if (now - lastMove < 250) return;
			lastMove = now;
			push("pointermove", where(event));
		},
		true,
	);
	window.addEventListener("focus", () => push("window focus"));
	window.addEventListener("blur", () => push("window blur"));
	document.addEventListener("visibilitychange", () => push("visibility", document.visibilityState));

	const api = {
		note: push,
		dump: () =>
			entries.map((e) => `${String(e.t).padStart(6)}ms  ${e.what}${e.detail ? "  " + e.detail : ""}`).join("\n"),
		entries: () => [...entries],
		clear: () => {
			entries.length = 0;
		},
	};
	(window as unknown as { __aoTrace?: typeof api }).__aoTrace = api;
	push("trace started");
}

/** Record something from elsewhere in the overlay (no-op when the trace is off). */
export function protoNote(what: string, detail?: string): void {
	(window as unknown as { __aoTrace?: { note(what: string, detail?: string): void } }).__aoTrace?.note(what, detail);
}
