import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { COMPANION_SETTINGS_FILE_NAME, readCompanionSettings, writeCompanionSettings } from "./companion-settings";

let dir = "";

beforeEach(async () => {
	dir = await mkdtemp(path.join(os.tmpdir(), "ao-companion-"));
});

afterEach(async () => {
	await rm(dir, { recursive: true, force: true });
});

describe("readCompanionSettings", () => {
	it("is off, and unasked, before the user has ever been offered it", async () => {
		expect(await readCompanionSettings(dir)).toEqual({ enabled: false, asked: false });
	});

	it("stays off when the file is corrupt rather than surprising the user with pets", async () => {
		await writeFile(path.join(dir, COMPANION_SETTINGS_FILE_NAME), "{not json");

		expect(await readCompanionSettings(dir)).toEqual({ enabled: false, asked: false });
	});

	it("treats any non-true value as off", async () => {
		await writeFile(path.join(dir, COMPANION_SETTINGS_FILE_NAME), JSON.stringify({ enabled: "yes", asked: 1 }));

		expect(await readCompanionSettings(dir)).toEqual({ enabled: false, asked: false });
	});

	it("reads back what was written", async () => {
		await writeCompanionSettings(dir, { enabled: true, asked: true });

		expect(await readCompanionSettings(dir)).toEqual({ enabled: true, asked: true });
	});
});

describe("writeCompanionSettings", () => {
	it("writes the settings file into the ~/.ao state dir it is given", async () => {
		await writeCompanionSettings(dir, { enabled: false, asked: true });

		const raw = await readFile(path.join(dir, COMPANION_SETTINGS_FILE_NAME), "utf8");
		expect(JSON.parse(raw)).toEqual({ enabled: false, asked: true });
	});
});
