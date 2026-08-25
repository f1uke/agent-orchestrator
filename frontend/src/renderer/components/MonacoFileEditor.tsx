import { useEffect, useMemo, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { MONO } from "../lib/comment-inbox";
import { ensureLanguage, ensureMonacoReady, languageForPath, monaco } from "../lib/monaco-setup";
import { editorThemeName } from "../lib/monaco-theme";

type LineChange = components["schemas"]["LineChangeDTO"];

/**
 * Xcode's `// MARK:` bands in the minimap. Monaco finds these itself
 * (`minimap.showMarkSectionHeaders`) and already drops any hit whose first token
 * is not a comment — but its default regex matches ANY line containing `MARK:`,
 * so a comment that merely mentions the convention becomes a band.
 *
 * Two things about this regex are load-bearing, and both were found by watching
 * it silently produce nothing:
 *
 * 1. **The match must START at the comment marker.** Monaco tests the token at
 *    the match's start column against `StandardTokenType.Comment`
 *    (`sectionHeaders.js:117-133`), and a grammar scopes a line's leading
 *    indentation as plain text, not as comment. Anchoring with `^\s*` therefore
 *    drops every INDENTED `// MARK:` — i.e. almost all of them in real Swift.
 *    The lookbehind keeps "the comment is the first thing on this line" without
 *    putting the whitespace inside the match.
 * 2. **Nothing in it may cross a newline.** Monaco compiles the pattern with
 *    `m` and, for anything it reads as multiline-capable, `s`
 *    (`findSectionHeaders.js:60-65`), and runs it over a 100-line chunk rather
 *    than line by line. With `\s` and `.` that makes the match swallow the blank
 *    lines either side, so its start column lands on an empty line — token type
 *    Other, filtered, no band, no error. Hence `[^\S\r\n]` for horizontal
 *    whitespace and `[^\r\n]` for the label.
 *
 * `label` and `separator` are the named groups Monaco reads; a non-empty
 * `separator` draws the rule under the band.
 */
const MARK_SECTION_HEADER_REGEX =
	"(?<=^[^\\S\\r\\n]*)(?://+|#+|--|;+|<!--|/\\*+|\\*+)[^\\S\\r\\n]*MARK:" +
	"[^\\S\\r\\n]*(?<separator>-?)[^\\S\\r\\n]*(?<label>[^\\r\\n]*?)[^\\S\\r\\n]*(?:\\*/|-->)?$";

/**
 * 🗝 Two of these settings are the difference between an Xcode-style `// MARK:`
 * band and one Monaco middle-truncates to `User…action`, and neither is the font
 * size it looks like.
 *
 * **`size` must be `proportional`, not `fit`.** Under `fit` (and `fill`) Monaco
 * silently resets `minimap.scale` to 1 as soon as the file is taller than the
 * minimap canvas can hold — `editorOptions.js:1105-1112`, the sampling branch.
 * A minimap column then measures half a pixel instead of two, the whole minimap
 * comes out ~4x narrower, and `_fitSectionHeader` clips the label in
 * minimap-canvas pixels (`minimap.js:1421-1441`). `proportional` skips that
 * branch, so the configured scale survives at any file length.
 *
 * **`scale: 2` with a `maxColumn` cap, rather than the default `scale: 1`.**
 * Minimap width is proportional to the editor's width, and this editor sits
 * between the sidebar and the inspector rail, so it is routinely far narrower
 * than the window. Measured at `sectionHeaderFontSize: 9`, in usable label px:
 *
 * | editor width | scale 1 | scale 2 | scale 2, maxColumn 80 |
 * |---|---|---|---|
 * | 620 px | 61 | 119 | 119 |
 * | 900 px | 97 | 173 | 152 |
 * | 1512 px | 152 | 302 | 152 |
 *
 * `User Interaction` needs 79 px, so scale 1 only fits it above ~755 px of
 * editor — it truncates exactly when a rail is open. Scale 2 fits it from
 * ~460 px, but uncapped it would hand a 1512 px editor a 310 px minimap.
 * `maxColumn: 80` caps the minimap at 160 px, keeping it between 7% and 19% of
 * the pane at every width the app produces and never below the ~100 px a
 * section label needs.
 */
const MINIMAP: monaco.editor.IEditorMinimapOptions = {
	enabled: true,
	size: "proportional",
	side: "right",
	renderCharacters: true,
	showSlider: "always",
	showMarkSectionHeaders: true,
	showRegionSectionHeaders: true,
	sectionHeaderFontSize: 9,
	sectionHeaderLetterSpacing: 0.4,
	scale: 2,
	maxColumn: 80,
};

const BASE_OPTIONS: monaco.editor.IStandaloneEditorConstructionOptions = {
	readOnly: true,
	domReadOnly: true,
	automaticLayout: true,
	fontFamily: MONO,
	fontSize: 12.5,
	lineHeight: 20,
	fontLigatures: false,
	scrollBeyondLastLine: false,
	smoothScrolling: true,
	renderLineHighlight: "line",
	renderLineHighlightOnlyWhenFocus: false,
	lineNumbersMinChars: 4,
	lineDecorationsWidth: 14,
	glyphMargin: false,
	folding: true,
	showFoldingControls: "mouseover",
	foldingHighlight: true,
	stickyScroll: { enabled: true, maxLineCount: 3 },
	guides: { indentation: true, highlightActiveIndentation: true, bracketPairs: false },
	// The seven `--code-*` roles are the app's syntax language; rainbow brackets
	// would add colours that belong to no role.
	bracketPairColorization: { enabled: false },
	occurrencesHighlight: "singleFile",
	renderWhitespace: "selection",
	// Every file in this repo is full of typographic dashes and arrows. Monaco's
	// ambiguous-character boxes would draw a warning around most comment lines.
	unicodeHighlight: { ambiguousCharacters: false, invisibleCharacters: false, nonBasicASCII: false },
	scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10, useShadows: false },
	padding: { top: 10, bottom: 24 },
	wordWrap: "off",
	minimap: MINIMAP,
};

/** The gutter bar classes; colours live in `styles.css` beside the diff tokens. */
const CHANGE_BAR_CLASS: Record<string, string> = {
	added: "ao-change-bar ao-change-bar--added",
	modified: "ao-change-bar ao-change-bar--modified",
	removed: "ao-change-bar ao-change-bar--removed",
};

/**
 * The read-only Monaco surface behind the workspace file viewer. Lazily imported
 * so Monaco never lands in the app's initial chunk — the cost is paid the first
 * time a file is opened, not at startup.
 */
export default function MonacoFileEditor({
	sessionId,
	path,
	text,
	changedLines,
	line,
	theme,
}: {
	sessionId: string;
	path: string;
	text: string;
	changedLines: LineChange[];
	line?: number;
	theme: "dark" | "light";
}) {
	const hostRef = useRef<HTMLDivElement | null>(null);
	const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
	const decorationsRef = useRef<monaco.editor.IEditorDecorationsCollection | null>(null);
	const [ready, setReady] = useState(false);
	// Bumped once the model is attached AND its grammar has loaded, so the
	// decoration and reveal effects below run against a tokenized model rather
	// than racing the async grammar fetch.
	const [modelGeneration, setModelGeneration] = useState(0);
	const [failed, setFailed] = useState<string | null>(null);

	const themeName = editorThemeName(theme);
	// Read inside async work that outlives the render that started it: the theme
	// can be switched while a grammar is still loading.
	const themeRef = useRef(themeName);
	themeRef.current = themeName;
	const language = useMemo(() => languageForPath(path), [path]);
	// One model per (session, file). Absolute paths are already unique; a
	// workspace-relative one is qualified by its session so two sessions viewing
	// the same relative path do not collide on one model.
	const uri = useMemo(
		() => monaco.Uri.from({ scheme: "ao-file", path: path.startsWith("/") ? path : `/${sessionId}/${path}` }),
		[path, sessionId],
	);

	// Create once per mount; content, language and decorations are applied by the
	// effects below so a re-render never tears the editor down.
	useEffect(() => {
		let disposed = false;
		const host = hostRef.current;
		if (!host) return;
		let editor: monaco.editor.IStandaloneCodeEditor | null = null;
		ensureMonacoReady()
			.then(() => {
				if (disposed) return;
				editor = monaco.editor.create(host, {
					...BASE_OPTIONS,
					theme: themeName,
					minimap: { ...MINIMAP, markSectionHeaderRegex: MARK_SECTION_HEADER_REGEX },
				});
				editorRef.current = editor;
				decorationsRef.current = editor.createDecorationsCollection([]);
				setReady(true);
			})
			.catch((err: unknown) => {
				if (disposed) return;
				console.error("[editor] Monaco failed to start", err);
				setFailed(err instanceof Error ? err.message : "The editor failed to start.");
			});
		return () => {
			disposed = true;
			decorationsRef.current?.clear();
			decorationsRef.current = null;
			editorRef.current = null;
			const model = editor?.getModel();
			editor?.dispose();
			model?.dispose();
		};
		// The editor instance outlives every prop; re-creating it on a theme or
		// path change would drop scroll position and fold state.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);

	// Grammar first, then content. `ensureLanguage` loads the shiki grammar for
	// this language (falling back to Monaco's own Monarch tokenizer when we ship
	// none) and re-applies the theme, which `shikiToMonaco` otherwise resets to
	// the first one it registered. Creating the model only afterwards means its
	// first tokenization pass already uses the right tokenizer — a model attached
	// before the provider exists keeps the tokens it computed then.
	useEffect(() => {
		if (!ready) return;
		let cancelled = false;
		void ensureLanguage(language, themeRef.current).then(() => {
			const editor = editorRef.current;
			if (cancelled || !editor) return;
			const previous = editor.getModel();
			const existing = monaco.editor.getModel(uri);
			const model = existing ?? monaco.editor.createModel(text, language, uri);
			if (existing) {
				if (existing.getValue() !== text) existing.setValue(text);
				monaco.editor.setModelLanguage(existing, language);
			}
			editor.setModel(model);
			if (previous && previous !== model) previous.dispose();
			setModelGeneration((n) => n + 1);
		});
		return () => {
			cancelled = true;
		};
	}, [ready, uri, text, language]);

	// Runtime theme switch: one global call, no editor rebuild.
	useEffect(() => {
		if (!ready) return;
		monaco.editor.setTheme(themeName);
	}, [ready, themeName]);

	// Uncommitted-change bars, the same meaning (working tree vs HEAD) and the
	// same colours the diff rows use.
	useEffect(() => {
		if (modelGeneration === 0) return;
		const editor = editorRef.current;
		const collection = decorationsRef.current;
		const model = editor?.getModel();
		if (!collection || !model) return;
		const lineCount = model.getLineCount();
		const decorations: monaco.editor.IModelDeltaDecoration[] = [];
		for (const change of changedLines) {
			const className = CHANGE_BAR_CLASS[change.kind];
			if (!className) continue;
			if (change.kind === "removed") {
				const at = Math.min(Math.max(change.start, 1), lineCount);
				decorations.push({ range: new monaco.Range(at, 1, at, 1), options: { linesDecorationsClassName: className } });
				continue;
			}
			const start = Math.min(Math.max(change.start, 1), lineCount);
			const end = Math.min(Math.max(change.end, start), lineCount);
			for (let ln = start; ln <= end; ln++) {
				decorations.push({ range: new monaco.Range(ln, 1, ln, 1), options: { linesDecorationsClassName: className } });
			}
		}
		collection.set(decorations);
	}, [modelGeneration, changedLines]);

	// Land on the referenced line rather than on line 1.
	useEffect(() => {
		if (modelGeneration === 0 || line == null) return;
		const editor = editorRef.current;
		const model = editor?.getModel();
		if (!editor || !model) return;
		const target = Math.min(Math.max(line, 1), model.getLineCount());
		editor.setPosition({ lineNumber: target, column: 1 });
		editor.revealLineInCenter(target);
	}, [modelGeneration, line]);

	return (
		<div style={{ position: "relative", flex: 1, minHeight: 0 }}>
			<div ref={hostRef} data-testid="monaco-file-editor" style={{ position: "absolute", inset: 0 }} />
			{failed && (
				<p style={{ position: "absolute", inset: 0, padding: 20, fontSize: 12.5, color: "var(--red)" }}>{failed}</p>
			)}
		</div>
	);
}
