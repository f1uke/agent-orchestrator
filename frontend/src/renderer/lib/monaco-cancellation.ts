/**
 * Monaco's `CancellationError`: control flow, never a fault. VS Code's own
 * `onUnexpectedError` drops it by exactly this name-and-message test
 * (`base/common/errors.js` `isCancellationError`), and so does this app.
 *
 * It has to be named outside the editor because standalone Monaco 0.56 lets one
 * escape as an UNHANDLED REJECTION each time the ghost text changes within 50 ms
 * of the last change: the inline-suggest accessibility signal awaits a
 * cancellable `timeout(50, …)` that nothing catches
 * (`inlineCompletionsController.js:312`). Typing through a prediction would
 * otherwise print `Uncaught (in promise) Canceled` per keystroke and report each
 * one to telemetry as a crash.
 */
export function isMonacoCancellation(reason: unknown): boolean {
	return reason instanceof Error && reason.name === "Canceled" && reason.message === "Canceled";
}
