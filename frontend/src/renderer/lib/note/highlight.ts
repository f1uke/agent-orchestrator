/**
 * Syntax highlighting for a note's fenced code blocks.
 *
 * It is `shiki` — the highlighter the code editor already uses — rather than a
 * second library, and it reuses the editor's own two themes so a Go snippet in
 * a note is coloured exactly like the same Go open in the editor.
 *
 * Two things are deliberately NOT shared with `lib/monaco-setup.ts`:
 *
 *   - the highlighter instance, because monaco-setup imports monaco-editor at
 *     module scope and importing it from here would drag the whole editor into
 *     the note view's chunk;
 *   - `codeToHtml`, because this module returns TOKENS. The note renderer draws
 *     them as React spans, so no HTML string is ever built and the "no
 *     innerHTML" rule that makes vault content safe holds for code too.
 */

import { createHighlighterCore, type HighlighterCore } from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";
import { AO_DARK_THEME, AO_LIGHT_THEME } from "../monaco-theme";

/** One highlighted run: its text and the colour to paint it. */
export type CodeToken = { text: string; color?: string };

/**
 * shiki grammars, one static `import()` per entry so Vite emits a small chunk
 * per language instead of bundling all of them (the same reasoning, and the
 * same list shape, as `monaco-setup.ts` GRAMMARS). Keys are what a fence is
 * actually written as in a note, aliases included.
 */
const GRAMMARS: Record<string, () => Promise<unknown>> = {
	bash: () => import("@shikijs/langs/shellscript"),
	c: () => import("@shikijs/langs/c"),
	cpp: () => import("@shikijs/langs/cpp"),
	csharp: () => import("@shikijs/langs/csharp"),
	css: () => import("@shikijs/langs/css"),
	diff: () => import("@shikijs/langs/diff"),
	dockerfile: () => import("@shikijs/langs/docker"),
	go: () => import("@shikijs/langs/go"),
	graphql: () => import("@shikijs/langs/graphql"),
	html: () => import("@shikijs/langs/html"),
	ini: () => import("@shikijs/langs/ini"),
	java: () => import("@shikijs/langs/java"),
	javascript: () => import("@shikijs/langs/jsx"),
	json: () => import("@shikijs/langs/json"),
	kotlin: () => import("@shikijs/langs/kotlin"),
	lua: () => import("@shikijs/langs/lua"),
	markdown: () => import("@shikijs/langs/markdown"),
	php: () => import("@shikijs/langs/php"),
	python: () => import("@shikijs/langs/python"),
	ruby: () => import("@shikijs/langs/ruby"),
	rust: () => import("@shikijs/langs/rust"),
	scss: () => import("@shikijs/langs/scss"),
	shell: () => import("@shikijs/langs/shellscript"),
	sql: () => import("@shikijs/langs/sql"),
	swift: () => import("@shikijs/langs/swift"),
	toml: () => import("@shikijs/langs/toml"),
	typescript: () => import("@shikijs/langs/tsx"),
	xml: () => import("@shikijs/langs/xml"),
	yaml: () => import("@shikijs/langs/yaml"),
};

/** Fence aliases people actually type, mapped onto the grammar keys above. */
const ALIASES: Record<string, string> = {
	sh: "shell",
	zsh: "shell",
	fish: "shell",
	console: "shell",
	shellscript: "shell",
	js: "javascript",
	jsx: "javascript",
	mjs: "javascript",
	cjs: "javascript",
	ts: "typescript",
	tsx: "typescript",
	py: "python",
	rb: "ruby",
	rs: "rust",
	kt: "kotlin",
	yml: "yaml",
	md: "markdown",
	golang: "go",
	"c++": "cpp",
	cs: "csharp",
	docker: "dockerfile",
	patch: "diff",
	htm: "html",
};

/** The grammar key for a fence's info string, or "" when there is none. */
export function grammarFor(language: string): string {
	const key = language.trim().toLowerCase().split(/\s+/)[0] ?? "";
	const resolved = ALIASES[key] ?? key;
	return resolved in GRAMMARS ? resolved : "";
}

let highlighterPromise: Promise<HighlighterCore> | null = null;
const loadedGrammars = new Map<string, Promise<void>>();

function highlighter(): Promise<HighlighterCore> {
	highlighterPromise ??= createHighlighterCore({
		themes: [AO_DARK_THEME, AO_LIGHT_THEME],
		langs: [],
		// The JS engine, not the default WASM one: the renderer's CSP has no
		// 'wasm-unsafe-eval', so oniguruma cannot instantiate. Same reasoning and
		// same `forgiving` flag as monaco-setup.
		engine: createJavaScriptRegexEngine({ forgiving: true }),
	});
	return highlighterPromise;
}

/**
 * Highlights one code block into per-line token runs. An unknown language, or
 * any failure loading a grammar, resolves to null — the caller then draws the
 * code plain, which is a fine outcome for a note.
 */
export async function highlightCode(
	code: string,
	language: string,
	theme: "dark" | "light",
): Promise<CodeToken[][] | null> {
	const grammar = grammarFor(language);
	if (!grammar) return null;
	try {
		const shiki = await highlighter();
		let load = loadedGrammars.get(grammar);
		if (!load) {
			load = GRAMMARS[grammar]().then(async (mod) => {
				await shiki.loadLanguage((mod as { default: never }).default);
			});
			loadedGrammars.set(grammar, load);
		}
		await load;
		const { tokens } = shiki.codeToTokens(code, {
			lang: grammar,
			theme: theme === "light" ? AO_LIGHT_THEME.name : AO_DARK_THEME.name,
		});
		return tokens.map((line) => line.map((token) => ({ text: token.content, color: token.color })));
	} catch {
		// A grammar that will not load must not take the note down with it.
		loadedGrammars.delete(grammar);
		return null;
	}
}
