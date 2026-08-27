// A real stdio LSP server, for tests. Not shipped: it exists so lsp-process and
// lsp-registry can be tested against an ACTUAL child process - framing, pipes,
// exit codes and the kill path included - without needing gopls installed.
//
// Behaviour is steered by env vars so one script covers every path the
// supervisor has to survive. See lsp-process.test.ts / lsp-registry.test.ts.
let buffer = Buffer.alloc(0);
/** Flipped by `workspace/synchronize`, so symbol answers can differ across it. */
let synchronized = false;
const send = (msg) => {
	const body = Buffer.from(JSON.stringify(msg), "utf8");
	process.stdout.write(`Content-Length: ${body.length}\r\n\r\n`);
	process.stdout.write(body);
};

const parseJson = (name, fallback) => {
	try {
		return process.env[name] ? JSON.parse(process.env[name]) : fallback;
	} catch {
		return fallback;
	}
};

// A server→client request nobody answers stalls a real server silently. The
// supervisor is supposed to answer workspace/configuration itself; this exits
// non-zero if it does not, so "we answered it" is a fact and not a hope.
let configAnswered = false;
if (process.env.FAKE_LSP_ASK_CONFIG === "1") {
	setTimeout(() => {
		send({ jsonrpc: "2.0", id: 9001, method: "workspace/configuration", params: { items: [{ section: "go" }] } });
		setTimeout(() => {
			if (!configAnswered) process.exit(3);
		}, 500);
	}, 10);
}

// The same probe for a request the client does NOT implement. A real client must
// still ANSWER it (with an error); leaving it hanging stalls a real server.
let unsupportedAnswered = false;
if (process.env.FAKE_LSP_ASK_UNSUPPORTED === "1") {
	setTimeout(() => {
		send({ jsonrpc: "2.0", id: 9002, method: "window/showMessageRequest", params: { type: 3, message: "hi" } });
		setTimeout(() => {
			if (!unsupportedAnswered) process.exit(4);
		}, 500);
	}, 10);
}

process.stdin.on("data", (chunk) => {
	buffer = Buffer.concat([buffer, chunk]);
	for (;;) {
		const sep = buffer.indexOf("\r\n\r\n");
		if (sep < 0) return;
		const len = Number(/content-length:\s*(\d+)/i.exec(buffer.subarray(0, sep).toString("ascii"))?.[1] ?? -1);
		if (len < 0 || buffer.length < sep + 4 + len) return;
		const msg = JSON.parse(buffer.subarray(sep + 4, sep + 4 + len).toString("utf8"));
		buffer = buffer.subarray(sep + 4 + len);
		handle(msg);
	}
});

function handle(msg) {
	if (msg.id === 9001) {
		configAnswered = true;
		return;
	}
	if (msg.id === 9002) {
		unsupportedAnswered = true;
		return;
	}
	switch (msg.method) {
		case "initialize":
			if (process.env.FAKE_LSP_HANG_INITIALIZE === "1") return;
			send({
				jsonrpc: "2.0",
				id: msg.id,
				result: {
					capabilities: {
						definitionProvider: true,
						workspaceSymbolProvider: true,
						textDocumentSync: 1,
						// A legend is only usable as the exact list the server sent, in
						// order, so the supervisor has to carry it through to the renderer
						// untouched. FAKE_LSP_NO_SEMANTIC_TOKENS covers the other case: a
						// server that advertises none at all.
						// The two real servers disagree here and BOTH halves matter:
						// gopls advertises `{triggerCharacters: ["."]}` and no
						// resolveProvider, sourcekit-lsp `{resolveProvider: true,
						// triggerCharacters: [".", "("]}`. Monaco reads the trigger
						// characters once, at registration, so they cannot be
						// discovered later. FAKE_LSP_NO_COMPLETION covers the third
						// case: a server that offers completion at all.
						...(process.env.FAKE_LSP_NO_COMPLETION === "1"
							? {}
							: {
									completionProvider: {
										resolveProvider: process.env.FAKE_LSP_NO_RESOLVE !== "1",
										triggerCharacters: [".", "("],
									},
								}),
						// Both are `boolean | { workDoneProgress }` on the wire. The object
						// form is exercised deliberately: a client that tested `=== true`
						// would decide a server offering hover offers none, and then report
						// that to the reader as a fact.
						...(process.env.FAKE_LSP_NO_HOVER === "1" ? {} : { hoverProvider: {} }),
						...(process.env.FAKE_LSP_NO_REFERENCES === "1" ? {} : { referencesProvider: true }),
						...(process.env.FAKE_LSP_NO_SEMANTIC_TOKENS === "1"
							? {}
							: {
									semanticTokensProvider: {
										full: true,
										range: true,
										legend: {
											tokenTypes: ["property", "identifier"],
											tokenModifiers: ["declaration", "defaultLibrary"],
										},
									},
								}),
					},
					// The rootUri is echoed back so a test can assert main sent a REAL
					// root rather than the null monaco.lsp hardcodes.
					...(process.env.FAKE_LSP_NO_SERVER_INFO === "1"
						? {}
						: { serverInfo: { name: "fake", version: String(msg.params?.rootUri ?? "") } }),
				},
			});
			return;
		case "initialized":
			if (process.env.FAKE_LSP_PROGRESS === "1") {
				send({ jsonrpc: "2.0", id: 9100, method: "window/workDoneProgress/create", params: { token: "load" } });
				send({
					jsonrpc: "2.0",
					method: "$/progress",
					params: { token: "load", value: { kind: "begin", title: "Loading packages" } },
				});
				setTimeout(
					() => send({ jsonrpc: "2.0", method: "$/progress", params: { token: "load", value: { kind: "end" } } }),
					150,
				);
			}
			return;
		case "textDocument/definition":
			send({ jsonrpc: "2.0", id: msg.id, result: parseJson("FAKE_LSP_DEFINITION", null) });
			return;
		case "workspace/symbol":
			// 🗝 Different answers before and after the index gate, so a test can
			// prove the gate MATTERS rather than merely that it exists. This is not
			// invention: measured on sourcekit-lsp against a real iOS app,
			// `workspace/symbol` returned one or two hits for the first 3.6 s and
			// NONE of them was the right one.
			send({
				jsonrpc: "2.0",
				id: msg.id,
				result: synchronized
					? parseJson("FAKE_LSP_SYMBOLS", [])
					: parseJson("FAKE_LSP_SYMBOLS_BEFORE_SYNC", parseJson("FAKE_LSP_SYMBOLS", [])),
			});
			return;
		case "workspace/synchronize":
			// A server that predates the request answers -32601. It is still usable;
			// it just cannot be asked whether its index is loaded.
			if (process.env.FAKE_LSP_SYNCHRONIZE_UNSUPPORTED === "1") {
				send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "method not found" } });
				return;
			}
			if (process.env.FAKE_LSP_SYNCHRONIZE_HANGS === "1") return;
			setTimeout(
				() => {
					synchronized = true;
					send({ jsonrpc: "2.0", id: msg.id, result: {} });
				},
				Number(process.env.FAKE_LSP_SYNCHRONIZE_MS ?? 0),
			);
			return;
		case "shutdown":
			send({ jsonrpc: "2.0", id: msg.id, result: null });
			return;
		case "exit":
			if (process.env.FAKE_LSP_IGNORE_SHUTDOWN === "1") return; // provoke the SIGKILL backstop
			process.exit(0);
			return;
		default:
			if (msg.id !== undefined) send({ jsonrpc: "2.0", id: msg.id, result: null });
	}
}
