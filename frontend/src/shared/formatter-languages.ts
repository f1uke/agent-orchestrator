/**
 * Which standard tool formats a language, by Monaco language id. Shared so the
 * renderer can tell "no formatter exists" from "the formatter failed" without
 * importing the main process's spawning code.
 */

/** Languages Prettier formats natively - used only where the project configures Prettier. */
export const PRETTIER_LANGUAGES: ReadonlySet<string> = new Set([
	"typescript",
	"javascript",
	"json",
	"css",
	"scss",
	"less",
	"html",
	"markdown",
	"yaml",
	"graphql",
]);

export type FormatterTool = "gofmt" | "swift-format" | "prettier";

export function formatterFor(languageId: string): FormatterTool | null {
	if (languageId === "go") return "gofmt";
	if (languageId === "swift") return "swift-format";
	if (PRETTIER_LANGUAGES.has(languageId)) return "prettier";
	return null;
}
