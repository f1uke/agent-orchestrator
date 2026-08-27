import type { CancellationToken, editor, languages } from "monaco-editor";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { LspClient } from "./lsp-client";

/**
 * The monaco barrel boots the whole editor on import, so it is replaced by the
 * one thing this module touches: the provider registry.
 *
 * Everything asserted here is about the POLICY — which requests reach the wire,
 * in what order, and what a refusal looks like — because the mapping is proved
 * in `hover-mapping.test.ts` and the *feel* is the smoke checklist's job.
 */
const registrations: { languageId: string; provider: languages.HoverProvider; disposed: boolean }[] = [];
vi.mock("../monaco-setup", () => ({
	monaco: {
		languages: {
			registerHoverProvider: (languageId: string, provider: languages.HoverProvider) => {
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

const { registerHover } = await import("./hover-provider");
const { runInLane, resetLanes } = await import("./request-lane");

type Deferred = { resolve: (v: unknown) => void; reject: (e: unknown) => void; method: string; params: unknown };

function harness(state = "ready", features = { hover: true, references: true }) {
	const inFlight: Deferred[] = [];
	const sent: { method: string; params: unknown }[] = [];
	const client = {
		handleId: "h1",
		// The Swift mapping in miniature: the server's own address is NOT the path.
		documentUri: (absolute: string) => `file:///shadow${absolute}`,
		semanticTokensLegend: () => null,
		completionCapability: () => null,
		features: () => features,
		request: (method: string, params: unknown) => {
			sent.push({ method, params });
			return new Promise((resolve, reject) => inFlight.push({ resolve, reject, method, params }));
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
		client,
		document: {
			languageId: "swift",
			modelUri: MODEL_URI,
			getClient: () => client,
			getAbsolutePath: () => "/w/View.swift",
			getServerText: () => "let x = 1",
			getState: () => state,
		},
	};
}

const MODEL_URI = "ao-file:///s/View.swift";
const model = { uri: { toString: () => MODEL_URI } } as unknown as editor.ITextModel;
const POSITION = { lineNumber: 56, column: 17 } as never;

function token(cancelled = false): CancellationToken {
	return { isCancellationRequested: cancelled, onCancellationRequested: () => ({ dispose: () => undefined }) } as never;
}

function latest(): languages.HoverProvider {
	const live = registrations.filter((r) => !r.disposed);
	return live[live.length - 1].provider;
}

/** 🗝 It RESOLVES for a refusal, with null, and never rejects — see below. */
function ask(cancelled = false): Promise<languages.Hover | null | undefined> {
	return Promise.resolve(latest().provideHover(model, POSITION, token(cancelled), undefined as never)) as Promise<
		languages.Hover | null | undefined
	>;
}

const HOVER = { contents: { kind: "markdown", value: "```swift\nlet x: Int\n```" } };
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
/** Several queued waiters need more than one microtask before the next request goes out. */
async function until(predicate: () => boolean): Promise<void> {
	for (let i = 0; i < 50 && !predicate(); i++) await tick();
}

let warn: ReturnType<typeof vi.spyOn>;
const open: { dispose: () => void }[] = [];
beforeEach(() => {
	registrations.length = 0;
	warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
});
afterEach(() => {
	for (const r of open.splice(0)) r.dispose();
	resetLanes();
	warn.mockRestore();
});
function register(document: Parameters<typeof registerHover>[0]) {
	const r = registerHover(document);
	open.push(r);
	return r;
}

describe("the answer", () => {
	test("the position goes out 0-based, at the SERVER's own uri", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		expect(h.sent[0]).toEqual({
			method: "textDocument/hover",
			params: { textDocument: { uri: "file:///shadow/w/View.swift" }, position: { line: 55, character: 16 } },
		});
		h.inFlight[0].resolve(HOVER);
		await expect(pending).resolves.toMatchObject({ contents: [{ value: "```swift\nlet x: Int\n```" }] });
	});

	test("a pane that is not this model is not answered for", async () => {
		const h = harness();
		register(h.document);
		const other = { uri: { toString: () => "ao-file:///s/Other.swift" } } as unknown as editor.ITextModel;
		await expect(latest().provideHover(other, POSITION, token(), undefined as never)).resolves.toBeNull();
		expect(h.sent).toEqual([]);
	});
});

describe("the policy: serialise, never cancel, never debounce", () => {
	/**
	 * 🗝 The decision this slice had to make on its own, and it was measured
	 * rather than inherited. Cancelling hover neither helps nor hurts the answer
	 * the reader waits for (2 410 vs 2 404 ms on sourcekit-lsp over an 8-position
	 * sweep) — but sourcekit-lsp answers `-32800` to 6 of the 8, and each cancel
	 * throws away the in-progress type-check the next one has to redo.
	 */
	test("NOTHING is ever cancelled on the wire", async () => {
		const h = harness();
		register(h.document);
		const first = ask();
		const second = ask();
		await tick();
		expect(h.sent.map((s) => s.method)).toEqual(["textDocument/hover"]);
		expect(h.client.notify).not.toHaveBeenCalled();
		h.inFlight[0].resolve(HOVER);
		await first;
		// The second was not superseded, so it goes out once the wire is free —
		// and still nothing was cancelled to make room for it.
		await until(() => h.inFlight.length === 2);
		h.inFlight[1].resolve(HOVER);
		await second;
		expect(h.client.notify).not.toHaveBeenCalled();
	});

	test("a pointer sweep keeps exactly one request on the wire", async () => {
		const h = harness();
		register(h.document);
		const asks = Array.from({ length: 6 }, () => ask());
		await tick();
		expect(h.inFlight).toHaveLength(1);
		h.inFlight[0].resolve(HOVER);
		await until(() => h.inFlight.length === 2);
		// The five that were superseded while queued never went out at all.
		expect(h.sent).toHaveLength(2);
		h.inFlight[1].resolve(HOVER);
		const answers = await Promise.all(asks);
		expect(answers.slice(0, 5).every((a) => a === null)).toBe(true);
		expect(answers[5]).toMatchObject({ contents: [{ value: expect.stringContaining("let x: Int") }] });
	});

	// The lane's check only runs for a call that WAITED. A call that found the
	// wire free and was cancelled while its own request was out would otherwise
	// land a stale position's type on a word the pointer has left.
	test("a call cancelled with nothing in flight never answers", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		await tick();
		// Supersede it: a newer pointer rest, which is what Monaco does.
		const newer = ask();
		h.inFlight[0].resolve(HOVER);
		await expect(pending).resolves.toBeNull();
		await until(() => h.inFlight.length === 2);
		h.inFlight[1].resolve(HOVER);
		await expect(newer).resolves.toMatchObject({ contents: expect.any(Array) });
	});

	/**
	 * 🗝 THE reason hover shares completion's lane rather than getting its own.
	 * Both wait on the file's first type-check — 1 919 ms on a cold Swift file —
	 * and two lanes would pay for it twice.
	 */
	test("a request already on the wire for this document is WAITED for, not raced", async () => {
		const h = harness();
		register(h.document);
		let release: (() => void) | undefined;
		// Something else — completion, in the app — takes the lane first.
		const other = runInLane(
			MODEL_URI,
			() => false,
			() => new Promise<string>((resolve) => (release = () => resolve("done"))),
		);
		const pending = ask();
		await tick();
		expect(h.sent).toEqual([]);
		release?.();
		await other;
		await tick();
		expect(h.sent).toHaveLength(1);
		h.inFlight[0].resolve(HOVER);
		await pending;
	});

	test("a failed request releases the slot rather than wedging every later hover", async () => {
		const h = harness();
		register(h.document);
		const failing = ask();
		await tick();
		h.inFlight[0].reject(new Error("boom"));
		await expect(failing).resolves.toBeNull();
		const next = ask();
		await tick();
		expect(h.sent).toHaveLength(2);
		h.inFlight[1].resolve(HOVER);
		await expect(next).resolves.toMatchObject({ contents: expect.any(Array) });
	});

	test("a server that never answers is given up on", async () => {
		vi.useFakeTimers();
		try {
			const h = harness();
			register(h.document);
			const pending = ask();
			await vi.advanceTimersByTimeAsync(8_001);
			await expect(pending).resolves.toBeNull();
			expect(warn.mock.calls.map((c: unknown[]) => String(c[0])).join(" ")).toContain("hover failed");
		} finally {
			vi.useRealTimers();
		}
	});
});

describe("refusing", () => {
	/**
	 * 🗝 null, never a throw. `getHover.js:22` hands a rejected provider to
	 * `onUnexpectedExternalError`, so a throw prints a stack trace on the
	 * renderer's global error channel once per pointer rest while a server
	 * starts — with no reason attached to any of them.
	 */
	test.each([
		["no client yet", { getClient: () => null }],
		["no path", { getAbsolutePath: () => null }],
		["the buffer has not reached the server", { getServerText: () => null }],
	])("%s: resolves with null rather than throwing", async (_name, override) => {
		const h = harness("starting");
		register({ ...h.document, ...override } as never);
		await expect(ask()).resolves.toBeNull();
		expect(h.sent).toEqual([]);
	});

	// A message per pointer rest is a nag, and noise is its own silent failure:
	// it is what makes a real warning unreadable. The status pill already says
	// the server is starting.
	test("a server that is starting is refused QUIETLY", async () => {
		const h = harness("starting");
		register({ ...h.document, getClient: () => null } as never);
		await ask();
		expect(warn).not.toHaveBeenCalled();
	});

	// Distinct from "not yet attached", and a reader has to be able to tell them
	// apart — but once per session, not once per rest.
	test("a server that offers no hover says so, exactly once", async () => {
		const h = harness("ready", { hover: false, references: true });
		register(h.document);
		await ask();
		await ask();
		expect(h.sent).toEqual([]);
		const lines = warn.mock.calls
			.map((c: unknown[]) => String(c[0]))
			.filter((l: string) => l.includes("offers no hover"));
		expect(lines).toHaveLength(1);
	});

	// Up, answering, and answering nothing — which for hover is often CORRECT
	// (whitespace, a comment). The state is part of the sentence because on a
	// cold Swift file the first hover costs ~1.9 s.
	test("ready and empty is logged with the timing and the state", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		h.inFlight[0].resolve(null);
		await expect(pending).resolves.toBeNull();
		const line = warn.mock.calls.map((c: unknown[]) => String(c[0])).find((l: string) => l.includes("hover → nothing"));
		expect(line).toContain("/w/View.swift:56:17");
		expect(line).toContain("server ready");
	});

	// A request that FAILED is the difference between a server with nothing to
	// say and a server that is broken, so it is always worth a line.
	test("a failed request is always logged, even though nobody asked", async () => {
		const h = harness();
		register(h.document);
		const pending = ask();
		await tick();
		h.inFlight[0].reject(new Error("connection lost"));
		await pending;
		expect(warn.mock.calls.map((c: unknown[]) => String(c[0])).join(" ")).toContain("hover failed");
	});
});

describe("registration", () => {
	test("one provider per language, however many panes", () => {
		const a = harness();
		const b = harness();
		register(a.document);
		register({ ...b.document, modelUri: "ao-file:///s/Other.swift" });
		expect(registrations.filter((r) => !r.disposed)).toHaveLength(1);
	});

	test("the provider goes when the LAST pane does", () => {
		const a = harness();
		const b = harness();
		const first = register(a.document);
		register({ ...b.document, modelUri: "ao-file:///s/Other.swift" });
		first.dispose();
		expect(registrations.filter((r) => !r.disposed)).toHaveLength(1);
		open.splice(0).forEach((r) => r.dispose());
		expect(registrations.filter((r) => !r.disposed)).toHaveLength(0);
	});

	test("a closed pane is really forgotten", async () => {
		const h = harness();
		const registration = register(h.document);
		const other = harness();
		register({ ...other.document, modelUri: "ao-file:///s/Other.swift" });
		registration.dispose();
		await expect(ask()).resolves.toBeNull();
		expect(h.sent).toEqual([]);
	});

	test("dispose is idempotent", () => {
		const h = harness();
		const registration = register(h.document);
		registration.dispose();
		registration.dispose();
		expect(registrations.filter((r) => !r.disposed)).toHaveLength(0);
	});
});
