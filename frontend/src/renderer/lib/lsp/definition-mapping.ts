import type { IRange } from "monaco-editor";

/**
 * Turning a language server's answer into editor coordinates.
 *
 * Kept apart from `definition.ts` because that file imports the monaco BARREL,
 * which boots the whole editor on import - so the arithmetic here, which is
 * where off-by-ones live, would otherwise be untestable without a DOM.
 */

type LspPosition = { line: number; character: number };
type LspRange = { start: LspPosition; end: LspPosition };

/**
 * What Monaco's DefinitionProvider returns, minus the runtime import. Generic in
 * the URI type because this function genuinely does not care what a URI is - it
 * only hands each one to the caller's factory - and that is what lets the
 * arithmetic be tested with plain strings.
 */
export type DefinitionLink<TUri> = { uri: TUri; range: IRange };

function isRange(value: unknown): value is LspRange {
	const r = value as LspRange | undefined;
	return (
		typeof r?.start?.line === "number" &&
		typeof r.start.character === "number" &&
		typeof r.end?.line === "number" &&
		typeof r.end.character === "number"
	);
}

/** LSP is 0-based in both axes; Monaco is 1-based in both. */
function toMonacoRange(range: LspRange): IRange {
	return {
		startLineNumber: range.start.line + 1,
		startColumn: range.start.character + 1,
		endLineNumber: range.end.line + 1,
		endColumn: range.end.character + 1,
	};
}

/**
 * Normalise every shape `textDocument/definition` may answer with - `Location`,
 * `Location[]`, `LocationLink[]` - into Monaco's links. Anything unrecognised
 * degrades to no definitions rather than throwing: a provider that throws inside
 * Monaco's pipeline takes the editor's whole ⌘click gesture down with it.
 */
export function toMonacoDefinitions<TUri>(
	result: unknown,
	_modelUri: TUri,
	targetUriToModelUri: (uri: string) => TUri,
): DefinitionLink<TUri>[] {
	const items = Array.isArray(result) ? result : result ? [result] : [];
	const links: DefinitionLink<TUri>[] = [];
	for (const item of items) {
		if (!item || typeof item !== "object") continue;
		const asLocation = item as { uri?: string; range?: unknown };
		const asLink = item as { targetUri?: string; targetRange?: unknown; targetSelectionRange?: unknown };
		const uri = asLink.targetUri ?? asLocation.uri;
		// `targetSelectionRange` is the IDENTIFIER; `targetRange` is the whole
		// declaration body. Landing on the identifier is what Xcode does.
		const range = asLink.targetSelectionRange ?? asLink.targetRange ?? asLocation.range;
		if (typeof uri !== "string" || !isRange(range)) continue;
		links.push({ uri: targetUriToModelUri(uri), range: toMonacoRange(range) });
	}
	return links;
}
