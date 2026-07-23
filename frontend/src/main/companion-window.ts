// The desktop-companion overlay: a SECOND BrowserWindow in this same app, not a
// second app instance — so it shares the process, the `~/.ao/electron` userData
// pin and the daemon, and cannot collide with the main window's state.
//
// The Electron surface it drives is adapted to the small `OverlayWindow` interface
// below (the pattern native-notifications.ts uses), so every rule here — off by
// default, click-through, the band above the Dock, one window not two, letting go
// of a window that closed itself — is unit-testable without launching Electron.
// What CANNOT be proven from here is that macOS honours the flags; that is what
// the human smoke checklist is for.

import { COMPANION_CONTENT_HEIGHT } from "../companion/layout";

export type OverlayBounds = { x: number; y: number; width: number; height: number };

/** The BrowserWindow constructor options the overlay needs. Asserted in the tests. */
export type OverlayWindowOptions = {
	transparent: boolean;
	frame: boolean;
	hasShadow: boolean;
	alwaysOnTop: boolean;
	resizable: boolean;
	movable: boolean;
	minimizable: boolean;
	maximizable: boolean;
	fullscreenable: boolean;
	focusable: boolean;
	skipTaskbar: boolean;
	backgroundColor: string;
	title: string;
	webPreferences: {
		preload: string;
		contextIsolation: boolean;
		nodeIntegration: boolean;
		sandbox: boolean;
	};
};

/** The subset of a BrowserWindow the overlay drives. */
export interface OverlayWindow {
	setBounds(bounds: OverlayBounds): void;
	setIgnoreMouseEvents(ignore: boolean, options?: { forward?: boolean }): void;
	/**
	 * PROTOTYPE (terminal bubble). The overlay is built `focusable: false` so it can
	 * never steal the keyboard from the app you are working in. A terminal you can
	 * type into needs exactly that, though — so the flag is flipped for the life of
	 * one open bubble and put back the moment it closes.
	 */
	setFocusable(focusable: boolean): void;
	focus(): void;
	blur(): void;
	setVisibleOnAllWorkspaces(visible: boolean, options?: { visibleOnFullScreen?: boolean }): void;
	setAlwaysOnTop(flag: boolean, level?: string): void;
	loadURL(url: string): Promise<void>;
	/** Push a message to the overlay page. */
	send(channel: string, ...args: unknown[]): void;
	onClosed(listener: () => void): void;
	destroy(): void;
	isDestroyed(): boolean;
}

/** "Go and re-read the chosen looks." Carries nothing; see `notifyLooksChanged`. */
export const LOOKS_CHANGED_CHANNEL = "companion:looksChanged";

/**
 * PROTOTYPE (terminal bubble): "the board window is up — let go of the terminal."
 *
 * Carries nothing either. The overlay's only correct response is to detach, and
 * a payload would invite it to decide whether to.
 */
export const MAIN_WINDOW_OPENED_CHANNEL = "companion:mainWindowOpened";

export type CompanionOverlayDeps = {
	createWindow(options: OverlayWindowOptions): OverlayWindow;
	/** The work area of the display the band lives on. Excludes the Dock and menu bar. */
	workArea(): OverlayBounds;
	overlayUrl(): string;
	preloadPath?: () => string;
	logError(message: string, error: unknown): void;
};

export type CompanionOverlay = {
	setEnabled(enabled: boolean): void;
	/** Called from the overlay renderer: true while the pointer is over a Proc. */
	setInteractive(interactive: boolean): void;
	/**
	 * Tell the overlay that the chosen looks moved, so it re-reads them.
	 *
	 * Deliberately CONTENT-FREE. Both windows are one origin, so the looks already
	 * travel by `storage` event and localStorage stays the single source of truth;
	 * this is a second way to say "look again", not a second copy of the answer. Two
	 * channels that both mean the same thing cannot disagree - the later one just
	 * finds the work already done.
	 */
	notifyLooksChanged(): void;
	/** PROTOTYPE: the board window came up; any live bubble terminal must detach. */
	notifyMainWindowOpened(): void;
	/** Re-band after a display change. */
	relayout(): void;
	isOpen(): boolean;
	/**
	 * PROTOTYPE (terminal bubble): take or give back the keyboard.
	 *
	 * Taking it is two steps because one is not enough: `setFocusable(true)` only
	 * makes the window ELIGIBLE for the keyboard, and `focus()` actually takes it.
	 * Giving it back is two steps for a different reason: on macOS
	 * `setFocusable(false)` explicitly does NOT drop focus a window already holds
	 * (Electron's own docs say so, and it is measured — see the probe in the
	 * record), so without the `blur()` the desktop keeps typing into a bubble that
	 * is no longer there.
	 */
	setKeyboard(on: boolean): void;
	dispose(): void;
};

/**
 * Height of the floor band, taken from the art rather than guessed.
 *
 * It WAS guessed once, at 190px, and it was too short: a Proc's hover tooltip
 * reaches 218px above the floor, so it was clipped off the top of the window and
 * hovering a Proc appeared to do nothing at all. Every pixel of the band is a pixel
 * of desktop the overlay must forward clicks through, so it is exactly as tall as
 * the tallest thing drawn in it and no taller.
 */
export const OVERLAY_BAND_HEIGHT = COMPANION_CONTENT_HEIGHT;

/**
 * How far above the band a Proc can be lifted, as a fraction of the work area.
 *
 * A Proc you can pick up and THROW needs sky to be thrown into, and the window is
 * the sky: it can only go as high as the window is tall. The whole work area is
 * the honest answer — the window is transparent and forwards every click that is
 * not on a Proc, so a taller one costs a bigger compositor layer and nothing else.
 */
export const OVERLAY_THROW_HEADROOM = 1;

/**
 * The band sits flush with the bottom of the WORK AREA rather than the display.
 * On macOS the work area already excludes the Dock and the menu bar, so "above
 * the Dock" comes out of the geometry instead of a guessed inset that would be
 * wrong for every Dock size, position and auto-hide setting.
 */
export function overlayBandBounds(workArea: OverlayBounds): OverlayBounds {
	const wanted = Math.max(OVERLAY_BAND_HEIGHT, Math.round(workArea.height * OVERLAY_THROW_HEADROOM));
	const height = Math.min(wanted, workArea.height);
	return {
		x: workArea.x,
		y: workArea.y + workArea.height - height,
		width: workArea.width,
		height,
	};
}

export function createCompanionOverlay(deps: CompanionOverlayDeps): CompanionOverlay {
	let win: OverlayWindow | null = null;

	const live = (): OverlayWindow | null => (win && !win.isDestroyed() ? win : null);

	const open = (): void => {
		if (live()) return;
		try {
			const created = deps.createWindow({
				transparent: true,
				frame: false,
				hasShadow: false,
				alwaysOnTop: true,
				resizable: false,
				movable: false,
				minimizable: false,
				maximizable: false,
				fullscreenable: false,
				// The overlay must never steal the keyboard from the app you are
				// working in — it is scenery you can occasionally poke, not a window.
				focusable: false,
				skipTaskbar: true,
				// Fully transparent: on a transparent window an opaque backgroundColor
				// paints a black slab over the desktop.
				backgroundColor: "#00000000",
				title: "Agent Orchestrator Companion",
				webPreferences: {
					preload: deps.preloadPath?.() ?? "",
					contextIsolation: true,
					nodeIntegration: false,
					sandbox: true,
				},
			});
			win = created;
			created.onClosed(() => {
				if (win === created) win = null;
			});
			created.setBounds(overlayBandBounds(deps.workArea()));
			// Click-through from the first frame: the desktop under the band must not
			// go dead for the moment between opening and the renderer booting.
			// `forward` still delivers move events, which is how the renderer knows
			// the pointer has reached a Proc.
			created.setIgnoreMouseEvents(true, { forward: true });
			created.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: false });
			// "floating" sits above ordinary windows but below the screen saver and
			// system UI. A higher level would cover menus and alerts.
			created.setAlwaysOnTop(true, "floating");
			void created.loadURL(deps.overlayUrl());
		} catch (err) {
			win = null;
			deps.logError("AO: failed to open the companion overlay", err);
		}
	};

	const close = (): void => {
		const current = live();
		win = null;
		if (!current) return;
		try {
			current.destroy();
		} catch (err) {
			deps.logError("AO: failed to close the companion overlay", err);
		}
	};

	return {
		setEnabled(enabled) {
			if (enabled) open();
			else close();
		},
		setInteractive(interactive) {
			const current = live();
			if (!current) return;
			if (interactive) current.setIgnoreMouseEvents(false);
			else current.setIgnoreMouseEvents(true, { forward: true });
		},
		notifyLooksChanged() {
			// A closed overlay needs no telling: it re-reads localStorage when it opens.
			try {
				live()?.send(LOOKS_CHANGED_CHANNEL);
			} catch (err) {
				deps.logError("AO: failed to tell the companion overlay about a look change", err);
			}
		},
		notifyMainWindowOpened() {
			try {
				live()?.send(MAIN_WINDOW_OPENED_CHANNEL);
			} catch (err) {
				deps.logError("AO: failed to tell the companion overlay the board window opened", err);
			}
		},
		relayout() {
			live()?.setBounds(overlayBandBounds(deps.workArea()));
		},
		isOpen() {
			return live() !== null;
		},
		setKeyboard(on) {
			const current = live();
			if (!current) return;
			try {
				if (on) {
					current.setFocusable(true);
					current.focus();
				} else {
					current.setFocusable(false);
					current.blur();
				}
			} catch (err) {
				deps.logError("AO: failed to move the keyboard in or out of the companion overlay", err);
			}
		},
		dispose() {
			close();
		},
	};
}
