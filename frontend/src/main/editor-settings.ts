import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import { coerceEditorSettings, DEFAULT_EDITOR_SETTINGS, type EditorSettings } from "../shared/editor-settings";

// How the code editor indents and formats, kept in the ~/.ao state dir beside
// update-settings.json and companion-settings.json. The shape, defaults and
// validation live in shared/editor-settings.ts; this is only the file.
export { coerceEditorSettings, DEFAULT_EDITOR_SETTINGS, type EditorSettings };

export const EDITOR_SETTINGS_FILE_NAME = "editor-settings.json";

/** Read the editor settings, tolerating a missing or corrupt file (returns defaults). */
export async function readEditorSettings(stateDir: string): Promise<EditorSettings> {
	let raw: string;
	try {
		raw = await readFile(path.join(stateDir, EDITOR_SETTINGS_FILE_NAME), "utf8");
	} catch {
		return { ...DEFAULT_EDITOR_SETTINGS };
	}
	try {
		return coerceEditorSettings(JSON.parse(raw));
	} catch {
		return { ...DEFAULT_EDITOR_SETTINGS };
	}
}

/** Atomically write the editor settings (temp file + rename), mirroring update-settings.ts. */
export async function writeEditorSettings(stateDir: string, settings: EditorSettings): Promise<EditorSettings> {
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, EDITOR_SETTINGS_FILE_NAME);
	const clean = coerceEditorSettings(settings);
	const tmp = path.join(stateDir, `.editor-settings-${process.pid}-${Date.now()}.json`);
	await writeFile(tmp, `${JSON.stringify(clean, null, 2)}\n`, { mode: 0o600 });
	await rename(tmp, file);
	return clean;
}
