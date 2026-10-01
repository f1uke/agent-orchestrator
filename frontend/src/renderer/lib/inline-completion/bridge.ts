import type { AoBridge } from "../../../preload";
import { aoBridge } from "../bridge";

export type InlineCompletionBridge = AoBridge["inlineCompletion"];

/**
 * The live inline-completion channel, read at CALL time rather than captured at
 * import - the same rule `use-language-server` follows, so a harness page that
 * installs `window.ao` after the app's modules load is still the one answering.
 */
export function inlineCompletionBridge(): InlineCompletionBridge {
	const live = (globalThis as unknown as { ao?: { inlineCompletion?: InlineCompletionBridge } }).ao?.inlineCompletion;
	return live ?? aoBridge.inlineCompletion;
}
