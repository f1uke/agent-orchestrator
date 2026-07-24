// Mouse-wheel handling for an attached terminal, shared by every surface that
// hosts one — the board's XtermTerminal and the companion's terminal card — so a
// pane scrolls the same way wherever it is shown, and there is ONE copy of the
// rule rather than one per surface drifting apart.
//
// A raw terminal turns the wheel into cursor-arrow keys inside an alt-screen app,
// which moves the agent's cursor instead of scrolling. What the human means by
// "scroll" depends on what the pane is:
//   - a full-screen app that keeps its own transcript and scrolls it by KEYBOARD
//     (opencode) wants Page Up/Down;
//   - a plain shell printing to the normal buffer wants the terminal's OWN
//     scrollback moved locally;
//   - anything tracking the mouse (tmux/zellij copy-mode, Claude Code's TUI) wants
//     SGR wheel reports, which it acts on itself.
// This picks the right one from the pane's live modes.

import type { Terminal } from "@xterm/xterm";
import type { TerminalUserInputSource } from "../hooks/useTerminalSession";

// SGR button 64 = wheel up, 65 = down; reports are 1-based and a single cell is
// enough for a borderless single pane.
export const SGR_WHEEL_UP = 64;
export const SGR_WHEEL_DOWN = 65;

export function sgrWheelReport(button: number, count: number): string {
	return `\x1b[<${button};1;1M`.repeat(count);
}

// PageUp (CSI 5~) / PageDown (CSI 6~) for pane apps that scroll their transcript by
// keyboard. One page key per wheel notch: a page already scrolls a full screen, so
// scaling by line count would over-scroll.
const PAGE_UP = "\x1b[5~";
const PAGE_DOWN = "\x1b[6~";

export function pageKeyReport(lines: number): string {
	return lines < 0 ? PAGE_UP : PAGE_DOWN;
}

/** The slice of xterm the wheel forwarder reads — modes, buffer, and local scroll. */
type WheelTerminal = Pick<Terminal, "modes" | "buffer" | "scrollLines" | "rows" | "options">;

export type WheelForwarderOptions = {
	/**
	 * Whether the pane scrolls its own transcript by KEYBOARD rather than by wheel
	 * reports (opencode). A getter, not a value, because the surface may learn it
	 * from session config after the handler is attached.
	 */
	paneScrollsByKeyboard: () => boolean;
	/** Send bytes to the pane, tagged so the owner can treat wheel input like any other. */
	emit: (data: string, source: TerminalUserInputSource) => void;
};

/**
 * Build the handler for `term.attachCustomWheelEventHandler`.
 *
 * Returns `false` in every branch to suppress xterm's own arrow-key wheel
 * fallback; Ctrl/Cmd wheel is left for the font-size zoom handler. Pixel deltas
 * (trackpads, macOS) accumulate so a full cell height emits one line; line- and
 * page-mode wheels (many Linux/Windows mice) are honoured directly.
 */
export function createWheelForwarder(
	term: WheelTerminal,
	options: WheelForwarderOptions,
): (event: WheelEvent) => boolean {
	let wheelAccumPx = 0;
	return (event) => {
		if (event.ctrlKey || event.metaKey) return false;
		let lines: number;
		if (event.deltaMode === 1 /* DOM_DELTA_LINE */) {
			lines = Math.trunc(event.deltaY) || Math.sign(event.deltaY);
		} else if (event.deltaMode === 2 /* DOM_DELTA_PAGE */) {
			lines = (Math.trunc(event.deltaY) || Math.sign(event.deltaY)) * term.rows;
		} else {
			const rowHeight = (term.options.fontSize ?? 12) * (term.options.lineHeight ?? 1);
			wheelAccumPx += event.deltaY;
			lines = Math.trunc(wheelAccumPx / rowHeight);
			wheelAccumPx -= lines * rowHeight;
		}
		if (lines === 0) return false;
		// Kept first so opencode is unaffected by the buffer-aware paths below.
		if (options.paneScrollsByKeyboard()) {
			options.emit(pageKeyReport(lines), "wheel");
			return false;
		}
		// Normal-buffer pane with mouse tracking off (a plain shell): scroll the
		// terminal's own scrollback locally; the pane never sees these bytes.
		if (term.modes.mouseTrackingMode === "none" && term.buffer.active.type === "normal") {
			term.scrollLines(lines);
			return false;
		}
		// Mouse tracking on: the pane (tmux/zellij copy-mode, or any app that tracks
		// the mouse — Claude Code) acts on SGR wheel reports itself.
		if (term.modes.mouseTrackingMode !== "none") {
			const button = lines < 0 ? SGR_WHEEL_UP : SGR_WHEEL_DOWN;
			options.emit(sgrWheelReport(button, Math.abs(lines)), "wheel");
			return false;
		}
		// Alt-buffer pane with mouse tracking off and no keyboard-scroll hint: no
		// scrollback to move locally, so fall back to page keys.
		options.emit(pageKeyReport(lines), "wheel");
		return false;
	};
}
