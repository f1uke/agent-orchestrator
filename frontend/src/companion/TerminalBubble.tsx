import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { buildTerminalThemes } from "../renderer/lib/terminal-themes";
import { muxUrlFromApiBase } from "../renderer/lib/terminal-mux";
import { attachBubbleTerminal, type BubbleAttachState } from "./terminal-attach";
import { PROCS_INK, PROCS_RIM_PX } from "./palette";

// PROTOTYPE (terminal bubble): the big chat bubble with a LIVE terminal in it.
//
// Two palettes, on purpose, and the seam between them is the design:
//
//   - The CHROME is wallpaper-robust, by the same two-channel rule as the speech
//     bubble and the pets: a light card fill carries dark wallpapers, the 2.4px
//     ink rim carries light ones. The card frame is a visible band around the
//     terminal rather than a hairline, because the thing it has to separate from
//     the desktop is a large DARK rectangle.
//   - The TERMINAL keeps its own palette (DESIGN.md exempts it), so a session
//     looks the same here as it does on the board. Nothing is re-themed.
//
// It is `focusable: false` on the window that makes this window scenery; the main
// process flips that for as long as one bubble is open (see companion-window.ts).
// This component only decides WHEN, by mounting and unmounting.

/** The card's outer size. Big enough to be a terminal, not so big it is a window. */
export const BUBBLE_TERMINAL_WIDTH = 720;
export const BUBBLE_TERMINAL_HEIGHT = 420;

const CARD_FILL = "#FBFAFD";
const TERMINAL_FONT_SIZE = 12;

export type TerminalBubbleProps = {
	/** The pane: a session's `terminalHandleId`. Without one there is nothing to attach. */
	handleId: string;
	/** What the human calls this session — the words on its card, same as the name tag. */
	title: string;
	/** Daemon base URL from the main process (`http://127.0.0.1:<port>`). */
	daemonUrl: string;
	/** Fill the window it is drawn in, rather than sizing itself. */
	fills?: boolean;
	onClose(): void;
};

const STATE_TEXT: Record<BubbleAttachState, string> = {
	connecting: "Connecting…",
	attached: "",
	exited: "This session's terminal ended.",
	error: "Could not attach to this session.",
};

export function TerminalBubble({ handleId, title, daemonUrl, fills, onClose }: TerminalBubbleProps) {
	const hostRef = useRef<HTMLDivElement | null>(null);
	const [state, setState] = useState<BubbleAttachState>("connecting");
	const [detail, setDetail] = useState<string | undefined>(undefined);

	useEffect(() => {
		const host = hostRef.current;
		if (!host) return;

		const term = new Terminal({
			fontSize: TERMINAL_FONT_SIZE,
			fontFamily:
				'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Monaco, "Cascadia Mono", "Roboto Mono", Consolas, monospace',
			// The terminal's OWN palette, not the overlay's. A session must not look
			// like a different session because of where you opened it.
			theme: buildTerminalThemes().dark,
			allowProposedApi: true,
			cursorBlink: true,
			scrollback: 5_000,
		});
		const fit = new FitAddon();
		term.loadAddon(fit);
		term.open(host);
		fit.fit();

		// Only keyboard and paste are forwarded — never xterm's raw `onData`, which
		// also carries the terminal's own answers to the attach handshake and would
		// corrupt the TUI (the rule XtermTerminal.tsx documents at length).
		const inputListeners = new Set<(data: string) => void>();
		const emit = (data: string) => inputListeners.forEach((listener) => listener(data));
		const listeners: Array<() => void> = [];
		const key = term.onKey(({ key: sequence }) => emit(sequence));
		const paste = term.textarea;
		const onPaste = (event: ClipboardEvent) => {
			const text = event.clipboardData?.getData("text");
			if (text) emit(text);
		};
		paste?.addEventListener("paste", onPaste);
		listeners.push(
			() => key.dispose(),
			() => paste?.removeEventListener("paste", onPaste),
		);

		const detach = attachBubbleTerminal({
			terminal: {
				get cols() {
					return term.cols;
				},
				get rows() {
					return term.rows;
				},
				write: (bytes) => term.write(bytes),
				onInput: (listener) => {
					inputListeners.add(listener);
					return { dispose: () => inputListeners.delete(listener) };
				},
				onResize: (listener) => {
					const sub = term.onResize(({ cols, rows }) => listener({ cols, rows }));
					return { dispose: () => sub.dispose() };
				},
			},
			handleId,
			muxUrl: muxUrlFromApiBase(daemonUrl),
			onState: (next, why) => {
				setState(next);
				setDetail(why);
			},
		});

		// PROTOTYPE seam: lets the harness type into this terminal exactly as the
		// keyboard does, without an OS keystroke the agent's shell is not allowed to
		// post. It goes through the SAME input path as a real key.
		(window as unknown as { __aoBubbleType?: (text: string) => void }).__aoBubbleType = (text: string) => emit(text);

		term.focus();
		const onWindowResize = () => fit.fit();
		window.addEventListener("resize", onWindowResize);

		return () => {
			window.removeEventListener("resize", onWindowResize);
			delete (window as unknown as { __aoBubbleType?: (text: string) => void }).__aoBubbleType;
			detach();
			listeners.forEach((dispose) => dispose());
			term.dispose();
		};
	}, [handleId, daemonUrl]);

	// Esc closes. A terminal that can only be closed with the mouse is a terminal
	// that keeps the keyboard when the user has moved on.
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				event.preventDefault();
				onClose();
			}
		};
		window.addEventListener("keydown", onKeyDown, true);
		return () => window.removeEventListener("keydown", onKeyDown, true);
	}, [onClose]);

	const status = STATE_TEXT[state];

	return (
		<div
			// The whole card takes the pointer: this is the ONE region of the overlay
			// that is a surface rather than scenery. `pointer-region.ts` reads the
			// attribute, so the window stays click-through everywhere else.
			data-companion-interactive="true"
			style={{
				// In its own window the card IS the window: the frame is transparent, so
				// the rim and the radius below are what the human sees on the wallpaper.
				width: fills ? "100%" : BUBBLE_TERMINAL_WIDTH,
				height: fills ? "100%" : BUBBLE_TERMINAL_HEIGHT,
				background: CARD_FILL,
				border: `${PROCS_RIM_PX}px solid ${PROCS_INK}`,
				borderRadius: 14,
				padding: 8,
				display: "flex",
				flexDirection: "column",
				gap: 6,
				boxSizing: "border-box",
				pointerEvents: "auto",
			}}
		>
			<div style={{ display: "flex", alignItems: "center", gap: 8, padding: "0 2px" }}>
				<span
					style={{
						font: "600 12px/1.2 ui-sans-serif, system-ui, -apple-system, sans-serif",
						color: PROCS_INK,
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
					}}
				>
					{title}
				</span>
				<span style={{ font: "11px/1.2 ui-sans-serif, system-ui, sans-serif", color: "#5b5766", flex: 1 }}>
					{status}
					{detail ? ` ${detail}` : ""}
				</span>
				<button
					type="button"
					onClick={onClose}
					aria-label="Close terminal"
					style={{
						font: "12px/1 ui-sans-serif, system-ui, sans-serif",
						color: PROCS_INK,
						background: "transparent",
						border: "none",
						cursor: "pointer",
						padding: 4,
					}}
				>
					✕
				</button>
			</div>
			<div
				ref={hostRef}
				style={{
					flex: 1,
					minHeight: 0,
					borderRadius: 8,
					overflow: "hidden",
					background: buildTerminalThemes().dark.background,
					padding: 6,
				}}
			/>
		</div>
	);
}
