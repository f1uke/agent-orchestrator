// @vitest-environment node
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtemp, rm, writeFile, readdir } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
	type ClampOptions,
	type Rect,
	type WindowState,
	WINDOW_STATE_FILE_NAME,
	clampWindowState,
	coerceWindowState,
	readWindowStateSync,
	writeWindowState,
	writeWindowStateSync,
} from "./window-state";

const OPTS: ClampOptions = { defaultWidth: 1440, defaultHeight: 900, minWidth: 960, minHeight: 640 };
const PRIMARY: Rect = { x: 0, y: 0, width: 1920, height: 1080 };

describe("coerceWindowState", () => {
	it("keeps a full, valid record", () => {
		expect(coerceWindowState({ width: 1000, height: 700, x: 100, y: 120, maximized: true, fullScreen: false })).toEqual(
			{ width: 1000, height: 700, x: 100, y: 120, maximized: true },
		);
	});

	it("returns null for a non-object", () => {
		expect(coerceWindowState(null)).toBeNull();
		expect(coerceWindowState("nope")).toBeNull();
		expect(coerceWindowState(42)).toBeNull();
	});

	it("returns null when width or height is missing or non-positive", () => {
		expect(coerceWindowState({ height: 700 })).toBeNull();
		expect(coerceWindowState({ width: 1000 })).toBeNull();
		expect(coerceWindowState({ width: 0, height: 700 })).toBeNull();
		expect(coerceWindowState({ width: 1000, height: -1 })).toBeNull();
		expect(coerceWindowState({ width: Number.NaN, height: 700 })).toBeNull();
	});

	it("drops position unless BOTH x and y are finite numbers", () => {
		expect(coerceWindowState({ width: 1000, height: 700, x: 100 })).toEqual({ width: 1000, height: 700 });
		expect(coerceWindowState({ width: 1000, height: 700, y: 100 })).toEqual({ width: 1000, height: 700 });
		expect(coerceWindowState({ width: 1000, height: 700, x: 100, y: Number.POSITIVE_INFINITY })).toEqual({
			width: 1000,
			height: 700,
		});
	});

	it("only persists known fields and omits false flags", () => {
		expect(
			coerceWindowState({ width: 1000, height: 700, x: 0, y: 0, maximized: false, fullScreen: false, junk: "x" }),
		).toEqual({ width: 1000, height: 700, x: 0, y: 0 });
	});
});

describe("clampWindowState", () => {
	it("returns the default size (no position) on first run", () => {
		expect(clampWindowState(null, [PRIMARY], OPTS)).toEqual({ width: 1440, height: 900 });
	});

	it("returns the default size when no displays are reported", () => {
		expect(clampWindowState({ width: 1000, height: 700, x: 10, y: 10 }, [], OPTS)).toEqual({
			width: 1440,
			height: 900,
		});
	});

	it("clamps the first-run default down to a small screen", () => {
		expect(clampWindowState(null, [{ x: 0, y: 0, width: 1200, height: 800 }], OPTS)).toEqual({
			width: 1200,
			height: 800,
		});
	});

	it("preserves saved bounds fully within a display", () => {
		expect(clampWindowState({ width: 1000, height: 700, x: 100, y: 120 }, [PRIMARY], OPTS)).toEqual({
			width: 1000,
			height: 700,
			x: 100,
			y: 120,
		});
	});

	it("drops an off-screen position (disconnected monitor) but keeps the size", () => {
		expect(clampWindowState({ width: 1000, height: 700, x: 3000, y: 100 }, [PRIMARY], OPTS)).toEqual({
			width: 1000,
			height: 700,
		});
	});

	it("drops a position that leaves too little of the window grabbable", () => {
		// Only 20px of the window peeks onto the display — below the grab threshold.
		expect(clampWindowState({ width: 1000, height: 700, x: 1900, y: 100 }, [PRIMARY], OPTS)).toEqual({
			width: 1000,
			height: 700,
		});
	});

	it("raises a below-minimum saved size up to the window minimum", () => {
		expect(clampWindowState({ width: 400, height: 300, x: 50, y: 50 }, [PRIMARY], OPTS)).toEqual({
			width: 960,
			height: 640,
			x: 50,
			y: 50,
		});
	});

	it("clamps an over-large saved size to the work area", () => {
		expect(clampWindowState({ width: 4000, height: 3000, x: 0, y: 0 }, [PRIMARY], OPTS)).toEqual({
			width: 1920,
			height: 1080,
			x: 0,
			y: 0,
		});
	});

	it("keeps a position visible on a secondary display with negative coordinates", () => {
		const left: Rect = { x: -1920, y: 0, width: 1920, height: 1080 };
		expect(clampWindowState({ width: 1000, height: 700, x: -1800, y: 100 }, [PRIMARY, left], OPTS)).toEqual({
			width: 1000,
			height: 700,
			x: -1800,
			y: 100,
		});
	});

	it("preserves the maximized flag", () => {
		expect(clampWindowState({ width: 1000, height: 700, x: 100, y: 100, maximized: true }, [PRIMARY], OPTS)).toEqual({
			width: 1000,
			height: 700,
			x: 100,
			y: 100,
			maximized: true,
		});
	});

	it("preserves the fullScreen flag", () => {
		expect(clampWindowState({ width: 1000, height: 700, x: 100, y: 100, fullScreen: true }, [PRIMARY], OPTS)).toEqual({
			width: 1000,
			height: 700,
			x: 100,
			y: 100,
			fullScreen: true,
		});
	});
});

describe("window-state persistence", () => {
	let dir: string;
	beforeEach(async () => {
		dir = await mkdtemp(path.join(os.tmpdir(), "ao-window-state-"));
	});
	afterEach(async () => {
		await rm(dir, { recursive: true, force: true });
	});

	it("returns null when no file exists", () => {
		expect(readWindowStateSync(dir)).toBeNull();
	});

	it("round-trips an async write", async () => {
		const state: WindowState = { width: 1000, height: 700, x: 40, y: 60, maximized: true };
		await writeWindowState(dir, state);
		expect(readWindowStateSync(dir)).toEqual(state);
	});

	it("round-trips a sync write", () => {
		const state: WindowState = { width: 1234, height: 810, x: 12, y: 34 };
		writeWindowStateSync(dir, state);
		expect(readWindowStateSync(dir)).toEqual(state);
	});

	it("returns null on a corrupt file", async () => {
		await writeFile(path.join(dir, WINDOW_STATE_FILE_NAME), "{not json", "utf8");
		expect(readWindowStateSync(dir)).toBeNull();
	});

	it("does not write a file for a stateless record", async () => {
		await writeWindowState(dir, { width: 0, height: 0 } as WindowState);
		expect(await readdir(dir)).toEqual([]);
	});

	it("atomic write leaves no temp file behind", async () => {
		await writeWindowState(dir, { width: 1000, height: 700, x: 0, y: 0 });
		expect(await readdir(dir)).toEqual([WINDOW_STATE_FILE_NAME]);
	});
});
