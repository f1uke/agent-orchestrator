// Self-contained xterm.js surface, ported from yyork's terminal architecture.
//
// Design rules (the reason this component exists):
//  - The mount effect is dependency-free: the terminal instance is created once
//    per mount and NEVER torn down because a callback identity changed.
//    TerminalPane chooses the mount lifetime; it keys mounts by terminal handle
//    so session switches get a clean surface, while same-handle reconnects reuse
//    the mounted renderer.
//  - Nothing writes into the buffer at mount. Status/empty-state belongs to DOM
//    chrome around the terminal, not inside it. Writing before layout settles
//    is what crashed xterm's Viewport (`dimensions` of a zero-sized renderer).
//  - Fitting runs on several triggers, not one: FitAddon derives the grid from
//    the measured cell box, and if it measures before the monospace font's real
//    metrics (and the post-open renderer) are resolved it mis-counts cols/rows
//    and the grid clips inside the panel. So: next frame, two settle timeouts,
//    fonts.ready, a ResizeObserver, AND an onRender convergence loop that
//    re-fits until the proposed grid stops changing (the last is the only
//    trigger that recovers a clipped grid without the host box resizing). xterm
//    itself only fires onResize when the grid actually changed, so repeated
//    fits don't spam the PTY.

import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { CanvasAddon } from "@xterm/addon-canvas";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import type { AttachableTerminal, TerminalUserInputSource } from "../hooks/useTerminalSession";
import { aoBridge } from "../lib/bridge";
import type { SessionLinkMatch } from "../lib/session-ref";
import type { ExternalRefMatch } from "../lib/terminal-scm-links";
import type { FileLinkMatch } from "../lib/terminal-file-links";
import { registerTerminalFocus } from "../lib/terminal-focus";
import { buildTerminalThemes } from "../lib/terminal-themes";
import type { Theme } from "../stores/ui-store";

export type XtermTerminalProps = {
	ariaLabel?: string;
	className?: string;
	fontSize?: number;
	theme: Theme;
	/**
	 * Focus the terminal as soon as it mounts. TerminalPane sets this for an
	 * attached session terminal so switching to a worker/orchestrator drops the
	 * caret straight into the terminal — no click needed before typing.
	 */
	autoFocus?: boolean;
	/**
	 * The pane app scrolls its transcript by keyboard (PageUp/PageDown) rather
	 * than acting on SGR wheel reports — e.g. opencode, which enables mouse
	 * tracking but never scrolls on wheel reports. Routes the wheel to page keys
	 * on every platform (see the wheel handler), fixing it under a mux too.
	 */
	paneScrollsByKeyboard?: boolean;
	/**
	 * Resolve AO session-id references on a line of terminal text to link ranges.
	 * TerminalPane supplies this from live workspace data; when a matched token
	 * names a known session, the token is linkified and clicking it fires
	 * {@link onSessionLinkActivate}. Absent → no session linkification.
	 */
	sessionLinkResolver?: (line: string) => SessionLinkMatch[];
	/**
	 * Navigate the app to a session when its terminal reference is clicked. This
	 * is internal navigation (select the session on the board), NOT the OS
	 * browser — unlike http/OSC-8 links which go through openTerminalLink.
	 */
	onSessionLinkActivate?: (sessionId: string) => void;
	/**
	 * Resolve SCM reference tokens (`#<num>` GitHub PR/issue, `!<num>` GitLab MR)
	 * on a line of terminal text to link ranges + external URLs. TerminalPane
	 * supplies this from the session's own remote(s); the URL is opened in the OS
	 * browser (via {@link openTerminalLink}), NOT navigated internally like a
	 * session ref. Absent → no PR/MR linkification.
	 */
	externalRefResolver?: (line: string) => ExternalRefMatch[];
	/**
	 * Resolve FILE reference tokens (absolute path, workspace-relative path, or a
	 * bare filename with a code extension) on a line of terminal text to link
	 * ranges. TerminalPane supplies this from terminal-file-links; a clicked token
	 * fires {@link onFileLinkActivate}, which resolves it against the session's
	 * workspace and opens it in the code viewer INTERNALLY (not the OS browser).
	 * Absent → no file linkification.
	 */
	fileLinkResolver?: (line: string) => FileLinkMatch[];
	/** Open a clicked file reference (resolve within the workspace, then view). */
	onFileLinkActivate?: (match: FileLinkMatch) => void;
	/** Terminal construction failed; the owner decides how to surface it. */
	onError?: (error: unknown) => void;
	/**
	 * The terminal is open in the DOM and ready to be attached to a PTY. The
	 * handle stays valid until unmount; cols/rows are live getters.
	 */
	onReady?: (terminal: AttachableTerminal) => void;
};

// Prefer the WebGL renderer, fall back to 2D canvas. Both rasterize box-drawing
// glyphs themselves onto a fixed cell grid; the DOM renderer does not, so TUI
// borders would drift. Loaded after open().
function loadRenderer(term: Terminal): void {
	try {
		const webgl = new WebglAddon();
		webgl.onContextLoss(() => webgl.dispose());
		term.loadAddon(webgl);
		return;
	} catch {
		// WebGL context unavailable — fall through to the canvas renderer.
	}
	try {
		term.loadAddon(new CanvasAddon());
	} catch (error) {
		console.warn("xterm: WebGL and canvas renderers unavailable; box-drawing may drift", error);
	}
}

// xterm palette tracks the app theme (see lib/terminal-themes.ts + --term-* in
// styles.css). The PTY content is still the agent's own ANSI output.
const terminalThemes = buildTerminalThemes();
const SUPPRESS_NATIVE_PASTE_MS = 100;

// Erase scrollback (3J) + display (2J) and home the cursor. Deliberately NOT
// term.reset(): every pane PTY is a fresh per-client attach whose handshake
// re-asserts terminal modes anyway, but a full RIS would drop them until that
// handshake arrives. The clear only wipes pixels; modes stay up.
const CLEAR_SEQUENCE = "\x1b[3J\x1b[2J\x1b[H";

// An SGR / SGR-Pixels mouse report: ESC [ < btn ; col ; row (M|m). The "\x1b[<"
// prefix is unique to SGR mouse reports in the terminal protocol, so matching it
// lets us forward ONLY mouse reports out of xterm's onData stream and leave every
// terminal-generated control response (DA/DSR/DECRPM/focus/OSC/window ops) behind
// — those must never be written back to the PTY (see the onData note below).
// Exported so a test can assert it matches real xterm's actual output format.
export const SGR_MOUSE_REPORT = /^\x1b\[<\d+;\d+;\d+[Mm]$/;

// Open a link the terminal surfaced. Auto-detected http(s) links (WebLinksAddon)
// and http(s) OSC 8 hyperlinks go through window.open, which the Electron main
// process routes to the OS browser (main.ts setWindowOpenHandler). Non-http OSC 8
// links — file:// for the .md links Claude Code / Superpowers emit — are denied by
// that handler, so they go through a dedicated bridge that opens them via the OS
// after a scheme allowlist check in the main process.
function openTerminalLink(uri: string): void {
	if (/^https?:\/\//i.test(uri)) {
		window.open(uri, "_blank", "noopener");
		return;
	}
	void aoBridge.shell.openExternal(uri).catch((error) => {
		console.warn("Unable to open terminal link", uri, error);
	});
}

function preparePastedText(text: string): string {
	return text.replace(/\r?\n/g, "\r");
}

function bracketPastedText(text: string, bracketedPasteMode: boolean): string {
	return bracketedPasteMode ? `\x1b[200~${text}\x1b[201~` : text;
}

function isTerminalCopyShortcut(event: KeyboardEvent): boolean {
	if (event.key === "Insert") return event.ctrlKey && !event.altKey && !event.metaKey;
	if (event.key.toLowerCase() !== "c") return false;
	if (event.metaKey) return true;
	if (event.ctrlKey && event.shiftKey && !event.altKey) return true;
	return isWindowsPlatform() && event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey;
}

function isWindowsPlatform(): boolean {
	const platform =
		(navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ?? navigator.platform;
	return platform.toLowerCase().startsWith("win");
}

function isTerminalPasteShortcut(event: KeyboardEvent): boolean {
	if (event.key === "Insert") return event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey;
	if (event.key.toLowerCase() !== "v") return false;
	if (event.metaKey) return true;
	if (event.ctrlKey && event.shiftKey && !event.altKey) return true;
	return isWindowsPlatform() && event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey;
}

// Shift+Enter: a bare line feed instead of xterm's default carriage return.
// xterm maps Shift+Enter to the same \r as plain Enter, so a TUI in the PTY
// (e.g. Claude Code) reads it as submit rather than newline. Sending \n mirrors
// what iTerm2 wires up via Claude Code's /terminal-setup. Guard on keydown so a
// single press emits once (the handler also fires on keyup); plain Enter and any
// modifier combo (Ctrl/Alt/Meta+Enter) fall through untouched.
function isTerminalNewlineShortcut(event: KeyboardEvent): boolean {
	if (event.type !== "keydown" || event.key !== "Enter") return false;
	return event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey;
}

function consumeTerminalShortcut(event: KeyboardEvent): void {
	event.preventDefault();
	event.stopPropagation();
}

function normalizedTerminalShortcut(event: KeyboardEvent): string | null {
	if (event.metaKey || event.shiftKey) return null;

	if (event.altKey && !event.ctrlKey) {
		switch (event.key) {
			case "ArrowLeft":
				return "\x1bb";
			case "ArrowRight":
				return "\x1bf";
			case "Backspace":
				return "\x1b\x7f";
			case "Delete":
				return "\x1bd";
			default:
				return null;
		}
	}

	if (event.ctrlKey && !event.altKey) {
		switch (event.key) {
			case "ArrowLeft":
				return "\x1b[1;5D";
			case "ArrowRight":
				return "\x1b[1;5C";
			case "Backspace":
				return "\x1b\x7f";
			case "Delete":
				return "\x1bd";
			default:
				return null;
		}
	}

	return null;
}

function terminalHasFocus(host: HTMLElement): boolean {
	const activeElement = document.activeElement;
	return !!activeElement && host.contains(activeElement);
}

// For mouse-tracking panes we synthesize SGR mouse-wheel reports and write them
// to the pane; tmux (with `mouse on`, set by the runtime adapter) acts on them
// and scrolls its scrollback via copy-mode. Left to itself xterm would convert
// the wheel into cursor-arrow keys (its alt-buffer fallback), which move the
// agent's cursor rather than scrolling. SGR button 64 = wheel up, 65 = down;
// reports are 1-based and a single cell is enough for a borderless single pane.
const SGR_WHEEL_UP = 64;
const SGR_WHEEL_DOWN = 65;

function sgrWheelReport(button: number, count: number): string {
	return `\x1b[<${button};1;1M`.repeat(count);
}

// PageUp (CSI 5~) / PageDown (CSI 6~) for pane apps that scroll their transcript
// by keyboard rather than mouse reports. One page key per wheel notch: a page
// already scrolls a full screen, so scaling by line count would over-scroll.
const PAGE_UP = "\x1b[5~";
const PAGE_DOWN = "\x1b[6~";

function pageKeyReport(lines: number): string {
	return lines < 0 ? PAGE_UP : PAGE_DOWN;
}

export function XtermTerminal(props: XtermTerminalProps) {
	const hostRef = useRef<HTMLDivElement | null>(null);
	const termRef = useRef<Terminal | null>(null);
	const fitRef = useRef<(() => void) | null>(null);
	// Latest callbacks in a ref so the mount effect stays dependency-free — we
	// never tear down and recreate the terminal because a handler identity
	// changed between renders.
	const callbacksRef = useRef(props);

	useEffect(() => {
		callbacksRef.current = props;
	});

	useEffect(() => {
		const term = termRef.current;
		if (!term) return;
		term.options.theme = props.theme === "dark" ? terminalThemes.dark : terminalThemes.light;
	}, [props.theme]);

	useEffect(() => {
		const term = termRef.current;
		if (!term || !props.fontSize) return undefined;
		term.options.fontSize = props.fontSize;
		fitRef.current?.();
		const timer = window.setTimeout(() => fitRef.current?.(), 50);
		return () => window.clearTimeout(timer);
	}, [props.fontSize]);

	useEffect(() => {
		const host = hostRef.current;
		if (!host) return undefined;

		let term: Terminal;
		try {
			term = new Terminal({
				// Required for the Unicode 11 width addon below.
				allowProposedApi: true,
				cursorBlink: true,
				// Resolve the Nerd Font stack from --font-mono (styles.css) at
				// construction so terminal glyphs follow the app's font tokens. The
				// box-drawing grid is rasterized by the WebGL/canvas renderer itself,
				// but powerline separators and file-type icons are real PUA codepoints
				// that must come from a system-installed Nerd Font.
				fontFamily:
					getComputedStyle(host).getPropertyValue("--font-mono").trim() ||
					'ui-monospace, Menlo, Monaco, "Courier New", monospace',
				fontSize: props.fontSize ?? 12,
				lineHeight: 1.35,
				// Agent TUIs leave SGR bold active while using ANSI black for
				// separators; keep bold weight-only so black stays black.
				drawBoldTextInBrightColors: false,
				// Auto-adjust glyph colors that don't clear WCAG AA against their cell
				// background, the way VS Code's terminal does; without it dim colors
				// render washed out.
				minimumContrastRatio: 4.5,
				// Alt-buffer panes (tmux attach, mouse-tracking agent TUIs) never feed
				// this buffer — the alt screen doesn't accumulate scrollback — so this
				// only matters for normal-buffer panes that print their transcript and
				// rely on the terminal's scrollback (codex, a plain shell). Keep it > 0
				// so that history survives to be scrolled locally (see the wheel
				// handler's normal-buffer branch). The scrollbar itself is hidden in
				// CSS so FitAddon's ~14px reservation doesn't shift the grid.
				scrollback: 5000,
				theme: props.theme === "dark" ? terminalThemes.dark : terminalThemes.light,
				// OSC 8 hyperlinks (\x1b]8;;URI\x1b\ text \x1b]8;;\x1b\), as Claude Code /
				// Superpowers emit for .md file links. WebLinksAddon only covers
				// auto-detected http(s) in plain text; this handles explicit hyperlinks of
				// any scheme. allowNonHttpProtocols lets file:// links through (xterm drops
				// non-http OSC 8 links otherwise), and our own activate replaces xterm's
				// default handler — whose confirm() dialog would freeze the renderer.
				linkHandler: {
					allowNonHttpProtocols: true,
					activate: (_event, uri) => openTerminalLink(uri),
				},
			});
		} catch (error) {
			callbacksRef.current.onError?.(error);
			return undefined;
		}

		termRef.current = term;

		const fit = new FitAddon();
		term.loadAddon(fit);
		const unicode = new Unicode11Addon();
		term.loadAddon(unicode);
		term.unicode.activeVersion = "11";
		// Open links in the OS browser. The default WebLinksAddon handler calls
		// window.open() with no URL and then assigns location.href, but the
		// Electron main process denies every window.open and only forwards the URL
		// passed to it (main.ts setWindowOpenHandler), so the default handler's
		// empty open is dropped and clicks silently no-op. Pass the matched URL to
		// window.open directly so the main process routes it to shell.openExternal.
		term.loadAddon(
			new WebLinksAddon((_event, uri) => {
				window.open(uri, "_blank", "noopener");
			}),
		);
		term.loadAddon(new SearchAddon());

		term.open(host);
		loadRenderer(term);
		// Native modifier-based selection (like iTerm2 / Terminal.app / VS Code): when
		// the agent TUI has mouse tracking on, a plain click/drag is a mouse report the
		// app acts on (so "Ran shell command" and file links are clickable), and holding
		// Option (mac) / Shift forces LOCAL text selection for copy. When no app mouse
		// mode is active (plain shell, scrollback) a plain drag still selects. We do NOT
		// force selection unconditionally — that made xterm's mousedown handler swallow
		// every click before it could emit a report (Terminal.bindMouse bails when
		// shouldForceSelection is true), which is exactly why clickables were dead.
		term.options.macOptionClickForcesSelection = true;

		let lastCopiedSelection = "";
		const copySelection = (options?: { clipboardData?: DataTransfer | null; dedupe?: boolean }) => {
			const selection = term.getSelection();
			if (!selection || (options?.dedupe && selection === lastCopiedSelection)) return false;
			options?.clipboardData?.setData("text/plain", selection);
			void aoBridge.clipboard
				.writeText(selection)
				.then(() => {
					lastCopiedSelection = selection;
				})
				.catch((error) => {
					console.warn("Unable to copy terminal selection", error);
				});
			return true;
		};
		const clearCopiedSelection = () => {
			lastCopiedSelection = "";
		};
		const userInputListeners = new Set<(data: string, source: TerminalUserInputSource) => void>();
		const emitUserInput = (data: string, source: TerminalUserInputSource) => {
			if (data.length === 0) return;
			userInputListeners.forEach((listener) => listener(data, source));
		};
		const pasteText = (text: string) => {
			const prepared = preparePastedText(text);
			const bracketed = term.modes.bracketedPasteMode && term.options.ignoreBracketedPasteMode !== true;
			emitUserInput(bracketPastedText(prepared, bracketed), "paste");
		};
		let suppressNextNativePaste = false;
		let suppressPasteTimer: number | null = null;
		const clearSuppressNativePaste = () => {
			suppressNextNativePaste = false;
			if (suppressPasteTimer !== null) {
				window.clearTimeout(suppressPasteTimer);
				suppressPasteTimer = null;
			}
		};
		const suppressNativePasteOnce = () => {
			suppressNextNativePaste = true;
			if (suppressPasteTimer !== null) window.clearTimeout(suppressPasteTimer);
			suppressPasteTimer = window.setTimeout(clearSuppressNativePaste, SUPPRESS_NATIVE_PASTE_MS);
		};
		const pasteFromClipboard = () => {
			void aoBridge.clipboard
				.readText()
				.then(pasteText)
				.catch((error) => {
					console.warn("Unable to paste terminal clipboard text", error);
				});
		};
		term.attachCustomKeyEventHandler((event) => {
			if (isTerminalCopyShortcut(event)) {
				if (copySelection()) {
					consumeTerminalShortcut(event);
					return false;
				}
				if ((event.ctrlKey && event.shiftKey) || (event.key === "Insert" && event.ctrlKey)) {
					consumeTerminalShortcut(event);
					return false;
				}
				return true;
			}
			if (isTerminalPasteShortcut(event)) {
				consumeTerminalShortcut(event);
				suppressNativePasteOnce();
				pasteFromClipboard();
				return false;
			}
			if (isTerminalNewlineShortcut(event)) {
				consumeTerminalShortcut(event);
				emitUserInput("\n", "shortcut");
				return false;
			}
			const normalized = normalizedTerminalShortcut(event);
			if (!normalized) return true;
			consumeTerminalShortcut(event);
			emitUserInput(normalized, "shortcut");
			return false;
		});
		const copyInput = (event: ClipboardEvent) => {
			if (!copySelection({ clipboardData: event.clipboardData })) return;
			event.preventDefault();
		};
		const copyShortcut = (event: KeyboardEvent) => {
			if (!isTerminalCopyShortcut(event) || !terminalHasFocus(host) || !copySelection()) return;
			event.preventDefault();
			event.stopPropagation();
		};
		// A pointer press anywhere in the terminal host focuses the terminal, so a
		// single click is enough to start typing even when focus was elsewhere — a
		// top-bar button, or a popover/dropdown/dialog that the same click just
		// dismissed. xterm focuses its helper textarea when you press on its screen,
		// but not reliably on host padding or right after an overlay yielded focus;
		// this makes the whole surface reclaim focus on one click. It never
		// preventDefaults, so drag-to-select is untouched.
		const focusTerminal = () => term.focus();
		host.addEventListener("mousedown", focusTerminal);
		// Register as the active terminal so anything that dismisses a transient
		// surface (the New task dialog, a toolbar overlay) can hand the caret back
		// here; and, when this pane is the one being switched to, grab focus on
		// mount so the user can type immediately without a first click.
		const unregisterFocus = registerTerminalFocus(focusTerminal);
		if (callbacksRef.current.autoFocus) focusTerminal();
		host.addEventListener("copy", copyInput);
		window.addEventListener("keydown", copyShortcut, true);
		const selectionChange = term.onSelectionChange(() => {
			if (!term.hasSelection()) {
				clearCopiedSelection();
				return;
			}
			window.setTimeout(() => copySelection({ dedupe: true }), 0);
		});

		const fitTerminal = () => {
			try {
				fit.fit();
			} catch {
				// Container momentarily has no size (hidden/unmounting) — a later
				// trigger retries.
			}
		};
		fitRef.current = fitTerminal;

		const raf = requestAnimationFrame(fitTerminal);
		// 50/250ms catch the common settle; 600/1200ms are a session-bounded
		// backstop. By 600ms the WebGL atlas and font metrics are unambiguously
		// warm, so even if the convergence loop below detached at a briefly-stable
		// wrong measurement, this re-measures the real cell box and corrects,
		// firing the PTY resize that makes the pane repaint cleanly (clearing
		// any ghost frame). fit() is idempotent: a no-op when the grid is already
		// right, so a correct terminal never reflows.
		const settleTimers = [50, 250, 600, 1200].map((ms) => window.setTimeout(fitTerminal, ms));
		if (document.fonts?.ready) {
			void document.fonts.ready.then(fitTerminal);
		}
		const observer = new ResizeObserver(fitTerminal);
		observer.observe(host);

		// Recovery re-fit that does NOT depend on the host box changing size.
		//
		// FitAddon derives the grid by dividing the pane box by the renderer's
		// measured cell box. That box is measured asynchronously: the WebGL
		// renderer loads after open() and the monospace font's real metrics
		// resolve a frame or more later, so the early fits above can divide by a
		// not-yet-final cell box, mis-count cols/rows, and clip the grid inside the
		// pane. The fixed settle window (rAF, timeouts, fonts.ready) may all run
		// before the cell box is final, and the ResizeObserver never fires to
		// correct it because the host's pixel box is a stable height:100%, so a
		// wrong grid would otherwise freeze for the whole session.
		//
		// onRender fires on every renderer repaint, including the repaint after
		// the metrics settle. Each fire re-proposes dimensions from the *current*
		// measured cell box. Crucially we never re-fit straight off a single
		// frame's proposal: the WebGL atlas warm-up can emit a one-frame transient
		// cell box (e.g. a doubled box on a HiDPI display) that halves the grid,
		// and committing it would lock the terminal at half size and detach (the
		// #313 ghost). So a differing proposal must REPEAT identically across two
		// consecutive renders — proving the measurement settled — before we apply
		// it. proposeDimensions returns undefined until the cell box is non-zero,
		// so a fit is never accepted from an unmeasured cell. Once the proposal
		// holds at the live grid for a few frames (or a hard re-fit cap is hit) the
		// listener detaches, so steady-state content renders cost nothing.
		const STABLE_FRAMES_TARGET = 3;
		const MAX_REFITS = 20;
		let stableFrames = 0;
		let refits = 0;
		let pending: { cols: number; rows: number } | null = null;
		const stabilizer = term.onRender(() => {
			const proposed = fit.proposeDimensions();
			if (!proposed || !proposed.cols || !proposed.rows) return;
			if (proposed.cols !== term.cols || proposed.rows !== term.rows) {
				stableFrames = 0;
				// Only act once the same differing proposal repeats — a single-frame
				// transient never gets committed, it just updates `pending`.
				if (pending && pending.cols === proposed.cols && pending.rows === proposed.rows) {
					pending = null;
					if (refits++ >= MAX_REFITS) {
						stabilizer.dispose();
						return;
					}
					fitTerminal();
					return;
				}
				pending = { cols: proposed.cols, rows: proposed.rows };
				return;
			}
			pending = null;
			if (++stableFrames >= STABLE_FRAMES_TARGET) stabilizer.dispose();
		});

		// OS window resize and monitor/DPR changes also alter the true cell box
		// without touching the host's height:100% box, so the ResizeObserver above
		// misses them. Listen on window directly as a session-long recovery path.
		window.addEventListener("resize", fitTerminal);

		// Do not forward term.onData wholesale. Its raw stream also carries
		// terminal-generated control responses during attach/repaint (device
		// attributes, cursor-position reports, DECRPM, focus, OSC color, window ops);
		// writing those back through the mux corrupts the real agent PTY. Keyboard is
		// forwarded via onKey; paste, composition, shortcuts, and wheel reports are
		// emitted explicitly. The ONE thing onData carries that we DO need is the
		// agent's mouse reports — without them Claude Code's TUI clickables ("Ran
		// shell command", file links) never register — so forward only those. SGR /
		// SGR-Pixels reports match SGR_MOUSE_REPORT (the "\x1b[<" prefix is unique to
		// them); the DEFAULT encoding is non-UTF-8 and arrives on onBinary, which
		// xterm uses exclusively for mouse reports, so forward it unconditionally.
		const keyInput = term.onKey(({ key }) => emitUserInput(key, "keyboard"));
		const mouseData = term.onData((data) => {
			if (SGR_MOUSE_REPORT.test(data)) emitUserInput(data, "mouse");
		});
		const mouseBinary = term.onBinary((data) => emitUserInput(data, "mouse"));

		// Linkify AO session-id references (`@<project>-<num>`, the bare canonical
		// `<project>-<num>` as it appears in logs / `[from …]` wrappers, and the
		// short `@<num>`) so a click navigates the app to that session. Unlike the
		// http/OSC-8 links above — which openExternal to the OS browser — session
		// links resolve INTERNALLY via onSessionLinkActivate. The resolver only
		// yields tokens that name a currently-known session, so unrelated
		// hyphen-number tokens (a Jira key like STAR-2272) are never linkified.
		// Session ids are ASCII and short, so a token's string offset on a line maps
		// 1:1 to its cell column (no wide-char remap) and we do not stitch wrapped
		// rows.
		const sessionLinks = term.registerLinkProvider({
			provideLinks(bufferLineNumber, callback) {
				const resolver = callbacksRef.current.sessionLinkResolver;
				if (!resolver) {
					callback(undefined);
					return;
				}
				const line = term.buffer.active.getLine(bufferLineNumber - 1);
				if (!line) {
					callback(undefined);
					return;
				}
				const text = line.translateToString(true);
				const matches = resolver(text);
				if (matches.length === 0) {
					callback(undefined);
					return;
				}
				callback(
					matches.map((match) => ({
						text: text.slice(match.startIndex, match.endIndex),
						range: {
							start: { x: match.startIndex + 1, y: bufferLineNumber },
							end: { x: match.endIndex, y: bufferLineNumber },
						},
						activate: () => callbacksRef.current.onSessionLinkActivate?.(match.sessionId),
					})),
				);
			},
		});

		// Linkify SCM reference tokens (`#<num>` GitHub PR/issue, `!<num>` GitLab
		// MR) so a click opens the PR/MR in the OS browser. Sibling to the session
		// provider above, but the activation differs: session refs navigate
		// INTERNALLY, while these open EXTERNALLY via openTerminalLink (the same
		// hardened https path the other terminal links use). The resolver only
		// yields tokens for a provider the session's own remote actually has (so a
		// GitHub-only project never linkifies `!`, and vice versa), and the URL is
		// built from that remote's trusted base — the token only supplies the
		// numeric id. Different sigils than the session provider (`#`/`!` vs
		// `@`/bare id), so the two never contend for the same range.
		const externalRefLinks = term.registerLinkProvider({
			provideLinks(bufferLineNumber, callback) {
				const resolver = callbacksRef.current.externalRefResolver;
				if (!resolver) {
					callback(undefined);
					return;
				}
				const line = term.buffer.active.getLine(bufferLineNumber - 1);
				if (!line) {
					callback(undefined);
					return;
				}
				const text = line.translateToString(true);
				const matches = resolver(text);
				if (matches.length === 0) {
					callback(undefined);
					return;
				}
				callback(
					matches.map((match) => ({
						text: text.slice(match.startIndex, match.endIndex),
						range: {
							start: { x: match.startIndex + 1, y: bufferLineNumber },
							end: { x: match.endIndex, y: bufferLineNumber },
						},
						activate: () => openTerminalLink(match.url),
					})),
				);
			},
		});

		// Linkify FILE reference tokens (a path or bare filename with a code
		// extension) so a click opens the file in the workspace code viewer.
		// Sibling to the two providers above; like session refs it activates
		// INTERNALLY (onFileLinkActivate resolves the ref within the session's
		// workspace and opens the viewer), NOT the OS browser. The resolver is
		// conservative (terminal-file-links) so dotted symbols/URLs never match,
		// and its token shapes carry a `.<ext>` that the digit-terminated session
		// and #/! SCM tokens never do, so ranges don't contend.
		const fileLinks = term.registerLinkProvider({
			provideLinks(bufferLineNumber, callback) {
				const resolver = callbacksRef.current.fileLinkResolver;
				if (!resolver) {
					callback(undefined);
					return;
				}
				const line = term.buffer.active.getLine(bufferLineNumber - 1);
				if (!line) {
					callback(undefined);
					return;
				}
				const text = line.translateToString(true);
				const matches = resolver(text);
				if (matches.length === 0) {
					callback(undefined);
					return;
				}
				callback(
					matches.map((match) => ({
						text: text.slice(match.startIndex, match.endIndex),
						range: {
							start: { x: match.startIndex + 1, y: bufferLineNumber },
							end: { x: match.endIndex, y: bufferLineNumber },
						},
						activate: () => callbacksRef.current.onFileLinkActivate?.(match),
					})),
				);
			},
		});

		// Translate wheel motion into SGR wheel reports for the pane (see
		// sgrWheelReport), one report per scrolled line. WheelEvent.deltaMode
		// varies by platform/device: trackpads and normalized wheels report
		// pixels (mode 0, the macOS case), while many Linux/Windows mouse wheels
		// report whole lines (mode 1) or pages (mode 2). Mirror xterm's native
		// getLinesScrolled across all three so scroll works everywhere; pixel
		// deltas accumulate so a full cell-height emits one line. Returning false
		// suppresses xterm's arrow-key wheel fallback. Ctrl/Cmd wheel is the
		// font-size zoom (CenterPane), so leave it for that handler.
		let wheelAccumPx = 0;
		term.attachCustomWheelEventHandler((event) => {
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
			// A full-screen TUI that keeps its own transcript and scrolls it only by
			// keyboard (opencode) ignores wheel/mouse reports on every platform; route
			// its wheel to page keys. Kept first so opencode is unaffected by the
			// buffer-aware paths below.
			if (callbacksRef.current.paneScrollsByKeyboard) {
				emitUserInput(pageKeyReport(lines), "wheel");
				return false;
			}
			// A normal-buffer pane with mouse tracking off (codex, a plain shell)
			// prints its transcript and relies on the terminal's own scrollback — the
			// way it scrolls in a raw terminal. Scroll xterm's viewport locally; the
			// pane never sees these bytes. Requires scrollback > 0 (see Terminal opts).
			if (term.modes.mouseTrackingMode === "none" && term.buffer.active.type === "normal") {
				term.scrollLines(lines);
				return false;
			}
			// Mouse tracking on: the pane (tmux/zellij copy-mode, or any app that
			// tracks the mouse) acts on SGR wheel reports. On Windows conpty this
			// reaches the app directly; under a mux it drives copy-mode.
			if (term.modes.mouseTrackingMode !== "none") {
				const button = lines < 0 ? SGR_WHEEL_UP : SGR_WHEEL_DOWN;
				emitUserInput(sgrWheelReport(button, Math.abs(lines)), "wheel");
				return false;
			}
			// Alt-buffer pane with mouse tracking off and no keyboard-scroll hint:
			// no scrollback to move locally, so fall back to page keys.
			emitUserInput(pageKeyReport(lines), "wheel");
			return false;
		});
		const pasteInput = (event: ClipboardEvent) => {
			event.preventDefault();
			event.stopPropagation();
			if (suppressNextNativePaste) {
				clearSuppressNativePaste();
				return;
			}
			const text = event.clipboardData?.getData("text/plain") ?? "";
			pasteText(text);
		};
		const compositionInput = (event: CompositionEvent) => {
			emitUserInput(event.data, "composition");
		};
		host.addEventListener("paste", pasteInput, true);
		host.addEventListener("compositionend", compositionInput, true);

		// Live cols/rows getters: the owner reads the current grid at attach time,
		// not a snapshot taken at ready time (the first fit may not have run yet).
		const handle: AttachableTerminal = {
			get cols() {
				return term.cols;
			},
			get rows() {
				return term.rows;
			},
			write: (data) => term.write(data),
			writeln: (line) => term.writeln(line),
			clear: () => term.write(CLEAR_SEQUENCE),
			onUserInput: (listener) => {
				userInputListeners.add(listener);
				return { dispose: () => userInputListeners.delete(listener) };
			},
			onResize: (listener) => term.onResize(listener),
		};
		callbacksRef.current.onReady?.(handle);

		return () => {
			termRef.current = null;
			fitRef.current = null;
			cancelAnimationFrame(raf);
			for (const timer of settleTimers) window.clearTimeout(timer);
			observer.disconnect();
			stabilizer.dispose();
			window.removeEventListener("resize", fitTerminal);
			host.removeEventListener("mousedown", focusTerminal);
			unregisterFocus();
			host.removeEventListener("copy", copyInput);
			window.removeEventListener("keydown", copyShortcut, true);
			selectionChange.dispose();
			host.removeEventListener("paste", pasteInput, true);
			host.removeEventListener("compositionend", compositionInput, true);
			clearSuppressNativePaste();
			keyInput.dispose();
			mouseData.dispose();
			mouseBinary.dispose();
			sessionLinks.dispose();
			externalRefLinks.dispose();
			fileLinks.dispose();
			userInputListeners.clear();
			try {
				term.dispose();
			} catch {
				// Some renderer addons can throw during dispose in certain GPU
				// environments; the terminal is being torn down regardless.
			}
		};
	}, []);

	return (
		<div
			ref={hostRef}
			aria-label={props.ariaLabel}
			className={props.className}
			style={{ height: "100%", overflow: "hidden", width: "100%" }}
		/>
	);
}
