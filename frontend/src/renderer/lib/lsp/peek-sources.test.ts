import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

/**
 * The models Monaco's peek widgets read out of.
 *
 * 🗝 Every case here is one where the widget OPENS and shows nothing: monaco
 * 0.56's standalone `createModelReference` rejects for any resource that is not
 * already a live model, so a missing model is not an error — it is a blank
 * preview pane and a tree of `File.swift:12:5` rows.
 *
 * The fake model registry below is the real one's contract: `getModel` finds by
 * URI, `createModel` throws if that URI is taken (Monaco does), and disposal is
 * observable.
 */
type FakeModel = {
	uri: { toString: () => string };
	value: string;
	language: string;
	disposed: boolean;
	isDisposed: () => boolean;
	dispose: () => void;
};
const store = new Map<string, FakeModel>();
let created = 0;

/** A model somebody else owns — a pane's, or one that appeared mid-read. */
function putModel(uri: string, value: string, language = "go"): FakeModel {
	const model: FakeModel = {
		uri: { toString: () => uri },
		value,
		language,
		disposed: false,
		isDisposed: () => model.disposed,
		dispose: () => {
			model.disposed = true;
		},
	};
	store.set(uri, model);
	return model;
}

vi.mock("../monaco-setup", () => ({
	languageForPath: (path: string) => (path.endsWith(".swift") ? "swift" : "go"),
	monaco: {
		editor: {
			getModel: (uri: { toString: () => string }) => store.get(uri.toString()) ?? null,
			createModel: (value: string, language: string, uri: { toString: () => string }) => {
				const key = uri.toString();
				// Monaco throws on a duplicate URI. A test that let it pass would hide
				// the race this module guards against.
				if (store.has(key)) throw new Error(`model already exists: ${key}`);
				created++;
				return putModel(key, value, language);
			},
		},
		Uri: { parse: (value: string) => ({ toString: () => value }) },
	},
}));

const {
	disposePreviewModels,
	ensurePreviewModels,
	modelUriForPath,
	modelUriForTargetUri,
	registerPaneModel,
	PREVIEW_FILE_LIMIT,
} = await import("./peek-sources");

const release: (() => void)[] = [];
beforeEach(() => {
	store.clear();
	created = 0;
});
afterEach(() => {
	for (const r of release.splice(0)) r();
	disposePreviewModels();
});

/** A reader that always succeeds, and records what it was asked for. */
function reader() {
	const asked: string[] = [];
	return {
		asked,
		read: async (path: string) => {
			asked.push(path);
			return `// ${path}\n`;
		},
	};
}

describe("addressing", () => {
	test("a file with no pane gets the ao-file spelling of its absolute path", () => {
		expect(modelUriForPath("/w/pkg/main.go").toString()).toBe("ao-file:///w/pkg/main.go");
	});

	// 🗝 The case that is easy to miss entirely. A pane's model is
	// `ao-file:///<session>/<relative>`; a server's answer maps to
	// `ao-file://<absolute>`. Two URIs, one file — so peeking a definition in the
	// file you are ALREADY READING opened with a blank pane.
	test("a file open in a pane is addressed by the PANE's model uri", () => {
		release.push(registerPaneModel("/w/pkg/main.go", "ao-file:///sess-1/pkg/main.go"));
		expect(modelUriForPath("/w/pkg/main.go").toString()).toBe("ao-file:///sess-1/pkg/main.go");
	});

	test("a closed pane stops claiming its file", () => {
		const off = registerPaneModel("/w/a.go", "ao-file:///sess-1/a.go");
		off();
		expect(modelUriForPath("/w/a.go").toString()).toBe("ao-file:///w/a.go");
	});

	// Two panes can show one file; the second to register owns it, and the first
	// closing must not un-register the second.
	test("a stale release does not evict the pane that replaced it", () => {
		const first = registerPaneModel("/w/a.go", "ao-file:///sess-1/a.go");
		release.push(registerPaneModel("/w/a.go", "ao-file:///sess-2/a.go"));
		first();
		expect(modelUriForPath("/w/a.go").toString()).toBe("ao-file:///sess-2/a.go");
	});

	test("a server's file: uri is decoded before it is addressed", () => {
		expect(modelUriForTargetUri("file:///w/my%20file.go").toString()).toBe("ao-file:///w/my%20file.go");
	});
});

describe("materialising previews", () => {
	test("creates a model per file, with the file's own language", () => {
		return (async () => {
			const r = reader();
			const result = await ensurePreviewModels(["/w/a.go", "/w/b.swift"], r.read);
			expect(result).toEqual({ requested: 2, missing: 0 });
			expect(store.get("ao-file:///w/b.swift")?.language).toBe("swift");
			expect(store.get("ao-file:///w/a.go")?.value).toBe("// /w/a.go\n");
		})();
	});

	// 164 hits across 35 files is 35 reads, not 164.
	test("a file already backed by a model is not read again", async () => {
		const r = reader();
		await ensurePreviewModels(["/w/a.go"], r.read);
		await ensurePreviewModels(["/w/a.go"], r.read);
		expect(r.asked).toEqual(["/w/a.go"]);
		expect(created).toBe(1);
	});

	// A pane's live buffer is the right preview for its own file, and reading
	// the saved text over it would show the file WITHOUT the reader's edits.
	test("a file open in a pane is never read or re-created", async () => {
		release.push(registerPaneModel("/w/open.go", "ao-file:///sess-1/open.go"));
		putModel("ao-file:///sess-1/open.go", "live buffer");
		const r = reader();
		const result = await ensurePreviewModels(["/w/open.go"], r.read);
		expect(r.asked).toEqual([]);
		expect(result.missing).toBe(0);
	});

	// 🗝 A file that cannot be shown counts as MISSING rather than becoming an
	// empty model. An empty model renders as an empty file, which is a worse lie
	// than Monaco's own `path:line:col` row.
	test("a file that cannot be read is counted, not faked", async () => {
		const result = await ensurePreviewModels(["/w/huge.go", "/w/ok.go"], async (path) =>
			path.includes("huge") ? null : "text",
		);
		expect(result.missing).toBe(1);
		expect(store.has("ao-file:///w/huge.go")).toBe(false);
		expect(store.has("ao-file:///w/ok.go")).toBe(true);
	});

	test("a reader that throws is counted the same way, and the rest still load", async () => {
		const result = await ensurePreviewModels(["/w/bad.go", "/w/ok.go"], async (path) => {
			if (path.includes("bad")) throw new Error("boom");
			return "text";
		});
		expect(result.missing).toBe(1);
		expect(store.has("ao-file:///w/ok.go")).toBe(true);
	});

	test(`past ${PREVIEW_FILE_LIMIT} files the rest are reported rather than fetched`, async () => {
		const paths = Array.from({ length: PREVIEW_FILE_LIMIT + 7 }, (_, i) => `/w/f${i}.go`);
		const r = reader();
		const result = await ensurePreviewModels(paths, r.read);
		expect(r.asked).toHaveLength(PREVIEW_FILE_LIMIT);
		expect(result).toEqual({ requested: paths.length, missing: 7 });
	});

	// The reads are concurrent, so two of them can finish either side of a pane
	// opening the same file. `createModel` on a taken URI throws.
	test("a model that appears mid-read is adopted rather than duplicated", async () => {
		const result = await ensurePreviewModels(["/w/race.go"], async (path) => {
			putModel("ao-file:///w/race.go", "opened meanwhile");
			return `// ${path}`;
		});
		expect(result.missing).toBe(0);
		expect(store.get("ao-file:///w/race.go")?.value).toBe("opened meanwhile");
	});

	test("no files at all is not a request", async () => {
		const r = reader();
		expect(await ensurePreviewModels([], r.read)).toEqual({ requested: 0, missing: 0 });
		expect(r.asked).toEqual([]);
	});
});

describe("what gets disposed", () => {
	// 🗝 Disposing a model a PANE owns would blank the editor showing it. Only
	// models this module created are ever ours to dispose.
	test("a pane's model is never disposed", async () => {
		release.push(registerPaneModel("/w/open.go", "ao-file:///sess-1/open.go"));
		const pane = putModel("ao-file:///sess-1/open.go", "live");
		await ensurePreviewModels(["/w/open.go"], async () => "unused");
		disposePreviewModels();
		expect(pane.disposed).toBe(false);
	});

	test("preview models are disposed on reset", async () => {
		await ensurePreviewModels(["/w/a.go"], async () => "text");
		disposePreviewModels();
		expect(store.get("ao-file:///w/a.go")?.disposed).toBe(true);
	});
});
