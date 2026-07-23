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

/** How big the human last made it. Carried between openings. */
export type TerminalSize = { width: number; height: number };

export type TerminalWindowOptions = {
	frame: boolean;
	transparent: boolean;
	hasShadow: boolean;
	alwaysOnTop: boolean;
	resizable: boolean;
	minWidth: number;
	minHeight: number;
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
	getBounds(): TerminalBounds;
	loadURL(url: string): Promise<void>;
	focus(): void;
	onClosed(listener: () => void): void;
	/** Fired as the human drags the window's edge; the last size is remembered. */
	onResized(listener: () => void): void;
	/**
	 * Ask the page to let its pane go, and resolve when it says it has.
	 *
	 * This is the hand-off's whole safety argument. Destroying a window kills its
	 * renderer where it stands: React's cleanup never runs, so the mux is never told
	 * to close the pane and the daemon is left to notice a dead socket in its own
	 * time. While it does, the board window may already have attached the same pane —
	 * two `tmux attach` clients on one session, fighting over the grid. Asking first
	 * and waiting for the answer makes "never attached twice" true by ORDER rather
	 * than by hoping the race goes our way.
	 */
	requestDetach(timeoutMs: number): Promise<void>;
	destroy(): void;
	isDestroyed(): boolean;
}

/** The card's default size. Roughly 86×22 cells at the terminal's 12px font. */
export const TERMINAL_WINDOW_WIDTH = 720;
export const TERMINAL_WINDOW_HEIGHT = 420;

/** Below this it stops being a terminal you can read. ~40 cols × 8 rows plus chrome. */
export const TERMINAL_MIN_WIDTH = 360;
export const TERMINAL_MIN_HEIGHT = 200;

/** How far above the floor the card floats, so it never sits on its Proc's head. */
export const TERMINAL_ANCHOR_GAP = 150;

/**
 * How long the hand-off waits for the page to say it has let go.
 *
 * Long enough for a healthy renderer to answer (it is one message and a socket
 * frame), short enough that a wedged one cannot hold the board window shut. On a
 * timeout we go ahead anyway: a pane attached twice for a moment is recoverable,
 * an app that will not open is not.
 */
export const TERMINAL_DETACH_TIMEOUT_MS = 250;

/**
 * How long a terminal may sit untouched before it closes itself.
 *
 * An open card is an attached pane, and a pane attached all night because someone
 * walked away is exactly the kind of thing nobody notices until tmux is confused
 * about how wide the screen is. Generous, because closing a terminal somebody is
 * reading would be worse than the attach it saves.
 */
export const TERMINAL_IDLE_CLOSE_MS = 45 * 60 * 1000;

/** The margin the card keeps from the edges of the screen. */
const SCREEN_MARGIN = 12;

/**
 * Where the card goes for a Proc standing at `anchor`.
 *
 * Centred on its Proc, lifted clear of it, and always wholly on screen: a card
 * that hangs off the edge is a terminal with lines you cannot read.
 */
export function terminalBoundsFor(
	anchor: TerminalAnchor,
	workArea: TerminalBounds,
	size?: TerminalSize,
): TerminalBounds {
	const wanted = size ?? { width: TERMINAL_WINDOW_WIDTH, height: TERMINAL_WINDOW_HEIGHT };
	const width = Math.max(TERMINAL_MIN_WIDTH, Math.min(wanted.width, workArea.width - SCREEN_MARGIN * 2));
	const height = Math.max(TERMINAL_MIN_HEIGHT, Math.min(wanted.height, workArea.height - SCREEN_MARGIN * 2));
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
	/** The work area of the display the ANCHOR is on, so a Proc on a second screen opens there. */
	workAreaNear(anchor: TerminalAnchor): TerminalBounds;
	preloadPath(): string;
	/** The size the human last left it at, and where to put the new one. */
	readSize(): TerminalSize | null;
	writeSize(size: TerminalSize): void;
	/** Told whenever the window goes away, however it went. */
	onClosed(): void;
	logError(message: string, error: unknown): void;
	/** Test seam for the idle timer. */
	setTimer?(fn: () => void, ms: number): unknown;
	clearTimer?(handle: unknown): void;
};

export type TerminalWindowHost = {
	/** Open (or re-point) the terminal. One at a time: one keyboard, one caret. */
	open(input: { sessionId: string; handleId: string; anchor: TerminalAnchor }): void;
	/** The Proc moved — carry the card with it. */
	moveTo(anchor: TerminalAnchor): void;
	/** Close it now, without asking. For teardown, where nothing will attach after us. */
	close(): void;
	/**
	 * Close it BEFORE something else attaches the same pane: ask the page to detach,
	 * wait for the ack (bounded), and only then destroy.
	 */
	closeForHandoff(): Promise<void>;
	/** The human typed; the idle clock starts again. */
	noteActivity(): void;
	isOpen(): boolean;
	/** Which session's terminal is up, if any. */
	openFor(): string | null;
	/** Where its Proc is standing, so the card can point at it. */
	anchor(): TerminalAnchor | null;
};

export function createTerminalWindowHost(deps: TerminalWindowDeps): TerminalWindowHost {
	let win: TerminalWindow | null = null;
	let session: string | null = null;
	let anchored: TerminalAnchor | null = null;
	let idleTimer: unknown = null;
	const setTimer = deps.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
	const clearTimer = deps.clearTimer ?? ((handle) => clearTimeout(handle as ReturnType<typeof setTimeout>));

	const live = (): TerminalWindow | null => (win && !win.isDestroyed() ? win : null);

	const stopIdleClock = (): void => {
		if (idleTimer !== null) clearTimer(idleTimer);
		idleTimer = null;
	};

	const close = (): void => {
		const current = live();
		win = null;
		session = null;
		anchored = null;
		stopIdleClock();
		if (!current) return;
		try {
			current.destroy();
		} catch (err) {
			deps.logError("AO: failed to close the terminal window", err);
		}
	};

	const startIdleClock = (): void => {
		stopIdleClock();
		idleTimer = setTimer(() => {
			idleTimer = null;
			close();
		}, TERMINAL_IDLE_CLOSE_MS);
	};

	/** Remember the size the human left, so the next terminal opens the way they like it. */
	const rememberSize = (current: TerminalWindow): void => {
		try {
			const { width, height } = current.getBounds();
			deps.writeSize({ width, height });
		} catch (err) {
			deps.logError("AO: failed to remember the terminal window's size", err);
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
					// A terminal is a thing you make bigger when the output is wide.
					resizable: true,
					minWidth: TERMINAL_MIN_WIDTH,
					minHeight: TERMINAL_MIN_HEIGHT,
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
				anchored = anchor;
				created.onClosed(() => {
					if (win === created) {
						win = null;
						session = null;
						anchored = null;
						stopIdleClock();
					}
					deps.onClosed();
				});
				created.onResized(() => rememberSize(created));
				created.setBounds(terminalBoundsFor(anchor, deps.workAreaNear(anchor), deps.readSize() ?? undefined));
				void created.loadURL(deps.urlFor(sessionId, handleId));
				// A terminal is opened to be typed into; it takes the keyboard on the
				// way up rather than waiting to be clicked a second time.
				created.focus();
				startIdleClock();
			} catch (err) {
				win = null;
				session = null;
				deps.logError("AO: failed to open the terminal window", err);
			}
		},
		moveTo(anchor) {
			const current = live();
			if (!current) return;
			anchored = anchor;
			try {
				const { width, height } = current.getBounds();
				current.setBounds(terminalBoundsFor(anchor, deps.workAreaNear(anchor), { width, height }));
			} catch (err) {
				deps.logError("AO: failed to move the terminal window", err);
			}
		},
		close,
		async closeForHandoff() {
			const current = live();
			if (!current) return;
			try {
				await current.requestDetach(TERMINAL_DETACH_TIMEOUT_MS);
			} catch (err) {
				// A page that cannot answer must not be able to hold the app shut.
				deps.logError("AO: the terminal window did not confirm it had detached", err);
			}
			close();
		},
		noteActivity() {
			if (!live()) return;
			startIdleClock();
		},
		isOpen() {
			return live() !== null;
		},
		openFor() {
			return live() ? session : null;
		},
		anchor() {
			return live() ? anchored : null;
		},
	};
}
