import { shikiToMonaco } from "@shikijs/monaco";
import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/editor/editor.worker?worker";
import { createHighlighterCore, type HighlighterCore } from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";
import { AO_DARK_THEME, AO_LIGHT_THEME, type EditorThemeName } from "./monaco-theme";

/**
 * One-time Monaco wiring, shared by every editor surface in the renderer.
 *
 * Three things here are load-bearing and were each read out of monaco-editor
 * 0.56's own sources rather than from a tutorial:
 *
 * 1. **The barrel import is mandatory.** `import * as monaco from "monaco-editor"`
 *    registers the standalone contributions; the widely-documented "import
 *    `editor.api` plus hand-picked contribs" recipe is smaller and leaves
 *    `editor.action.revealDefinition` unregistered, so ⌘click silently does
 *    nothing. It also fails to reach `monaco.lsp`, which lives outside the
 *    package's export map.
 * 2. **`MonacoEnvironment.getWorker` is required, and must refuse every label
 *    but its own.** Monaco 0.56 builds the base editor worker by wrapping the
 *    real script in a generated bootstrap and handing `new Worker` a
 *    `URL.createObjectURL` blob (`webWorkerServiceImpl.js:27-31, 85-90`). The
 *    renderer's CSP sets no `worker-src`, so it falls back to
 *    `script-src 'self'` and Chromium blocks that blob outright — and because
 *    everything worker-backed (section headers among them) merely never
 *    answers, the editor looks fine and quietly loses features. Supplying the
 *    worker ourselves via Vite's `?worker` import gives a same-origin script.
 *    The catch is that `MonacoEnvironment.getWorker` intercepts EVERY label at
 *    once (`internal/common/workers.js:101-121`), so a language service would
 *    silently receive a worker that does not speak its protocol; those services
 *    are all disabled below, and this throws rather than hands one over.
 * 3. **Monaco's own language services are switched off** (below). shiki does the
 *    highlighting, we have no tsconfig, and their squiggles and hovers would be
 *    confidently wrong on a file read out of somebody's checkout.
 */

/** The one worker label this app has: the base editor worker. */
const EDITOR_WORKER_LABEL = "editorWorkerService";

declare global {
	var MonacoEnvironment: monaco.Environment | undefined;
}

globalThis.MonacoEnvironment = {
	getWorker(_workerId: string, label: string) {
		if (label !== EDITOR_WORKER_LABEL) {
			// Reachable only if one of the language services below is re-enabled
			// without also being given its own worker. Loud on purpose.
			throw new Error(`[editor] no worker for Monaco language service "${label}"`);
		}
		return new EditorWorker();
	},
};

/** `setModeConfiguration({})` leaves every provider flag falsy → none registered. */
const NO_PROVIDERS = {};

let servicesDisabled = false;
function disableBuiltInLanguageServices(): void {
	if (servicesDisabled) return;
	servicesDisabled = true;
	for (const d of [monaco.typescript.typescriptDefaults, monaco.typescript.javascriptDefaults]) {
		d.setModeConfiguration(NO_PROVIDERS);
		d.setDiagnosticsOptions({ noSemanticValidation: true, noSyntaxValidation: true, noSuggestionDiagnostics: true });
	}
	monaco.json.jsonDefaults.setModeConfiguration(NO_PROVIDERS);
	monaco.json.jsonDefaults.setDiagnosticsOptions({ validate: false, schemaValidation: "ignore" });
	for (const d of [monaco.css.cssDefaults, monaco.css.scssDefaults, monaco.css.lessDefaults]) {
		d.setModeConfiguration(NO_PROVIDERS);
		d.setOptions({ validate: false });
	}
	for (const d of [monaco.html.htmlDefaults, monaco.html.handlebarDefaults, monaco.html.razorDefaults]) {
		d.setModeConfiguration(NO_PROVIDERS);
	}
}

/**
 * shiki grammars, keyed by the MONACO language id they highlight. Written out
 * one static `import()` per entry on purpose: a template-literal specifier
 * would make Vite bundle all 361 grammars, where this emits one small chunk per
 * language and fetches it only when a file of that language is opened.
 *
 * Two ids map to a superset grammar because Monaco resolves `.ts`/`.tsx` (and
 * `.js`/`.jsx`) to a single language id, and the JSX grammars parse the
 * non-JSX dialect correctly while the reverse is not true.
 */
const GRAMMARS: Record<string, () => Promise<unknown>> = {
	c: () => import("@shikijs/langs/c"),
	cpp: () => import("@shikijs/langs/cpp"),
	csharp: () => import("@shikijs/langs/csharp"),
	css: () => import("@shikijs/langs/css"),
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
	"objective-c": () => import("@shikijs/langs/objective-c"),
	perl: () => import("@shikijs/langs/perl"),
	php: () => import("@shikijs/langs/php"),
	proto: () => import("@shikijs/langs/proto"),
	python: () => import("@shikijs/langs/python"),
	r: () => import("@shikijs/langs/r"),
	ruby: () => import("@shikijs/langs/ruby"),
	rust: () => import("@shikijs/langs/rust"),
	scala: () => import("@shikijs/langs/scala"),
	scss: () => import("@shikijs/langs/scss"),
	shell: () => import("@shikijs/langs/shellscript"),
	sql: () => import("@shikijs/langs/sql"),
	swift: () => import("@shikijs/langs/swift"),
	toml: () => import("@shikijs/langs/toml"),
	typescript: () => import("@shikijs/langs/tsx"),
	xml: () => import("@shikijs/langs/xml"),
	yaml: () => import("@shikijs/langs/yaml"),
};

/**
 * Languages Monaco has no definition for, so `getLanguages()` cannot resolve
 * their extension. Registered with no Monarch tokenizer — the shiki grammar
 * above is the whole tokenizer.
 */
const EXTRA_LANGUAGES: monaco.languages.ILanguageExtensionPoint[] = [
	{ id: "toml", extensions: [".toml"], aliases: ["TOML", "toml"] },
];

let extrasRegistered = false;
function registerExtraLanguages(): void {
	if (extrasRegistered) return;
	extrasRegistered = true;
	const known = new Set(monaco.languages.getLanguages().map((l) => l.id));
	for (const lang of EXTRA_LANGUAGES) if (!known.has(lang.id)) monaco.languages.register(lang);
}

/**
 * The Monaco language id for a path, from Monaco's OWN registry (extensions and
 * exact filenames), so there is no second extension table to keep right.
 * `plaintext` when nothing matches.
 */
export function languageForPath(path: string): string {
	registerExtraLanguages();
	const base = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
	const dot = base.lastIndexOf(".");
	const ext = dot > 0 ? base.slice(dot) : "";
	let byExtension = "";
	for (const lang of monaco.languages.getLanguages()) {
		if (lang.filenames?.some((f) => f.toLowerCase() === base)) return lang.id;
		if (!byExtension && ext && lang.extensions?.some((e) => e.toLowerCase() === ext)) byExtension = lang.id;
	}
	return byExtension || "plaintext";
}

// `shikiToMonaco` wraps `monaco.editor.setTheme` and `monaco.editor.create`. It
// has to run again each time a grammar is added, so the pristine functions are
// captured once and restored before every re-sync — otherwise the wrappers nest
// once per opened language.
const editorApi = monaco.editor as unknown as {
	setTheme: (name: string) => void;
	create: typeof monaco.editor.create;
};
const pristine = { setTheme: editorApi.setTheme, create: editorApi.create };

let highlighter: HighlighterCore | null = null;
let readyPromise: Promise<void> | null = null;
// Keyed by language id, so a second editor opening the same language awaits the
// first load rather than racing it — attaching a model before its tokenizer is
// registered leaves the file rendered with whatever tokenizer existed then.
const grammarLoads = new Map<string, Promise<void>>();

async function init(): Promise<void> {
	disableBuiltInLanguageServices();
	registerExtraLanguages();
	highlighter = await createHighlighterCore({
		themes: [AO_DARK_THEME, AO_LIGHT_THEME],
		langs: [],
		// The JS engine keeps `script-src 'self'` intact: shiki's default oniguruma
		// engine is WASM, and Chromium blocks `WebAssembly.instantiate` without
		// 'wasm-unsafe-eval'. Widening the CSP for a syntax highlighter is a bad
		// trade. `forgiving` degrades a pattern the JS engine cannot express into
		// "no match" instead of throwing away the whole grammar.
		engine: createJavaScriptRegexEngine({ forgiving: true }),
	});
	resync();
}

/** Re-register shiki's tokenizers and themes, with exactly one wrapper layer. */
function resync(): void {
	if (!highlighter) return;
	editorApi.setTheme = pristine.setTheme;
	editorApi.create = pristine.create;
	shikiToMonaco(highlighter, monaco);
}

/** Boots Monaco + shiki once; safe to await repeatedly. */
export function ensureMonacoReady(): Promise<void> {
	readyPromise ??= init();
	return readyPromise;
}

/**
 * Loads the shiki grammar for `languageId`, if we ship one, and re-registers the
 * tokenizers. A language we ship no grammar for keeps Monaco's own Monarch
 * tokenizer, which the app theme also colours — so callers need no fallback.
 *
 * `theme` is re-applied because `shikiToMonaco` ends by setting the FIRST loaded
 * theme, which would otherwise flip a light-mode editor to dark on every
 * grammar load.
 */
export async function ensureLanguage(languageId: string, theme: EditorThemeName): Promise<void> {
	await ensureMonacoReady();
	const load = GRAMMARS[languageId];
	if (load) {
		let pending = grammarLoads.get(languageId);
		if (!pending) {
			pending = load()
				.then(async (grammar) => {
					await highlighter?.loadLanguage(grammar as Parameters<HighlighterCore["loadLanguage"]>[0]);
					resync();
				})
				.catch((err: unknown) => {
					// Monarch still tokenizes the file, so this degrades rather than blanks.
					grammarLoads.delete(languageId);
					console.warn(`[editor] no shiki grammar for ${languageId}, falling back to Monarch`, err);
				});
			grammarLoads.set(languageId, pending);
		}
		await pending;
	}
	monaco.editor.setTheme(theme);
}

export { monaco };
