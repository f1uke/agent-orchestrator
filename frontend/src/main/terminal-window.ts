// The terminal a Proc opens: its OWN window, and that is the whole point.
//
// The first cut put the terminal inside the companion overlay and borrowed the
// keyboard for it — `setFocusable(true)` while a card was open, `setFocusable(false)`
// plus `blur()` when it closed. Both were measured on a real desk and both failed,
// for the same reason: the overlay is not a window you may mutate.
//
//   - The page reported `visibilitychange → hidden → visible` at the hand-back:
//     every Proc on the desktop vanished for a beat, because the window itself had
//     briefly stopped being on screen.
//   - Worse, the overlay then stopped receiving mouse events ALTOGETHER. Its
//     click-through state could no longer be updated, so clicks went to the desktop
//     behind it — the pets could not be clicked or dragged at all until the user
//     clicked another app twice and macOS handed the overlay back its events.
//
// So the pets' window keeps exactly the properties it ships with — never focusable,
// click-through except over a Proc, never touched — and the terminal is a second
// window that is a NORMAL window from birth: focusable, taking its own keyboard and
// its own mouse, owning its own lifetime. Still one Electron app, one process, one
// daemon; just a window that behaves like a window instead of a scene that has been
// asked to behave like one.

export type TerminalBounds = { x: number; y: number; width: number; height: number };

/** Where the card wants to be: its Proc's middle, on the floor, in screen coordinates. */
export type TerminalAnchor = { x: number; y: number };

export type TerminalWindowOptions = {
	frame: boolean;
	transparent: boolean;
	hasShadow: boolean;
	alwaysOnTop: boolean;
	resizable: boolean;
	minimizable: boolean;
	maximizable: boolean;
	fullscreenable: boolean;
	skipTaskbar: boolean;
	backgroundColor: string;
	title: string;
	webPreferences: { preload: string; contextIsolation: boolean; nodeIntegration: boolean; sandbox: boolean };
};

export interface TerminalWindow {
	setBounds(bounds: TerminalBounds): void;
	loadURL(url: string): Promise<void>;
	focus(): void;
	onClosed(listener: () => void): void;
	destroy(): void;
	isDestroyed(): boolean;
}

/** The card's size. Roughly 86×22 cells at the terminal's 12px font. */
export const TERMINAL_WINDOW_WIDTH = 720;
export const TERMINAL_WINDOW_HEIGHT = 420;

/** How far above the floor the card floats, so it never sits on its Proc's head. */
export const TERMINAL_ANCHOR_GAP = 150;

/** The margin the card keeps from the edges of the screen. */
const SCREEN_MARGIN = 12;

/**
 * Where the card goes for a Proc standing at `anchor`.
 *
 * Centred on its Proc, lifted clear of it, and always wholly on screen: a card
 * that hangs off the edge is a terminal with lines you cannot read.
 */
export function terminalBoundsFor(anchor: TerminalAnchor, workArea: TerminalBounds): TerminalBounds {
	const width = Math.min(TERMINAL_WINDOW_WIDTH, workArea.width - SCREEN_MARGIN * 2);
	const height = Math.min(TERMINAL_WINDOW_HEIGHT, workArea.height - SCREEN_MARGIN * 2);
	const minX = workArea.x + SCREEN_MARGIN;
	const maxX = workArea.x + workArea.width - width - SCREEN_MARGIN;
	const minY = workArea.y + SCREEN_MARGIN;
	const maxY = workArea.y + workArea.height - height - SCREEN_MARGIN;
	return {
		x: Math.round(Math.min(Math.max(anchor.x - width / 2, minX), Math.max(minX, maxX))),
		y: Math.round(Math.min(Math.max(anchor.y - TERMINAL_ANCHOR_GAP - height, minY), Math.max(minY, maxY))),
		width,
		height,
	};
}

export type TerminalWindowDeps = {
	createWindow(options: TerminalWindowOptions): TerminalWindow;
	/** `companion.html`, already carrying the query the terminal page reads. */
	urlFor(sessionId: string, handleId: string): string;
	workArea(): TerminalBounds;
	preloadPath(): string;
	/** Told whenever the window goes away, however it went. */
	onClosed(): void;
	logError(message: string, error: unknown): void;
};

export type TerminalWindowHost = {
	/** Open (or re-point) the terminal. One at a time: one keyboard, one caret. */
	open(input: { sessionId: string; handleId: string; anchor: TerminalAnchor }): void;
	/** The Proc moved — carry the card with it. */
	moveTo(anchor: TerminalAnchor): void;
	close(): void;
	isOpen(): boolean;
	/** Which session's terminal is up, if any. */
	openFor(): string | null;
};

export function createTerminalWindowHost(deps: TerminalWindowDeps): TerminalWindowHost {
	let win: TerminalWindow | null = null;
	let session: string | null = null;

	const live = (): TerminalWindow | null => (win && !win.isDestroyed() ? win : null);

	const close = (): void => {
		const current = live();
		win = null;
		session = null;
		if (!current) return;
		try {
			current.destroy();
		} catch (err) {
			deps.logError("AO: failed to close the terminal window", err);
		}
	};

	return {
		open({ sessionId, handleId, anchor }) {
			// A second terminal would be a second keyboard and a second attach to a
			// pane; opening one always replaces the last.
			close();
			try {
				const created = deps.createWindow({
					frame: false,
					// Transparent so the card keeps its rounded rim and its own shape
					// rather than sitting in an opaque rectangle on the wallpaper.
					transparent: true,
					hasShadow: true,
					alwaysOnTop: true,
					resizable: false,
					minimizable: false,
					maximizable: false,
					fullscreenable: false,
					skipTaskbar: true,
					backgroundColor: "#00000000",
					title: "Agent Orchestrator Terminal",
					webPreferences: {
						preload: deps.preloadPath(),
						contextIsolation: true,
						nodeIntegration: false,
						sandbox: true,
					},
				});
				win = created;
				session = sessionId;
				created.onClosed(() => {
					if (win === created) {
						win = null;
						session = null;
					}
					deps.onClosed();
				});
				created.setBounds(terminalBoundsFor(anchor, deps.workArea()));
				void created.loadURL(deps.urlFor(sessionId, handleId));
				// A terminal is opened to be typed into; it takes the keyboard on the
				// way up rather than waiting to be clicked a second time.
				created.focus();
			} catch (err) {
				win = null;
				session = null;
				deps.logError("AO: failed to open the terminal window", err);
			}
		},
		moveTo(anchor) {
			const current = live();
			if (!current) return;
			try {
				current.setBounds(terminalBoundsFor(anchor, deps.workArea()));
			} catch (err) {
				deps.logError("AO: failed to move the terminal window", err);
			}
		},
		close,
		isOpen() {
			return live() !== null;
		},
		openFor() {
			return live() ? session : null;
		},
	};
}
