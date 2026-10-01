import type { InfillRequest } from "../../../main/inline-completion/infill";
import { monaco } from "../monaco-setup";
import { inlineCompletionBridge } from "./bridge";
import { CompletionCache } from "./cache";
import { CHUNK_LINES, ChunkRing } from "./chunk-ring";
import { buildInfillRequest, cleanCompletion, PREFIX_LINES, SUFFIX_LINES } from "./context";
import { isInlineCompletionReady, subscribeInlineCompletionStatus } from "./status";

/**
 * Ghost text: Monaco's own inline-completions contribution, fed by the local
 * model main runs.
 *
 * 🗝 Everything a person touches is Monaco's, not ours: the grey text after the
 * cursor, Tab to accept (whose keybinding already stands down while the suggest
 * widget is open, while the selection is non-empty, and when the ghost text is
 * only indentation - so Tab still indents), Esc to dismiss, and the ghost text
 * shrinking as the person types along it or vanishing when they diverge. This
 * module only answers "what would come next here", and answers NOTHING - never
 * a throw, never an error - whenever the model is not ready.
 */

/**
 * Monaco waits this long after a keystroke before asking: not at all. Main's
 * request lane already coalesces (one on the wire, the newest waiting), so a
 * debounce only adds its own delay - measured on the 1.5B model, typing at
 * 90-160 ms a key, first ghost text p50 258 ms with none, 313 ms at 50 ms,
 * 367 ms at 100 ms, at the same ~0.2 model requests per keystroke. (The same
 * finding as the language-server completion slice: once requests are
 * serialised, a debounce is pure latency.)
 */
export const DEBOUNCE_MS = 0;
/** No keystroke for this long and queued context is moved into the ring and pre-processed. */
export const IDLE_FLUSH_MS = 1_000;
/** How far from the cursor (in lines) the current file contributes ring chunks. */
const FAR_SCOPE_LINES = 1_024;

const cache = new CompletionCache();
const ring = new ChunkRing();
let nextId = 0;
let flushTimer: ReturnType<typeof setTimeout> | null = null;
let warmupId: string | null = null;
let registration: monaco.IDisposable | null = null;

/** The name a chunk is filed under: the tail of the model's path, as a person would say it. */
export function fileLabel(uri: monaco.Uri): string {
	const parts = uri.path.split("/").filter(Boolean);
	return parts.slice(-3).join("/");
}

function requestId(): string {
	nextId += 1;
	return `ic-${Date.now().toString(36)}-${nextId}`;
}

function cancelWarmup(): void {
	if (!warmupId) return;
	inlineCompletionBridge().cancel(warmupId);
	warmupId = null;
}

/**
 * After a pause: move queued chunks into the ring and have the server process
 * them now, with no generation, so the next keystroke finds them cached. A
 * keystroke arriving meanwhile cancels the warm-up; the server keeps whatever it
 * had already processed.
 */
function scheduleFlush(): void {
	if (flushTimer) clearTimeout(flushTimer);
	flushTimer = setTimeout(() => {
		flushTimer = null;
		if (!isInlineCompletionReady() || !ring.flush()) return;
		const warmup: InfillRequest = {
			inputPrefix: "",
			prompt: "",
			inputSuffix: "",
			inputExtra: ring.extra(),
			nIndent: 0,
			nPredict: 0,
		};
		const id = requestId();
		warmupId = id;
		void inlineCompletionBridge()
			.complete(id, warmup)
			.finally(() => {
				if (warmupId === id) warmupId = null;
			});
	}, IDLE_FLUSH_MS);
}

/** `count` lines of `model` from 0-based `start`, clamped - without copying the whole buffer. */
function linesOf(model: monaco.editor.ITextModel, start: number, count: number): string[] {
	const total = model.getLineCount();
	const out: string[] = [];
	for (let n = Math.max(1, start + 1); n <= Math.min(total, start + count); n++) out.push(model.getLineContent(n));
	return out;
}

let lastFarPick: { uri: string; line: number } | null = null;

/**
 * Lines of the current file far from the cursor, as candidates for the ring -
 * what the prefix/suffix window cannot reach. Re-done only when the cursor has
 * moved a chunk's worth, so typing never pays for it.
 */
function pickFarChunks(model: monaco.editor.ITextModel, filename: string, lineNumber: number): void {
	const uri = model.uri.toString();
	if (lastFarPick && lastFarPick.uri === uri && Math.abs(lastFarPick.line - lineNumber) < CHUNK_LINES) return;
	lastFarPick = { uri, line: lineNumber };
	const index = lineNumber - 1;
	const above = index - PREFIX_LINES - CHUNK_LINES;
	if (above >= 0 && index - above <= FAR_SCOPE_LINES) ring.pick(filename, linesOf(model, above, CHUNK_LINES), 0);
	const below = index + SUFFIX_LINES + 1;
	if (below + CHUNK_LINES <= model.getLineCount() && below - index <= FAR_SCOPE_LINES) {
		ring.pick(filename, linesOf(model, below, CHUNK_LINES), 0);
	}
}

/** A file was opened in an editor: its neighbourhood of `lineNumber` becomes context. */
export function noteFileOpened(model: monaco.editor.ITextModel, lineNumber = 1): void {
	const start = Math.max(0, Math.min(lineNumber - 1 - CHUNK_LINES / 2, model.getLineCount() - CHUNK_LINES));
	ring.pick(fileLabel(model.uri), linesOf(model, start, CHUNK_LINES), 0);
	scheduleFlush();
}

/** A file was saved: what was just written is the most likely thing to be referred to next. */
export function noteFileSaved(model: monaco.editor.ITextModel, lineNumber: number): void {
	noteFileOpened(model, lineNumber);
}

const provider: monaco.languages.InlineCompletionsProvider = {
	displayName: "AO local model",
	debounceDelayMs: DEBOUNCE_MS,

	async provideInlineCompletions(model, position, context, token) {
		// Off, downloading, starting or failed: exactly the editor that existed
		// before this feature. No request, no message, no log line.
		if (!isInlineCompletionReady()) return undefined;
		/**
		 * 🗝 The suggest widget is open with a row selected. The language server's
		 * list keeps Tab (Monaco's own keybinding condition), so the model must not
		 * offer anything that competes with it - but it can CONTINUE it. The
		 * prediction is made as if the selected row were already inserted, and
		 * offered as that row plus what follows; Monaco shows it only because it
		 * extends the row. Accepting the row with Tab then lands exactly on the
		 * place this request asked about, so the continuation comes straight out
		 * of the cache, and a second Tab takes it. A snippet row is left alone:
		 * its placeholders are not text the model can continue.
		 */
		const selected = context.selectedSuggestionInfo;
		if (
			selected &&
			(selected.isSnippetText ||
				selected.range.startLineNumber !== position.lineNumber ||
				selected.range.endLineNumber !== position.lineNumber)
		) {
			return undefined;
		}
		cancelWarmup();
		scheduleFlush();

		const filename = fileLabel(model.uri);
		pickFarChunks(model, filename, position.lineNumber);
		// Only the window the request is made of is read out of the model: a
		// keystroke in a 10,000-line file must not copy 10,000 lines.
		const first = Math.max(0, position.lineNumber - 1 - PREFIX_LINES);
		const window = linesOf(model, first, position.lineNumber - first + SUFFIX_LINES);
		const at = position.lineNumber - 1 - first;
		let column = position.column;
		if (selected) {
			const line = window[at] ?? "";
			const head = `${line.slice(0, selected.range.startColumn - 1)}${selected.text}`;
			window[at] = `${head}${line.slice(selected.range.endColumn - 1)}`;
			column = head.length + 1;
		}
		const view = { lines: window, lineNumber: at + 1, column };
		const request = buildInfillRequest(view, ring.extra(filename, window));
		if (!request) return undefined;

		let completion = cache.get(request);
		if (completion === null) {
			const id = requestId();
			const cancelled = token.onCancellationRequested(() => inlineCompletionBridge().cancel(id));
			let content: string | null = null;
			try {
				content = (await inlineCompletionBridge().complete(id, request))?.content ?? null;
			} catch {
				content = null;
			} finally {
				cancelled.dispose();
			}
			if (content === null) return undefined;
			completion = cleanCompletion(content, view) ?? "";
			// Cached EVEN WHEN superseded: main lets a request on the wire finish
			// rather than abort it (see request-lane.ts), and this answer is exactly
			// what the typed-through lookup needs for the keystrokes since. An empty
			// answer is cached too - asking again at the same spot would only spend
			// model time to be told the same nothing.
			cache.set(request, completion);
			if (token.isCancellationRequested) return undefined;
		}
		if (!completion) return undefined;
		if (selected) {
			return { items: [{ insertText: `${selected.text}${completion}`, range: selected.range }] };
		}
		const range = new monaco.Range(position.lineNumber, position.column, position.lineNumber, position.column);
		return { items: [{ insertText: completion, range }] };
	},

	disposeInlineCompletions() {
		// Nothing is held per list.
	},
};

let wired = false;

/**
 * Wire once per window: the provider is REGISTERED only while the local model
 * is ready, and disposed the moment it is not. Every editor of every language
 * shares it.
 *
 * 🗝 Registered-but-silent is not the same as absent. With any inline provider
 * registered, Monaco runs its inline-suggest machinery on every keystroke, and
 * on a CPU throttled to CI speed that extra main-thread work was enough to let
 * the language server's slow first answer land mid-burst and add a completion
 * request (`editor-completion.spec.ts`, "typing faster than the server", 6/9
 * runs at 6x throttling vs 0/9 without the provider). Off therefore means NO
 * provider - the editor exactly as it was before this feature.
 */
export function ensureInlineCompletionProvider(): void {
	if (wired) return;
	wired = true;
	const sync = () => {
		if (isInlineCompletionReady()) {
			// `ao-file` only: the buffers people edit. A diff's original side and a
			// peek preview are other schemes and never get ghost text.
			registration ??= monaco.languages.registerInlineCompletionsProvider({ scheme: "ao-file" }, provider);
		} else {
			registration?.dispose();
			registration = null;
		}
	};
	subscribeInlineCompletionStatus(sync);
	sync();
}

/** For tests. */
export function resetInlineCompletionProviderForTests(): void {
	registration?.dispose();
	registration = null;
	wired = false;
	cache.clear();
	ring.clear();
	if (flushTimer) clearTimeout(flushTimer);
	flushTimer = null;
	warmupId = null;
	lastFarPick = null;
}

export const inlineCompletionInternals = { cache, ring, provider };
