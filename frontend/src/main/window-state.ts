import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { mkdir, rename, writeFile } from "node:fs/promises";
import path from "node:path";

/**
 * Persist + restore the desktop window's size/position so it survives a
 * close-without-quit (macOS red button / Cmd-W → Dock reopen) and a full app
 * relaunch. The file lives under ~/.ao (never Electron's OS-default app-data
 * dir — see the hard rule in CLAUDE.md/AGENTS.md), alongside app-state.json and
 * update-settings.json, and is written atomically like those (temp + rename).
 *
 * Everything here is pure/host-agnostic: main.ts feeds it the saved JSON and the
 * current displays' work areas, so the clamp/validate + read/write logic is unit
 * testable without spinning up Electron.
 */

/** A display work area (or window rectangle) in Electron DIP coordinates. */
export interface Rect {
	x: number;
	y: number;
	width: number;
	height: number;
}

/** Persisted window bounds. x/y are optional (absent → let the OS place it). */
export interface WindowState {
	width: number;
	height: number;
	x?: number;
	y?: number;
	maximized?: boolean;
	fullScreen?: boolean;
}

export interface ClampOptions {
	/** First-run size when there is no saved state (clamped to the screen). */
	defaultWidth: number;
	defaultHeight: number;
	/** Lower bound for the restored size, matching the BrowserWindow min*. */
	minWidth: number;
	minHeight: number;
}

/** File holding the window bounds under the ~/.ao state dir. */
export const WINDOW_STATE_FILE_NAME = "window-state.json";

// A saved window must keep at least this many pixels grabbable on some display,
// else its title bar is unreachable (e.g. it was last on a now-disconnected
// monitor). When it doesn't, the position is dropped and the OS re-centers it.
const MIN_VISIBLE_PX = 48;

function finiteNumber(value: unknown): number | undefined {
	return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

/**
 * Parse persisted JSON into a WindowState, tolerating partial/garbage input.
 * Returns null when there is no usable width/height (treated as "no saved
 * state"). Position is kept only when BOTH x and y are finite; unknown fields
 * and false flags are dropped so the file only ever holds known keys.
 */
export function coerceWindowState(raw: unknown): WindowState | null {
	if (raw === null || typeof raw !== "object") return null;
	const o = raw as Record<string, unknown>;
	const width = finiteNumber(o.width);
	const height = finiteNumber(o.height);
	if (width === undefined || height === undefined || width <= 0 || height <= 0) return null;
	const state: WindowState = { width, height };
	const x = finiteNumber(o.x);
	const y = finiteNumber(o.y);
	if (x !== undefined && y !== undefined) {
		state.x = x;
		state.y = y;
	}
	if (o.maximized === true) state.maximized = true;
	if (o.fullScreen === true) state.fullScreen = true;
	return state;
}

function areaOf(rect: Rect): number {
	return rect.width * rect.height;
}

function clampNumber(value: number, lo: number, hi: number): number {
	return Math.min(Math.max(value, lo), hi);
}

// A window counts as visible when it overlaps some display by at least a
// grabbable margin on both axes (capped by the window's own size, so a
// legitimately tiny window still qualifies).
function isVisibleOnAnyDisplay(win: Rect, workAreas: Rect[]): boolean {
	return workAreas.some((area) => {
		const overlapW = Math.min(win.x + win.width, area.x + area.width) - Math.max(win.x, area.x);
		const overlapH = Math.min(win.y + win.height, area.y + area.height) - Math.max(win.y, area.y);
		return overlapW >= Math.min(MIN_VISIBLE_PX, win.width) && overlapH >= Math.min(MIN_VISIBLE_PX, win.height);
	});
}

/**
 * Resolve the bounds to open the window at. Falls back to the (screen-clamped)
 * default size on first run or when displays are unknown. Otherwise clamps the
 * saved size to the largest available work area and keeps the saved position
 * only when it lands visibly on some display — a position saved on a
 * now-disconnected monitor is dropped so the OS re-centers the window instead
 * of opening it off-screen.
 */
export function clampWindowState(saved: WindowState | null, workAreas: Rect[], opts: ClampOptions): WindowState {
	if (workAreas.length === 0) {
		return { width: opts.defaultWidth, height: opts.defaultHeight };
	}

	const largest = workAreas.reduce((best, area) => (areaOf(area) > areaOf(best) ? area : best));
	const fitSize = (width: number, height: number): { width: number; height: number } => ({
		width: clampNumber(Math.round(width), Math.min(opts.minWidth, largest.width), largest.width),
		height: clampNumber(Math.round(height), Math.min(opts.minHeight, largest.height), largest.height),
	});

	if (saved === null) {
		// First run: default size clamped to the screen, positioned by the OS.
		return fitSize(opts.defaultWidth, opts.defaultHeight);
	}

	const { width, height } = fitSize(saved.width, saved.height);
	const result: WindowState = { width, height };

	if (saved.x !== undefined && saved.y !== undefined) {
		const candidate: Rect = { x: Math.round(saved.x), y: Math.round(saved.y), width, height };
		if (isVisibleOnAnyDisplay(candidate, workAreas)) {
			result.x = candidate.x;
			result.y = candidate.y;
		}
	}
	if (saved.maximized) result.maximized = true;
	if (saved.fullScreen) result.fullScreen = true;
	return result;
}

function parse(raw: string): WindowState | null {
	try {
		return coerceWindowState(JSON.parse(raw));
	} catch {
		return null;
	}
}

/**
 * Read the saved window state synchronously, tolerating a missing or corrupt
 * file (returns null). Sync so it can run inside the synchronous createWindow()
 * path (whenReady + the macOS 'activate'/reopen path).
 */
export function readWindowStateSync(stateDir: string): WindowState | null {
	let raw: string;
	try {
		raw = readFileSync(path.join(stateDir, WINDOW_STATE_FILE_NAME), "utf8");
	} catch {
		return null;
	}
	return parse(raw);
}

function serialize(state: WindowState): string | null {
	const coerced = coerceWindowState(state);
	if (!coerced) return null;
	return `${JSON.stringify(coerced, null, 2)}\n`;
}

/**
 * Atomically write the window state (temp file + rename), mirroring
 * app-state.ts / update-settings.ts. Used for the debounced resize/move saves so
 * disk writes never block the drag. A stateless/garbage record is not persisted.
 */
export async function writeWindowState(stateDir: string, state: WindowState): Promise<void> {
	const data = serialize(state);
	if (data === null) return;
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, WINDOW_STATE_FILE_NAME);
	const tmp = path.join(stateDir, `.window-state-${process.pid}-${Date.now()}.json`);
	await writeFile(tmp, data, { mode: 0o600 });
	await rename(tmp, file);
}

/**
 * Synchronous atomic write, used on window close/app quit so the final bounds
 * are durable even when the process exits immediately after (an async write
 * could be cut off before it flushes).
 */
export function writeWindowStateSync(stateDir: string, state: WindowState): void {
	const data = serialize(state);
	if (data === null) return;
	mkdirSync(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, WINDOW_STATE_FILE_NAME);
	const tmp = path.join(stateDir, `.window-state-${process.pid}-${Date.now()}.json`);
	writeFileSync(tmp, data, { mode: 0o600 });
	renameSync(tmp, file);
}
