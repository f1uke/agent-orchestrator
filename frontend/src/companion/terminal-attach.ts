// PROTOTYPE (terminal bubble): binding a terminal on the OVERLAY to a session's
// pane, over the app's existing multiplexer.
//
// Deliberately the same wire as the board's terminal — `createTerminalMux` and
// `muxUrlFromApiBase` are imported from the renderer, not re-implemented — because
// the daemon's `/mux` is the ONLY way a pane is reached and a second dialect of it
// would be a second thing to keep correct. What this file does NOT reproduce is
// `useTerminalSession`'s lifecycle (reattach backoff, open timeouts, status
// invalidation): that hook needs a React Query client the overlay does not have.
// The build plan's first refactor is to lift that hook off the query client so
// BOTH surfaces run the one attachment; until then this is a thin stand-in whose
// only job is to prove the round trip.
//
// Safety property this file must never break: closing the attachment closes the
// PANE, never the session. `attachment.close()` on the backend "never touches the
// runtime session itself" (backend/internal/terminal/attachment.go), which is what
// makes the hand-off to the board window safe.

import { createTerminalMux, type TerminalMux } from "../renderer/lib/terminal-mux";

export type { TerminalMux };

/** The slice of xterm the attachment drives. Structural, so tests need no DOM. */
export type BubbleTerminal = {
	cols: number;
	rows: number;
	write(bytes: Uint8Array): void;
	onInput(listener: (data: string) => void): { dispose(): void };
	onResize(listener: (size: { cols: number; rows: number }) => void): { dispose(): void };
};

export type BubbleAttachState = "connecting" | "attached" | "exited" | "error";

export type AttachOptions = {
	terminal: BubbleTerminal;
	/** The session's `terminalHandleId` — the pane, not the session row. */
	handleId: string;
	/** ws://127.0.0.1:<port>/mux, derived from the daemon URL the main process hands over. */
	muxUrl: string;
	onState?(state: BubbleAttachState, detail?: string): void;
	/** Test seam. */
	createMux?(url: string): TerminalMux;
};

/** Same trailing debounce the board's terminal uses, for the same reason. */
const RESIZE_DEBOUNCE_MS = 100;

/**
 * Attach and return the detach function.
 *
 * Detach is the load-bearing call: it is what the hand-off to the board window
 * runs BEFORE the app attaches, so one pane is never attached in two places.
 */
export function attachBubbleTerminal(options: AttachOptions): () => void {
	const { terminal, handleId, muxUrl, onState } = options;
	const mux = (options.createMux ?? createTerminalMux)(muxUrl);
	let resizeTimer: ReturnType<typeof setTimeout> | null = null;
	let live = true;

	onState?.("connecting");

	const disposers: Array<() => void> = [
		mux.onData(handleId, (bytes) => {
			if (live) terminal.write(bytes);
		}),
		mux.onOpened(handleId, () => {
			if (live) onState?.("attached");
		}),
		mux.onExit(handleId, () => {
			if (live) onState?.("exited");
		}),
		mux.onError(handleId, (message) => {
			if (live) onState?.("error", message);
		}),
	];

	const input = terminal.onInput((data) => {
		if (live) mux.sendInput(handleId, data);
	});
	const resize = terminal.onResize(({ cols, rows }) => {
		if (!live) return;
		if (resizeTimer) clearTimeout(resizeTimer);
		resizeTimer = setTimeout(() => {
			resizeTimer = null;
			if (live) mux.resize(handleId, cols, rows);
		}, RESIZE_DEBOUNCE_MS);
	});

	mux.open(handleId, terminal.cols, terminal.rows);
	mux.resize(handleId, terminal.cols, terminal.rows);

	return () => {
		if (!live) return;
		live = false;
		if (resizeTimer) clearTimeout(resizeTimer);
		input.dispose();
		resize.dispose();
		disposers.forEach((dispose) => dispose());
		// Close the PANE first, then the socket: the daemon frees this client's
		// `tmux attach` on the close frame, and a socket dropped without it would
		// leave the attach process alive until the daemon noticed.
		mux.close(handleId);
		mux.dispose();
	};
}
