import type { CancellationToken, editor, languages } from "monaco-editor";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { LspClient } from "./lsp-client";

/**
 * Find all references.
 *
 * 🗝 The assertions that matter are about the PREVIEW: Monaco's peek widget is
 * already built, and the only way it can fail is by opening with a blank pane
 * because no model exists for the file a row points at. So this file checks what
 * was materialised, not only what was returned.
 */
const registrations: { languageId: string; provider: languages.ReferenceProvider; disposed: boolean }[] = [];
const models = new Map<string, { isDisposed: () => boolean }>();
vi.mock("../monaco-setup", () => ({
	languageForPath: () => "go",
	monaco: {
		languages: {
			registerReferenceProvider: (languageId: string, provider: languages.ReferenceProvider) => {
				const entry = { languageId, provider, disposed: false };
				registrations.push(entry);
				return {
					dispose: () => {
						entry.disposed = true;
					},
				};
			},
		},
		editor: {
			getModel: (uri: { toString: () => string }) => models.get(uri.toString()) ?? null,
			createModel: (_text: string, _language: string, uri: { toString: () => string }) => {
				const model = { isDisposed: () => false, dispose: () => undefined, uri };
				models.set(uri.toString(), model);
				return model;
			},
		},
		Uri: { parse: (value: string) => ({ toString: () => value }) },
	},
}));

const { registerReferences } = await import("./references");
const { resetLanes } = await import("./request-lane");
const { disposePreviewModels, PREVIEW_FILE_LIMIT } = await import("./peek-sources");

type Deferred = { resolve: (v: unknown) => void; reject: (e: unknown) => void };

const MODEL_URI = "ao-file:///s/service.go";
const model = { uri: { toString: () => MODEL_URI } } as unknown as editor.ITextModel;
const POSITION = { lineNumber: 40, column: 6 } as never;
const CONTEXT = { includeDeclaration: true };

function token(cancelled = false): CancellationToken {
	return { isCancellationRequested: cancelled, onCancellationRequested: () => ({ dispose: () => undefined }) } as never;
}

function harness(state = "ready", features = { hover: true, references: true }) {
	const inFlight: Deferred[] = [];
	const sent: { method: string; params: unknown }[] = [];
	const shown: string[] = [];
	const read: string[] = [];
	const client = {
		handleId: "h1",
		documentUri: (absolute: string) => `file://${absolute}`,
		semanticTokensLegend: () => null,
		completionCapability: () => null,
		features: () => features,
		request: (method: string, params: unknown) => {
			sent.push({ method, params });
			return new Promise((resolve, reject) => inFlight.push({ resolve, reject }));
		},
		notify: vi.fn(),
		didOpen: vi.fn(),
		didClose: vi.fn(),
		isOpen: () => true,
		dispose: vi.fn(),
		onNotification: () => () => undefined,
	} as unknown as LspClient;
	return {
		inFlight,
		sent,
		shown,
		read,
		client,
		document: {
			languageId: "go",
			modelUri: MODEL_URI,
			getClient: () => client,
			getAbsolutePath: () => "/w/service.go",
			getState: () => state,
			readFile: async (absolutePath: string) => {
				read.push(absolutePath);
				return `// ${absolutePath}\n`;
			},
			onUnavailable: (reason: string) => shown.push(reason),
		},
	};
}

function latest(): languages.ReferenceProvider {
	const live = registrations.filter((r) => !r.disposed);
	return live[live.length - 1].provider;
}

function ask(cancelled = false): Promise<languages.Location[] | undefined> {
	return Promise.resolve(latest().provideReferences(model, POSITION, CONTEXT, token(cancelled))) as Promise<
		languages.Location[] | undefined
	>;
}

const location = (path: string, line: number) => ({
	uri: `file://${path}`,
	range: { start: { line, character: 4 }, end: { line, character: 11 } },
});

let warn: ReturnType<typeof vi.spyOn>;
const open: { dispose: () => void }[] = [];
beforeEach(() => {
	registrations.length = 0;
	models.clear();
	warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
});
afterEach(() => {
	for (const r of open.splice(0)) r.dispose();
	resetLanes();
	disposePreviewModels();
	warn.mockRestore();
});
function register(document: Parameters<typeof registerReferences>[0]) {
	const r = registerReferences(document);
	open.push(r);
	return r;
}

describe("the request", () => {
	test("the position goes out 0-based, at the server's own uri, with the declaration", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		expect(h.sent[0]).toEqual({
			method: "textDocument/references",
			params: {
				textDocument: { uri: "file:///w/service.go" },
				position: { line: 39, character: 5 },
				context: { includeDeclaration: true },
			},
		});
		h.inFlight[0].resolve([location("/w/service.go", 39)]);
		await pending;
	});

	test("Monaco's own includeDeclaration:false is honoured rather than overridden", async () => {
		const h = harness();
		register(h.document);
		const pending = Promise.resolve(
			latest().provideReferences(model, POSITION, { includeDeclaration: false }, token()),
		);
		expect((h.sent[0].params as { context: unknown }).context).toEqual({ includeDeclaration: false });
		h.inFlight[0].resolve([]);
		await pending;
	});
});

describe("the answer, and its previews", () => {
	// The real shape: 164 hits across 35 files on this repo's backend. Every hit
	// is returned - the COUNT is the answer to the question - and one read is
	// made per FILE, not per hit.
	test("every hit is returned, and each file is read exactly once", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		h.inFlight[0].resolve([location("/w/service.go", 39), location("/w/service.go", 71), location("/w/other.go", 12)]);
		const links = await pending;
		expect(links).toHaveLength(3);
		expect(links?.map((l) => l.uri.toString())).toEqual([
			"ao-file:///w/service.go",
			"ao-file:///w/service.go",
			"ao-file:///w/other.go",
		]);
		expect(h.read.sort()).toEqual(["/w/other.go", "/w/service.go"]);
	});

	test("LSP is 0-based; the ranges come back 1-based", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		h.inFlight[0].resolve([location("/w/other.go", 12)]);
		expect((await pending)?.[0].range).toEqual({
			startLineNumber: 13,
			startColumn: 5,
			endLineNumber: 13,
			endColumn: 12,
		});
	});

	// 🗝 The models have to EXIST before this promise settles: Monaco resolves the
	// preview by synchronous lookup the moment it does, and rejects when there is
	// none - which is a blank preview pane, not an error.
	test("a model exists for every file before the answer is returned", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		h.inFlight[0].resolve([location("/w/a.go", 1), location("/w/b.go", 2)]);
		await pending;
		expect([...models.keys()].sort()).toEqual(["ao-file:///w/a.go", "ao-file:///w/b.go"]);
	});

	test(`past ${PREVIEW_FILE_LIMIT} files the shortfall is said out loud, and every hit still returns`, async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		const many = Array.from({ length: PREVIEW_FILE_LIMIT + 5 }, (_, i) => location(`/w/f${i}.go`, i));
		h.inFlight[0].resolve(many);
		expect(await pending).toHaveLength(many.length);
		const line = warn.mock.calls.map((c: unknown[]) => String(c[0])).find((l: string) => l.includes("no preview"));
		expect(line).toContain(`${many.length} hits in ${many.length} files`);
		expect(line).toContain(`the cap is ${PREVIEW_FILE_LIMIT}`);
	});

	test("a file that cannot be read is reported as unreadable, not as a cap", async () => {
		const h = harness();
		register({ ...h.document, readFile: async () => null });
		const pending = ask();
		h.inFlight[0].resolve([location("/w/gone.go", 1)]);
		await pending;
		const line = warn.mock.calls.map((c: unknown[]) => String(c[0])).find((l: string) => l.includes("no preview"));
		expect(line).toContain("(unreadable)");
	});

	// Monaco says "No references found for 'x'" at the cursor by itself when the
	// list is empty, so an empty ARRAY is the right answer here - and the log
	// carries the state, because nothing from a server that is still starting is
	// a wait rather than a verdict.
	test("an empty answer is an empty list, logged with the state", async () => {
		const h = harness("indexing");
		register(h.document);
		const pending = ask();
		h.inFlight[0].resolve([]);
		expect(await pending).toEqual([]);
		const line = warn.mock.calls
			.map((c: unknown[]) => String(c[0]))
			.find((l: string) => l.includes("references → 0 locations"));
		expect(line).toContain("server indexing");
		expect(h.read).toEqual([]);
	});
});

describe("refusing", () => {
	/**
	 * 🗝 `undefined`, never a throw and never an empty array. Monaco routes a
	 * rejected provider to `onUnexpectedExternalError`; an empty array makes it
	 * print "No references found for 'x'", which is exactly the wrong thing to say
	 * about a server that has not started.
	 */
	// 🗝 The message is deferred by a turn ON PURPOSE. Monaco prints its own
	// "No references found for 'x'" from the `.then` that follows this provider
	// settling, and said at the cursor any earlier ours is overwritten by a
	// sentence that is false — the server did not fail to find references, it
	// does not do reference search at all.
	const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

	test("no client yet: undefined, and the reason lands at the cursor", async () => {
		const h = harness("starting");
		register({ ...h.document, getClient: () => null });
		await expect(ask()).resolves.toBeUndefined();
		expect(h.shown, "shown before Monaco's own message could be").toEqual([]);
		await settle();
		expect(h.shown).toEqual(["the language server is starting"]);
	});

	test("a server that offers no reference search says which silence it is", async () => {
		const h = harness("ready", { hover: true, references: false });
		register(h.document);
		await expect(ask()).resolves.toBeUndefined();
		await settle();
		expect(h.shown[0]).toContain("offers no reference search");
		expect(h.sent).toEqual([]);
	});

	// ⇧F12 is a deliberate gesture, so unlike hover EVERY refusal is answered
	// where the reader is looking, and logged.
	test("a failed request is shown and logged", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		h.inFlight[0].reject(new Error("connection lost"));
		await expect(pending).resolves.toBeUndefined();
		await settle();
		expect(h.shown).toEqual(["connection lost"]);
		expect(warn.mock.calls.map((c: unknown[]) => String(c[0])).join(" ")).toContain("references unavailable");
	});

	test("a cancelled call answers nothing rather than an empty list", async () => {
		const h = harness();
		register(h.document);
		await expect(ask(true)).resolves.toBeUndefined();
		expect(h.sent).toEqual([]);
	});
});

describe("registration", () => {
	test("one provider per language, however many panes", () => {
		register(harness().document);
		register({ ...harness().document, modelUri: "ao-file:///s/other.go" });
		expect(registrations.filter((r) => !r.disposed)).toHaveLength(1);
	});

	test("the provider goes when the last pane does", () => {
		const first = register(harness().document);
		const second = register({ ...harness().document, modelUri: "ao-file:///s/other.go" });
		first.dispose();
		expect(registrations.filter((r) => !r.disposed)).toHaveLength(1);
		second.dispose();
		expect(registrations.filter((r) => !r.disposed)).toHaveLength(0);
	});
});
