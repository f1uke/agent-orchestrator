/**
 * Monaco's language id → the language id the server catalogue uses.
 *
 * They agree for Go. This exists so they are allowed to disagree later without a
 * lookup table growing inside a component, and so that "this app ships no server
 * for this language" is a value the UI can render rather than a silent absence.
 */
const LSP_LANGUAGES = new Set(["go"]);

export function languageIdForLsp(monacoLanguageId: string): string | null {
	return LSP_LANGUAGES.has(monacoLanguageId) ? monacoLanguageId : null;
}
