import { useEffect, useMemo, useRef, useState } from "react";
import type { components } from "../../api/schema";
import { MONO } from "../lib/comment-inbox";
import type { Hunk } from "../lib/editor/change-lanes";
import { branchMarks, GUTTER_LANE_CLASS, uncommittedMarks } from "../lib/editor/gutter-lanes";
import { revertEdit } from "../lib/editor/revert";
import { registerCompletion } from "../lib/lsp/completion-provider";
import { registerLspNavigation } from "../lib/lsp/definition";
import { registerDiagnostics } from "../lib/lsp/diagnostics";
import { type DocumentSync, openDocumentSync } from "../lib/lsp/document-sync";
import { registerHover } from "../lib/lsp/hover-provider";
import { peekFileReader } from "../lib/lsp/peek-file-reader";
import { registerPaneModel } from "../lib/lsp/peek-sources";
import { registerReferences } from "../lib/lsp/references";
import { forgetLane } from "../lib/lsp/request-lane";
import { registerSemanticTokens } from "../lib/lsp/semantic-provider";
import { languageIdForLsp } from "../lib/lsp/language-ids";
import { hasLanguageServers, type LanguageServerHandle, useLanguageServer } from "../lib/lsp/use-language-server";
import { ensureLanguage, ensureMonacoReady, languageForPath, monaco } from "../lib/monaco-setup";
import { editorThemeName } from "../lib/monaco-theme";
import type { WorkspaceFileOpen } from "../lib/open-workspace-file";
import { DiscardHunkPopover } from "./DiscardHunkPopover";

type LineChange = components["schemas"]["LineChangeDTO"];

/**
 * Monaco's own floating message at the cursor - the widget it uses itself to say
 * "no definition found for 'x'" (`gotoSymbol/browser/goToCommands.js:114`).
 *
 * 🗝 Reused rather than reinvented, because slice 3's whole point is that this
 * stack fails by ANSWERING NOTHING: an explicit ⌃Space against a server that is
 * still starting is otherwise indistinguishable from a type that genuinely has
 * no members, since Monaco renders "No suggestions" for both. It is not in the
 * public `.d.ts`, but the contribution is in the barrel build (⌘click pulls it
 * in), and the shape used here is one method.
 */
const MESSAGE_CONTRIBUTION = "editor.contrib.messageController";
type MessageContribution = monaco.editor.IEditorContribution & {
	showMessage(message: string, position: monaco.IPosition): void;
};

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
 * 🗝 Two of these settings decide whether an Xcode-style `// MARK:` band prints
 * its label or Monaco middle-truncates it to `User…action`, and neither is the
 * font size it looks like.
 *
 * **`size` must be `proportional`, not `fit`.** Under `fit` (and `fill`) Monaco
 * silently resets `minimap.scale` to 1 as soon as the file is taller than the
 * minimap canvas can hold — `editorOptions.js:1105-1112`, the sampling branch.
 * A minimap column then measures half a pixel instead of two, the whole minimap
 * comes out ~4x narrower, and `_fitSectionHeader` clips the label in
 * minimap-canvas pixels (`minimap.js:1421-1441`). `proportional` skips that
 * branch, so the configured scale survives at any file length.
 *
 * **`scale: 2` with `maxColumn: 80`.** Minimap width is proportional to the
 * editor's, and this editor sits between the sidebar and the inspector rail, so
 * it is routinely far narrower than the window. What a label may measure, in css
 * px, at `sectionHeaderFontSize: 9` (measured at 1x, which is the tighter of 1x
 * and 2x):
 *
 * | editor width | 520 | 620 | 700 | 780 | 900 | 1240 | 1512 |
 * |---|---|---|---|---|---|---|---|
 * | Monaco default (`scale: 1`, `maxColumn: 120`) | 52 | 64 | 73 | 83 | 97 | 112 | 112 |
 * | **shipped** (`scale: 2`, `maxColumn: 80`) | 94 | 115 | 131 | 148 | 152 | 152 | 152 |
 *
 * `User Interaction` measures 79, so the default only prints it above ~760 px of
 * editor — it truncates exactly when a rail is open, which is the normal case.
 * Scale 2 prints it at every width the app can produce; `maxColumn: 80` then caps
 * the minimap at 160 px from ~800 px of editor upward, so a full-window editor
 * does not hand 20% of the pane to a minimap.
 *
 * ⚠️ The binding case is the NARROW editor, never the wide one: anything that
 * fits at 620 px fits everywhere above it. `Networking & Cache` (99) prints from
 * ~560 px, `Collection View Source` (112) from ~610 px, and
 * `Collection View Data Source` (138) needs ~830 px. Section names past ~22
 * characters are the ones to check when the rails are open.
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
	// 0, not slice 1's 14. The two change lanes moved OUT of this margin (see
	// glyphMargin below) and nothing else needs reserved space here — Monaco
	// sizes the folding controls itself. Keeping 14 on top of the glyph margin's
	// 20 would take 34px off the content width, and the minimap's `// MARK:`
	// label budget is a function of that width: slice 1 measured
	// "Collection View Source" as printing from ~610px, and it stopped.
	lineDecorationsWidth: 0,
	// 🗝 The two change lanes live in the GLYPH MARGIN, not in the line-decorations
	// margin beside the code — and that is not a cosmetic choice. Monaco puts its
	// folding controls in the line-decorations margin, and a folding chevron is a
	// full-width node that sits ON TOP of anything decorated there: a click aimed
	// at a change bar landed on the chevron and collapsed the block instead, and
	// the discard popover could not be opened at all. The glyph margin is a strip
	// nothing else claims. It is also where Xcode draws its change bar — the far
	// left edge, outboard of the line numbers.
	glyphMargin: true,
	folding: true,
	showFoldingControls: "mouseover",
	foldingHighlight: true,
	stickyScroll: { enabled: true, maxLineCount: 3 },
	guides: { indentation: true, highlightActiveIndentation: true, bracketPairs: false },
	// The `--code-*` roles are Xcode's palette, role for role; rainbow brackets
	// would add colours that belong to no role — and Xcode has none.
	bracketPairColorization: { enabled: false },
	occurrencesHighlight: "singleFile",
	/**
	 * 🗝 Semantic tokens are OFF in Monaco standalone without this.
	 * `StandaloneTheme.semanticHighlighting` is hardcoded false and the setting
	 * defaults to `'configuredByTheme'`, so `isSemanticColoringEnabled` says no
	 * and the provider is never asked - silently, like everything else here.
	 * Passing it as a construction option is what writes
	 * `editor.semanticHighlighting.enabled` into the standalone config service.
	 */
	"semanticHighlighting.enabled": true,
	renderWhitespace: "selection",
	// Every file in this repo is full of typographic dashes and arrows. Monaco's
	// ambiguous-character boxes would draw a warning around most comment lines.
	unicodeHighlight: { ambiguousCharacters: false, invisibleCharacters: false, nonBasicASCII: false },
	scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10, useShadows: false },
	padding: { top: 10, bottom: 24 },
	wordWrap: "off",
	minimap: MINIMAP,
};

/**
 * How wide the pane must be before the diff editor shows two columns. Below it
 * Monaco falls back to the inline view on its own, which is the right answer in
 * a split pane with a rail open — two 300px columns of code are unreadable.
 */
const SIDE_BY_SIDE_BREAKPOINT = 900;

/** What the chrome above can do to the buffer without knowing it is Monaco. */
export type EditorHandle = {
	/** The buffer's current text, or null before a model exists. */
	getValue(): string | null;
	focus(): void;
};

export type MonacoFileEditorProps = {
	sessionId: string;
	path: string;
	/** The content considered SAVED. Dirty is measured against this. */
	text: string;
	changedLines: LineChange[];
	/**
	 * New-side lines the BRANCH lane marks: everything this branch changed,
	 * committed or not. Flat and kindless on purpose — see `gutter-lanes.ts`.
	 */
	branchLines?: readonly number[];
	/** The uncommitted hunks, with the old text each replaced, for Discard Change. */
	hunks?: readonly Hunk[];
	line?: number;
	/** 1-based column. Slice 2 left this field on the seam for exactly this. */
	column?: number;
	theme: "dark" | "light";
	readOnly: boolean;
	/**
	 * `code` is Browse mode. `diff` puts the SAME model on the modified side of a
	 * diff editor, with `diffOriginal` on the left.
	 */
	mode?: "code" | "diff";
	diffOriginal?: { text: string; label: string } | null;
	/**
	 * The file's absolute on-disk path. A language server speaks `file:` URIs and
	 * knows nothing about this app's `ao-file:` models, so without it there is no
	 * intelligence for this pane.
	 */
	absolutePath?: string;
	/** The session's worktree root - the key the language server is shared under. */
	workspaceRoot?: string;
	/** The single file-open seam, so ⌘click opens a target the way everything else does. */
	onOpenFile?: (file: WorkspaceFileOpen) => void;
	/** Lets the chrome above report what the language server is doing. */
	onServerState?: (state: { state: LanguageServerHandle["state"]; detail?: string }) => void;
	/**
	 * Errors and warnings currently on this file.
	 *
	 * 🗝 Reported as a COUNT and never as a verdict. gopls's first publish after
	 * opening a file is empty and lands four seconds before the real one, so a
	 * header that renders zero as "no problems" is lying for four seconds.
	 */
	onDiagnostics?: (counts: { errors: number; warnings: number }) => void;
	onDirtyChange?: (dirty: boolean) => void;
	/** ⌘S inside the editor. The chrome above owns what saving means. */
	onSave?: () => void;
	onHandle?: (handle: EditorHandle | null) => void;
};

/**
 * The Monaco surface behind the workspace file viewer. Lazily imported so Monaco
 * never lands in the app's initial chunk — the cost is paid the first time a
 * file is opened, not at startup.
 *
 * 🗝 ONE MODEL PER FILE, shared by both modes. The obvious implementation gives
 * the diff its own `diff-head://` model, and then an edit made while reviewing
 * is invisible in Browse and lost on the next open. Here the mode switch
 * disposes the EDITOR and never the model; the diff's original side is the only
 * extra model, and it is a throwaway.
 */
export default function MonacoFileEditor({
	sessionId,
	path,
	text,
	changedLines,
	branchLines,
	hunks,
	line,
	column,
	theme,
	readOnly,
	mode = "code",
	diffOriginal,
	absolutePath,
	workspaceRoot,
	onOpenFile,
	onServerState,
	onDiagnostics,
	onDirtyChange,
	onSave,
	onHandle,
}: MonacoFileEditorProps) {
	const hostRef = useRef<HTMLDivElement | null>(null);
	const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | monaco.editor.IStandaloneDiffEditor | null>(null);
	/** The editable code editor, whichever mode is mounted. */
	const codeEditorRef = useRef<monaco.editor.ICodeEditor | null>(null);
	const decorationsRef = useRef<monaco.editor.IEditorDecorationsCollection | null>(null);
	const originalModelRef = useRef<monaco.editor.ITextModel | null>(null);
	const [ready, setReady] = useState(false);
	// Bumped once the model is attached AND its grammar has loaded, so the
	// decoration and reveal effects below run against a tokenized model rather
	// than racing the async grammar fetch.
	const [modelGeneration, setModelGeneration] = useState(0);
	// Bumped whenever the editor instance is replaced, which a mode switch does.
	const [editorGeneration, setEditorGeneration] = useState(0);
	const [failed, setFailed] = useState<string | null>(null);
	const [discarding, setDiscarding] = useState<{ hunk: Hunk; top: number; left: number } | null>(null);
	/**
	 * 🗝 Owned here rather than left to Monaco's `useInlineViewWhenSpaceIsLimited`.
	 * The column labels above the diff have to say the same thing the diff is
	 * doing, and Monaco's own decision is not readable from the outside — so at
	 * 1000px the editor fell back to the inline view while two side-by-side
	 * labels went on claiming two columns that were not there.
	 */
	const [sideBySide, setSideBySide] = useState(true);

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

	// The language server for this file's language, in this workspace. Nothing
	// starts until a file of a language we serve is actually opened, so a session
	// nobody opens a .go file in never pays for gopls.
	const lspLanguage = useMemo(() => languageIdForLsp(language), [language]);
	const server = useLanguageServer(workspaceRoot, lspLanguage);

	// Read through refs by the Monaco provider, which is registered once per
	// LANGUAGE and must survive an idle stop and re-attach without being torn
	// down and rebuilt underneath the editor.
	const serverRef = useRef(server);
	serverRef.current = server;
	const openFileRef = useRef(onOpenFile);
	openFileRef.current = onOpenFile;
	const absolutePathRef = useRef(absolutePath);
	absolutePathRef.current = absolutePath;
	const onSaveRef = useRef(onSave);
	onSaveRef.current = onSave;
	const onDirtyRef = useRef(onDirtyChange);
	onDirtyRef.current = onDirtyChange;
	const hunksRef = useRef(hunks);
	hunksRef.current = hunks;
	// The text the model was last SET to. A model whose value has moved away from
	// it carries unsaved edits, and must never be overwritten by an incoming
	// refetch — which is exactly what the read-only version did on every `text`
	// change, and would have silently destroyed edits the moment this pane became
	// editable.
	const appliedTextRef = useRef<string | null>(null);
	// The text the server has: `didOpen` below is keyed on this same value.
	const textRef = useRef(text);
	textRef.current = text;
	const semanticRefreshRef = useRef<(() => void) | null>(null);
	const onDiagnosticsRef = useRef(onDiagnostics);
	onDiagnosticsRef.current = onDiagnostics;
	// One reader per session, stable across a re-registration: the peek widgets
	// need file TEXT, and the daemon's read route is the only path to it.
	const readFile = useMemo(() => peekFileReader(sessionId), [sessionId]);
	// What the language server has been told about this buffer. Read by BOTH
	// providers: the semantic one answers only while the model and the server
	// agree, and now that `didChange` exists the saved text is no longer what the
	// server has.
	const syncRef = useRef<DocumentSync | null>(null);

	useEffect(() => {
		onServerState?.({ state: server.state, detail: server.detail });
	}, [server.state, server.detail, onServerState]);

	// ⌘click. Both halves - the provider AND the opener - live in
	// registerLspNavigation; a provider alone resolves the definition and then
	// does not move the editor.
	useEffect(() => {
		if (!ready || !lspLanguage) return;
		const registration = registerLspNavigation({
			languageId: lspLanguage,
			getClient: () => serverRef.current.client,
			getState: () => serverRef.current.state,
			getWorkspaceRoot: () => workspaceRoot,
			getAbsolutePath: () => absolutePathRef.current ?? null,
			openFile: (file) => openFileRef.current?.(file),
			readFile,
		});
		return () => registration.dispose();
	}, [ready, lspLanguage, workspaceRoot, readFile]);

	// 🗝 The pane's own model, published so a PEEK at a target inside this very
	// file previews the live buffer. Monaco resolves a preview by model URI, and
	// this pane's URI (`ao-file:///<session>/<relative>`) is not the one a
	// server's answer maps to (`ao-file://<absolute>`) — so without this the one
	// case that obviously ought to work, peeking a definition in the file you are
	// already reading, opens with a blank pane.
	useEffect(() => {
		if (!absolutePath || modelGeneration === 0) return;
		return registerPaneModel(absolutePath, uri.toString());
	}, [absolutePath, uri, modelGeneration]);

	// Semantic tokens: the colours the grammar cannot know. Registered per MODEL
	// as well as per language, because Monaco asks one provider about every model
	// of that language and two Swift panes are ordinary.
	useEffect(() => {
		// 🗝 `hasLanguageServers()` and not the pane's state: it is a property of the
		// environment and never flips, so the registration is stable. Gating on the
		// state instead would tear the provider down and rebuild it on every
		// transition, and disposing it is what clears the colours already applied.
		if (!ready || !lspLanguage || modelGeneration === 0 || !hasLanguageServers()) return;
		const registration = registerSemanticTokens({
			languageId: lspLanguage,
			modelUri: uri.toString(),
			getClient: () => serverRef.current.client,
			getAbsolutePath: () => absolutePathRef.current ?? null,
			// 🗝 What the server ACTUALLY has, not the saved text. Since slice 6 the
			// buffer is streamed as it is edited, so "saved" and "what the server
			// holds" are different strings - and answering against the wrong one
			// puts real tokens on the wrong columns with every offset still in
			// range, which is to say silently.
			getServerText: () => syncRef.current?.serverText() ?? textRef.current,
			getState: () => serverRef.current.state,
		});
		semanticRefreshRef.current = registration.refresh;
		return () => {
			semanticRefreshRef.current = null;
			registration.dispose();
		};
	}, [ready, lspLanguage, uri, modelGeneration]);

	// The pane almost always registers before the server has attached, and a
	// provider that could not answer is not asked again on its own.
	useEffect(() => {
		semanticRefreshRef.current?.();
	}, [server.client, server.state, text]);

	// Tell the server about this buffer, and KEEP TELLING IT. Keyed on the CLIENT,
	// so a re-attached server learns about the file that is on screen - the
	// spike's prototype short-circuited here and left the pane with no
	// intelligence and no error.
	//
	// 🗝 Deliberately NOT keyed on `text`. Until slice 6 the server was handed the
	// saved text and re-handed it on every refetch; now the MODEL is the source of
	// truth and `openDocumentSync` streams each edit as an incremental
	// `didChange`. Re-running this effect on a refetch would close and re-open the
	// document underneath a half-typed word.
	useEffect(() => {
		const client = server.client;
		const model = monaco.editor.getModel(uri);
		if (!client || !absolutePath || !lspLanguage || modelGeneration === 0 || !model) return;
		const sync = openDocumentSync({ client, model, absolutePath, languageId: lspLanguage });
		syncRef.current = sync;
		// 🗝 Diagnostics ride the SAME document: the server addresses its
		// unsolicited publishes by the URI `openDocumentSync` opened, which on
		// Swift is the shadow-root spelling and not the file's real path. Deriving
		// it a second time here is how the two would drift apart and every publish
		// would be dropped in silence.
		const diagnostics = registerDiagnostics({
			languageId: lspLanguage,
			client,
			model,
			uri: sync.uri,
			onCounts: (counts) => onDiagnosticsRef.current?.(counts),
		});
		// The colours were computed against the text the server had a moment ago;
		// now that it has this buffer, Monaco has to be asked again.
		semanticRefreshRef.current?.();
		return () => {
			if (syncRef.current === sync) syncRef.current = null;
			diagnostics.dispose();
			sync.dispose();
		};
	}, [server.client, absolutePath, lspLanguage, uri, modelGeneration]);

	// The document is gone, so the one request slot kept for it is too. Owned
	// here rather than in any one provider: hover, completion and references
	// share that slot, and whichever happened to unmount last would otherwise
	// clear it out from under the others.
	useEffect(() => {
		const modelUri = uri.toString();
		return () => forgetLane(modelUri);
	}, [uri]);

	// Autocompletion.
	useEffect(() => {
		// 🗝 `hasLanguageServers()` and not the pane's state, for the same reason
		// the semantic provider uses it: it is a property of the ENVIRONMENT. A
		// registration gated on the attachment would leave a server that failed to
		// start with no provider at all, and ⌃Space against it would then be
		// silent - which is the one thing this feature must never be.
		if (!ready || !lspLanguage || modelGeneration === 0 || !hasLanguageServers()) return;
		const registration = registerCompletion(
			{
				languageId: lspLanguage,
				modelUri: uri.toString(),
				getClient: () => serverRef.current.client,
				getAbsolutePath: () => absolutePathRef.current ?? null,
				getServerText: () => syncRef.current?.serverText() ?? null,
				getState: () => serverRef.current.state,
				getDetail: () => serverRef.current.detail,
				// 🗝 Only ever called for an EXPLICIT ⌃Space. This is Monaco's own
				// "no definition found" widget - the one thing on screen that can say
				// WHY the list is empty, at the place the reader is looking. Firing it
				// while somebody types would be a nag.
				onUnavailable: (reason) => {
					const editor = codeEditorRef.current;
					const position = editor?.getPosition();
					if (!editor || !position) return;
					editor.getContribution<MessageContribution>(MESSAGE_CONTRIBUTION)?.showMessage(reason, position);
				},
			},
			// Null until the server has answered `initialize`, and null for good if
			// it named no completion provider. The provider says which.
			server.client?.completionCapability() ?? null,
		);
		return () => registration.dispose();
	}, [ready, lspLanguage, uri, modelGeneration, server.client]);

	// Hover, and find-all-references. Both registered on the same terms as
	// completion — `hasLanguageServers()` rather than the pane's state, so a
	// server that failed to start still has a provider that can SAY so.
	useEffect(() => {
		if (!ready || !lspLanguage || modelGeneration === 0 || !hasLanguageServers()) return;
		const shared = {
			languageId: lspLanguage,
			modelUri: uri.toString(),
			getClient: () => serverRef.current.client,
			getAbsolutePath: () => absolutePathRef.current ?? null,
			getState: () => serverRef.current.state,
		};
		const hover = registerHover({
			...shared,
			// 🗝 What the server ACTUALLY has. A hover answered against the saved
			// text names the type of a word that is no longer under the pointer,
			// silently, because every offset is still in range.
			getServerText: () => syncRef.current?.serverText() ?? null,
		});
		const references = registerReferences({
			...shared,
			readFile,
			// ⇧F12 is a deliberate gesture, so a refusal answers where the reader is
			// looking — the same widget Monaco uses for "no definition found".
			onUnavailable: (reason) => {
				const editor = codeEditorRef.current;
				const position = editor?.getPosition();
				if (!editor || !position) return;
				editor.getContribution<MessageContribution>(MESSAGE_CONTRIBUTION)?.showMessage(reason, position);
			},
		});
		return () => {
			hover.dispose();
			references.dispose();
		};
	}, [ready, lspLanguage, uri, modelGeneration, readFile]);

	// The host's own width, which is what decides the diff layout: this pane sits
	// between a sidebar and a rail, so it is routinely far narrower than the
	// window and a media query would be measuring the wrong box.
	useEffect(() => {
		const host = hostRef.current;
		if (!host || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver((entries) => {
			const width = entries[0]?.contentRect.width ?? 0;
			if (width > 0) setSideBySide(width >= SIDE_BY_SIDE_BREAKPOINT);
		});
		observer.observe(host);
		return () => observer.disconnect();
	}, []);

	// Monaco itself, once per mount.
	useEffect(() => {
		let disposed = false;
		ensureMonacoReady()
			.then(() => {
				if (!disposed) setReady(true);
			})
			.catch((err: unknown) => {
				if (disposed) return;
				console.error("[editor] Monaco failed to start", err);
				setFailed(err instanceof Error ? err.message : "The editor failed to start.");
			});
		return () => {
			disposed = true;
		};
	}, []);

	// The EDITOR, rebuilt when the mode changes. The model is not touched here:
	// disposing it on a mode switch is precisely the bug the one-buffer rule
	// exists to prevent.
	useEffect(() => {
		if (!ready) return;
		const host = hostRef.current;
		if (!host) return;
		let editor: monaco.editor.IStandaloneCodeEditor | monaco.editor.IStandaloneDiffEditor;
		if (mode === "diff") {
			const diffEditor = monaco.editor.createDiffEditor(host, {
				...BASE_OPTIONS,
				theme: themeName,
				minimap: { ...MINIMAP, markSectionHeaderRegex: MARK_SECTION_HEADER_REGEX },
				readOnly: false,
				domReadOnly: false,
				// The reviewer edits the MODIFIED side in place: that is what makes
				// Changes mode a place to fix something rather than only to read.
				originalEditable: false,
				renderSideBySide: sideBySide,
				// Monaco's own narrow-pane fallback is turned OFF: it decides
				// silently, and the labels above cannot follow a decision they
				// cannot read.
				useInlineViewWhenSpaceIsLimited: false,
				renderMarginRevertIcon: true,
				renderOverviewRuler: false,
			});
			codeEditorRef.current = diffEditor.getModifiedEditor();
			editor = diffEditor;
		} else {
			const codeEditor = monaco.editor.create(host, {
				...BASE_OPTIONS,
				theme: themeName,
				minimap: { ...MINIMAP, markSectionHeaderRegex: MARK_SECTION_HEADER_REGEX },
			});
			codeEditorRef.current = codeEditor;
			editor = codeEditor;
		}
		editorRef.current = editor;
		decorationsRef.current = codeEditorRef.current.createDecorationsCollection([]);
		setEditorGeneration((n) => n + 1);
		return () => {
			decorationsRef.current?.clear();
			decorationsRef.current = null;
			codeEditorRef.current = null;
			editorRef.current = null;
			editor.dispose();
			originalModelRef.current?.dispose();
			originalModelRef.current = null;
		};
		// themeName is applied by its own effect; rebuilding on a theme change
		// would drop scroll position and fold state. sideBySide is applied by the
		// effect below for the same reason.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [ready, mode]);

	// Layout follows the pane's width without rebuilding the editor.
	useEffect(() => {
		if (editorGeneration === 0 || mode !== "diff") return;
		(editorRef.current as monaco.editor.IStandaloneDiffEditor | null)?.updateOptions({ renderSideBySide: sideBySide });
	}, [editorGeneration, mode, sideBySide]);

	// Grammar first, then content. `ensureLanguage` loads the shiki grammar for
	// this language (falling back to Monaco's own Monarch tokenizer when we ship
	// none) and re-applies the theme, which `shikiToMonaco` otherwise resets to
	// the first one it registered. Creating the model only afterwards means its
	// first tokenization pass already uses the right tokenizer — a model attached
	// before the provider exists keeps the tokens it computed then.
	useEffect(() => {
		if (!ready || editorGeneration === 0) return;
		let cancelled = false;
		void ensureLanguage(language, themeRef.current).then(() => {
			const editor = editorRef.current;
			const codeEditor = codeEditorRef.current;
			if (cancelled || !editor || !codeEditor) return;
			const previous = codeEditor.getModel();
			const existing = monaco.editor.getModel(uri);
			const model = existing ?? monaco.editor.createModel(text, language, uri);
			if (existing) {
				// 🗝 Only adopt incoming content when the buffer has NOT moved away
				// from what we last put in it. Overwriting here would throw away
				// unsaved edits every time the file query refetched — and this pane
				// now polls while dirty, so that would be constant.
				const dirty = appliedTextRef.current !== null && existing.getValue() !== appliedTextRef.current;
				if (!dirty && existing.getValue() !== text) existing.setValue(text);
				monaco.editor.setModelLanguage(existing, language);
			}
			if (!existing || appliedTextRef.current === null || model.getValue() === text) {
				appliedTextRef.current = text;
			}
			if (mode === "diff") {
				const diffEditor = editor as monaco.editor.IStandaloneDiffEditor;
				const wanted = diffOriginal?.text ?? "";
				const current = originalModelRef.current;
				// 🗝 Two rules here, and BOTH were needed to stop this pane throwing
				// `TextModel got disposed before DiffEditorWidget model got reset`
				// and `no diff result available` on every single entry into diff
				// mode — the conflict flow's "Review changes" included, which is
				// this slice's headline path. The next setModel papered over them,
				// so the diff still drew, but a whole diff computation was thrown
				// away each time and in Electron these are real unhandled errors in
				// the renderer.
				//
				// 1. REUSE the original when its text has not changed. This effect
				//    runs twice on entry — once for `mode`, once when
				//    `editorGeneration` catches up — and building a second model
				//    disposed the first while the diff worker was still computing
				//    against it, which is where "no diff result available" came
				//    from.
				// 2. Point the widget at the new pair BEFORE disposing the old
				//    model, never after.
				if (!current || current.isDisposed() || current.getValue() !== wanted) {
					const stale = current;
					const original = monaco.editor.createModel(wanted, language);
					originalModelRef.current = original;
					diffEditor.setModel({ original, modified: model });
					stale?.dispose();
				} else if (diffEditor.getModel()?.modified !== model) {
					diffEditor.setModel({ original: current, modified: model });
				}
			} else {
				(editor as monaco.editor.IStandaloneCodeEditor).setModel(model);
			}
			if (previous && previous !== model && previous.uri.scheme === "ao-file") previous.dispose();
			setModelGeneration((n) => n + 1);
		});
		return () => {
			cancelled = true;
		};
	}, [ready, editorGeneration, uri, text, language, mode, diffOriginal?.text]);

	// The model outlives every editor and every mode switch, and is disposed only
	// when this pane goes away or points at a different file.
	//
	// 🗝 Never dispose it while a live editor still holds it — the same defect as
	// the diff-original one above, in a second place. On UNMOUNT this is safe
	// because the editor effect's cleanup is declared earlier and so runs first,
	// leaving `codeEditorRef` null. On a PATH CHANGE it is not: the editor is
	// still on screen showing this model, and the content effect below disposes
	// it properly after attaching the replacement.
	//
	// It was previously closed only by accident — a newly opened file has no
	// cached branch diff, so the pane fell back out of diff mode before the
	// switch. Re-opening a file whose diff IS already cached leaves it in diff
	// mode, and then this fired with the model still attached.
	useEffect(() => {
		return () => {
			appliedTextRef.current = null;
			const model = monaco.editor.getModel(uri);
			if (model && codeEditorRef.current?.getModel() !== model) model.dispose();
		};
	}, [uri]);

	// Dirty tracking and the save command both live with the editor instance.
	useEffect(() => {
		if (modelGeneration === 0) return;
		const codeEditor = codeEditorRef.current;
		const model = codeEditor?.getModel();
		if (!codeEditor || !model) return;
		const report = () => onDirtyRef.current?.(model.getValue() !== text);
		report();
		const subscription = model.onDidChangeContent(report);
		return () => subscription.dispose();
	}, [modelGeneration, editorGeneration, text]);

	// ⌘S from inside the editor. Deliberately a key handler rather than
	// `addCommand`: that only exists on the STANDALONE editor, and Changes mode's
	// editable side is the diff editor's modified pane, which is a plain
	// ICodeEditor. One handler serves both modes.
	useEffect(() => {
		if (editorGeneration === 0) return;
		const codeEditor = codeEditorRef.current;
		if (!codeEditor) return;
		const subscription = codeEditor.onKeyDown((event) => {
			if (!(event.metaKey || event.ctrlKey) || event.keyCode !== monaco.KeyCode.KeyS) return;
			event.preventDefault();
			event.stopPropagation();
			onSaveRef.current?.();
		});
		return () => subscription.dispose();
	}, [editorGeneration]);

	useEffect(() => {
		if (editorGeneration === 0) return;
		codeEditorRef.current?.updateOptions({ readOnly, domReadOnly: readOnly });
	}, [editorGeneration, readOnly]);

	// Expose the buffer to the chrome above, which owns what saving means.
	useEffect(() => {
		if (editorGeneration === 0) {
			onHandle?.(null);
			return;
		}
		onHandle?.({
			getValue: () => codeEditorRef.current?.getModel()?.getValue() ?? null,
			focus: () => codeEditorRef.current?.focus(),
		});
		return () => onHandle?.(null);
	}, [editorGeneration, onHandle]);

	// Runtime theme switch: one global call, no editor rebuild.
	useEffect(() => {
		if (!ready) return;
		monaco.editor.setTheme(themeName);
	}, [ready, themeName]);

	// The two gutter lanes. Branch first so it draws on the outside, nearer the
	// line number; the uncommitted bar sits inboard of it, next to the code.
	useEffect(() => {
		if (modelGeneration === 0) return;
		const collection = decorationsRef.current;
		const model = codeEditorRef.current?.getModel();
		if (!collection || !model) return;
		const lineCount = model.getLineCount();
		const marks = [...branchMarks(branchLines ?? [], lineCount), ...uncommittedMarks(changedLines, lineCount)];
		collection.set(
			marks.map((mark) => ({
				range: new monaco.Range(mark.line, 1, mark.line, 1),
				options: { glyphMarginClassName: mark.className },
			})),
		);
	}, [modelGeneration, editorGeneration, changedLines, branchLines]);

	// A click on an uncommitted bar opens the discard popover. Never discards on
	// the first click: a gutter bar is a one-pixel target beside a line number.
	useEffect(() => {
		if (editorGeneration === 0 || readOnly) return;
		const codeEditor = codeEditorRef.current;
		const host = hostRef.current;
		if (!codeEditor || !host) return;
		const subscription = codeEditor.onMouseDown((event) => {
			if (event.target.type !== monaco.editor.MouseTargetType.GUTTER_GLYPH_MARGIN) return;
			// Belt and braces: the glyph margin is ours alone today, but a later
			// slice putting breakpoints or diagnostics there must not silently start
			// opening the discard popover.
			const element = event.target.element;
			if (!element || !element.className.includes(GUTTER_LANE_CLASS)) return;
			const lineNumber = event.target.position?.lineNumber;
			if (lineNumber == null) return;
			const hunk = (hunksRef.current ?? []).find((h) => lineNumber >= h.start && lineNumber <= h.end);
			if (!hunk) {
				setDiscarding(null);
				return;
			}
			// getScrolledVisiblePosition, not getTopForLineNumber: the latter is off
			// by a line once the editor has scrolled.
			const at = codeEditor.getScrolledVisiblePosition({ lineNumber: hunk.start, column: 1 });
			if (!at) return;
			// Anchored to where the CODE starts, not to a magic number: the gutter's
			// width is a function of the file's line count.
			setDiscarding({ hunk, top: at.top + at.height + 4, left: at.left });
		});
		// 🗝 Only a scroll that MOVED the content closes the popover. Monaco fires
		// onDidScrollChange for the caret placement the very same click makes — so
		// an unfiltered listener dismissed the popover in the same tick it opened,
		// and a gutter click looked like it did nothing at all.
		const scroll = codeEditor.onDidScrollChange((event) => {
			if (event.scrollTopChanged || event.scrollLeftChanged) setDiscarding(null);
		});
		return () => {
			subscription.dispose();
			scroll.dispose();
		};
	}, [editorGeneration, readOnly]);

	const applyDiscard = () => {
		const hunk = discarding?.hunk;
		const codeEditor = codeEditorRef.current;
		const model = codeEditor?.getModel();
		setDiscarding(null);
		if (!hunk || !codeEditor || !model) return;
		const edit = revertEdit(hunk, model.getLineCount(), (ln) => model.getLineMaxColumn(ln));
		// Through the model, so ⌘Z undoes it and the buffer simply becomes dirty:
		// the reader saves it like any other edit, rather than the discard writing
		// the whole file behind their back the way the spike's prototype did.
		model.pushEditOperations(
			null,
			[
				{
					range: new monaco.Range(edit.startLine, edit.startColumn, edit.endLine, edit.endColumn),
					text: edit.text,
				},
			],
			() => null,
		);
		codeEditor.focus();
	};

	// Land on the referenced line and column rather than on line 1. A terminal
	// reference carries no column and ⌘⇧O over files opens at the top, but a
	// go-to-definition target lands on a SYMBOL, and putting the caret on it is
	// the difference between "it moved" and "it moved to roughly the right place".
	useEffect(() => {
		if (modelGeneration === 0 || line == null) return;
		const codeEditor = codeEditorRef.current;
		const model = codeEditor?.getModel();
		if (!codeEditor || !model) return;
		const target = Math.min(Math.max(line, 1), model.getLineCount());
		const targetColumn = Math.min(Math.max(column ?? 1, 1), model.getLineMaxColumn(target));
		codeEditor.setPosition({ lineNumber: target, column: targetColumn });
		codeEditor.revealLineInCenter(target);
	}, [modelGeneration, line, column]);

	return (
		<div style={{ position: "relative", flex: 1, minHeight: 0 }}>
			{mode === "diff" && diffOriginal && (
				<div
					style={{
						position: "absolute",
						top: 0,
						left: 0,
						right: 0,
						zIndex: 5,
						display: "flex",
						fontFamily: MONO,
						fontSize: 10.5,
						letterSpacing: ".03em",
						color: "var(--inbox-muted-2)",
						background: "var(--viewer-bg)",
						borderBottom: "1px solid var(--inbox-divider)",
						pointerEvents: "none",
					}}
				>
					{sideBySide ? (
						<>
							<span style={{ flex: 1, padding: "4px 12px" }}>{diffOriginal.label}</span>
							<span style={{ flex: 1, padding: "4px 12px" }}>this worktree</span>
						</>
					) : (
						// One column, so one label. Two of them over an inline diff
						// claim a layout that is not on screen.
						<span style={{ flex: 1, padding: "4px 12px" }}>{diffOriginal.label} → this worktree</span>
					)}
				</div>
			)}
			{/* The two data attributes are the e2e handles for state that lives
			    inside Monaco and has no DOM of its own. */}
			<div
				ref={hostRef}
				data-testid="monaco-file-editor"
				data-editable={readOnly ? "false" : "true"}
				data-mode={mode}
				style={{ position: "absolute", inset: 0, top: mode === "diff" ? 22 : 0 }}
			/>
			{discarding && (
				<DiscardHunkPopover
					hunk={discarding.hunk}
					top={discarding.top}
					left={discarding.left}
					onDiscard={applyDiscard}
					onDismiss={() => setDiscarding(null)}
				/>
			)}
			{failed && (
				<p style={{ position: "absolute", inset: 0, padding: 20, fontSize: 12.5, color: "var(--red)" }}>{failed}</p>
			)}
		</div>
	);
}
