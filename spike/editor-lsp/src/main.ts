// THROWAWAY SPIKE. Proves three things and measures them:
//   1. Cmd+Shift+O — fuzzy over files AND workspace/symbol, in one list.
//   2. Correct syntax highlighting — TextMate grammars via shiki.
//   3. Cmd+click — textDocument/definition, opening the target file at the line.
// monaco-editor 0.56's package exports only expose `editor.api` (no
// contributions) and the barrel. Importing `contrib/gotoSymbol/...` by hand is
// NOT enough in 0.56 — `editor.action.revealDefinition` never registers, so
// ⌘click resolves a definition and then goes nowhere. Measured, see the
// proposal. The barrel is the supported entry; its 160 language registrations
// are lazy chunks, so they cost disk, not cold load.
import * as monaco from "monaco-editor";
import { createHighlighterCore } from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";
import { shikiToMonaco } from "@shikijs/monaco";
// `bundledLanguages` is a map of id -> lazy dynamic import, so nothing is pulled
// until a file of that language is opened. That is what makes "every language"
// affordable: 361 grammars on disk, one fetched when you first open a .rs file.
import { bundledLanguages } from "shiki";
import themeDark from "@shikijs/themes/github-dark-default";
import themeLight from "@shikijs/themes/github-light-default";
import { Lsp } from "./lsp";
import "./style.css";

// Vite emits each worker as a same-origin file. Monaco's default worker loader
// builds a blob: URL, which `default-src 'self'` blocks — this is the CSP trap.
//
// Once every built-in language is reachable, Monaco's OWN language services
// wake up for ts/js/css/html/json and demand their dedicated workers. Handing
// them the plain editor worker throws a stream of
// "Missing requestHandler or method: getSyntacticDiagnostics". They are lazy
// chunks, so this costs disk, not cold load — and it buys real validation and
// completion in the languages we have no language server for.
import EditorWorker from "monaco-editor/editor/editor.worker?worker";
import TsWorker from "monaco-editor/language/typescript/ts.worker?worker";
import JsonWorker from "monaco-editor/language/json/json.worker?worker";
import CssWorker from "monaco-editor/language/css/css.worker?worker";
import HtmlWorker from "monaco-editor/language/html/html.worker?worker";
self.MonacoEnvironment = {
	getWorker(_moduleId: string, label: string) {
		switch (label) {
			case "typescript":
			case "javascript":
				return new TsWorker();
			case "json":
				return new JsonWorker();
			case "css":
			case "scss":
			case "less":
				return new CssWorker();
			case "html":
			case "handlebars":
			case "razor":
				return new HtmlWorker();
			default:
				return new EditorWorker();
		}
	},
};

const BRIDGE = "http://127.0.0.1:8917";
const el = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const logEl = el("log");
const statsEl = el("stats");
const t0 = performance.now();
const log = (s: string) => {
	const line = document.createElement("div");
	line.textContent = `${(performance.now() - t0).toFixed(0).padStart(6)}ms  ${s}`;
	logEl.prepend(line);
};

type Meta = { root: string; lang: "go" | "swift"; files: string[]; indexMs: number; languages: string[] };
const meta: Meta = await (await fetch(`${BRIDGE}/files`)).json();
log(`file index: ${meta.files.length} files in ${meta.indexMs}ms (${meta.lang} @ ${meta.root})`);

// ---- 2. syntax highlighting: real TextMate grammars ----------------------
const tHl = performance.now();
// Only the workspace's own language is loaded up front; everything else arrives
// on demand through ensureGrammar().
const langs = meta.lang === "swift" ? ["swift", "objective-c"] : ["go"];
const highlighter = await createHighlighterCore({
	themes: [themeDark, themeLight],
	langs: await Promise.all(langs.map((l) => bundledLanguages[l as keyof typeof bundledLanguages]())),
	// The JS engine avoids oniguruma's WASM. Under `script-src 'self'` Chromium
	// refuses WebAssembly.instantiate unless 'wasm-unsafe-eval' is present, so
	// the WASM engine would need a CSP change; this one does not.
	engine: createJavaScriptRegexEngine(),
});
for (const l of langs) monaco.languages.register({ id: l });
shikiToMonaco(highlighter, monaco);

// ---- every language Monaco or shiki knows ---------------------------------
// Monaco already ships 84 Monarch grammars and registers them all through the
// barrel import; shiki has 361 TextMate grammars, which are higher fidelity.
// So: resolve the language from Monaco's own registry (it owns the extension
// table), then upgrade to shiki's grammar when it has one, and otherwise keep
// Monaco's — which still colours the file rather than showing it grey.
const byExtension = new Map<string, string>();
const byFilename = new Map<string, string>();
for (const l of monaco.languages.getLanguages()) {
	for (const e of l.extensions ?? []) byExtension.set(e.toLowerCase(), l.id);
	for (const f of l.filenames ?? []) byFilename.set(f.toLowerCase(), l.id);
}
// Where the two registries disagree on the id for the same language.
const SHIKI_ALIAS: Record<string, string> = {
	shell: "shellscript",
	bat: "batch",
	dockerfile: "docker",
	"objective-c": "objective-c",
	"objective-cpp": "objective-cpp",
	restructuredtext: "rst",
	freemarker2: "handlebars",
	mips: "asm",
	pascaligo: "pascal",
	sb: "vb",
};
const loadedGrammars = new Set(langs);
const failedGrammars = new Set<string>();

async function ensureGrammar(id: string) {
	if (id === "plaintext" || loadedGrammars.has(id) || failedGrammars.has(id)) return;
	const shikiId = SHIKI_ALIAS[id] ?? id;
	const loader = bundledLanguages[shikiId as keyof typeof bundledLanguages];
	if (!loader) {
		// No TextMate grammar — Monaco's own Monarch one is already registered and
		// will colour it. Remember, so we do not look again on every open.
		failedGrammars.add(id);
		return;
	}
	const t = performance.now();
	try {
		await highlighter.loadLanguage(await loader());
		monaco.languages.register({ id });
		shikiToMonaco(highlighter, monaco);
		loadedGrammars.add(id);
		log(`grammar "${id}" loaded in ${(performance.now() - t).toFixed(0)}ms (${loadedGrammars.size} loaded)`);
	} catch {
		failedGrammars.add(id);
	}
}
log(`shiki grammars (${langs.join(", ")}) ready in ${(performance.now() - tHl).toFixed(0)}ms`);

const editor = monaco.editor.create(el("editor"), {
	value: "",
	language: langs[0],
	theme: "github-dark-default",
	automaticLayout: true,
	fontSize: 12,
	// `size: "fit"` scales the minimap so the WHOLE file always fits — Xcode's
	// minimap behaves the same way, and it also means the document maps onto the
	// minimap linearly, which is what lets the MARK overlay below line up.
	minimap: {
		enabled: true,
		size: "fit",
		renderCharacters: true,
		showSlider: "always",
		// Xcode's minimap section labels, built in. `markSectionHeaderRegex`
		// covers Swift/ObjC `// MARK:` and `showRegionSectionHeaders` covers
		// `// #region`. Default 9px/1px is small on a Retina panel.
		showMarkSectionHeaders: true,
		showRegionSectionHeaders: true,
		sectionHeaderFontSize: 9,
		sectionHeaderLetterSpacing: 0.4,
		// scale 2 gives the minimap canvas 2x the pixels, which is what makes it
		// readable at all on a Retina panel; maxColumn buys the labels room.
		scale: 2,
		maxColumn: 220,
	},
	scrollBeyondLastLine: false,
	renderLineHighlight: "all",
	// Room for two decoration lanes: branch-level and uncommitted.
	lineDecorationsWidth: 16,
});
log(`monaco editor created`);

// ---- LSP: one client per LANGUAGE ----------------------------------------
// gopls has no idea what a .swift file is. A single server for the whole
// workspace only works while the workspace is single-language — the moment it
// is not, every request for the other language goes to the wrong process and
// fails quietly. So: a client per language, connected the first time a file of
// that language is opened, and never before.
const rootUri = "file://" + meta.root;
const clients = new Map<string, Lsp>();
const noServer = new Set<string>();

async function lspFor(langId: string): Promise<Lsp | null> {
	if (!meta.languages.includes(langId)) return null; // no server configured
	if (noServer.has(langId)) return null;
	const existing = clients.get(langId);
	if (existing) return existing;
	const client = new Lsp(`ws://127.0.0.1:8917/lsp?lang=${encodeURIComponent(langId)}`, rootUri, log);
	clients.set(langId, client);
	const t = performance.now();
	try {
		await client.connect();
	} catch {
		clients.delete(langId);
		noServer.add(langId);
		log(`no language server for "${langId}"`);
		return null;
	}
	log(`language server for "${langId}" ready in ${(performance.now() - t).toFixed(0)}ms`);
	registerProviders(langId, client);
	return client;
}

// The client that owns the file currently in front of us.
const lspForModel = (uri: string) => clients.get(langFor(uri)) ?? null;

const opened = new Set<string>();
let currentUri = "";
let firstDefinitionAt = 0;

// ONE buffer per file, shared by the editor and the diff's modified side. Two
// models for the same path would let an edit made in one mode vanish in the
// other, which is the kind of bug nobody reports because they assume they
// imagined it.
async function modelFor(rel: string, abs?: string) {
	const q = abs
		? `${BRIDGE}/open-external?abs=${encodeURIComponent(abs)}`
		: `${BRIDGE}/file?path=${encodeURIComponent(rel)}`;
	const f = await (await fetch(q)).json();
	await ensureGrammar(langFor(f.uri));
	const uri = monaco.Uri.parse(f.uri);
	const model = monaco.editor.getModel(uri) ?? monaco.editor.createModel(f.text, langFor(f.uri), uri);
	if (!endsWithNewline.has(f.uri)) endsWithNewline.set(f.uri, String(f.text ?? "").endsWith("\n"));
	// Only reset from disk when the buffer has no unsaved edits of its own.
	if (!dirtyBuffers.has(f.uri) && model.getValue() !== f.text) model.setValue(f.text);
	if (!opened.has(f.uri)) {
		opened.add(f.uri);
		// This is the moment a language server for THIS language starts — not at
		// app launch, and not for languages nobody opened.
		const client = await lspFor(langFor(f.uri));
		client?.didOpen(f.uri, langFor(f.uri), model.getValue());
	}
	return { f, model };
}

async function openPath(rel: string, line = 1, abs?: string) {
	// Reached from ⌘click, ⌘⇧O or the Browse tree — all of which mean "put me in
	// the file", so leave the diff behind rather than editing a hidden buffer.
	if (mode === "changes") setMode("browse");
	const { f, model } = await modelFor(rel, abs);
	editor.setModel(model);
	editor.revealLineInCenter(line);
	editor.setPosition({ lineNumber: line, column: 1 });
	editor.focus();
	currentUri = f.uri;
	currentRel = rel;
	el<HTMLElement>("title").textContent = `AO spike · ${rel || f.abs}:${line}`;
	await refreshChanges();
	return f;
}
function langFor(uri: string): string {
	const name = (uri.split("/").pop() ?? "").toLowerCase();
	const byName = byFilename.get(name);
	if (byName) return byName;
	const dot = name.lastIndexOf(".");
	if (dot < 0) return "plaintext";
	return byExtension.get(name.slice(dot)) ?? "plaintext";
}

// ---- 3. Cmd+click → textDocument/definition -------------------------------
// Monaco routes cmd+click through its own definition provider, so registering
// one is all it takes — no gesture handling of our own.
const providersFor = new Set<string>();

function registerProviders(langId: string, client: Lsp) {
	if (providersFor.has(langId)) return;
	providersFor.add(langId);

	// ---- 3. Cmd+click -> textDocument/definition --------------------------
	// Monaco routes cmd+click through its own definition provider, so
	// registering one is all it takes — no gesture handling of our own.
	monaco.languages.registerDefinitionProvider(langId, {
		provideDefinition: async (model, position) => {
			const r = await client.definition(model.uri.toString(), position.lineNumber - 1, position.column - 1);
			const locs: any[] = Array.isArray(r.result) ? r.result : r.result ? [r.result] : [];
			if (!firstDefinitionAt && locs.length) {
				firstDefinitionAt = performance.now();
				log(`FIRST WORKING definition at ${(firstDefinitionAt - t0).toFixed(0)}ms from page load`);
			}
			log(`definition (${langId}) → ${locs.length} hit(s) in ${r.elapsedMs.toFixed(0)}ms`);
			// A hit can live outside the workspace (Pods, SDK headers). Fetch it so
			// Monaco has a model to reveal, then let Monaco do the navigation.
			const out: monaco.languages.Location[] = [];
			for (const l of locs) {
				const uri = l.uri ?? l.targetUri;
				const range = l.range ?? l.targetSelectionRange ?? l.targetRange;
				const mUri = monaco.Uri.parse(uri);
				if (!monaco.editor.getModel(mUri)) {
					const abs = decodeURIComponent(uri.replace("file://", ""));
					try {
						const f = await (await fetch(`${BRIDGE}/open-external?abs=${encodeURIComponent(abs)}`)).json();
						await ensureGrammar(langFor(uri));
						monaco.editor.createModel(f.text, langFor(uri), mUri);
					} catch {
						continue;
					}
				}
				out.push({
					uri: mUri,
					range: {
						startLineNumber: range.start.line + 1,
						startColumn: range.start.character + 1,
						endLineNumber: range.end.line + 1,
						endColumn: range.end.character + 1,
					},
				});
			}
			return out;
		},
	});

	// ---- 4. autocompletion -> textDocument/completion ----------------------
	monaco.languages.registerCompletionItemProvider(langId, {
		triggerCharacters: [".", ":", "("],
		provideCompletionItems: async (model, position, context) => {
			const uri = model.uri.toString();
			clearTimeout(changeTimers.get(uri));
			client.didChange(uri, model.getValue());
			const r = await client.completion(uri, position.lineNumber - 1, position.column - 1, context.triggerCharacter);
			const list = Array.isArray(r.result) ? r.result : (r.result?.items ?? []);
			lastCompletion = { ms: Math.round(r.elapsedMs), n: list.length };
			log(
				`completion (${langId}) → ${list.length} item(s) in ${r.elapsedMs.toFixed(0)}ms${r.result?.isIncomplete ? " (incomplete)" : ""}`,
			);
			const word = model.getWordUntilPosition(position);
			const range = {
				startLineNumber: position.lineNumber,
				endLineNumber: position.lineNumber,
				startColumn: word.startColumn,
				endColumn: word.endColumn,
			};
			return {
				incomplete: !!r.result?.isIncomplete,
				suggestions: list.slice(0, 300).map((it: any) => {
					const edit = it.textEdit;
					const er = edit?.range ?? edit?.replace;
					return {
						label: it.labelDetails
							? { label: it.label, detail: it.labelDetails.detail, description: it.labelDetails.description }
							: it.label,
						kind: LSP_KIND[it.kind] ?? monaco.languages.CompletionItemKind.Property,
						detail: it.detail,
						documentation: typeof it.documentation === "string" ? it.documentation : it.documentation?.value,
						insertText: edit?.newText ?? it.insertText ?? it.label,
						insertTextRules:
							it.insertTextFormat === 2 ? monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
						filterText: it.filterText,
						sortText: it.sortText,
						range: er
							? {
									startLineNumber: er.start.line + 1,
									startColumn: er.start.character + 1,
									endLineNumber: er.end.line + 1,
									endColumn: er.end.character + 1,
								}
							: range,
					} as monaco.languages.CompletionItem;
				}),
			};
		},
	});
}

// Monaco will not switch models on its own in a bare editor; do it on reveal.
editor.onDidChangeModel(() => {
	currentUri = editor.getModel()?.uri.toString() ?? "";
});
(editor as any)._codeEditorService.openCodeEditor = async (input: any, source: any) => {
	const uri: monaco.Uri = input.resource;
	const model = monaco.editor.getModel(uri) ?? monaco.editor.createModel("", langFor(uri.toString()), uri);
	source.setModel(model);
	const line = input.options?.selection?.startLineNumber ?? 1;
	source.revealLineInCenter(line);
	source.setPosition({ lineNumber: line, column: input.options?.selection?.startColumn ?? 1 });
	el<HTMLElement>("title").textContent = `AO spike · ${uri.path.replace(meta.root + "/", "")}:${line}`;
	log(`jumped → ${uri.path.replace(meta.root + "/", "")}:${line}`);
	return source;
};

// ---- 9. save -------------------------------------------------------------
// Editing is only real once it reaches disk. One save path for both modes,
// because both edit the same buffer.
const dirtyBuffers = new Set<string>();
// Whether the file on disk ended with a newline when we read it.
const endsWithNewline = new Map<string, boolean>();

async function saveCurrent() {
	const model = mode === "changes" ? diffEditor?.getModel()?.modified : editor.getModel();
	if (!model) return;
	const rel = relOf(model.uri.toString());
	if (!rel) {
		log("cannot save a file outside the workspace");
		return;
	}
	// Found by qa: writing model.getValue() straight through drops a trailing
	// newline whenever the last line is blank, and git then reports
	// "\ No newline at end of file" — a one-line edit reads as two. Preserve
	// whatever the file had rather than silently changing it.
	let text = model.getValue();
	if (endsWithNewline.get(model.uri.toString()) && !text.endsWith("\n")) text += "\n";
	const t = performance.now();
	const res = await fetch(`${BRIDGE}/write`, {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({ path: rel, text }),
	});
	if (!res.ok) {
		log(`save failed: ${res.status}`);
		return;
	}
	dirtyBuffers.delete(model.uri.toString());
	log(`saved ${rel} in ${(performance.now() - t).toFixed(0)}ms`);
	await Promise.all([refreshChanges(), loadBranchChanges()]);
	if (mode === "changes") await openDiff(rel);
	paintDirty();
}

function paintDirty() {
	const model = mode === "changes" ? diffEditor?.getModel()?.modified : editor.getModel();
	const isDirty = model ? dirtyBuffers.has(model.uri.toString()) : false;
	el<HTMLElement>("title").classList.toggle("dirty", isDirty);
}

window.addEventListener("keydown", (e) => {
	if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
		e.preventDefault();
		void saveCurrent();
	}
});

// ---- 8. two modes: Browse (a normal editor) and Changes (a diff) ----------
// The same pair the Files tab already offers. Browse is a normal editor over
// the whole project; Changes is this branch against its target, as a real diff.
// Monaco has a diff editor built in, so Changes is a second editor instance
// rather than a hand-rolled two-column renderer.
type Mode = "changes" | "browse";
let mode: Mode = "browse";
let diffEditor: monaco.editor.IStandaloneDiffEditor | null = null;

function ensureDiffEditor() {
	if (diffEditor) return diffEditor;
	diffEditor = monaco.editor.createDiffEditor(el("diff"), {
		theme: "github-dark-default",
		automaticLayout: true,
		fontSize: 12,
		// Side by side needs width the rail has already taken; inline reads better
		// at this size and matches how the app renders a diff today.
		renderSideBySide: sideBySide,
		// The modified side is editable; the original is the merge base and cannot
		// meaningfully be written to.
		readOnly: false,
		originalEditable: false,
		// Monaco shows this when someone types into the read-only side. Without it
		// the caret appears, nothing happens, and there is no explanation — which
		// is exactly how the first person to try this got stuck.
		readOnlyMessage: { value: "This is the last commit. Edit on the right — that side is the working tree." },
		renderOverviewRuler: true,
		minimap: { enabled: false },
		scrollBeyondLastLine: false,
		ignoreTrimWhitespace: false,
	});
	diffEditor.getOriginalEditor().onDidAttemptReadOnlyEdit(() => {
		log("that is the left side — the last commit. Type on the right.");
	});
	return diffEditor;
}

async function openDiff(rel: string) {
	const d = ensureDiffEditor();
	const lang = langFor(rel);
	await ensureGrammar(lang);
	const [{ model: modified }, before] = await Promise.all([
		modelFor(rel),
		(await fetch(`${BRIDGE}/file-at?path=${encodeURIComponent(rel)}`)).json(),
	]);
	// The old side only ever exists at the merge base, so it is its own model and
	// stays read-only. The NEW side is the same buffer Browse mode edits, which
	// is what lets you fix something without leaving the review.
	const oldUri = monaco.Uri.parse(`diff-base://${rel}`);
	const original = monaco.editor.getModel(oldUri) ?? monaco.editor.createModel(before.text ?? "", lang, oldUri);
	if (original.getValue() !== (before.text ?? "")) original.setValue(before.text ?? "");
	d.setModel({ original, modified });
	// Land the caret where typing works, rather than wherever it was last.
	d.getModifiedEditor().focus();
	paintDiffHeads(rel);
	currentRel = rel;
	el<HTMLElement>("title").textContent = `AO spike · ${rel} — vs ${baseLabel}`;
	renderRail();
}

let currentRel = "";
let baseLabel = "target";

function setMode(next: Mode) {
	mode = next;
	localStorage.setItem("spike.mode", next);
	el<HTMLButtonElement>("modeChanges").setAttribute("aria-selected", String(next === "changes"));
	el<HTMLButtonElement>("modeBrowse").setAttribute("aria-selected", String(next === "browse"));
	el<HTMLElement>("editor").hidden = next !== "browse";
	el<HTMLElement>("diffWrap").hidden = next !== "changes";
	el<HTMLElement>("railHead").hidden = next !== "changes";
	hideRevert();
	renderRail();
	if (next === "changes") {
		ensureDiffEditor();
		applyDiffLayout();
		// Land on something rather than an empty pane.
		const target = branchFiles.find((f) => f.path === currentRel) ?? branchFiles[0];
		if (target) void openDiff(target.path);
	} else {
		editor.layout();
		editor.focus();
	}
}

let sideBySide = localStorage.getItem("spike.diffSideBySide") === "1";
// Which side is which, said out loud. A file added on this branch has an EMPTY
// original pane, so the read-only half is a large blank target that invites a
// click and then silently ignores it.
function paintDiffHeads(rel = currentRel) {
	const base = el<HTMLElement>("diffHead").querySelector(".base") as HTMLElement;
	const head = el<HTMLElement>("diffHead").querySelector(".head") as HTMLElement;
	base.textContent = `${baseLabel} · read-only`;
	head.textContent = `working tree · editable${rel ? "" : ""}`;
	el<HTMLElement>("diffHead").classList.toggle("inline", !sideBySide);
}

function applyDiffLayout() {
	el<HTMLButtonElement>("diffLayout").setAttribute("aria-pressed", String(sideBySide));
	el<HTMLButtonElement>("diffLayout").title = sideBySide
		? "Side by side — click for inline"
		: "Inline — click for side by side";
	diffEditor?.updateOptions({ renderSideBySide: sideBySide });
	paintDiffHeads();
}
el<HTMLButtonElement>("diffLayout").onclick = () => {
	sideBySide = !sideBySide;
	localStorage.setItem("spike.diffSideBySide", sideBySide ? "1" : "0");
	applyDiffLayout();
};

el<HTMLButtonElement>("modeChanges").onclick = () => setMode("changes");
el<HTMLButtonElement>("modeBrowse").onclick = () => setMode("browse");

// ---- the Browse tree ------------------------------------------------------
// Built from the same file index that feeds ⌘⇧O, so there is one source of
// truth for "what files exist".
const expanded = new Set<string>();

function renderBrowseTree() {
	const list = el<HTMLUListElement>("railList");
	list.innerHTML = "";
	const cur = relOf(editor.getModel()?.uri.toString() ?? "");
	// Keep the path to the open file expanded, so ⌘click landing somewhere far
	// away still shows you where you are.
	if (cur) {
		const parts = cur.split("/");
		for (let i = 1; i < parts.length; i++) expanded.add(parts.slice(0, i).join("/"));
	}
	type Node = { name: string; path: string; dir: boolean; children: Map<string, Node> };
	const root: Node = { name: "", path: "", dir: true, children: new Map() };
	for (const f of meta.files) {
		let node = root;
		const parts = f.split("/");
		parts.forEach((part, i) => {
			const isDir = i < parts.length - 1;
			const p = parts.slice(0, i + 1).join("/");
			let child = node.children.get(part);
			if (!child) {
				child = { name: part, path: p, dir: isDir, children: new Map() };
				node.children.set(part, child);
			}
			node = child;
		});
	}
	const walk = (node: Node, depth: number) => {
		const kids = [...node.children.values()].sort((a, b) =>
			a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1,
		);
		for (const k of kids) {
			const li = document.createElement("li");
			li.className = "node" + (k.dir ? " d" : "") + (!k.dir && k.path === cur ? " cur" : "");
			li.style.paddingLeft = `${8 + depth * 12}px`;
			const tw = document.createElement("span");
			tw.className = "tw";
			tw.textContent = k.dir ? (expanded.has(k.path) ? "▾" : "▸") : "";
			li.appendChild(tw);
			const nm = document.createElement("span");
			nm.className = "nm";
			nm.textContent = k.name;
			nm.title = k.path;
			li.appendChild(nm);
			li.onclick = () => {
				if (k.dir) {
					expanded.has(k.path) ? expanded.delete(k.path) : expanded.add(k.path);
					renderBrowseTree();
				} else {
					void openPath(k.path, 1);
				}
			};
			list.appendChild(li);
			if (k.dir && expanded.has(k.path)) walk(k, depth + 1);
		}
	};
	walk(root, 0);
}

// ---- 7. what this BRANCH changed vs its target branch ---------------------
// This is the app's existing Changes view (workspace_changes.go), brought into
// the editor instead of living beside it. Two levels, never merged:
//   branch       — merge base vs working tree. Everything the branch did.
//   uncommitted  — working tree vs HEAD. What Discard Change can undo.
// The second is a subset of the first, so each gets its own gutter lane.
type ChangedFile = { path: string; additions: number; deletions: number; status: string; untracked?: boolean };
let branchFiles: ChangedFile[] = [];
let branchHunks: Hunk[] = [];
let branchDecorations = editor.createDecorationsCollection([]);

async function loadBranchChanges() {
	try {
		const r = await (await fetch(`${BRIDGE}/branch-changes`)).json();
		branchFiles = r.available ? (r.files ?? []) : [];
		el<HTMLElement>("railBase").textContent = r.available ? `vs ${r.baseRef}` : "no target branch";
	} catch {
		branchFiles = [];
		el<HTMLElement>("railBase").textContent = "not a git checkout";
	}
	const add = branchFiles.reduce((a, f) => a + f.additions, 0);
	const del = branchFiles.reduce((a, f) => a + f.deletions, 0);
	el<HTMLElement>("railStat").innerHTML =
		`${branchFiles.length} file${branchFiles.length === 1 ? "" : "s"} <span class="a">+${add}</span> <span class="d">−${del}</span>`;
	renderRail();
}

function renderChangesRail() {
	const list = el<HTMLUListElement>("railList");
	list.innerHTML = "";
	const cur = relOf(editor.getModel()?.uri.toString() ?? "");
	let lastDir = "";
	for (const f of branchFiles) {
		const dir = f.path.includes("/") ? f.path.slice(0, f.path.lastIndexOf("/")) : "";
		if (dir !== lastDir) {
			lastDir = dir;
			const d = document.createElement("li");
			d.className = "dir";
			d.textContent = dir || "/";
			d.title = dir;
			list.appendChild(d);
		}
		const li = document.createElement("li");
		li.className = "f" + (f.path === (mode === "changes" ? currentRel : cur) ? " cur" : "");
		const n = document.createElement("span");
		n.className = "n";
		n.textContent = f.path.split("/").pop() ?? f.path;
		n.title = f.path;
		li.appendChild(n);
		if (dirtyPaths.has(f.path)) {
			const dot = document.createElement("span");
			dot.className = "dot";
			dot.title = "has uncommitted changes";
			li.appendChild(dot);
		}
		const c = document.createElement("span");
		c.className = "c";
		c.innerHTML = `<span class="a">+${f.additions}</span><span class="d">−${f.deletions}</span>`;
		li.appendChild(c);
		li.onclick = () =>
			mode === "changes" ? openDiff(f.path) : openPath(f.path, 1).then(() => jumpToFirstBranchHunk());
		list.appendChild(li);
	}
}

function renderRail() {
	if (mode === "browse") renderBrowseTree();
	else renderChangesRail();
}

// Opening a changed file at line 1 is not what anyone wants; land on the change.
function jumpToFirstBranchHunk() {
	const first = branchHunks[0];
	if (!first) return;
	const line = first.newLines === 0 ? first.newStart : first.newStart;
	editor.revealLineInCenter(line);
	editor.setPosition({ lineNumber: line, column: 1 });
}

// Files known to carry uncommitted work, for the dot in the rail. Filled in as
// files are opened rather than up front: one `git diff` per changed file on
// load would be the wrong trade for a dot.
const dirtyPaths = new Set<string>();

// ---- 6. uncommitted changes in the gutter, and revert ---------------------
// Xcode paints a bar in the gutter beside every line that differs from the
// last commit, and clicking it offers "Discard Change". The SHOWING half
// already exists in this app: the daemon returns `changedLines` with
// ReadWorkspaceFile, and DiffRows.tsx already draws the bar in the rail.
// Only REVERT is new. Colours follow the app's tokens (DiffRows.tsx:12):
// added = success green, modified = accent blue, removed = error red.
type Hunk = {
	oldStart: number;
	oldLines: number;
	newStart: number;
	newLines: number;
	oldText: string[];
	kind: "added" | "modified" | "removed";
};

let hunks: Hunk[] = [];
let changeDecorations = editor.createDecorationsCollection([]);

async function refreshChanges() {
	const rel = relOf(editor.getModel()?.uri.toString() ?? "");
	if (!rel) {
		hunks = [];
		branchHunks = [];
		changeDecorations.set([]);
		branchDecorations.set([]);
		paintChangeCount();
		return;
	}
	try {
		const c = await (await fetch(`${BRIDGE}/changes?path=${encodeURIComponent(rel)}`)).json();
		hunks = c.hunks ?? [];
		branchHunks = c.branchHunks ?? [];
	} catch {
		hunks = []; // not a git checkout, or the file is outside it
		branchHunks = [];
	}
	if (hunks.length) dirtyPaths.add(rel);
	else dirtyPaths.delete(rel);
	// The outer lane: everything this branch changed against its target. Drawn
	// quieter than the uncommitted lane beside it — on a branch under review
	// nearly every line is "changed", so at full strength it would drown the
	// signal it sits next to.
	branchDecorations.set(
		branchHunks.map((h) => {
			const start = h.newLines === 0 ? Math.max(1, h.newStart) : h.newStart;
			const end = h.newLines === 0 ? start : h.newStart + h.newLines - 1;
			return {
				range: new monaco.Range(start, 1, end, 1),
				options: {
					isWholeLine: true,
					linesDecorationsClassName: `brbar br-${h.kind}`,
					overviewRuler: {
						color:
							h.kind === "added"
								? "rgba(127,216,160,0.45)"
								: h.kind === "removed"
									? "rgba(232,143,143,0.45)"
									: "rgba(77,141,255,0.45)",
						position: monaco.editor.OverviewRulerLane.Right,
					},
				},
			};
		}),
	);
	renderRail();
	changeDecorations.set(
		hunks.map((h) => {
			// A pure deletion has no line of its own; mark the line it sits above.
			const start = h.newLines === 0 ? Math.max(1, h.newStart) : h.newStart;
			const end = h.newLines === 0 ? start : h.newStart + h.newLines - 1;
			return {
				range: new monaco.Range(start, 1, end, 1),
				options: {
					isWholeLine: true,
					linesDecorationsClassName: `chgbar chg-${h.kind}`,
					overviewRuler: {
						color: h.kind === "added" ? "#7fd8a0" : h.kind === "removed" ? "#e88f8f" : "#4d8dff",
						position: monaco.editor.OverviewRulerLane.Left,
					},
				},
			};
		}),
	);
	paintChangeCount();
}
const relOf = (uri: string) => {
	const abs = decodeURIComponent(uri.replace("file://", ""));
	return abs.startsWith(meta.root + "/") ? abs.slice(meta.root.length + 1) : "";
};
function paintChangeCount() {
	const n = hunks.length;
	el<HTMLElement>("changes").textContent =
		n === 0 ? "no uncommitted changes" : `${n} uncommitted change${n === 1 ? "" : "s"}`;
	el<HTMLElement>("changes").classList.toggle("dirty", n > 0);
}
const hunkAt = (line: number) =>
	hunks.find((h) =>
		h.newLines === 0
			? h.newStart === line || h.newStart + 1 === line
			: line >= h.newStart && line <= h.newStart + h.newLines - 1,
	);

async function revertHunk(h: Hunk) {
	const model = editor.getModel();
	const rel = relOf(model?.uri.toString() ?? "");
	if (!model || !rel) return;
	// Replace the hunk's NEW lines with the OLD ones git handed us, working in
	// WHOLE LINES. Replacing "line 96..96" with "" leaves an empty line 96
	// behind; the range has to reach the start of the following line so the
	// newline goes with it.
	const lineCount = model.getLineCount();
	// For a pure deletion git reports `+c,0`, meaning the lines were removed
	// after line c — so they go back at the start of line c+1.
	const startLine = h.newLines === 0 ? h.newStart + 1 : h.newStart;
	const endExclusive = h.newLines === 0 ? startLine : h.newStart + h.newLines;
	let range: monaco.Range;
	let text: string;
	if (endExclusive <= lineCount) {
		range = new monaco.Range(startLine, 1, endExclusive, 1);
		text = h.oldText.length ? h.oldText.join("\n") + "\n" : "";
	} else {
		// The hunk runs to the end of the file, so there is no trailing newline to
		// consume — take the preceding one instead.
		const prevLine = Math.max(1, startLine - 1);
		range = new monaco.Range(
			prevLine,
			startLine > 1 ? model.getLineMaxColumn(prevLine) : 1,
			lineCount,
			model.getLineMaxColumn(lineCount),
		);
		text = h.oldText.length ? (startLine > 1 ? "\n" : "") + h.oldText.join("\n") : "";
	}
	model.pushEditOperations([], [{ range, text }], () => null);
	// Undo still works: this went through the model, not through git.
	await fetch(`${BRIDGE}/write`, {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({ path: rel, text: model.getValue() }),
	});
	log(`reverted ${h.kind} hunk at line ${h.newStart} (⌘Z still undoes it)`);
	hideRevert();
	await refreshChanges();
}

// Xcode shows a small popover on the change bar rather than reverting on the
// first click, because discarding work on a stray click is unforgivable.
const revertBox = document.createElement("div");
revertBox.id = "revertBox";
revertBox.hidden = true;
// The popover is a child of the editor's DOM node, so Monaco sees its mousedown
// and treats it as "clicked outside the gutter" — hiding the popover before the
// button ever receives its click. Stop the event here.
revertBox.addEventListener("mousedown", (e) => e.stopPropagation());
el("editor").appendChild(revertBox);
const hideRevert = () => {
	revertBox.hidden = true;
};

function showRevert(h: Hunk, top: number) {
	revertBox.hidden = false;
	revertBox.innerHTML = "";
	const title = document.createElement("div");
	title.className = "rtitle";
	title.textContent =
		h.kind === "added"
			? "Added, not committed"
			: h.kind === "removed"
				? "Removed, not committed"
				: "Modified, not committed";
	revertBox.appendChild(title);
	// Never make someone click a red button to find out what it does.
	const what = document.createElement("div");
	what.className = "rwhat";
	what.textContent =
		h.kind === "added"
			? `Discarding removes ${h.newLines} line${h.newLines === 1 ? "" : "s"}.`
			: h.kind === "removed"
				? `Discarding puts ${h.oldText.length} line${h.oldText.length === 1 ? "" : "s"} back.`
				: `Discarding restores ${h.oldText.length} line${h.oldText.length === 1 ? "" : "s"} from the last commit:`;
	revertBox.appendChild(what);
	if (h.oldText.length) {
		const pre = document.createElement("pre");
		pre.className = "rprev";
		pre.textContent =
			h.oldText.slice(0, 8).join("\n") + (h.oldText.length > 8 ? `\n… ${h.oldText.length - 8} more` : "");
		revertBox.appendChild(pre);
	}
	const row = document.createElement("div");
	row.className = "rrow";
	const discard = document.createElement("button");
	discard.className = "rdiscard";
	discard.textContent = "Discard Change";
	discard.onclick = () => revertHunk(h);
	const cancel = document.createElement("button");
	cancel.textContent = "Cancel";
	cancel.onclick = hideRevert;
	row.append(discard, cancel);
	revertBox.appendChild(row);
	revertBox.style.top = `${Math.max(4, top)}px`;
}

// Anchor BELOW the last line of the hunk, so the popover never covers the very
// change it is asking about.
function revertAnchorTop(h: Hunk) {
	const last = h.newLines === 0 ? h.newStart : h.newStart + h.newLines - 1;
	// getScrolledVisiblePosition is already relative to the editor container and
	// already accounts for scroll; deriving it from getTopForLineNumber by hand
	// was off by a line and the popover covered the change it asked about.
	const p = editor.getScrolledVisiblePosition({ lineNumber: last, column: 1 });
	const lineH = editor.getOption(monaco.editor.EditorOption.lineHeight);
	return (p?.top ?? 0) + (p?.height ?? lineH) + 6;
}

editor.onMouseDown((e) => {
	if (e.target.type !== monaco.editor.MouseTargetType.GUTTER_LINE_DECORATIONS) {
		hideRevert();
		return;
	}
	const line = e.target.position?.lineNumber;
	const h = line ? hunkAt(line) : undefined;
	if (!h) {
		hideRevert();
		return;
	}
	showRevert(h, revertAnchorTop(h));
});
editor.onDidScrollChange(hideRevert);
editor.onDidChangeModel(() => {
	hideRevert();
	refreshChanges();
});
// Editing changes what is uncommitted, so the bars have to follow the edits.
let changeDebounce = 0 as unknown as number;
editor.onDidChangeModelContent(() => {
	clearTimeout(changeDebounce);
	changeDebounce = setTimeout(refreshChanges, 400) as unknown as number;
});

editor.addAction({
	id: "spike.discardChange",
	label: "Discard Change",
	contextMenuGroupId: "1_modification",
	contextMenuOrder: 2,
	keybindings: [],
	run: (ed) => {
		const h = hunkAt(ed.getPosition()?.lineNumber ?? 0);
		if (h) showRevert(h, revertAnchorTop(h));
		else log("no uncommitted change on this line");
	},
});

// ---- 5. minimap section marks --------------------------------------------
// NOTHING TO BUILD HERE. Monaco already renders `// MARK: - Helpers` into the
// minimap as a labelled band, exactly like Xcode — `showMarkSectionHeaders`
// defaults to true and `markSectionHeaderRegex` defaults to
//   \bMARK:\s*(?<separator>-?)\s*(?<label>.*)$
// A hand-rolled overlay was written first, drifted against the minimap's own
// sliding on any file taller than the viewport, and was deleted once the
// built-in was found. The options live on `minimap` in the editor construction
// above; only the type sizes are tuned here.
//
// The two settings below are load-bearing, not taste: at the default
// `scale: 1` / `maxColumn: 120` Monaco middle-truncates the label —
// `// MARK: - User Interaction` renders as "User...ction" — and changing
// sectionHeaderFontSize alone (tried 6-10) does not help, because the label is
// clipped in minimap-canvas pixels. `scale: 2` doubles that canvas and the name
// renders in full, matching Xcode.
//
// Gotcha seen live in this very file: the regex matches ANY line containing
// "MARK:", including a comment that merely documents the regex, which puts a
// nonsense band in the minimap. A real feature should confine it to comment
// tokens rather than raw line text.

// ---- 1. Cmd+Shift+O — files AND symbols in one list -----------------------
type Row = {
	kind: "file" | "symbol";
	label: string;
	detail: string;
	rel: string;
	abs?: string;
	line: number;
	score: number;
	hits?: number[];
};

// Open Quickly is for navigating CODE. Assets and generated files are in the
// index too, but Xcode does not float them above source, and neither should we.
const SOURCE_EXT = /\.(swift|go|m|mm|h|hpp|c|cc|cpp|ts|tsx|js|jsx)$/i;
const ASSET_EXT = /\.(png|jpg|jpeg|pdf|svg|json|plist|strings|xcassets|imageset|lproj|xcconfig)$/i;
const fileKindBonus = (base: string) =>
	SOURCE_EXT.test(base) ? 12 : /\.(xib|storyboard)$/i.test(base) ? 0 : ASSET_EXT.test(base) ? -35 : -8;

function fuzzyMatch(query: string, text: string): { score: number; hits: number[] } {
	// subsequence match, Xcode-ish: reward consecutive hits and word-boundary hits
	if (!query) return { score: 0, hits: [] };
	const q = query.toLowerCase(),
		t = text.toLowerCase();
	let qi = 0,
		score = 0,
		streak = 0;
	const hits: number[] = [];
	for (let i = 0; i < t.length && qi < q.length; i++) {
		if (t[i] === q[qi]) {
			streak++;
			score += 1 + streak;
			if (i === 0 || /[^a-z0-9]/.test(t[i - 1]) || (text[i] >= "A" && text[i] <= "Z")) score += 4;
			hits.push(i);
			qi++;
		} else streak = 0;
	}
	if (qi < q.length) return { score: -1, hits: [] };
	return { score: score - text.length * 0.02, hits };
}
const fuzzy = (query: string, text: string) => fuzzyMatch(query, text).score;

const palette = el("palette"),
	input = el<HTMLInputElement>("paletteInput"),
	list = el<HTMLUListElement>("paletteList");
let rows: Row[] = [],
	sel = 0,
	seq = 0;

async function refresh() {
	const q = input.value.trim();
	const mine = ++seq;
	const fileRows: Row[] = meta.files
		.map((f) => {
			const base = f.split("/").pop()!;
			const m = fuzzyMatch(q, base);
			// A name hit outranks a path hit, exactly as Open Quickly orders them,
			// and a hit that STARTS the name outranks one scattered through it —
			// that is what puts PromotionHubViewController.swift above
			// OG-Promotion-Hub 2.png for the query "promohub".
			const prefix = m.score >= 0 && m.hits[0] === 0 ? 40 : 0;
			return m.score >= 0
				? {
						kind: "file" as const,
						label: base,
						detail: f,
						rel: f,
						line: 1,
						score: m.score + 20 + prefix + fileKindBonus(base),
						hits: m.hits,
					}
				: {
						kind: "file" as const,
						label: base,
						detail: f,
						rel: f,
						line: 1,
						score: fuzzy(q, f) + fileKindBonus(base),
						hits: [],
					};
		})
		.filter((r) => r.score >= 0)
		.sort((a, b) => b.score - a.score)
		.slice(0, 40);
	render(fileRows);
	if (q.length < 2) return;
	// Ask every language server that is actually running and merge — Open Quickly
	// should not care which language a symbol came from.
	const live = [...clients.values()];
	const results = await Promise.all(live.map((c) => c.workspaceSymbol(q)));
	const r = {
		result: results.flatMap((x) => x.result ?? []),
		elapsedMs: Math.max(0, ...results.map((x) => x.elapsedMs)),
	};
	if (mine !== seq) return;
	// MEASURED PROBLEM, worth carrying into the real feature: on the iOS app the
	// index store also holds symbols from DerivedData — generated asset symbols
	// (`ImageResource.couponBookSearchIcon`, `_R.image.…`) and build
	// intermediates — and they outnumber the hand-written ones. Xcode's Open
	// Quickly does not show them. Rank by where the symbol LIVES, not just by
	// how well the name matches.
	const rank = (abs: string): number => {
		// Judge by the SHAPE of the path, not by whether it starts at our root:
		// sourcekit-lsp answers from an index store whose paths belong to the tree
		// that was built, which is not always the tree that is open.
		if (/\/(DerivedData|Intermediates\.noindex|Build\/Products)\//.test(abs)) return -60; // generated
		if (/\/(Pods|Carthage|\.build|node_modules)\//.test(abs)) return -25; // dependency source
		if (/Tests?\//.test(abs)) return -10;
		return 0;
	};
	const symsRaw: Row[] = (r.result ?? [])
		.slice(0, 400)
		.map((s: any) => {
			const uri = s.location?.uri ?? "";
			const abs = decodeURIComponent(uri.replace("file://", ""));
			const inRoot = abs.startsWith(meta.root + "/");
			const rel = inRoot ? abs.slice(meta.root.length + 1) : abs;
			const container =
				String(s.containerName ?? "")
					.split("/")
					.pop() ?? "";
			return {
				kind: "symbol" as const,
				label: (container ? container + "." : "") + s.name,
				detail: `${rel}:${(s.location?.range?.start?.line ?? 0) + 1}`,
				rel: inRoot ? rel : "",
				abs: inRoot ? undefined : abs,
				line: (s.location?.range?.start?.line ?? 0) + 1,
				score: fuzzy(q, s.name) + rank(abs),
				hits: fuzzyMatch(q, (container ? container + "." : "") + s.name).hits,
			};
		})
		.filter((s: Row) => s.score >= -20)
		.sort((a: Row, b: Row) => b.score - a.score);
	// The index store holds one unit per built target/arch, so the same
	// declaration comes back 2-3 times (arm64 + x86_64 + the test target).
	// Collapse on where it is actually declared.
	const seenSym = new Set<string>();
	const syms = symsRaw
		.filter((r) => {
			const key = `${r.label}|${r.rel || r.abs}|${r.line}`;
			if (seenSym.has(key)) return false;
			seenSym.add(key);
			return true;
		})
		.slice(0, 30);
	log(`workspace/symbol "${q}" → ${(r.result ?? []).length} raw, ${syms.length} shown, ${r.elapsedMs.toFixed(0)}ms`);
	// One ranked list. Xcode does not put all symbols above all files; the best
	// match wins whichever kind it is.
	render([...syms, ...fileRows].sort((a, b) => b.score - a.score).slice(0, 40));
}

// ---- Open Quickly row rendering, following Xcode's ---------------------
// Xcode draws: a document icon tinted by file type, the name with the matched
// characters in bold, and under it a breadcrumb of the containing folders
// separated by "›", led by a small badge for the target.
const FILE_KIND: Record<string, { tint: string; glyph: string }> = {
	swift: { tint: "#F05138", glyph: "swift" },
	xib: { tint: "#E8B33E", glyph: "ib" },
	storyboard: { tint: "#E8B33E", glyph: "ib" },
	go: { tint: "#00ADD8", glyph: "go" },
	m: { tint: "#7B7FE8", glyph: "m" },
	h: { tint: "#8E8E93", glyph: "h" },
	json: { tint: "#8E8E93", glyph: "{}" },
	png: { tint: "#5AC8FA", glyph: "img" },
	plist: { tint: "#8E8E93", glyph: "pl" },
};
const kindFor = (p: string) => FILE_KIND[p.split(".").pop()?.toLowerCase() ?? ""] ?? { tint: "#8E8E93", glyph: "" };

function docIcon(path: string): SVGSVGElement {
	const { tint, glyph } = kindFor(path);
	const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
	svg.setAttribute("viewBox", "0 0 26 32");
	svg.classList.add("icon");
	svg.innerHTML =
		`<path d="M2 3.2A2.2 2.2 0 0 1 4.2 1h11.3L24 9.1v19.7A2.2 2.2 0 0 1 21.8 31H4.2A2.2 2.2 0 0 1 2 28.8z" fill="#F2F2F4"/>` +
		`<path d="M15.5 1 24 9.1h-6.3a2.2 2.2 0 0 1-2.2-2.2z" fill="#C9C9CE"/>` +
		`<rect x="5" y="17" width="16" height="10" rx="2.6" fill="${tint}"/>` +
		`<text x="13" y="24.6" text-anchor="middle" font-size="7" font-weight="700" fill="#fff"
			font-family="ui-monospace, Menlo, monospace">${glyph}</text>`;
	return svg;
}
function symbolIcon(label: string): HTMLElement {
	// Xcode marks symbols with a filled square carrying a kind letter.
	const d = document.createElement("span");
	d.className = "icon symicon";
	d.textContent = /ViewController$/.test(label)
		? "C"
		: /Protocol$|Input$|Output$/.test(label)
			? "P"
			: /^[a-z]/.test(label.split(".").pop() ?? "")
				? "M"
				: "S";
	return d;
}
function highlighted(text: string, hits: number[] | undefined): DocumentFragment {
	const frag = document.createDocumentFragment();
	const set = new Set(hits ?? []);
	let run = "",
		runHit = false;
	const flush = () => {
		if (!run) return;
		if (runHit) {
			const b = document.createElement("b");
			b.textContent = run;
			frag.appendChild(b);
		} else frag.appendChild(document.createTextNode(run));
		run = "";
	};
	for (let i = 0; i < text.length; i++) {
		const hit = set.has(i);
		if (hit !== runHit) {
			flush();
			runHit = hit;
		}
		run += text[i];
	}
	flush();
	return frag;
}
function breadcrumb(row: Row): HTMLElement {
	const wrap = document.createElement("span");
	wrap.className = "crumbs";
	const raw = row.rel || row.abs || "";
	const parts = raw.split("/").filter(Boolean);
	const file = parts.pop() ?? ""; // the filename is shown by the title (files)
	// or by the trailing file:line (symbols)
	const badge = document.createElement("span");
	badge.className = "target";
	badge.textContent = "▸";
	wrap.appendChild(badge);
	// Deep trees would push the row wide; Xcode keeps the tail, which is the part
	// that tells you where you are.
	const shown = parts.length > 6 ? ["…", ...parts.slice(-5)] : parts;
	shown.forEach((p, i) => {
		if (i) {
			const c = document.createElement("i");
			c.textContent = "›";
			wrap.appendChild(c);
		}
		const seg = document.createElement("span");
		seg.textContent = p;
		wrap.appendChild(seg);
	});
	if (row.kind === "symbol") {
		const c = document.createElement("i");
		c.textContent = "›";
		wrap.appendChild(c);
		const seg = document.createElement("span");
		seg.className = "loc";
		seg.textContent = `${file}:${row.line}`;
		wrap.appendChild(seg);
	}
	return wrap;
}

function render(next: Row[]) {
	rows = next;
	sel = 0;
	list.innerHTML = "";
	rows.forEach((r, i) => {
		const li = document.createElement("li");
		li.className = i === 0 ? "sel" : "";
		li.appendChild(r.kind === "file" ? docIcon(r.rel || r.abs || "") : symbolIcon(r.label));
		const text = document.createElement("span");
		text.className = "rowtext";
		const title = document.createElement("span");
		title.className = "l";
		title.appendChild(highlighted(r.label, r.hits));
		text.appendChild(title);
		text.appendChild(breadcrumb(r));
		li.appendChild(text);
		li.onclick = () => choose(i);
		list.appendChild(li);
	});
	el<HTMLElement>("paletteEmpty").hidden = rows.length > 0;
}
async function choose(i: number) {
	const r = rows[i];
	if (!r) return;
	closePalette();
	await openPath(r.rel, r.line, r.abs);
}
function openPalette() {
	palette.hidden = false;
	input.value = "";
	input.focus();
	refresh();
}
function closePalette() {
	palette.hidden = true;
	editor.focus();
}

input.addEventListener("input", refresh);
el<HTMLElement>("paletteLang").textContent = meta.lang;
el<HTMLButtonElement>("paletteClear").onclick = () => {
	input.value = "";
	input.focus();
	refresh();
};
window.addEventListener("keydown", (e) => {
	if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.code === "KeyO") {
		e.preventDefault();
		openPalette();
		return;
	}
	if (palette.hidden) return;
	if (e.key === "Escape") {
		e.preventDefault();
		closePalette();
	}
	if (e.key === "ArrowDown" || e.key === "ArrowUp") {
		e.preventDefault();
		sel = Math.max(0, Math.min(rows.length - 1, sel + (e.key === "ArrowDown" ? 1 : -1)));
		[...list.children].forEach((c, i) => c.classList.toggle("sel", i === sel));
		list.children[sel]?.scrollIntoView({ block: "nearest" });
	}
	if (e.key === "Enter") {
		e.preventDefault();
		choose(sel);
	}
});

await loadBranchChanges();
baseLabel = el<HTMLElement>("railBase").textContent?.replace(/^vs /, "") ?? "target";

// open something to start on
const seed =
	meta.lang === "swift"
		? (meta.files.find((f) => f.endsWith("NterApp/NterApp/AppDelegate.swift")) ??
			meta.files.find((f) => f.endsWith(".swift"))!)
		: (meta.files.find((f) => f.endsWith("internal/service/session/workspace_file.go")) ??
			meta.files.find((f) => f.endsWith(".go"))!);
// Found by qa: a module-level await that rejects (a failed fetch, or no file of
// this language in the index) throws before `window.__spike` is ever assigned —
// leaving a blank editor and nothing to debug with.
try {
	if (seed) await openPath(seed, 1);
	else log("no file of this language in the index; open one from the tree");
} catch (e) {
	log(`could not open the seed file: ${String(e)}`);
}

setMode((localStorage.getItem("spike.mode") as Mode) ?? "browse");

setInterval(async () => {
	const s = await (await fetch(`${BRIDGE}/stats`)).json();
	// Say how many language servers are alive and which — the cost of a
	// mixed-language session should be visible, not implied.
	const running = (s.servers ?? []).map((x: any) => `${x.lang}:${x.pid}`).join(" ");
	statsEl.textContent = `${running || "no language server yet"} · ${s.files} files`;
}, 2000);

// Exposed so the spike can be driven from the console / a test harness without
// synthesising mouse gestures. Throwaway: production code would not do this.
(window as any).__spike = {
	editor,
	clientFor: (langId: string) => clients.get(langId) ?? null,
	meta,
	openPath,
	async jump(rel: string, line: number) {
		return openPath(rel, line);
	},
	async defAt(line: number, column: number) {
		const model = editor.getModel()!;
		const c = lspForModel(model.uri.toString());
		if (!c) return { elapsedMs: 0, result: null, note: "no server for this language" };
		const r = await c.definition(model.uri.toString(), line - 1, column - 1);
		return { elapsedMs: r.elapsedMs, result: r.result };
	},
	timings: () => [...clients.values()].flatMap((c) => c.timings),
	clients: () => [...clients.keys()],
	openDiff: (rel: string) => openDiff(rel),
	diff: () => diffEditor,
	monaco,
	dirty: () => [...dirtyBuffers],
	save: () => saveCurrent(),
	mode: () => mode,
	lastCompletion: () => lastCompletion,
	async completeAt(line: number, column: number, trigger?: string) {
		const model = editor.getModel()!;
		const c = lspForModel(model.uri.toString());
		if (!c) return { elapsedMs: 0, count: 0, isIncomplete: false, sample: [], note: "no server for this language" };
		c.didChange(model.uri.toString(), model.getValue());
		const r = await c.completion(model.uri.toString(), line - 1, column - 1, trigger);
		const list = Array.isArray(r.result) ? r.result : (r.result?.items ?? []);
		return {
			elapsedMs: Math.round(r.elapsedMs),
			count: list.length,
			isIncomplete: !!r.result?.isIncomplete,
			sample: list.slice(0, 8).map((i: any) => `${i.label}${i.detail ? " : " + i.detail : ""}`),
		};
	},
	firstDefinitionMs: () => (firstDefinitionAt ? firstDefinitionAt - t0 : null),
};
