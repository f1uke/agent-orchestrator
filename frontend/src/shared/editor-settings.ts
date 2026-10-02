import { DEFAULT_FORMAT_SHORTCUT, DEFAULT_REINDENT_SHORTCUT, normalizeShortcut } from "./editor-shortcuts";

/**
 * How the code editor indents and formats. Stored by the main process in
 * `~/.ao/editor-settings.json`; read by every editor pane and the Settings page.
 * Kept here, free of `node:` imports, so the renderer can share the defaults
 * and the validation instead of restating them.
 */
export interface EditorSettings {
	/** Return, closing brackets and paste re-indent as you type. */
	indentOnType: boolean;
	/** Format the document before every save. */
	formatOnSave: boolean;
	/** Re-indent the selected lines (Xcode's Re-Indent). "" = unbound. */
	reindentShortcut: string;
	/** Format the whole document. "" = unbound. */
	formatShortcut: string;
}

export const DEFAULT_EDITOR_SETTINGS: EditorSettings = {
	indentOnType: true,
	formatOnSave: false,
	reindentShortcut: DEFAULT_REINDENT_SHORTCUT,
	formatShortcut: DEFAULT_FORMAT_SHORTCUT,
};

/**
 * Every unreadable value lands on its default: a preference that cannot be
 * read is not a reason for the editor to stop indenting.
 */
export function coerceEditorSettings(raw: unknown): EditorSettings {
	const o = (raw ?? {}) as Record<string, unknown>;
	return {
		indentOnType: typeof o.indentOnType === "boolean" ? o.indentOnType : DEFAULT_EDITOR_SETTINGS.indentOnType,
		formatOnSave: o.formatOnSave === true,
		reindentShortcut: normalizeShortcut(o.reindentShortcut) ?? DEFAULT_EDITOR_SETTINGS.reindentShortcut,
		formatShortcut: normalizeShortcut(o.formatShortcut) ?? DEFAULT_EDITOR_SETTINGS.formatShortcut,
	};
}
