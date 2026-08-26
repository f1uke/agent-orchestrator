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
export type JsonRpcMessage = Record<string, unknown>;

export type LspResultOutcome = "ok" | "empty" | "error";

export type LspTransport = {
	send(handleId: string, message: JsonRpcMessage): void;
	noteResult(handleId: string, outcome: LspResultOutcome): void;
	onMessage(cb: (event: { handleId: string; message: JsonRpcMessage }) => void): () => void;
};

export type LspClient = {
	readonly handleId: string;
	request<T>(method: string, params: unknown): Promise<T>;
	notify(method: string, params: unknown): void;
	didOpen(uri: string, languageId: string, text: string): void;
	didClose(uri: string): void;
	isOpen(uri: string): boolean;
	dispose(): void;
};

/** A result that means "the server answered, and it had nothing to say". */
function isEmptyResult(result: unknown): boolean {
	if (result === null || result === undefined) return true;
	if (Array.isArray(result)) return result.length === 0;
	return false;
}

export function createLspClient(handleId: string, transport: LspTransport): LspClient {
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
			transport.noteResult(handleId, "error");
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
