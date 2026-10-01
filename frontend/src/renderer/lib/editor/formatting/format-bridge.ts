import type { AoBridge } from "../../../../preload";
import { aoBridge } from "../../bridge";

/**
 * The main process's formatting and editor-settings channels, read at CALL
 * time rather than captured at import - the same reason `use-language-server`
 * reads `ao.lsp` lazily: the e2e gallery installs its fakes after the modules
 * that use them have loaded.
 */
type Live = Partial<Pick<AoBridge, "format" | "editorSettings">>;

export function formatBridge(): AoBridge["format"] {
	return (globalThis as { ao?: Live }).ao?.format ?? aoBridge.format;
}

export function editorSettingsBridge(): AoBridge["editorSettings"] {
	return (globalThis as { ao?: Live }).ao?.editorSettings ?? aoBridge.editorSettings;
}
