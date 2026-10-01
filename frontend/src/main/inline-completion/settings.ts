import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import { DEFAULT_MODEL_ID, isModelId, type ModelId } from "./catalog";

/**
 * The person's two choices about inline completion: whether it is on, and which
 * model it runs. Everything else (download progress, process state) is derived
 * at runtime and never written down - a crash must not leave a "downloading"
 * that is not.
 */
export interface InlineCompletionSettings {
	enabled: boolean;
	modelId: ModelId;
}

/** Beside update-settings.json and companion-settings.json, under the ~/.ao state dir. */
export const INLINE_COMPLETION_SETTINGS_FILE_NAME = "inline-completion.json";

const DEFAULTS: InlineCompletionSettings = { enabled: false, modelId: DEFAULT_MODEL_ID };

function coerce(raw: unknown): InlineCompletionSettings {
	const o = (raw ?? {}) as Record<string, unknown>;
	return {
		enabled: o.enabled === true,
		modelId: isModelId(o.modelId) ? o.modelId : DEFAULT_MODEL_ID,
	};
}

/** Read the settings, tolerating a missing or corrupt file (returns defaults). */
export async function readInlineCompletionSettings(stateDir: string): Promise<InlineCompletionSettings> {
	let raw: string;
	try {
		raw = await readFile(path.join(stateDir, INLINE_COMPLETION_SETTINGS_FILE_NAME), "utf8");
	} catch {
		return { ...DEFAULTS };
	}
	try {
		return coerce(JSON.parse(raw));
	} catch {
		return { ...DEFAULTS };
	}
}

/** Atomically write the settings (temp file + rename), mirroring update-settings.ts. */
export async function writeInlineCompletionSettings(
	stateDir: string,
	settings: InlineCompletionSettings,
): Promise<void> {
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, INLINE_COMPLETION_SETTINGS_FILE_NAME);
	const data = `${JSON.stringify(coerce(settings), null, 2)}\n`;
	const tmp = path.join(stateDir, `.inline-completion-${process.pid}-${Date.now()}.json`);
	await writeFile(tmp, data, { mode: 0o600 });
	await rename(tmp, file);
}
