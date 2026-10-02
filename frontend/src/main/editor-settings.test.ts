import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
	DEFAULT_EDITOR_SETTINGS,
	EDITOR_SETTINGS_FILE_NAME,
	readEditorSettings,
	writeEditorSettings,
} from "./editor-settings";

let dir = "";

beforeEach(async () => {
	dir = await mkdtemp(path.join(os.tmpdir(), "ao-editor-settings-"));
});

afterEach(async () => {
	await rm(dir, { recursive: true, force: true });
});

describe("readEditorSettings", () => {
	it("indents as you type, does not format on save, and binds ⌃I / ⌃⇧I by default", async () => {
		expect(await readEditorSettings(dir)).toEqual({
			indentOnType: true,
			formatOnSave: false,
			reindentShortcut: "Ctrl+KeyI",
			formatShortcut: "Ctrl+Shift+KeyI",
		});
	});

	it("falls back to the defaults when the file is corrupt", async () => {
		await writeFile(path.join(dir, EDITOR_SETTINGS_FILE_NAME), "{not json");
		expect(await readEditorSettings(dir)).toEqual(DEFAULT_EDITOR_SETTINGS);
	});

	it("keeps what it can read and defaults what it cannot", async () => {
		await writeFile(
			path.join(dir, EDITOR_SETTINGS_FILE_NAME),
			JSON.stringify({ indentOnType: false, formatOnSave: "yes", reindentShortcut: "Hyper+KeyI", formatShortcut: "" }),
		);
		expect(await readEditorSettings(dir)).toEqual({
			indentOnType: false,
			formatOnSave: false,
			reindentShortcut: "Ctrl+KeyI",
			formatShortcut: "",
		});
	});
});

describe("writeEditorSettings", () => {
	it("writes into the state dir it is given, in canonical spelling, and reads back the same", async () => {
		const saved = await writeEditorSettings(dir, {
			indentOnType: true,
			formatOnSave: true,
			reindentShortcut: "Shift+Ctrl+KeyJ",
			formatShortcut: "Alt+Meta+KeyF",
		});
		expect(saved.reindentShortcut).toBe("Ctrl+Shift+KeyJ");
		const raw = JSON.parse(await readFile(path.join(dir, EDITOR_SETTINGS_FILE_NAME), "utf8"));
		expect(raw).toEqual(saved);
		expect(await readEditorSettings(dir)).toEqual(saved);
	});
});
