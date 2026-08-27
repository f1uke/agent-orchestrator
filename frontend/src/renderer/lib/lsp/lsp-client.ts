/**
 * A JSON-RPC client for one attached language server, over the main-process IPC
 * channel.
 *
 * Hand-rolled, per slice 1's evaluation of monaco-editor 0.56's own
 * `monaco.lsp` with its source in hand: that class hardcodes `rootUri: null`,
 * cannot be disposed, opens every renderer model to every server, and has no
 * `workspace/symbol` feature. This slice needs all four of those to be
 * different, and only the first is patchable from outside.
 *
 * The handshake is NOT here. Main sends `initialize` and withholds readiness
 * until the workspace has settled, so by the time a client exists the server is
 * already usable.
 */
import { type DocumentMapping, documentUriForPath } from "./lsp-uri";

export type JsonRpcMessage = Record<string, unknown>;

export type LspResultOutcome = "ok" | "empty" | "error" | "cancelled";

/**
 * The server's own token vocabulary, from its `initialize` reply. Every index in
 * a `textDocument/semanticTokens` answer is an offset into these lists, so it
 * travels with the client rather than being assumed: sourcekit-lsp publishes 28
 * types and 21 modifiers, gopls 14 and 15, and neither is a prefix of the other.
 */
export type SemanticTokensLegend = { tokenTypes: string[]; tokenModifiers: string[] };

/**
 * What the server said it can do about completion. Travels with the client for
 * the same reason the legend does - Monaco reads `triggerCharacters` once, at
 * registration, and the two servers do not agree on them.
 */
export type CompletionCapability = { triggerCharacters?: string[]; resolveProvider?: boolean };

export type LspTransport = {
	send(handleId: string, message: JsonRpcMessage): void;
	noteResult(handleId: string, outcome: LspResultOutcome): void;
	onMessage(cb: (event: { handleId: string; message: JsonRpcMessage }) => void): () => void;
};

export type LspClient = {
	readonly handleId: string;
	/**
	 * The URI to address a workspace file by. NOT `fileUriForPath` - see
	 * `documentUriForPath`, whose whole reason to exist is that getting this wrong
	 * on Swift kills ⌘click silently while symbol search carries on working.
	 */
	documentUri(absolutePath: string): string;
	/** Null when this server advertised no `semanticTokensProvider`. */
	semanticTokensLegend(): SemanticTokensLegend | null;
	/** Null when this server advertised no `completionProvider`. */
	completionCapability(): CompletionCapability | null;
	request<T>(method: string, params: unknown): Promise<T>;
	notify(method: string, params: unknown): void;
	didOpen(uri: string, languageId: string, text: string): void;
	didClose(uri: string): void;
	isOpen(uri: string): boolean;
	dispose(): void;
};

/** LSP's own code for "you asked me to stop", which is not a failure. */
const REQUEST_CANCELLED = -32800;

/** A result that means "the server answered, and it had nothing to say". */
function isEmptyResult(result: unknown): boolean {
	if (result === null || result === undefined) return true;
	if (Array.isArray(result)) return result.length === 0;
	return false;
}

export function createLspClient(
	handleId: string,
	transport: LspTransport,
	mapping: DocumentMapping & {
		semanticTokens?: SemanticTokensLegend | null;
		completion?: CompletionCapability | null;
	},
): LspClient {
	let nextId = 1;
	let disposed = false;
	const pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void; method: string }>();

	/**
	 * 🗝 PER-CLIENT, and that is the whole fix. The editor spike kept this set for
	 * the life of the app, so after an idle stop a file that had been open once
	 * never woke its language again - no intelligence, no error, nothing to see.
	 * A stopped server means a new client, and a new client starts empty, because
	 * the replacement server knows nothing about those documents anyway.
	 */
	const opened = new Set<string>();

	const unsubscribe = transport.onMessage(({ handleId: from, message }) => {
		// One IPC channel carries every server's traffic, so this filter is what
		// stops another workspace's answer resolving this client's request.
		if (from !== handleId) return;
		// 🗝 A RESPONSE is a message with an id and NO method. A server→client
		// REQUEST also carries an id, drawn from the SERVER's own id space, which
		// overlaps ours - so matching on id alone resolves one of our pending
		// requests with the server's request payload and leaves the server waiting
		// forever. Measured: gopls sent `window/workDoneProgress/create` with id 2
		// while our id 2 was in flight, and the workspace never loaded.
		if (typeof message.method === "string") return;
		const id = message.id;
		if (typeof id !== "number") return;
		const waiting = pending.get(id);
		if (!waiting) return;
		pending.delete(id);
		if (message.error) {
			const err = message.error as { message?: string; code?: number };
			// 🗝 `-32800 RequestCancelled` is a NORMAL outcome, not a fault: it is what
			// a server replies to `$/cancelRequest`. Counting it would put a number in
			// the health panel's error column that says the server is misbehaving when
			// the client is the one that changed its mind. (Measured: sourcekit-lsp
			// answers exactly this, seven times over an eight-keystroke burst, under
			// the cancel-per-keystroke policy this slice measured and rejected.)
			transport.noteResult(handleId, err.code === REQUEST_CANCELLED ? "cancelled" : "error");
			waiting.reject(new Error(err.message ?? `LSP error ${err.code ?? "?"}`));
			return;
		}
		// Reported to main so a server that is up and answering nothing shows up in
		// health rather than being indistinguishable from one that is working.
		transport.noteResult(handleId, isEmptyResult(message.result) ? "empty" : "ok");
		waiting.resolve(message.result);
	});

	return {
		handleId,
		documentUri(absolutePath) {
			return documentUriForPath(absolutePath, mapping);
		},
		semanticTokensLegend() {
			return mapping.semanticTokens ?? null;
		},
		completionCapability() {
			return mapping.completion ?? null;
		},
		request<T>(method: string, params: unknown): Promise<T> {
			if (disposed) return Promise.reject(new Error(`LSP client disposed (${method})`));
			const id = nextId++;
			return new Promise<T>((resolve, reject) => {
				pending.set(id, { resolve: resolve as (v: unknown) => void, reject, method });
				transport.send(handleId, { jsonrpc: "2.0", id, method, params });
			});
		},

		notify(method, params) {
			if (disposed) return;
			transport.send(handleId, { jsonrpc: "2.0", method, params });
		},

		didOpen(uri, languageId, text) {
			if (disposed || opened.has(uri)) return;
			opened.add(uri);
			transport.send(handleId, {
				jsonrpc: "2.0",
				method: "textDocument/didOpen",
				params: { textDocument: { uri, languageId, version: 1, text } },
			});
		},

		didClose(uri) {
			if (disposed || !opened.delete(uri)) return;
			transport.send(handleId, { jsonrpc: "2.0", method: "textDocument/didClose", params: { textDocument: { uri } } });
		},

		isOpen(uri) {
			return opened.has(uri);
		},

		dispose() {
			if (disposed) return;
			disposed = true;
			unsubscribe();
			// A promise that never settles is a silent failure in promise form: the
			// palette would sit on "searching…" for the rest of the session.
			for (const waiting of pending.values()) waiting.reject(new Error(`LSP client disposed (${waiting.method})`));
			pending.clear();
			opened.clear();
		},
	};
}
