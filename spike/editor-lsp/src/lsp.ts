// THROWAWAY SPIKE. Minimal LSP client over the bridge WebSocket.
// Deliberately NOT monaco-languageclient: see the proposal — that package pulls
// the whole @codingame/monaco-vscode-* fleet (workbench, views, extension host,
// 14 language packs). Everything the spike needs is four requests.

type Pending = { resolve: (v: any) => void; sent: number };

export class Lsp {
	private ws!: WebSocket;
	private id = 1;
	private pending = new Map<number, Pending>();
	private ready!: Promise<void>;
	readonly timings: { label: string; ms: number }[] = [];
	spawnedAt = 0;
	initializedAt = 0;

	constructor(
		private url: string,
		private rootUri: string,
		private onLog: (s: string) => void,
	) {}

	connect() {
		this.spawnedAt = performance.now();
		this.ws = new WebSocket(this.url);
		this.ready = new Promise((resolve, reject) => {
			this.ws.onopen = async () => {
				await this.request("initialize", {
					processId: null,
					rootUri: this.rootUri,
					workspaceFolders: [{ uri: this.rootUri, name: "workspace" }],
					capabilities: {
						workspace: {
							symbol: { symbolKind: { valueSet: Array.from({ length: 26 }, (_, i) => i + 1) } },
							workspaceFolders: true,
							configuration: true,
						},
						textDocument: {
							synchronization: { dynamicRegistration: false },
							definition: { linkSupport: true },
							completion: {
								completionItem: {
									snippetSupport: true,
									documentationFormat: ["markdown", "plaintext"],
									insertReplaceSupport: true,
									labelDetailsSupport: true,
								},
								contextSupport: true,
							},
							documentSymbol: { hierarchicalDocumentSymbolSupport: true },
							publishDiagnostics: {},
						},
					},
					initializationOptions: {},
				});
				this.notify("initialized", {});
				this.initializedAt = performance.now();
				this.onLog(`initialize → ${(this.initializedAt - this.spawnedAt).toFixed(0)}ms`);
				resolve();
			};
			this.ws.onerror = (e) => reject(e);
		});
		this.ws.onmessage = (ev) => {
			const msg = JSON.parse(ev.data as string);
			if (msg.id !== undefined && this.pending.has(msg.id)) {
				const p = this.pending.get(msg.id)!;
				this.pending.delete(msg.id);
				p.resolve(msg);
			} else if (msg.id !== undefined && msg.method) {
				// server → client request. Answer so the server does not stall.
				this.send({ jsonrpc: "2.0", id: msg.id, result: msg.method === "workspace/configuration" ? [{}] : null });
			}
		};
		return this.ready;
	}

	private send(o: unknown) {
		this.ws.send(JSON.stringify(o));
	}
	notify(method: string, params: unknown) {
		this.send({ jsonrpc: "2.0", method, params });
	}
	request(method: string, params: unknown): Promise<any> {
		const id = this.id++;
		const sent = performance.now();
		return new Promise((resolve) => {
			this.pending.set(id, { resolve, sent });
			this.send({ jsonrpc: "2.0", id, method, params });
		}).then((msg: any) => {
			const ms = performance.now() - sent;
			this.timings.push({ label: method, ms });
			return { ...msg, elapsedMs: ms };
		});
	}

	private versions = new Map<string, number>();
	didOpen(uri: string, languageId: string, text: string) {
		this.versions.set(uri, 1);
		this.notify("textDocument/didOpen", { textDocument: { uri, languageId, version: 1, text } });
	}
	// Completion is only useful on the text as it is RIGHT NOW, so every edit has
	// to reach the server before the request does. Full-document sync is the
	// blunt version; a real integration would send incremental ranges.
	didChange(uri: string, text: string) {
		const v = (this.versions.get(uri) ?? 1) + 1;
		this.versions.set(uri, v);
		this.notify("textDocument/didChange", { textDocument: { uri, version: v }, contentChanges: [{ text }] });
	}
	completion(uri: string, line: number, character: number, trigger?: string) {
		return this.request("textDocument/completion", {
			textDocument: { uri },
			position: { line, character },
			context: trigger ? { triggerKind: 2, triggerCharacter: trigger } : { triggerKind: 1 },
		});
	}
	definition(uri: string, line: number, character: number) {
		return this.request("textDocument/definition", { textDocument: { uri }, position: { line, character } });
	}
	workspaceSymbol(query: string) {
		return this.request("workspace/symbol", { query });
	}
	documentSymbol(uri: string) {
		return this.request("textDocument/documentSymbol", { textDocument: { uri } });
	}
}
