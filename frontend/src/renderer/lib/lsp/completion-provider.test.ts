import type { CancellationToken, editor, languages } from "monaco-editor";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

/**
 * The monaco barrel boots the whole editor on import, so it is replaced by the
 * two things this module actually touches: the provider registry, and the
 * trigger-kind enum. Everything asserted below is about the POLICY - which
 * requests reach the wire, in what order, and what a refusal says - which is the
 * entire content of this slice.
 */
const registrations: { languageId: string; provider: languages.CompletionItemProvider; disposed: boolean }[] = [];
vi.mock("../monaco-setup", () => ({
	monaco: {
		languages: {
			CompletionTriggerKind: { Invoke: 0, TriggerCharacter: 1, TriggerForIncompleteCompletions: 2 },
			registerCompletionItemProvider: (languageId: string, provider: languages.CompletionItemProvider) => {
				const entry = { languageId, provider, disposed: false };
				registrations.push(entry);
				return {
					dispose: () => {
						entry.disposed = true;
					},
				};
			},
		},
	},
}));

const { registerCompletion } = await import("./completion-provider");
const { resetLanes } = await import("./request-lane");
import type { CompletionCapability, CompletionDocument } from "./completion-provider";
import type { LspCompletionList } from "./completion-mapping";
import type { LspClient } from "./lsp-client";

type Deferred = { resolve: (v: unknown) => void; reject: (e: Error) => void; method: string; params: unknown };

function harness(overrides: Partial<CompletionDocument> = {}) {
	const inFlight: Deferred[] = [];
	const sent: { method: string; params: unknown }[] = [];
	const shown: string[] = [];
	let state = "ready";
	let detail: string | undefined;

	const client = {
		handleId: "h1",
		documentUri: (absolute: string) => `file://${absolute}`,
		semanticTokensLegend: () => null,
		completionCapability: () => null,
		request: (method: string, params: unknown) => {
			sent.push({ method, params });
			return new Promise((resolve, reject) => inFlight.push({ resolve, reject, method, params }));
		},
		notify: (method: string, params: unknown) => sent.push({ method, params }),
		didOpen: vi.fn(),
		didClose: vi.fn(),
		isOpen: () => true,
		dispose: vi.fn(),
	} as unknown as LspClient;

	const document: CompletionDocument = {
		languageId: "swift",
		modelUri: "ao-file:///a.swift",
		getClient: () => client,
		getAbsolutePath: () => "/root/a.swift",
		getServerText: () => "let x = 1\n",
		getState: () => state,
		getDetail: () => detail,
		onUnavailable: (reason) => shown.push(reason),
		...overrides,
	};

	return {
		document,
		client,
		sent,
		shown,
		inFlight,
		setState: (next: string, why?: string) => {
			state = next;
			detail = why;
		},
		completions: () => sent.filter((s) => s.method === "textDocument/completion"),
	};
}

const model = { uri: { toString: () => "ao-file:///a.swift" } } as unknown as editor.ITextModel;
const modelWithWords = {
	...model,
	getWordUntilPosition: () => ({ word: "", startColumn: 5, endColumn: 5 }),
	getWordAtPosition: () => null,
} as unknown as editor.ITextModel;
const POSITION = { lineNumber: 3, column: 5 } as never;

function token(cancelled = false): CancellationToken {
	return { isCancellationRequested: cancelled, onCancellationRequested: () => ({ dispose: () => undefined }) } as never;
}

const TYPING = { triggerKind: 2 } as languages.CompletionContext;
const EXPLICIT = { triggerKind: 0 } as languages.CompletionContext;
const TRIGGER_CHAR = { triggerKind: 1, triggerCharacter: "." } as languages.CompletionContext;

const SWIFT: CompletionCapability = { triggerCharacters: [".", "("], resolveProvider: true };
const GO: CompletionCapability = { triggerCharacters: ["."] };

function latest(): languages.CompletionItemProvider {
	const live = registrations.filter((r) => !r.disposed);
	return live[live.length - 1].provider;
}

/** Let every queued waiter run: eight of them need more than one microtask. */
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

/**
 * The provider is always async here; this is only to keep the types honest.
 *
 * 🗝 It RESOLVES for a refusal, with `undefined`, and never rejects. A throw
 * would reach Monaco's `onUnexpectedExternalError` and print a stack trace in the
 * renderer per keystroke while a server is still starting; an empty list would
 * render "No suggestions", which is what a type with genuinely no members says.
 */
function ask(context: languages.CompletionContext, cancelled = false): Promise<languages.CompletionList | undefined> {
	return Promise.resolve(
		latest().provideCompletionItems(modelWithWords, POSITION, context, token(cancelled)),
	) as Promise<languages.CompletionList | undefined>;
}

function resolve(item: languages.CompletionItem): Promise<languages.CompletionItem> {
	return Promise.resolve(latest().resolveCompletionItem?.(item, token())) as Promise<languages.CompletionItem>;
}

let warn: ReturnType<typeof vi.spyOn>;
const open: { dispose: () => void }[] = [];
beforeEach(() => {
	registrations.length = 0;
	warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
	warn.mockClear();
});
afterEach(() => {
	for (const r of open.splice(0)) r.dispose();
	// The request lane is process-global and shared with hover and references.
	// A test that deliberately leaves a request unanswered would otherwise wedge
	// every later one on the same document.
	resetLanes();
});
function register(document: CompletionDocument, capability: CompletionCapability | null = SWIFT) {
	const r = registerCompletion(document, capability);
	open.push(r);
	return r;
}

const ITEMS: LspCompletionList = {
	isIncomplete: true,
	items: [{ label: "numberOfLines", kind: 10, sortText: "4998.5-numberOfLines" }],
};

describe("registration", () => {
	test("the server's own trigger characters are what Monaco gets", () => {
		const h = harness();
		register(h.document, SWIFT);
		expect(latest().triggerCharacters).toEqual([".", "("]);
	});

	// 🗝 Monaco reads triggerCharacters ONCE. A provider registered before the
	// attachment would never fire on `.` and would look like dead code.
	test("a capability change re-registers, carrying the panes across", async () => {
		const h = harness();
		register(h.document, GO);
		expect(latest().triggerCharacters).toEqual(["."]);
		register(h.document, SWIFT);
		expect(latest().triggerCharacters).toEqual([".", "("]);
		expect(registrations.filter((r) => r.disposed)).toHaveLength(1);
		// and the pane is still owned, so the new provider can answer for it
		const pending = ask(TYPING);
		h.inFlight[0].resolve(ITEMS);
		await expect(pending).resolves.toMatchObject({ incomplete: true });
	});

	// The pane registers before the server has answered `initialize`, and stays
	// registered when it fails - otherwise ⌃Space against a dead server is silent.
	test("with no capability yet, an explicit invoke still says why", async () => {
		const h = harness({ getClient: () => null, getState: () => "starting" });
		register(h.document, null);
		expect(latest().triggerCharacters).toBeUndefined();
		await expect(ask(EXPLICIT)).resolves.toBeUndefined();
		expect(h.shown).toEqual(["the language server is starting"]);
	});

	test("a server that offers no completion says SO, rather than nothing", async () => {
		// Distinct from "not attached yet": the client is right there and working.
		const h = harness({ languageId: "go" });
		register(h.document, null);
		await expect(ask(EXPLICIT)).resolves.toBeUndefined();
		expect(h.shown).toEqual(["gopls offers no completion for this file"]);
		expect(h.completions()).toHaveLength(0);
	});

	/**
	 * Found by reading the disposers rather than by a failure: a re-registration
	 * that COPIED the pane map would leave every disposer created against the old
	 * entry deleting from a map nobody reads, so a pane closed after the
	 * capability arrived would be answered for out of a dead closure forever.
	 */
	test("a pane that closes after a re-registration really is forgotten", async () => {
		const a = harness();
		const b = harness({ modelUri: "ao-file:///b.swift" });
		const disposeA = register(a.document, null);
		register(b.document, null);
		// The capability arrives; both panes carry across to the new provider.
		register(a.document, SWIFT);
		disposeA.dispose();

		// A's model is gone, so the live provider must not answer for it.
		const modelA = {
			...modelWithWords,
			uri: { toString: () => "ao-file:///a.swift" },
		} as unknown as editor.ITextModel;
		const answer = await Promise.resolve(latest().provideCompletionItems(modelA, POSITION, TRIGGER_CHAR, token()));
		expect(answer).toBeUndefined();
		expect(a.completions()).toHaveLength(0);

		// And B, which never re-registered, still is.
		const modelB = {
			...modelWithWords,
			uri: { toString: () => "ao-file:///b.swift" },
		} as unknown as editor.ITextModel;
		const forB = Promise.resolve(latest().provideCompletionItems(modelB, POSITION, TRIGGER_CHAR, token()));
		await Promise.resolve();
		b.inFlight[0]?.resolve(ITEMS);
		await expect(forB).resolves.toMatchObject({ incomplete: true });
	});

	test("resolveCompletionItem exists only where the server advertised it", () => {
		const h = harness();
		register(h.document, GO);
		expect(latest().resolveCompletionItem).toBeUndefined();
		register(h.document, SWIFT);
		expect(latest().resolveCompletionItem).toBeTypeOf("function");
	});
});

describe("the policy: serialise, never cancel", () => {
	/**
	 * The measurement this whole slice turns on. A burst of keystrokes into a cold
	 * Swift file, per-keystroke with `$/cancelRequest`, took 2 172-2 422 ms to an
	 * answer; serialised and uncancelled it took 2 ms. So a second call must WAIT
	 * for the first rather than replacing it.
	 */
	test("a second call does not reach the wire while the first is in flight", async () => {
		const h = harness();
		register(h.document);
		const first = ask(TRIGGER_CHAR);
		const second = ask(TYPING);
		await Promise.resolve();
		expect(h.completions()).toHaveLength(1);

		h.inFlight[0].resolve(ITEMS);
		await expect(first).resolves.toBeUndefined();
		await Promise.resolve();
		expect(h.completions()).toHaveLength(2);
		h.inFlight[1].resolve(ITEMS);
		await expect(second).resolves.toMatchObject({ incomplete: true });
	});

	/**
	 * The burst the measurement was taken on: eight keystrokes arriving while a
	 * cold request is still running. Only the first and the newest reach the
	 * server, one at a time, and the six in between are dropped before they are
	 * ever sent.
	 */
	test("a burst of keystrokes keeps exactly one request on the wire", async () => {
		const h = harness();
		register(h.document);
		const calls = [ask(TRIGGER_CHAR).then((v) => v)];
		for (let i = 0; i < 8; i++) calls.push(ask(TYPING).then((v) => v));
		await Promise.resolve();
		expect(h.completions()).toHaveLength(1);

		// The cold one lands; the six superseded calls never reach the wire, and
		// the newest takes the slot.
		h.inFlight[0].resolve(ITEMS);
		await tick();
		expect(h.completions()).toHaveLength(2);
		h.inFlight[1].resolve(ITEMS);
		const outcomes = await Promise.all(calls);
		// Eight of the nine calls refused - they were superseded before they could
		// reach the wire - and exactly one carried the answer the reader sees.
		expect(outcomes.filter((o) => o === undefined)).toHaveLength(8);
		expect(h.completions()).toHaveLength(2);
		expect(h.sent.filter((s) => s.method === "$/cancelRequest")).toHaveLength(0);
	});

	test("NOTHING is ever cancelled on the wire", async () => {
		const h = harness();
		register(h.document);
		const first = ask(TRIGGER_CHAR);
		const second = ask(TYPING);
		await Promise.resolve();
		h.inFlight[0].resolve(ITEMS);
		await first.catch(() => undefined);
		await Promise.resolve();
		h.inFlight[1].resolve(ITEMS);
		await second.catch(() => undefined);
		expect(h.sent.filter((s) => s.method === "$/cancelRequest")).toHaveLength(0);
	});

	// The non-negotiable from the brief: a stale prefix's answer must not land on
	// top of a newer one.
	test("a superseded call refuses instead of answering", async () => {
		const h = harness();
		register(h.document);
		const stale = ask(TYPING);
		ask(TYPING);
		await Promise.resolve();
		h.inFlight[0].resolve(ITEMS);
		await expect(stale).resolves.toBeUndefined();
	});

	test("a call Monaco has already cancelled never reaches the wire", async () => {
		const h = harness();
		register(h.document);
		const first = ask(TRIGGER_CHAR);
		const cancelled = ask(TYPING, true);
		await Promise.resolve();
		h.inFlight[0].resolve(ITEMS);
		await first.catch(() => undefined);
		await expect(cancelled).resolves.toBeUndefined();
		await Promise.resolve();
		expect(h.completions()).toHaveLength(1);
	});

	// Found by mutation: with a request already in flight the check inside the
	// wait loop covers this, so the case that pins the check AFTER the loop is the
	// one where there was nothing to wait for.
	test("a cancelled call with nothing in flight never reaches the wire", async () => {
		const h = harness();
		register(h.document);
		await expect(ask(TYPING, true)).resolves.toBeUndefined();
		expect(h.completions()).toHaveLength(0);
	});

	test("a server that never answers is given up on, and says so", async () => {
		vi.useFakeTimers();
		try {
			const h = harness();
			register(h.document);
			const outcome = ask(EXPLICIT);
			await vi.advanceTimersByTimeAsync(7_999);
			expect(h.shown).toEqual([]);
			await vi.advanceTimersByTimeAsync(2);
			expect(await outcome).toBeUndefined();
			expect(h.shown).toEqual(["the language server did not answer"]);
			// And the slot is released, so the next keystroke is not stuck behind it.
			const next = ask(TYPING);
			await Promise.resolve();
			expect(h.completions()).toHaveLength(2);
			h.inFlight[1].resolve(ITEMS);
			await expect(next).resolves.toMatchObject({ incomplete: true });
		} finally {
			vi.useRealTimers();
		}
	});

	test("a failed request releases the slot rather than wedging every later keystroke", async () => {
		const h = harness();
		register(h.document);
		const first = ask(TRIGGER_CHAR);
		await Promise.resolve();
		h.inFlight[0].reject(new Error("boom"));
		await expect(first).resolves.toBeUndefined();
		const second = ask(TYPING);
		await Promise.resolve();
		expect(h.completions()).toHaveLength(2);
		h.inFlight[1].resolve(ITEMS);
		await expect(second).resolves.toMatchObject({ incomplete: true });
	});
});

describe("isIncomplete", () => {
	// Measured: sourcekit-lsp caps at 200 items and a longer prefix returns items
	// the shorter one omitted (6 of 9 for `emailLabel.numb`). Honouring the flag
	// is what makes Monaco come back for them.
	test("is carried verbatim, both ways", async () => {
		const h = harness();
		register(h.document);
		const incomplete = ask(TRIGGER_CHAR);
		h.inFlight[0].resolve({ isIncomplete: true, items: [{ label: "a" }] });
		await expect(incomplete).resolves.toMatchObject({ incomplete: true });

		const complete = ask(TRIGGER_CHAR);
		await Promise.resolve();
		h.inFlight[1].resolve({ isIncomplete: false, items: [{ label: "a" }] });
		await expect(complete).resolves.toMatchObject({ incomplete: false });
	});

	test("a bare array answer is a complete list", async () => {
		const h = harness();
		register(h.document);
		const pending = ask(TRIGGER_CHAR);
		h.inFlight[0].resolve([{ label: "a" }]);
		await expect(pending).resolves.toMatchObject({ incomplete: false });
	});
});

describe("the three states, and none of them is silence", () => {
	test("no client yet: refuses with the reason, and an EXPLICIT invoke shows it at the cursor", async () => {
		const h = harness({ getClient: () => null, getState: () => "starting" });
		register(h.document);
		await expect(ask(EXPLICIT)).resolves.toBeUndefined();
		expect(h.shown).toEqual(["the language server is starting"]);
	});

	test("the same refusal while TYPING says nothing at the cursor", async () => {
		const h = harness({ getClient: () => null, getState: () => "starting" });
		register(h.document);
		await expect(ask(TYPING)).resolves.toBeUndefined();
		expect(h.shown).toEqual([]);
	});

	/**
	 * 🗝 Noise is its own failure. Monaco hands a provider's rejection to
	 * `onUnexpectedExternalError`, which raises the renderer's global error
	 * channel - so a provider that threw while a server was starting would print a
	 * stack trace per keystroke, with no reason attached to any of them. And an
	 * empty list is worse than either: it renders "No suggestions", which is
	 * exactly what a type with genuinely no members says.
	 */
	test("refusing while typing is quiet; refusing an explicit ask is logged", async () => {
		const h = harness({ getClient: () => null, getState: () => "starting" });
		register(h.document);

		const typed = await ask(TYPING);
		expect(typed, "a refusal must not be an empty list").toBeUndefined();
		expect(warn).not.toHaveBeenCalled();

		expect(await ask(EXPLICIT)).toBeUndefined();
		expect(warn).toHaveBeenCalledWith(expect.stringContaining("the language server is starting"));
	});

	test("a request that FAILS is logged even when nobody asked explicitly", async () => {
		const h = harness();
		register(h.document);
		const pending = ask(TYPING);
		await Promise.resolve();
		h.inFlight[0].reject(new Error("Internal error"));
		expect(await pending).toBeUndefined();
		expect(warn).toHaveBeenCalledWith(expect.stringContaining("Internal error"));
	});

	test("failed: the server's OWN reason is what the reader is shown", async () => {
		const h = harness({
			getClient: () => null,
			getState: () => "failed",
			getDetail: () => "sourcekit-lsp is not on PATH",
		});
		register(h.document);
		await expect(ask(EXPLICIT)).resolves.toBeUndefined();
		expect(h.shown).toEqual(["sourcekit-lsp is not on PATH"]);
	});

	test("the buffer has not reached the server: refuses rather than completing on nothing", async () => {
		const h = harness({ getServerText: () => null });
		register(h.document);
		await expect(ask(TYPING)).resolves.toBeUndefined();
		expect(h.completions()).toHaveLength(0);
	});

	// Up, answering, and answering nothing - the failure this whole feature is
	// written against. It must be TELLABLE from a provider nobody ever asked.
	test("ready and empty: an empty list, logged with the timing and the state", async () => {
		const h = harness();
		register(h.document);
		const pending = ask(TRIGGER_CHAR);
		h.inFlight[0].resolve({ isIncomplete: true, items: [] });
		await expect(pending).resolves.toMatchObject({ suggestions: [] });
		expect(warn).toHaveBeenCalledWith(expect.stringContaining("0 items at /root/a.swift"));
		expect(warn).toHaveBeenCalledWith(expect.stringContaining("server ready"));
	});

	test("a request that fails is logged AND refused, never returned as an empty list", async () => {
		const h = harness();
		register(h.document);
		const pending = ask(EXPLICIT);
		h.inFlight[0].reject(new Error("Internal error"));
		await expect(pending).resolves.toBeUndefined();
		expect(h.shown).toEqual(["Internal error"]);
	});
});

describe("the request itself", () => {
	test("positions go out 0-based, at the server's own uri", async () => {
		const h = harness();
		register(h.document);
		const pending = ask(TRIGGER_CHAR);
		h.inFlight[0].resolve(ITEMS);
		await pending;
		expect(h.completions()[0].params).toEqual({
			textDocument: { uri: "file:///root/a.swift" },
			position: { line: 2, character: 4 },
			context: { triggerKind: 2, triggerCharacter: "." },
		});
	});

	test("typing and explicit invoke are both triggerKind 1 to the server", async () => {
		const h = harness();
		register(h.document);
		const pending = ask(TYPING);
		h.inFlight[0].resolve(ITEMS);
		await pending;
		expect((h.completions()[0].params as { context: { triggerKind: number } }).context.triggerKind).toBe(1);
	});
});

describe("completionItem/resolve", () => {
	test("resolves against the server's OWN item, and gains its documentation", async () => {
		const h = harness();
		register(h.document, SWIFT);
		const pending = ask(TRIGGER_CHAR);
		h.inFlight[0].resolve({
			isIncomplete: true,
			items: [{ label: "numberOfLines", kind: 10, data: { opaque: 7 } }],
		});
		const list = (await pending) as languages.CompletionList;
		const resolving = resolve(list.suggestions[0]);
		// The payload sent back is the SERVER's item - `data` is opaque and a
		// server is entitled to refuse a reconstruction of it.
		expect(h.sent.at(-1)).toEqual({
			method: "completionItem/resolve",
			params: { label: "numberOfLines", kind: 10, data: { opaque: 7 } },
		});
		h.inFlight[1].resolve({
			label: "numberOfLines",
			detail: "Int",
			documentation: { kind: "markdown", value: "**The maximum number of lines.**" },
		});
		await expect(resolving).resolves.toMatchObject({
			detail: "Int",
			documentation: { value: "**The maximum number of lines.**" },
		});
	});

	test("a resolve that fails costs a doc comment, never the completion", async () => {
		const h = harness();
		register(h.document, SWIFT);
		const pending = ask(TRIGGER_CHAR);
		h.inFlight[0].resolve(ITEMS);
		const list = (await pending) as languages.CompletionList;
		const resolving = resolve(list.suggestions[0]);
		h.inFlight[1].reject(new Error("MethodNotFound"));
		await expect(resolving).resolves.toBe(list.suggestions[0]);
	});
});
