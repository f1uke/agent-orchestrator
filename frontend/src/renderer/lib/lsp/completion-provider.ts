import { monaco } from "../monaco-setup";
import {
	asCompletionList,
	defaultRange,
	documentationOf,
	type LspCompletionItem,
	type LspCompletionList,
	toMonacoCompletionItem,
} from "./completion-mapping";
import { languageServerName } from "./language-ids";
import type { LspClient } from "./lsp-client";

/**
 * The Monaco half of autocompletion: one provider per LANGUAGE, one document per
 * open pane, and one request on the wire at a time.
 *
 * ## Why this is not a debounce, and not a local filter
 *
 * Measured against both real servers before any of it was written:
 *
 * - **`isIncomplete: true` is literally true.** sourcekit-lsp caps a response at
 *   200 items and re-ranks for the current prefix, so a longer prefix returns
 *   items the shorter one omitted — `emailLabel.numb` answers 9 items of which
 *   **6 are absent** from the list for `emailLabel.`. Filtering the previous list
 *   locally would show 3 of the 9. gopls hides its deep completions the same way.
 *   So the list is re-requested per keystroke, exactly as the protocol says.
 *
 * - **Re-requesting is the CHEAP half.** 2–6 ms on Swift, 1–15 ms on Go. The
 *   expensive request is the FIRST one in a file (Swift 1 019–1 333 ms, which is
 *   the file's type-check) and the first one in global scope (1 149–1 367 ms).
 *   Both are paid once whatever the client does, and #257's semantic tokens
 *   already pays two thirds of the first (1 333 → 400 ms).
 *
 * - 🗝 **Cancelling a slow request makes it worse.** sourcekit-lsp honours
 *   `$/cancelRequest` — and each cancellation throws away the in-progress
 *   type-check the next request has to redo. Measured on a burst of 8 keystrokes
 *   at 60 ms into a cold file:
 *
 *   | policy | requests | last keystroke → answer |
 *   |---|---|---|
 *   | per-keystroke + `$/cancelRequest` | 8 (7 cancelled) | **2 172–2 422 ms** |
 *   | debounce 120 ms | 1 | 522 ms |
 *   | **serialise, never cancel** | **3** | **2 ms** |
 *
 * So: at most one request in flight, later calls WAIT for it rather than
 * cancelling it, and the ones that have been superseded meanwhile are dropped
 * before they ever reach the wire. No artificial debounce — with the requests
 * serialised, every debounce measured (60/120/250 ms) only added its own delay.
 *
 * ## And it fails by saying so
 *
 * Every path that cannot answer THROWS rather than returning an empty list, so
 * Monaco keeps what the widget had instead of replacing a real list with
 * nothing. An explicit ⌃Space additionally shows the reason at the cursor
 * through `onUnavailable`, in slice 3's own words.
 */

export type CompletionCapability = {
	triggerCharacters?: string[];
	resolveProvider?: boolean;
};

export type CompletionDocument = {
	languageId: string;
	/** The model this pane is showing, so the provider answers per document. */
	modelUri: string;
	getClient: () => LspClient | null;
	getAbsolutePath: () => string | null;
	/** The text the SERVER has — see `document-sync.ts`. Null when nothing is synced. */
	getServerText: () => string | null;
	/** Slice 3's state, so a refusal can say which of the three it is. */
	getState: () => string;
	/** Slice 3's reason for that state, verbatim where there is one. */
	getDetail: () => string | undefined;
	/** Shown at the cursor, but only for an EXPLICIT invoke - never while typing. */
	onUnavailable?: (reason: string) => void;
};

export type CompletionRegistration = monaco.IDisposable;

/**
 * How long to wait for one completion before giving up on it.
 *
 * Well above the slowest thing measured (a cold global-scope completion on the
 * real iOS app, 1 367 ms) and well below "forever", because the failure this
 * whole feature is written against is a server that answers nothing at all.
 */
const REQUEST_TIMEOUT_MS = 8_000;

type LanguageEntry = {
	documents: Map<string, CompletionDocument>;
	/** Null until the server has said - and null for good if it offers none. */
	capability: CompletionCapability | null;
	dispose: () => void;
};

const registered = new Map<string, LanguageEntry>();

/**
 * The open panes per language, held OUTSIDE the registration.
 *
 * 🗝 Shared rather than copied across a re-registration, and that is a
 * correctness point, not tidiness. A capability arriving replaces the entry, and
 * every pane that registered against the old one still holds a disposer closed
 * over it. Copying the map would leave those disposers deleting from a map
 * nobody reads, so a pane that closed without re-registering would stay in the
 * live entry forever - answered for, out of a closure whose component is gone.
 */
const documentsByLanguage = new Map<string, Map<string, CompletionDocument>>();

function documentsFor(languageId: string): Map<string, CompletionDocument> {
	let documents = documentsByLanguage.get(languageId);
	if (!documents) {
		documents = new Map();
		documentsByLanguage.set(languageId, documents);
	}
	return documents;
}

/** Per document: the request on the wire, and which provider call is the current one. */
type DocumentState = { inFlight: Promise<unknown> | null; generation: number };
const documentState = new Map<string, DocumentState>();

function stateFor(modelUri: string): DocumentState {
	let state = documentState.get(modelUri);
	if (!state) {
		state = { inFlight: null, generation: 0 };
		documentState.set(modelUri, state);
	}
	return state;
}

/**
 * What "cannot answer" looks like to Monaco.
 *
 * 🗝 `undefined`, never a throw and never an empty list, and both halves of that
 * are deliberate.
 *
 * A THROW is caught by `suggest.js:205` and handed to `onUnexpectedExternalError`,
 * which raises the renderer's global error channel - so a server that is merely
 * still starting would print a stack trace per keystroke, with no reason attached
 * to it. An EMPTY LIST is worse: `didAddResult` goes true, Monaco stops asking
 * the remaining providers, and the widget renders "No suggestions" - the exact
 * sentence a type with genuinely no members produces. `undefined` is the
 * protocol's own way of saying "nothing from me", and it leaves the reason to be
 * told properly: `console.warn` for a machine, `onUnavailable` at the cursor for
 * a person who explicitly asked.
 */
type NoAnswer = undefined;
const NO_ANSWER: NoAnswer = undefined;

/**
 * The LSP item behind a Monaco one, so `completionItem/resolve` can be sent with
 * the payload the server issued rather than with a reconstruction of it — the
 * `data` field is opaque and a server is entitled to refuse anything else.
 */
const ORIGIN = Symbol("ao.lsp.completion");
type WithOrigin = monaco.languages.CompletionItem & {
	[ORIGIN]?: { item: LspCompletionItem; client: LspClient };
};

async function requestCompletion(
	client: LspClient,
	uri: string,
	position: monaco.IPosition,
	context: monaco.languages.CompletionContext,
): Promise<LspCompletionList> {
	const result = await client.request<LspCompletionList | LspCompletionItem[] | null>("textDocument/completion", {
		textDocument: { uri },
		position: { line: position.lineNumber - 1, character: position.column - 1 },
		context: {
			triggerKind: context.triggerKind === monaco.languages.CompletionTriggerKind.TriggerCharacter ? 2 : 1,
			triggerCharacter: context.triggerCharacter,
		},
	});
	return asCompletionList(result);
}

function provider(entry: LanguageEntry): monaco.languages.CompletionItemProvider {
	return {
		triggerCharacters: entry.capability?.triggerCharacters,

		async provideCompletionItems(model, position, context, token) {
			const source = entry.documents.get(model.uri.toString());
			if (!source) return NO_ANSWER;
			const explicit = context.triggerKind === monaco.languages.CompletionTriggerKind.Invoke;
			const refuse = (reason: string, loud = false): NoAnswer => {
				// 🗝 The message at the cursor is only ever for an EXPLICIT ⌃Space.
				// The reader asked a question, so they get an answer where they are
				// looking; a reader who was merely typing gets nothing, because a
				// message per keystroke is a nag rather than information.
				if (explicit) source.onUnavailable?.(reason);
				// And the log is for the cases a machine has to be able to find: a
				// request that failed, a server that went silent, or a person who
				// asked and got nothing. Not for every keystroke typed while a server
				// is still starting - the status pill is already saying that.
				if (loud || explicit) console.warn(`[lsp] ${source.languageId} completion unavailable: ${reason}`);
				return NO_ANSWER;
			};

			const client = source.getClient();
			const absolute = source.getAbsolutePath();
			if (!client || !absolute) return refuse(source.getDetail() || `the language server is ${source.getState()}`);
			// The client exists and the server named no completion provider. Distinct
			// from "not yet attached", and a reader has to be able to tell them apart.
			if (!entry.capability) {
				return refuse(`${languageServerName(source.languageId)} offers no completion for this file`);
			}
			if (source.getServerText() === null) return refuse("this file has not reached the language server yet");

			const state = stateFor(source.modelUri);
			const generation = ++state.generation;

			// 🗝 WAIT for whatever is on the wire; do not cancel it. On a cold file
			// that request is doing the type-check this one would otherwise have to
			// redo from scratch, and cancelling it measured 34x slower.
			while (state.inFlight) {
				try {
					await state.inFlight;
				} catch {
					// Its own caller reports it. All this one needs is the slot. The
					// loop repeats because another queued call may have taken the slot
					// while this one was waiting.
				}
				// Superseded while queued - by Monaco cancelling this call for a newer
				// keystroke, or by a newer call arriving. Either way this answer is for
				// a prefix that is no longer on screen and must never reach the widget.
				// Silently: a superseded call is the ordinary case while somebody
				// types, and there is nothing wrong to report about it.
				if (token.isCancellationRequested || state.generation !== generation) return NO_ANSWER;
			}
			// Nothing was in flight at all, so the check above never ran.
			if (token.isCancellationRequested) return NO_ANSWER;

			const startedAt = performance.now();
			let timer: ReturnType<typeof setTimeout> | undefined;
			const timeout = new Promise<never>((_, reject) => {
				timer = setTimeout(() => reject(new Error("the language server did not answer")), REQUEST_TIMEOUT_MS);
			});
			const answer = Promise.race([
				requestCompletion(client, client.documentUri(absolute), position, context),
				timeout,
			]);
			// The slot is released by the timeout too: after eight seconds of silence
			// the server is presumed wedged, and holding the slot for it would make
			// every later keystroke wait on a request that is never coming back.
			const slot = answer.catch(() => undefined);
			state.inFlight = slot;

			let list: LspCompletionList;
			try {
				list = await answer;
			} catch (err) {
				return refuse(err instanceof Error ? err.message : "the request failed", true);
			} finally {
				clearTimeout(timer);
				// Unconditionally, and only if it is still OURS: leaving a settled
				// promise in the slot spins every waiter forever.
				if (state.inFlight === slot) state.inFlight = null;
			}

			if (token.isCancellationRequested || state.generation !== generation) return NO_ANSWER;

			if (list.items.length === 0) {
				// Up, answering, and answering NOTHING. Logged with the timing and the
				// state so it is distinguishable from a provider that was never asked,
				// which is this stack's characteristic failure. `noteResult("empty")`
				// has already counted it in the health panel.
				console.warn(
					`[lsp] ${source.languageId} completion → 0 items at ${absolute}` +
						` (${Math.round(performance.now() - startedAt)}ms, server ${source.getState()})`,
				);
			}

			const fallback = defaultRange(model, position);
			const suggestions = list.items.map((item) => {
				const mapped = toMonacoCompletionItem(item, position, fallback) as WithOrigin;
				if (entry.capability?.resolveProvider) mapped[ORIGIN] = { item, client };
				return mapped;
			});
			// Verbatim, not asserted: both servers say `true` today, and a client that
			// hard-codes it would stop re-requesting the day one of them says false.
			return { suggestions, incomplete: list.isIncomplete ?? false };
		},

		/**
		 * Only registered when the server advertised `resolveProvider`.
		 *
		 * gopls does NOT (it ships `detail` and `documentation` on every item
		 * inline, 121 of them in 6 ms). sourcekit-lsp DOES, sends no documentation
		 * until asked, and answers in 0–1 ms — so this is what stops 200 doc
		 * comments being fetched to show one.
		 */
		resolveCompletionItem: entry.capability?.resolveProvider
			? async (item) => {
					const origin = (item as WithOrigin)[ORIGIN];
					if (!origin) return item;
					try {
						const resolved = await origin.client.request<LspCompletionItem | null>(
							"completionItem/resolve",
							origin.item,
						);
						if (!resolved) return item;
						return {
							...item,
							detail: resolved.detail ?? item.detail,
							documentation: documentationOf(resolved) ?? item.documentation,
						};
					} catch (err) {
						// A resolve that fails costs the reader a doc comment, never the
						// completion itself - so the unresolved item is the right answer.
						console.warn("[lsp] completionItem/resolve failed", err);
						return item;
					}
				}
			: undefined,
	};
}

/**
 * Attach one pane's document to the language's completion provider, registering
 * that provider on first use.
 *
 * 🗝 Registered BEFORE the capability is known, and re-registered when it
 * arrives. Two reasons, and they pull in the same direction:
 *
 * - `triggerCharacters` is a STATIC array Monaco reads once at registration, and
 *   it only exists in the server's `initialize` reply. A provider registered with
 *   a guessed `["."]` would never fire on Swift's `(`, and would look exactly
 *   like a feature nobody wired up.
 * - Registering only once a capability exists would mean that a server which
 *   FAILED to start has no provider at all - so ⌃Space against it would be
 *   silent, which is the one thing this feature is not allowed to be.
 */
export function registerCompletion(
	document: CompletionDocument,
	capability: CompletionCapability | null,
): CompletionRegistration {
	const { languageId, modelUri } = document;
	const signature = signatureOf(capability);
	const existing = registered.get(languageId);
	if (existing && signatureOf(existing.capability) !== signature) {
		// The server has said what it can do, so Monaco has to be told again: it
		// read the trigger characters once, at registration. The panes carry over
		// untouched because the map is shared, not owned by the entry.
		registered.delete(languageId);
		existing.dispose();
	}
	if (!registered.has(languageId)) createEntry(languageId, capability);
	const documents = documentsFor(languageId);
	documents.set(modelUri, document);

	let released = false;
	return {
		dispose: () => {
			if (released) return;
			released = true;
			documents.delete(modelUri);
			documentState.delete(modelUri);
			if (documents.size > 0) return;
			documentsByLanguage.delete(languageId);
			const owner = registered.get(languageId);
			if (!owner) return;
			registered.delete(languageId);
			owner.dispose();
		},
	};
}

function signatureOf(capability: CompletionCapability | null): string {
	return JSON.stringify(capability && [capability.triggerCharacters ?? [], capability.resolveProvider ?? false]);
}

function createEntry(languageId: string, capability: CompletionCapability | null): LanguageEntry {
	const created: LanguageEntry = { documents: documentsFor(languageId), capability, dispose: () => undefined };
	const disposable = monaco.languages.registerCompletionItemProvider(languageId, provider(created));
	created.dispose = () => disposable.dispose();
	registered.set(languageId, created);
	return created;
}
