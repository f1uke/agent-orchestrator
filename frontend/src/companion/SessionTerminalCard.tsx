import { useCallback, useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { buildTerminalThemes } from "../renderer/lib/terminal-themes";
import { createTerminalMux, muxUrlFromApiBase } from "../renderer/lib/terminal-mux";
import {
	useTerminalSession,
	type AttachableTerminal,
	type TerminalSessionState,
	type TerminalUserInputSource,
} from "../renderer/hooks/useTerminalSession";
import type { SessionStatus } from "../renderer/types/workspace";
import { STATUS_LABELS } from "./preview";
import { PROCS_INK, PROCS_RIM_PX, PROP_COLOURS } from "./palette";

// The card a Proc opens: one session's live terminal, floating over the pet it
// belongs to.
//
// Two palettes, on purpose, and the seam between them is the design:
//
//   - The CHROME is wallpaper-robust by the same two-channel rule as the pets and
//     the speech bubble: a light card fill carries dark wallpapers, the 2.4px ink
//     rim carries light ones. The frame around the terminal is a visible band
//     rather than a hairline, because what it has to separate from the desktop is
//     a large DARK rectangle.
//   - The TERMINAL keeps its own palette (DESIGN.md exempts it), so a session
//     looks the same here as it does on the board. Nothing is re-themed.
//
// The attachment is the BOARD's `useTerminalSession` — reattach with backoff, open
// timeouts, exit and error handling. There is one way to hold a pane open in this
// app and this card uses it.

/** How the card says which way its Proc is, since the window cannot draw outside itself. */
export type TailSide = "left" | "centre" | "right";

const CARD_FILL = "#FBFAFD";
const TERMINAL_FONT_SIZE = 12;
/** The tail's footprint. Kept small: it is a tether, not a speech balloon's spout. */
const TAIL_WIDTH = 22;
const TAIL_HEIGHT = 11;

export type SessionTerminalCardProps = {
	/** The pane: a session's `terminalHandleId`. Without one there is nothing to attach. */
	handleId: string;
	/** What the human calls this session — the words on its card on the board. */
	title: string;
	/** What the session is doing, in the companion's own words. */
	status?: SessionStatus;
	/** Daemon base URL from the main process (`http://127.0.0.1:<port>`). */
	daemonUrl: string;
	/** Which way the Proc is, so the tail points at it. */
	tail?: TailSide;
	onClose(): void;
	/** The human is using this window, so it is not idle. */
	onActivity?(): void;
	/** Hand the detach function up, so the hand-off can let the pane go on request. */
	registerDetach?(detach: (() => void) | null): void;
};

const STATE_TEXT: Record<TerminalSessionState, string> = {
	idle: "",
	connecting: "Connecting…",
	attached: "",
	reattaching: "Reconnecting…",
	exited: "This session's terminal has ended.",
	error: "Could not attach to this session.",
};

/**
 * The one distinction the companion draws about a session's state.
 *
 * The board has a four-lane hue system; the overlay deliberately does not, because
 * a pet is an object on a wallpaper rather than a row in a list. What it does have
 * is "this one wants you" versus "this one is getting on with it", which is the
 * same split the speech bubbles use, so the dot says exactly that and no more.
 */
function statusTone(status: SessionStatus | undefined): string {
	if (status === "needs_input" || status === "ci_failed" || status === "changes_requested") return "#e8734a";
	if (status === "no_signal" || status === "terminated" || status === "unknown") return PROP_COLOURS.quiet;
	return "#5fae7a";
}

export function SessionTerminalCard({
	handleId,
	title,
	status,
	daemonUrl,
	tail = "centre",
	onClose,
	onActivity,
	registerDetach,
}: SessionTerminalCardProps) {
	const hostRef = useRef<HTMLDivElement | null>(null);
	const [terminal, setTerminal] = useState<AttachableTerminal | null>(null);

	// The mux is built against the daemon URL this window was handed, not the
	// renderer's API base — the overlay's pages have no api-client configured.
	const createMux = useCallback(() => createTerminalMux(muxUrlFromApiBase(daemonUrl)), [daemonUrl]);
	const { attach, state, error } = useTerminalSession(
		{ terminalHandleId: handleId, status },
		{
			daemonReady: true,
			createMux,
		},
	);

	useEffect(() => {
		const host = hostRef.current;
		if (!host) return;

		const term = new Terminal({
			fontSize: TERMINAL_FONT_SIZE,
			fontFamily:
				'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Monaco, "Cascadia Mono", "Roboto Mono", Consolas, monospace',
			// The terminal's OWN palette, not the card's. A session must not look like
			// a different session because of where you opened it.
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
		const inputListeners = new Set<(data: string, source: TerminalUserInputSource) => void>();
		const emit = (data: string, source: TerminalUserInputSource) => {
			onActivity?.();
			inputListeners.forEach((listener) => listener(data, source));
		};
		const key = term.onKey(({ key: sequence }) => emit(sequence, "keyboard"));
		const textarea = term.textarea;
		const onPaste = (event: ClipboardEvent) => {
			const text = event.clipboardData?.getData("text");
			if (text) emit(text, "paste");
		};
		textarea?.addEventListener("paste", onPaste);

		const bound: AttachableTerminal = {
			get cols() {
				return term.cols;
			},
			get rows() {
				return term.rows;
			},
			write: (bytes) => term.write(bytes),
			writeln: (line) => term.writeln(line),
			clear: () => term.clear(),
			onUserInput: (listener) => {
				inputListeners.add(listener);
				return { dispose: () => inputListeners.delete(listener) };
			},
			onResize: (listener) => {
				const sub = term.onResize(({ cols, rows }) => listener({ cols, rows }));
				return { dispose: () => sub.dispose() };
			},
		};
		setTerminal(bound);
		term.focus();

		// The window is resizable, so the grid follows the frame the human drags.
		const refit = () => fit.fit();
		window.addEventListener("resize", refit);
		const observer = new ResizeObserver(refit);
		observer.observe(host);

		return () => {
			window.removeEventListener("resize", refit);
			observer.disconnect();
			textarea?.removeEventListener("paste", onPaste);
			key.dispose();
			setTerminal(null);
			term.dispose();
		};
	}, [onActivity]);

	// Attach once the terminal exists, and hand the detach up so the hand-off can
	// call it: the pane must be released BEFORE this window is destroyed, because a
	// destroyed window's cleanup never runs.
	useEffect(() => {
		if (!terminal) return;
		const detach = attach(terminal);
		registerDetach?.(detach);
		return () => {
			registerDetach?.(null);
			detach();
		};
	}, [attach, terminal, registerDetach]);

	// Esc closes. A terminal you can only close with the mouse is a terminal that
	// keeps the keyboard when the human has moved on.
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key !== "Escape") return;
			event.preventDefault();
			onClose();
		};
		window.addEventListener("keydown", onKeyDown, true);
		return () => window.removeEventListener("keydown", onKeyDown, true);
	}, [onClose]);

	const note = error ?? STATE_TEXT[state];
	const tone = statusTone(status);

	return (
		<div
			data-session-terminal
			style={{
				position: "absolute",
				inset: 0,
				// The tail hangs BELOW the card, so the card itself stops short of the
				// window's bottom edge and the pointed bit lives in the gap.
				paddingBottom: TAIL_HEIGHT,
				boxSizing: "border-box",
			}}
		>
			<div
				style={{
					height: "100%",
					background: CARD_FILL,
					border: `${PROCS_RIM_PX}px solid ${PROCS_INK}`,
					borderRadius: 14,
					padding: 8,
					display: "flex",
					flexDirection: "column",
					gap: 6,
					boxSizing: "border-box",
				}}
			>
				<div style={{ display: "flex", alignItems: "center", gap: 8, padding: "0 2px" }}>
					<span
						aria-hidden="true"
						title={status ? STATUS_LABELS[status] : undefined}
						style={{
							width: 8,
							height: 8,
							borderRadius: 999,
							background: tone,
							border: `1px solid ${PROCS_INK}`,
							flexShrink: 0,
						}}
					/>
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
					<span
						style={{
							font: "11px/1.2 ui-sans-serif, system-ui, sans-serif",
							color: PROP_COLOURS.bubbleMuted,
							flex: 1,
							overflow: "hidden",
							textOverflow: "ellipsis",
							whiteSpace: "nowrap",
						}}
					>
						{note}
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
			<Tail side={tail} />
		</div>
	);
}

/**
 * The tether: a small pointed tab under the card, aimed at the Proc.
 *
 * The card is its own window, so it cannot draw a line onto the band underneath —
 * but it does not need to. The window is placed directly above its Proc, so a tab
 * on the card's own bottom edge lands in the gap between them and reads as the
 * thing that joins them. It is drawn the way the pets are: an ink rim with the
 * card's fill inside, so it survives any wallpaper the card does.
 */
function Tail({ side }: { side: TailSide }) {
	const position = side === "left" ? { left: 28 } : side === "right" ? { right: 28 } : { left: "50%" };
	return (
		<svg
			data-terminal-tail
			width={TAIL_WIDTH}
			height={TAIL_HEIGHT}
			viewBox={`0 0 ${TAIL_WIDTH} ${TAIL_HEIGHT}`}
			aria-hidden="true"
			style={{
				position: "absolute",
				bottom: 0,
				...position,
				...(side === "centre" ? { transform: `translateX(-${TAIL_WIDTH / 2}px)` } : {}),
				overflow: "visible",
			}}
		>
			{/* The rim first, then the fill over the card's own border line, so the tab
			    and the card read as one shape rather than a triangle stuck to a box. */}
			<path
				d={`M0 0 L${TAIL_WIDTH / 2} ${TAIL_HEIGHT} L${TAIL_WIDTH} 0`}
				fill={CARD_FILL}
				stroke={PROCS_INK}
				strokeWidth={PROCS_RIM_PX}
				strokeLinejoin="round"
			/>
			<rect
				x={PROCS_RIM_PX}
				y={-PROCS_RIM_PX}
				width={TAIL_WIDTH - PROCS_RIM_PX * 2}
				height={PROCS_RIM_PX * 2}
				fill={CARD_FILL}
			/>
		</svg>
	);
}
