import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";

// The desktop companion's two facts, kept in the ~/.ao state dir alongside
// update-settings.json and app-state.json.
//
// `enabled` is false by default and stays false through every failure path: a
// corrupt file, a missing file, a garbage value. Pets appearing on someone's
// desktop because a JSON parse failed would be a genuinely bad surprise, so the
// safe direction is always off.
//
// `asked` records that the first-run offer has been shown, so it is offered once
// and never nags. Declining sets asked=true with enabled=false.
export interface CompanionSettings {
	enabled: boolean;
	asked: boolean;
}

export const COMPANION_SETTINGS_FILE_NAME = "companion-settings.json";

const DEFAULTS: CompanionSettings = { enabled: false, asked: false };

function coerce(raw: unknown): CompanionSettings {
	const o = (raw ?? {}) as Record<string, unknown>;
	return { enabled: o.enabled === true, asked: o.asked === true };
}

/** Read the companion settings, tolerating a missing or corrupt file (returns defaults). */
export async function readCompanionSettings(stateDir: string): Promise<CompanionSettings> {
	let raw: string;
	try {
		raw = await readFile(path.join(stateDir, COMPANION_SETTINGS_FILE_NAME), "utf8");
	} catch {
		return { ...DEFAULTS };
	}
	try {
		return coerce(JSON.parse(raw));
	} catch {
		return { ...DEFAULTS };
	}
}

/** Atomically write the companion settings (temp file + rename), mirroring update-settings.ts. */
export async function writeCompanionSettings(stateDir: string, settings: CompanionSettings): Promise<void> {
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, COMPANION_SETTINGS_FILE_NAME);
	const data = `${JSON.stringify(coerce(settings), null, 2)}\n`;
	const tmp = path.join(stateDir, `.companion-settings-${process.pid}-${Date.now()}.json`);
	await writeFile(tmp, data, { mode: 0o600 });
	await rename(tmp, file);
}
