import type { monaco } from "../monaco-setup";
import type { LspClient } from "./lsp-client";

/**
 * The single owner of ONE question: what text does the language server actually
 * hold for this document right now?
 *
 * Until this slice the answer was "the last saved text", because the app sent
 * `didOpen`/`didClose` and no `didChange` — which is fine for colouring a saved
 * file and useless for completion, whose entire value is being right about the
 * half-typed word under the cursor.
 *
 * 🗝 It has to be ONE owner, not one per feature. The semantic-token provider
 * answers only while the model and the server agree, and it decides that by
 * comparing texts. The moment `didChange` exists, "the saved text" stops being
 * the server's text, and a provider still comparing against the saved text would
 * paint last-synced columns onto a differently-edited buffer — silently, because
 * every offset is still in range.
 */
export type DocumentSync = {
	/** The server's own address for this document. */
	readonly uri: string;
	/** Exactly the text the server has been sent, reconstructed from what we sent it. */
	serverText(): string;
	/**
	 * The `version` the server was last told, so an UNSOLICITED answer about this
	 * document can say which revision it is about.
	 *
	 * 🗝 Only gopls fills it in. Measured on the real iOS app: every
	 * `publishDiagnostics` from sourcekit-lsp carries `version: undefined`, on
	 * open and on change alike - so a reader of this number must treat "absent"
	 * as the ordinary case and never as a reason to withhold the answer.
	 */
	version(): number;
	dispose(): void;
};

/** Both servers this app runs advertise `textDocumentSync.change: 2` — incremental. */
function positionOf(text: string, offset: number): { line: number; character: number } {
	let line = 0;
	let lineStart = 0;
	for (let i = 0; i < offset; i++) {
		if (text.charCodeAt(i) === 10) {
			line++;
			lineStart = i + 1;
		}
	}
	return { line, character: offset - lineStart };
}

function endOf(text: string): { line: number; character: number } {
	return positionOf(text, text.length);
}

export function openDocumentSync(input: {
	client: LspClient;
	model: monaco.editor.ITextModel;
	absolutePath: string;
	languageId: string;
}): DocumentSync {
	const { client, model, absolutePath, languageId } = input;
	// 🗝 The server's OWN address for this file, which on Swift is not its real
	// path: sourcekit-lsp answers a document outside its (shadow) root with
	// silence rather than with an error.
	const uri = client.documentUri(absolutePath);
	let mirror = model.getValue();
	let version = 1;
	client.didOpen(uri, languageId, mirror);

	const apply = (changes: readonly monaco.editor.IModelContentChange[]): void => {
		let next = mirror;
		const wire = changes.map((change) => {
			const start = positionOf(next, change.rangeOffset);
			const end = positionOf(next, change.rangeOffset + change.rangeLength);
			next = next.slice(0, change.rangeOffset) + change.text + next.slice(change.rangeOffset + change.rangeLength);
			return { range: { start, end }, rangeLength: change.rangeLength, text: change.text };
		});
		mirror = next;
		client.notify("textDocument/didChange", {
			textDocument: { uri, version: ++version },
			contentChanges: wire,
		});
	};

	const resync = (): void => {
		const whole = { start: { line: 0, character: 0 }, end: endOf(mirror) };
		mirror = model.getValue();
		// A whole-document replacement is a legal INCREMENTAL change, so this
		// self-heal does not depend on a server that never advertised full sync
		// accepting one anyway.
		client.notify("textDocument/didChange", {
			textDocument: { uri, version: ++version },
			contentChanges: [{ range: whole, text: mirror }],
		});
	};

	const listener = model.onDidChangeContent((event) => {
		apply(event.changes);
		// The mirror exists to be CHECKED, not assumed. If our reconstruction ever
		// disagrees with the model, every subsequent completion and every semantic
		// token would be computed against text nobody has — the exact failure this
		// stack keeps producing, with no error anywhere. Costs one string compare
		// per keystroke and turns a permanent silent corruption into one resync.
		if (mirror !== model.getValue()) {
			console.warn(`[lsp] ${languageId} document mirror drifted at ${absolutePath}; resyncing the whole document`);
			resync();
		}
	});

	let disposed = false;
	return {
		uri,
		serverText: () => mirror,
		version: () => version,
		dispose() {
			if (disposed) return;
			disposed = true;
			listener.dispose();
			client.didClose(uri);
		},
	};
}
