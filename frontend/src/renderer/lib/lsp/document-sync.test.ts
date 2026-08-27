import type { editor } from "monaco-editor";
import { beforeEach, describe, expect, test, vi } from "vitest";
import { openDocumentSync } from "./document-sync";
import type { LspClient } from "./lsp-client";

/**
 * What the server is TOLD about the buffer.
 *
 * Every assertion here is about a failure that produces no error: a completion
 * computed against the file as it was saved, a semantic token placed on the
 * column it occupied two keystrokes ago, or a document the server never heard
 * about at all.
 */

type Sent = { method: string; params: Record<string, unknown> };

function fakeClient(sent: Sent[]): LspClient {
	return {
		handleId: "h1",
		documentUri: (absolute) => `file://${absolute}`,
		semanticTokensLegend: () => null,
		completionCapability: () => null,
		request: vi.fn(),
		notify: (method, params) => sent.push({ method, params: params as Record<string, unknown> }),
		didOpen: (uri, languageId, text) =>
			sent.push({ method: "textDocument/didOpen", params: { uri, languageId, text } }),
		didClose: (uri) => sent.push({ method: "textDocument/didClose", params: { uri } }),
		isOpen: () => true,
		dispose: vi.fn(),
	};
}

/**
 * A model that behaves like Monaco's for the two things this module uses: it
 * holds a value, and it announces each edit as an offset/length/text triple.
 */
function fakeModel(initial: string) {
	let value = initial;
	const listeners: ((e: { changes: editor.IModelContentChange[] }) => void)[] = [];
	return {
		model: {
			getValue: () => value,
			onDidChangeContent: (cb: (e: { changes: editor.IModelContentChange[] }) => void) => {
				listeners.push(cb);
				return { dispose: () => listeners.splice(listeners.indexOf(cb), 1) };
			},
		} as unknown as editor.ITextModel,
		/** Apply an edit the way Monaco would, and announce it the way Monaco does. */
		edit(rangeOffset: number, rangeLength: number, text: string) {
			value = value.slice(0, rangeOffset) + text + value.slice(rangeOffset + rangeLength);
			const change = { rangeOffset, rangeLength, text } as editor.IModelContentChange;
			for (const cb of [...listeners]) cb({ changes: [change] });
		},
		/** Move the value WITHOUT announcing it - the drift this module guards against. */
		desync(next: string) {
			value = next;
		},
		announceNothing() {
			for (const cb of [...listeners]) cb({ changes: [] });
		},
		get value() {
			return value;
		},
	};
}

let warn: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
	// `mockClear`, not just `spyOn`: vitest hands back the SAME spy for an
	// already-spied method, so without this the calls of every earlier test in
	// the file are still on it and "did not warn" can never be true.
	warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
	warn.mockClear();
});

describe("opening", () => {
	test("the document is opened at the server's OWN uri, not its real path", () => {
		const sent: Sent[] = [];
		openDocumentSync({
			client: fakeClient(sent),
			model: fakeModel("package main\n").model,
			absolutePath: "/root/main.go",
			languageId: "go",
		});
		expect(sent[0]).toEqual({
			method: "textDocument/didOpen",
			params: { uri: "file:///root/main.go", languageId: "go", text: "package main\n" },
		});
	});

	test("the MODEL's text is opened, not any text handed in beside it", () => {
		const sent: Sent[] = [];
		const m = fakeModel("edited already\n");
		openDocumentSync({ client: fakeClient(sent), model: m.model, absolutePath: "/a.go", languageId: "go" });
		expect((sent[0].params as { text: string }).text).toBe("edited already\n");
	});
});

describe("didChange", () => {
	test("an edit is sent as an INCREMENTAL range - both servers advertise change: 2", () => {
		const sent: Sent[] = [];
		const m = fakeModel("func main() {\n\tx\n}\n");
		openDocumentSync({ client: fakeClient(sent), model: m.model, absolutePath: "/a.go", languageId: "go" });
		// insert "." after the x on line 1
		m.edit(16, 0, ".");
		const change = sent.at(-1);
		expect(change?.method).toBe("textDocument/didChange");
		expect(change?.params).toEqual({
			textDocument: { uri: "file:///a.go", version: 2 },
			contentChanges: [
				{
					range: { start: { line: 1, character: 2 }, end: { line: 1, character: 2 } },
					rangeLength: 0,
					text: ".",
				},
			],
		});
	});

	test("the version increases with every change - a server that sees them out of order drops them", () => {
		const sent: Sent[] = [];
		const m = fakeModel("a\n");
		openDocumentSync({ client: fakeClient(sent), model: m.model, absolutePath: "/a.go", languageId: "go" });
		m.edit(1, 0, "b");
		m.edit(2, 0, "c");
		const versions = sent
			.filter((s) => s.method === "textDocument/didChange")
			.map((s) => (s.params.textDocument as { version: number }).version);
		expect(versions).toEqual([2, 3]);
	});

	test("a deletion carries the range it removed", () => {
		const sent: Sent[] = [];
		const m = fakeModel("hello\nworld\n");
		openDocumentSync({ client: fakeClient(sent), model: m.model, absolutePath: "/a.go", languageId: "go" });
		m.edit(6, 5, "");
		expect((sent.at(-1)?.params.contentChanges as unknown[])[0]).toEqual({
			range: { start: { line: 1, character: 0 }, end: { line: 1, character: 5 } },
			rangeLength: 5,
			text: "",
		});
	});
});

describe("serverText - the answer both providers read", () => {
	test("it is the SAVED text before any edit", () => {
		const m = fakeModel("saved\n");
		const sync = openDocumentSync({
			client: fakeClient([]),
			model: m.model,
			absolutePath: "/a.go",
			languageId: "go",
		});
		expect(sync.serverText()).toBe("saved\n");
	});

	// 🗝 The whole reason this module exists. Before slice 6 this stayed at the
	// saved text forever, so completion completed on the file as it was opened.
	test("it follows the buffer, keystroke by keystroke", () => {
		const m = fakeModel("x\n");
		const sync = openDocumentSync({
			client: fakeClient([]),
			model: m.model,
			absolutePath: "/a.go",
			languageId: "go",
		});
		m.edit(1, 0, "y");
		expect(sync.serverText()).toBe("xy\n");
		m.edit(2, 0, "z");
		expect(sync.serverText()).toBe("xyz\n");
		expect(sync.serverText()).toBe(m.value);
	});
});

describe("drift", () => {
	test("a mirror that disagrees with the model resyncs the WHOLE document and says so", () => {
		const sent: Sent[] = [];
		const m = fakeModel("one\ntwo\n");
		const sync = openDocumentSync({
			client: fakeClient(sent),
			model: m.model,
			absolutePath: "/a.go",
			languageId: "go",
		});
		// The model moved without announcing it, so our reconstruction is now wrong.
		m.desync("one\ntwo\nthree\n");
		m.announceNothing();
		expect(warn).toHaveBeenCalledWith(expect.stringContaining("drifted"));
		expect(sync.serverText()).toBe("one\ntwo\nthree\n");
		const resync = sent.at(-1);
		expect(resync?.params).toEqual({
			textDocument: { uri: "file:///a.go", version: 3 },
			contentChanges: [
				// A whole-document replacement is a legal INCREMENTAL change, so this
				// never depends on a server accepting a full-sync payload it did not
				// advertise support for.
				{ range: { start: { line: 0, character: 0 }, end: { line: 2, character: 0 } }, text: "one\ntwo\nthree\n" },
			],
		});
	});

	test("no drift, no warning, no resync", () => {
		const sent: Sent[] = [];
		const m = fakeModel("one\n");
		openDocumentSync({ client: fakeClient(sent), model: m.model, absolutePath: "/a.go", languageId: "go" });
		m.edit(4, 0, "two\n");
		expect(warn).not.toHaveBeenCalled();
		expect(sent.filter((s) => s.method === "textDocument/didChange")).toHaveLength(1);
	});
});

describe("closing", () => {
	test("dispose closes the document and stops listening", () => {
		const sent: Sent[] = [];
		const m = fakeModel("a\n");
		const sync = openDocumentSync({
			client: fakeClient(sent),
			model: m.model,
			absolutePath: "/a.go",
			languageId: "go",
		});
		sync.dispose();
		expect(sent.at(-1)).toEqual({ method: "textDocument/didClose", params: { uri: "file:///a.go" } });
		m.edit(1, 0, "b");
		expect(sent.filter((s) => s.method === "textDocument/didChange")).toHaveLength(0);
	});

	test("disposing twice does not close twice", () => {
		const sent: Sent[] = [];
		const sync = openDocumentSync({
			client: fakeClient(sent),
			model: fakeModel("a\n").model,
			absolutePath: "/a.go",
			languageId: "go",
		});
		sync.dispose();
		sync.dispose();
		expect(sent.filter((s) => s.method === "textDocument/didClose")).toHaveLength(1);
	});
});
