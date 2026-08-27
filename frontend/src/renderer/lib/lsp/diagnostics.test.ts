import type { editor } from "monaco-editor";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { LspClient } from "./lsp-client";

/**
 * What a reader can actually SEE about diagnostics is a marker on a line, so
 * every assertion here is on what reached `setModelMarkers` — never on the
 * absence of an error, which is this whole area's characteristic failure.
 *
 * The monaco barrel boots the editor on import, so it is replaced by the three
 * things this module touches.
 */
const markers: { resource: string; owner: string; data: editor.IMarkerData[] }[] = [];
vi.mock("../monaco-setup", () => ({
	languageForPath: () => "go",
	monaco: {
		editor: {
			setModelMarkers: (model: { uri: { toString: () => string } }, owner: string, data: editor.IMarkerData[]) =>
				markers.push({ resource: model.uri.toString(), owner, data }),
			getModel: () => null,
			createModel: () => ({ isDisposed: () => false, dispose: () => undefined }),
		},
		Uri: { parse: (value: string) => ({ toString: () => value }) },
	},
}));

const { registerDiagnostics } = await import("./diagnostics");

const URI = "file:///w/main.go";

function fakeClient(): LspClient & { publish: (params: unknown) => void; listeners: number } {
	const listeners = new Set<(params: unknown) => void>();
	return {
		handleId: "h1",
		documentUri: (p: string) => `file://${p}`,
		semanticTokensLegend: () => null,
		completionCapability: () => null,
		features: () => ({ hover: true, references: true }),
		request: vi.fn(),
		notify: vi.fn(),
		didOpen: vi.fn(),
		didClose: vi.fn(),
		isOpen: () => true,
		dispose: vi.fn(),
		onNotification: (method: string, listener: (params: unknown) => void) => {
			expect(method).toBe("textDocument/publishDiagnostics");
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
		publish: (params: unknown) => {
			for (const listener of [...listeners]) listener(params);
		},
		get listeners() {
			return listeners.size;
		},
	} as never;
}

function fakeModel(uri = "ao-file:///s/main.go") {
	let disposed = false;
	return {
		uri: { toString: () => uri },
		isDisposed: () => disposed,
		kill: () => {
			disposed = true;
		},
	} as unknown as editor.ITextModel & { kill: () => void };
}

const diagnostic = (line: number, severity: number, message: string) => ({
	range: { start: { line, character: 2 }, end: { line, character: 9 } },
	severity,
	message,
});

let warn: ReturnType<typeof vi.spyOn>;
const open: { dispose: () => void }[] = [];
beforeEach(() => {
	markers.length = 0;
	warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
});
afterEach(() => {
	for (const r of open.splice(0)) r.dispose();
	warn.mockRestore();
});
function register(input: Parameters<typeof registerDiagnostics>[0]) {
	const r = registerDiagnostics(input);
	open.push(r);
	return r;
}

function setup(overrides: Partial<Parameters<typeof registerDiagnostics>[0]> = {}) {
	const client = fakeClient();
	const model = fakeModel();
	const counts: { errors: number; warnings: number }[] = [];
	register({
		languageId: "go",
		client,
		model,
		uri: URI,
		onCounts: (c) => counts.push(c),
		...overrides,
	});
	return { client, model, counts };
}

describe("the door that was missing", () => {
	test("an unsolicited publish becomes markers on the model", () => {
		const { client, counts } = setup();
		client.publish({ uri: URI, diagnostics: [diagnostic(4, 1, "undefined: Foo"), diagnostic(9, 2, "unused")] });
		expect(markers).toHaveLength(1);
		expect(markers[0].data.map((m) => [m.startLineNumber, m.message])).toEqual([
			[5, "undefined: Foo"],
			[10, "unused"],
		]);
		expect(counts).toEqual([{ errors: 1, warnings: 1 }]);
	});

	test("the owner is per LANGUAGE, so a re-attached server replaces rather than doubles", () => {
		const { client } = setup();
		client.publish({ uri: URI, diagnostics: [diagnostic(1, 1, "a")] });
		client.publish({ uri: URI, diagnostics: [diagnostic(1, 1, "a")] });
		expect(markers.map((m) => m.owner)).toEqual(["lsp:go", "lsp:go"]);
	});

	// gopls's FIRST publish after opening a file is empty and arrives ~932 ms in;
	// the real one lands at ~5 010 ms. Applying the empty one is right — the
	// header just must not call it a verdict, which is `countMarkers`' job.
	test("an EMPTY publish is applied, and counted as zero", () => {
		const { client, counts } = setup();
		client.publish({ uri: URI, diagnostics: [] });
		expect(markers[0].data).toEqual([]);
		expect(counts).toEqual([{ errors: 0, warnings: 0 }]);
	});
});

describe("addressing", () => {
	test("a publish for a DIFFERENT document is not painted onto this one", () => {
		const { client } = setup();
		client.publish({ uri: "file:///w/other.go", diagnostics: [diagnostic(1, 1, "elsewhere")] });
		expect(markers).toEqual([]);
	});

	// 🗝 The Swift trap's shape: address a document by the wrong path and the
	// server answers about a file this app cannot match, in silence. One
	// dispatcher per CLIENT is what makes that visible.
	test("an unmatched target is said out loud, once per file", () => {
		const { client } = setup();
		client.publish({ uri: "file:///w/other.go", diagnostics: [] });
		client.publish({ uri: "file:///w/other.go", diagnostics: [] });
		client.publish({ uri: "file:///w/third.go", diagnostics: [] });
		const lines = warn.mock.calls.map((c: unknown[]) => String(c[0]));
		expect(lines.filter((l: string) => l.includes("/w/other.go"))).toHaveLength(1);
		expect(lines.filter((l: string) => l.includes("/w/third.go"))).toHaveLength(1);
	});

	test("a percent-encoded spelling of the same file still matches", () => {
		const { client } = setup({ uri: "file:///w/my%20file.go" });
		client.publish({ uri: "file:///w/my file.go", diagnostics: [diagnostic(0, 1, "hit")] });
		expect(markers).toHaveLength(1);
	});

	test("two documents on one client are dispatched to their own models", () => {
		const client = fakeClient();
		const a = fakeModel("ao-file:///s/a.go");
		const b = fakeModel("ao-file:///s/b.go");
		register({ languageId: "go", client, model: a, uri: "file:///w/a.go" });
		register({ languageId: "go", client, model: b, uri: "file:///w/b.go" });
		// One listener, not two: the dispatcher is per client.
		expect(client.listeners).toBe(1);
		client.publish({ uri: "file:///w/b.go", diagnostics: [diagnostic(0, 1, "b")] });
		expect(markers.map((m) => m.resource)).toEqual(["ao-file:///s/b.go"]);
	});
});

describe("versions", () => {
	// gopls sends one; sourcekit-lsp sends none, ever. Reordered delivery would
	// otherwise leave a file showing diagnostics the server already retracted.
	test("an OLDER version is dropped", () => {
		const { client } = setup();
		client.publish({ uri: URI, version: 5, diagnostics: [diagnostic(0, 1, "new")] });
		client.publish({ uri: URI, version: 3, diagnostics: [diagnostic(0, 1, "old")] });
		expect(markers).toHaveLength(1);
		expect(markers[0].data[0].message).toBe("new");
	});

	test("the same version is applied - a server may re-publish for one revision", () => {
		const { client } = setup();
		client.publish({ uri: URI, version: 5, diagnostics: [] });
		client.publish({ uri: URI, version: 5, diagnostics: [diagnostic(0, 1, "arrived late")] });
		expect(markers).toHaveLength(2);
	});

	// 🗝 sourcekit-lsp NEVER sends a version. A gate that required one would drop
	// every Swift diagnostic there has ever been, silently.
	test("a publish with NO version is always applied", () => {
		const { client } = setup();
		client.publish({ uri: URI, version: 9, diagnostics: [] });
		client.publish({ uri: URI, diagnostics: [diagnostic(0, 2, "swift-style")] });
		expect(markers[1].data[0].message).toBe("swift-style");
	});
});

describe("teardown", () => {
	// 🗝 The registration is keyed on the CLIENT, so it is disposed when the
	// server stops, is evicted or fails. Squiggles from a server that is no
	// longer running are the most confident kind of wrong answer available.
	test("dispose clears the markers and zeroes the count", () => {
		const client = fakeClient();
		const model = fakeModel();
		const counts: { errors: number; warnings: number }[] = [];
		const registration = registerDiagnostics({
			languageId: "go",
			client,
			model,
			uri: URI,
			onCounts: (c) => counts.push(c),
		});
		client.publish({ uri: URI, diagnostics: [diagnostic(0, 1, "e")] });
		registration.dispose();
		expect(markers[markers.length - 1].data).toEqual([]);
		expect(counts[counts.length - 1]).toEqual({ errors: 0, warnings: 0 });
	});

	test("dispose unsubscribes, so a late publish paints nothing", () => {
		const client = fakeClient();
		const registration = registerDiagnostics({ languageId: "go", client, model: fakeModel(), uri: URI });
		registration.dispose();
		markers.length = 0;
		client.publish({ uri: URI, diagnostics: [diagnostic(0, 1, "late")] });
		expect(markers).toEqual([]);
		expect(client.listeners).toBe(0);
	});

	test("disposing one of two documents leaves the other listening", () => {
		const client = fakeClient();
		const first = registerDiagnostics({
			languageId: "go",
			client,
			model: fakeModel("ao-file:///s/a.go"),
			uri: "file:///w/a.go",
		});
		register({ languageId: "go", client, model: fakeModel("ao-file:///s/b.go"), uri: "file:///w/b.go" });
		first.dispose();
		markers.length = 0;
		client.publish({ uri: "file:///w/b.go", diagnostics: [diagnostic(0, 1, "b")] });
		expect(markers).toHaveLength(1);
	});

	test("dispose is idempotent", () => {
		const client = fakeClient();
		const registration = registerDiagnostics({ languageId: "go", client, model: fakeModel(), uri: URI });
		registration.dispose();
		const after = markers.length;
		registration.dispose();
		expect(markers).toHaveLength(after);
	});

	// A model can be disposed before the pane's effects unwind, and
	// `setModelMarkers` on a dead model throws inside the IPC callback that
	// every server's traffic shares.
	test("a disposed model is never written to", () => {
		const client = fakeClient();
		const model = fakeModel();
		register({ languageId: "go", client, model, uri: URI });
		model.kill();
		expect(() => client.publish({ uri: URI, diagnostics: [diagnostic(0, 1, "e")] })).not.toThrow();
		expect(markers).toEqual([]);
	});
});
