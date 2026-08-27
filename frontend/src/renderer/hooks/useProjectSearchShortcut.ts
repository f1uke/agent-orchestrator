import { useEffect, useRef } from "react";

/**
 * ⌘⇧F (Ctrl+Shift+F off a Mac) — search the contents of every file.
 *
 * A hook rather than a listener inline in `SessionView` for the reason #247
 * established for ⌘⇧O: a chord is worth a test of its own, both for what it
 * fires on and for what it must NOT fire on, and a listener buried in a
 * 900-line component can only be tested by mounting that component.
 *
 * CAPTURE phase, because Monaco and xterm both take keys before they reach the
 * window — the same reason back/forward and `SplitTreeView` capture. Monaco owns
 * ⌘F for its own find widget, so a bubbling listener would be racing a
 * neighbouring chord rather than merely arriving late.
 *
 * `⌥` is excluded explicitly: ⌥⌘F is Monaco's find-and-replace, and a chord that
 * fires on a superset of its own keys steals a shortcut the editor already has.
 */
export function useProjectSearchShortcut(enabled: boolean, onRequest: () => void): void {
	// Read from a ref so a caller that rebuilds its callback every render — which
	// SessionView does — does not re-subscribe the listener on every render.
	const requestRef = useRef(onRequest);
	requestRef.current = onRequest;

	useEffect(() => {
		if (!enabled) return undefined;
		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.key.toLowerCase() !== "f" || !event.shiftKey || event.altKey) return;
			if (!event.metaKey && !event.ctrlKey) return;
			event.preventDefault();
			event.stopPropagation();
			requestRef.current();
		};
		window.addEventListener("keydown", handleKeyDown, true);
		return () => window.removeEventListener("keydown", handleKeyDown, true);
	}, [enabled]);
}
