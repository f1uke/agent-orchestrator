import type { editor, languages } from "monaco-editor";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { LspClient } from "./lsp-client";

/**
 * ⌘click's THIRD half, added by this slice: the same DefinitionProvider feeds
 * Peek Definition (⌥F12) and the ⌘-hover preview, and both of those open with a
 * BLANK pane unless a Monaco model already exists for the target file.
 *
 * 🗝 Including a target in the file you are already reading — the pane's model
 * is `ao-file:///<session>/<relative>` and a server's answer is addressed
 * `ao-file://<absolute>`, so the lookup missed even then.
 *
 * The navigation half is proved by `lsp-registry.gopls.test.ts` against a real
 * server, where the assertion is where the editor LANDS.
 */
const providers: languages.DefinitionProvider[] = [];
const models = new Map<string, unknown>();
const created: string[] = [];
vi.mock("../monaco-setup", () => ({
	languageForPath: () => "go",
	monaco: {
		languages: {
			registerDefinitionProvider: (_languageId: string, provider: languages.DefinitionProvider) => {
				providers.push(provider);
				return { dispose: () => providers.splice(providers.indexOf(provider), 1) };
			},
		},
		editor: {
			registerEditorOpener: () => ({ dispose: () => undefined }),
			getModel: (uri: { toString: () => string }) => models.get(uri.toString()) ?? null,
			createModel: (_text: string, _language: string, uri: { toString: () => string }) => {
				created.push(uri.toString());
				const model = { isDisposed: () => false, dispose: () => undefined, uri };
				models.set(uri.toString(), model);
				return model;
			},
		},
		Uri: { parse: (value: string) => ({ toString: () => value }) },
	},
}));

const { registerLspNavigation } = await import("./definition");
const { disposePreviewModels, registerPaneModel } = await import("./peek-sources");

const MODEL_URI = "ao-file:///sess-1/pkg/service.go";
const model = { uri: { toString: () => MODEL_URI } } as unknown as editor.ITextModel;
const POSITION = { lineNumber: 40, column: 6 } as never;

function harness() {
	const inFlight: { resolve: (v: unknown) => void; reject: (e: unknown) => void }[] = [];
	const read: string[] = [];
	const client = {
		documentUri: (absolute: string) => `file://${absolute}`,
		request: () => new Promise((resolve, reject) => inFlight.push({ resolve, reject })),
	} as unknown as LspClient;
	return {
		inFlight,
		read,
		input: {
			languageId: "go",
			getClient: () => client,
			getState: () => "ready",
			getWorkspaceRoot: () => "/w",
			getAbsolutePath: () => "/w/pkg/service.go",
			openFile: vi.fn(),
			readFile: async (absolutePath: string) => {
				read.push(absolutePath);
				return `// ${absolutePath}\n`;
			},
		},
	};
}

const location = (path: string, line: number) => ({
	uri: `file://${path}`,
	range: { start: { line, character: 5 }, end: { line, character: 12 } },
});

let warn: ReturnType<typeof vi.spyOn>;
const open: { dispose: () => void }[] = [];
const release: (() => void)[] = [];
beforeEach(() => {
	providers.length = 0;
	models.clear();
	created.length = 0;
	warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
});
afterEach(() => {
	for (const r of open.splice(0)) r.dispose();
	for (const r of release.splice(0)) r();
	disposePreviewModels();
	warn.mockRestore();
});
function register(input: Parameters<typeof registerLspNavigation>[0]) {
	const r = registerLspNavigation(input);
	open.push(r);
	return r;
}
function ask() {
	return Promise.resolve(providers[providers.length - 1].provideDefinition(model, POSITION, undefined as never));
}

describe("the peek preview", () => {
	test("a cross-file target gets a model before the answer is returned", async () => {
		const h = harness();
		register(h.input);
		const pending = ask();
		h.inFlight[0].resolve([location("/w/pkg/entry.go", 11)]);
		const links = (await pending) as languages.LocationLink[];
		expect(links[0].uri.toString()).toBe("ao-file:///w/pkg/entry.go");
		expect(created).toEqual(["ao-file:///w/pkg/entry.go"]);
		expect(h.read).toEqual(["/w/pkg/entry.go"]);
	});

	// 🗝 The obvious case, and the one that was broken in the least visible way.
	test("a target in the OPEN file resolves to the pane's own model, unread", async () => {
		release.push(registerPaneModel("/w/pkg/service.go", MODEL_URI));
		models.set(MODEL_URI, { isDisposed: () => false });
		const h = harness();
		register(h.input);
		const pending = ask();
		h.inFlight[0].resolve([location("/w/pkg/service.go", 7)]);
		const links = (await pending) as languages.LocationLink[];
		// The pane's URI, so the preview shows the LIVE buffer rather than a second
		// copy of the saved text.
		expect(links[0].uri.toString()).toBe(MODEL_URI);
		expect(h.read).toEqual([]);
		expect(created).toEqual([]);
	});

	test("a definition in GOROOT is read once and reused", async () => {
		const h = harness();
		register(h.input);
		const first = ask();
		h.inFlight[0].resolve([location("/usr/local/go/src/fmt/print.go", 40)]);
		await first;
		const second = ask();
		h.inFlight[1].resolve([location("/usr/local/go/src/fmt/print.go", 40)]);
		await second;
		expect(h.read).toEqual(["/usr/local/go/src/fmt/print.go"]);
	});

	// ⌘click still works - it goes through the app's own file-open seam. It is
	// the peek pane that would be blank, and saying so is what separates a known
	// limit from a broken widget.
	test("a target that cannot be read still navigates, and says the preview is missing", async () => {
		const h = harness();
		register({ ...h.input, readFile: async () => null });
		const pending = ask();
		h.inFlight[0].resolve([location("/w/huge.go", 3)]);
		expect(await pending).toHaveLength(1);
		expect(warn.mock.calls.map((c: unknown[]) => String(c[0])).join(" ")).toContain("no peek preview");
	});

	test("an empty answer reads nothing and logs the state", async () => {
		const h = harness();
		register(h.input);
		const pending = ask();
		h.inFlight[0].resolve([]);
		expect(await pending).toEqual([]);
		expect(h.read).toEqual([]);
		expect(warn.mock.calls.map((c: unknown[]) => String(c[0])).join(" ")).toContain("definition → 0 locations");
	});
});

describe("registration", () => {
	// Registration is refcounted per LANGUAGE, so a second pane must hand its own
	// reader in rather than leaving the entry closed over a pane that is gone.
	test("a second pane's reader replaces the first", async () => {
		const first = harness();
		const second = harness();
		register(first.input);
		register(second.input);
		const pending = ask();
		first.inFlight[0].resolve([location("/w/pkg/entry.go", 11)]);
		await pending;
		expect(second.read).toEqual(["/w/pkg/entry.go"]);
		expect(first.read).toEqual([]);
	});

	test("the provider goes when the last pane does", () => {
		const a = register(harness().input);
		const b = register(harness().input);
		a.dispose();
		expect(providers).toHaveLength(1);
		b.dispose();
		expect(providers).toHaveLength(0);
	});
});
