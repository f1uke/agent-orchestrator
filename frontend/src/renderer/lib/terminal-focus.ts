// Imperative focus for the active terminal pane.
//
// The terminal is the app's primary work surface, but it lives deep in the
// layout (CenterPane) while the things that should hand focus back to it —
// closing the New task dialog, switching sessions, dismissing a toolbar
// overlay — live elsewhere (the top bar, the router). Rather than thread a ref
// through the tree, the mounted XtermTerminal registers its focus function here
// and any component can call focusTerminal() to return the caret to the
// terminal so the user can type immediately.
//
// Only one terminal pane is active at a time (CenterPane shows exactly one —
// worker, orchestrator, or reviewer), so a single slot is enough; the most
// recently mounted pane wins and a stale unregister never clears a newer one.

let activeFocus: (() => void) | null = null;

/**
 * Register the active terminal's focus function. Returns a disposer that clears
 * the slot only if this registration is still the active one (so a slow-unmount
 * of the previous pane can't wipe the pane that replaced it).
 */
export function registerTerminalFocus(focus: () => void): () => void {
	activeFocus = focus;
	return () => {
		if (activeFocus === focus) activeFocus = null;
	};
}

/**
 * Focus the active terminal, if one is mounted. Returns whether a terminal was
 * focused — callers (e.g. an overlay's onCloseAutoFocus) use this to decide
 * whether to preventDefault the framework's own focus return, or let it happen
 * when there is no terminal to return to (e.g. on the board).
 */
export function focusTerminal(): boolean {
	if (!activeFocus) return false;
	activeFocus();
	return true;
}

/**
 * `onCloseAutoFocus` handler for an overlay that opens over the terminal (the
 * New task dialog, the restart-session confirm, …). Returns the caret to the
 * terminal on close and suppresses the framework's default focus return so it
 * doesn't land back on the trigger. When no terminal is mounted (e.g. the
 * overlay was opened from the board), it does nothing and lets the default
 * behavior restore focus to the trigger.
 */
export function returnFocusToTerminal(event: { preventDefault: () => void }): void {
	if (focusTerminal()) event.preventDefault();
}
