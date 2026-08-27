import { execFile, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { pathToFileURL } from "node:url";
import type { LanguageServerSpec } from "./language-servers";
import { createFrameDecoder, encodeMessage, type JsonRpcMessage } from "./lsp-framing";

/**
 * One supervised language server.
 *
 * 🗝 MAIN OWNS THE HANDSHAKE, not the renderer. Main is the process that knows
 * the workspace root, so it is the only one that can send a real `rootUri` - and
 * it is the only one that can kill a child that never answers. `initialized`
 * therefore resolves when the server is genuinely usable, and a hung handshake
 * becomes a visible `failed` rather than a renderer spinner.
 *
 * That is also the structural reason this app hand-rolls rather than taking
 * monaco-editor's own `monaco.lsp`: that client hardcodes `rootUri: null`, never
 * answers `workspace/workspaceFolders`, and has no way to be shut down.
 */
export type LspState = "starting" | "initializing" | "indexing" | "ready" | "failed" | "stopped";

export type LspProcessOptions = {
	spec: LanguageServerSpec;
	/** The session's worktree. Reported in health; NOT necessarily the LSP root. */
	root: string;
	/**
	 * Where the server is rooted: `cwd` and `rootUri`. Differs from `root` only
	 * for Swift, whose Xcode workspaces are served from a shadow root under the
	 * AO data dir so that nothing is ever written into the user's checkout.
	 */
	lspRoot?: string;
	dataDir: string;
	env: NodeJS.ProcessEnv;
	initializeTimeoutMs?: number;
	killGraceMs?: number;
	readinessSettleMs?: number;
	indexTimeoutMs?: number;
	/** What `prepare` learned about this workspace, before the server says anything. */
	initialDetail?: string;
	onState: (state: LspState, detail?: string) => void;
	/** Server→client traffic main does NOT answer itself. */
	onMessage: (message: JsonRpcMessage) => void;
};

/**
 * The token vocabulary a server paints with, straight out of its `initialize`
 * reply. Carried rather than assumed: the indices in every
 * `textDocument/semanticTokens` answer mean nothing without it, and the two
 * servers this app runs publish legends of different shapes (sourcekit-lsp 28
 * types / 21 modifiers, gopls 14 / 15).
 */
export type SemanticTokensLegend = { tokenTypes: string[]; tokenModifiers: string[] };

/**
 * What the server said it can do about completion, from its `initialize` reply.
 *
 * Carried rather than assumed for the same reason the legend is: the two servers
 * this app runs answer differently, and Monaco reads `triggerCharacters` ONCE at
 * registration - so a renderer that guesses `["."]` gets no argument-label
 * completion on Swift, and one that assumes `resolveProvider` fetches nothing on
 * Go while quietly swallowing a `MethodNotFound` per highlighted row.
 */
export type CompletionCapability = { triggerCharacters?: string[]; resolveProvider?: boolean };

/**
 * The plain yes/no capabilities, carried for one reason: so a feature that
 * cannot answer can say WHICH of the two silences it is.
 *
 * "The server is still starting" and "this server does not do hover" produce the
 * same empty widget, and only the `initialize` reply can tell them apart. Both
 * of these are `boolean | { workDoneProgress }` on the wire, so presence is the
 * signal and the object form is not a promise of anything extra.
 */
export type ServerFeatures = { hover: boolean; references: boolean };

export type LspProcess = {
	readonly pid: number | null;
	readonly state: LspState;
	readonly startedAt: number;
	readonly detail: string | undefined;
	/** Null when the server advertised no `semanticTokensProvider`. */
	readonly semanticTokensLegend: SemanticTokensLegend | null;
	/** What the server said it can do about hover and references. */
	readonly features: ServerFeatures;
	/** Null when the server advertised no `completionProvider`. */
	readonly completionCapability: CompletionCapability | null;
	/** Resolves when the handshake completes; rejects on timeout or spawn failure. */
	readonly initialized: Promise<void>;
	/** Client→server. Ids belong to the renderer; this only frames and writes. */
	send(message: JsonRpcMessage): void;
	stop(why: string): Promise<void>;
	/** RSS in MB for the child, or null if it is gone. */
	rss(): Promise<number | null>;
};

/** Ids main uses for its own traffic, kept far from the renderer's counter. */
const INITIALIZE_ID = -1;
const SHUTDOWN_ID = -2;
const SYNCHRONIZE_ID = -3;

const DEFAULT_INITIALIZE_TIMEOUT_MS = 20_000;
const DEFAULT_KILL_GRACE_MS = 3_000;

/**
 * 🗝 How long to wait after `initialize` before believing "nothing is indexing".
 *
 * `initialize` returning does NOT mean the workspace is loaded - gopls answers
 * it in ~40 ms and only then begins the multi-second package load it announces
 * with `$/progress`. Going straight to `ready` on the initialize response
 * therefore reports readiness during exactly the window where
 * `workspace/symbol` answers WRONG rather than empty, which is the measured
 * failure this slice is built to prevent (spike §3.3: wrong hits at 5 s,
 * settled by ~15 s on a large project).
 *
 * So `ready` is withheld for this long, and any progress that begins inside the
 * window flips the state to `indexing` instead. A server that genuinely has
 * nothing to load pays this once, on attach, and never again.
 */
const DEFAULT_READINESS_SETTLE_MS = 400;

/**
 * How long to wait for `workspace/synchronize` before giving up on it.
 *
 * It blocked for 6.15 s on an 8 964-source iOS app with a warm 212 MB index
 * store, so this is ~30x that - generous, because the alternative to waiting is
 * showing results that are measurably wrong. If it does expire the state moves
 * to `ready` with the reason attached rather than sitting in `indexing` for the
 * rest of the session, since a permanently-loading palette is its own silent
 * failure.
 */
const DEFAULT_INDEX_TIMEOUT_MS = 180_000;

export type PsRow = { pid: number; ppid: number; rssMb: number; command: string };

/** `ps -axo pid=,ppid=,rss=,comm=`, one row per line. Exported so it can be tested. */
export function parsePsTable(stdout: string): PsRow[] {
	const rows: PsRow[] = [];
	for (const line of String(stdout ?? "").split("\n")) {
		const match = /^\s*(\d+)\s+(\d+)\s+(\d+)\s+(.*\S)\s*$/.exec(line);
		if (!match) continue;
		rows.push({
			pid: Number(match[1]),
			ppid: Number(match[2]),
			rssMb: Math.round(Number(match[3]) / 1024),
			command: match[4],
		});
	}
	return rows;
}

/**
 * RSS for a process and everything descended from it, or null when the process
 * is gone. The children matter: a Swift server's build server is a real 19 MB
 * Python process that only exists because we started it.
 */
export function treeRss(rows: readonly PsRow[], pid: number): number | null {
	const byParent = new Map<number, PsRow[]>();
	let root: PsRow | undefined;
	for (const row of rows) {
		if (row.pid === pid) root = row;
		const siblings = byParent.get(row.ppid);
		if (siblings) siblings.push(row);
		else byParent.set(row.ppid, [row]);
	}
	if (!root) return null;
	let total = 0;
	const queue = [root];
	const seen = new Set<number>();
	while (queue.length > 0) {
		const row = queue.pop() as PsRow;
		// A cycle is impossible in a real process table, but `ps` output is parsed
		// text and a self-parented row would otherwise hang the app.
		if (seen.has(row.pid)) continue;
		seen.add(row.pid);
		total += row.rssMb;
		for (const child of byParent.get(row.pid) ?? []) queue.push(child);
	}
	return total;
}

const basename = (command: string) => command.slice(command.lastIndexOf("/") + 1);

/**
 * Sidecar pids already spoken for by another live server.
 *
 * 🗝 An XPC service cannot be attributed by anything the OS exposes: measured
 * with two sourcekit-lsp servers running, each `SourceKitService` carried
 * `ppid=1`, its own process group and session 0. What IS true is that they
 * appear one per client and are reaped with their client, so they are claimed by
 * pid diffing across the spawn - and this set is what stops two servers claiming
 * the same one and double-counting it.
 */
const claimedSidecars = new Set<number>();

/**
 * The client capabilities are a property of what THIS APP implements, not of any
 * session, so they live here beside the catalogue rather than in the renderer.
 */
function clientCapabilities() {
	return {
		workspace: {
			workspaceFolders: true,
			configuration: true,
			symbol: { symbolKind: { valueSet: Array.from({ length: 26 }, (_, i) => i + 1) } },
		},
		textDocument: {
			synchronization: { dynamicRegistration: false },
			definition: { linkSupport: true },
			/**
			 * Declared in full because a server is entitled to withhold what was
			 * never asked for, and every one of these changes what comes back.
			 * Measured against both servers: `snippetSupport` is what turns
			 * `configure(userDefaultManager:)` into a real snippet rather than a
			 * bare name, `insertReplaceSupport` is why gopls answers with an
			 * `InsertReplaceEdit`, `labelDetailsSupport` is what carries Swift's
			 * signatures beside the label, and `resolveSupport` is what makes
			 * sourcekit-lsp hold documentation back until a row is highlighted
			 * instead of sending 200 doc comments to show one.
			 */
			completion: {
				dynamicRegistration: false,
				contextSupport: true,
				completionItem: {
					snippetSupport: true,
					insertReplaceSupport: true,
					labelDetailsSupport: true,
					deprecatedSupport: true,
					commitCharactersSupport: true,
					documentationFormat: ["markdown", "plaintext"],
					resolveSupport: { properties: ["documentation", "detail", "additionalTextEdits"] },
				},
				completionItemKind: { valueSet: Array.from({ length: 25 }, (_, i) => i + 1) },
			},
			documentSymbol: { hierarchicalDocumentSymbolSupport: true },
			publishDiagnostics: {},
			/**
			 * Declared even though sourcekit-lsp advertises its provider regardless:
			 * the spec makes the legend a negotiation, and a server is entitled to
			 * withhold the feature from a client that never asked. Empty type and
			 * modifier lists mean "whatever you have" - the renderer maps by NAME
			 * from the legend that comes back, so it does not need to pick.
			 */
			semanticTokens: {
				dynamicRegistration: false,
				requests: { full: true, range: false },
				tokenTypes: [],
				tokenModifiers: [],
				formats: ["relative"],
				overlappingTokenSupport: false,
				multilineTokenSupport: false,
			},
		},
		window: { workDoneProgress: true },
	};
}

export function startLspProcess(options: LspProcessOptions): LspProcess {
	const { spec, root, dataDir, env, onState, onMessage } = options;
	const lspRoot = options.lspRoot ?? root;
	const initializeTimeoutMs = options.initializeTimeoutMs ?? DEFAULT_INITIALIZE_TIMEOUT_MS;
	const killGraceMs = options.killGraceMs ?? DEFAULT_KILL_GRACE_MS;
	const readinessSettleMs = options.readinessSettleMs ?? DEFAULT_READINESS_SETTLE_MS;
	const indexTimeoutMs = options.indexTimeoutMs ?? DEFAULT_INDEX_TIMEOUT_MS;

	let state: LspState = "starting";
	let detail: string | undefined = options.initialDetail;
	const startedAt = Date.now();
	// Work-done progress tokens the server opens while it loads packages. While
	// any is outstanding the index is not settled and workspace/symbol answers
	// WRONG rather than empty, which is why readiness is tracked and not guessed.
	const progress = new Set<string>();
	let child: ChildProcessWithoutNullStreams | null = null;
	let stopping: Promise<void> | null = null;
	let semanticTokensLegend: SemanticTokensLegend | null = null;
	let completionCapability: CompletionCapability | null = null;
	let features: ServerFeatures = { hover: false, references: false };

	const setState = (next: LspState, why?: string) => {
		state = next;
		detail = why;
		onState(next, why);
	};

	let resolveInit!: () => void;
	let rejectInit!: (err: Error) => void;
	let settledInit = false;
	const initialized = new Promise<void>((resolve, reject) => {
		resolveInit = () => {
			if (settledInit) return;
			settledInit = true;
			resolve();
		};
		rejectInit = (err) => {
			if (settledInit) return;
			settledInit = true;
			reject(err);
		};
	});
	// A rejection nobody has attached to yet would otherwise surface as an
	// unhandled rejection and take the process down.
	initialized.catch(() => {});

	const rootUri = pathToFileURL(lspRoot).href;

	const psTable = (): Promise<PsRow[]> =>
		new Promise((resolve) =>
			execFile("ps", ["-axo", "pid=,ppid=,rss=,comm="], { maxBuffer: 8 << 20 }, (err, stdout) =>
				resolve(err ? [] : parsePsTable(stdout)),
			),
		);

	// Started BEFORE the spawn so anything matching that already existed - another
	// AO window's server, or Xcode's own - is never mistaken for ours.
	const preexistingSidecars: Promise<Set<number>> | null = spec.sidecarCommand
		? psTable().then(
				(rows) => new Set(rows.filter((r) => basename(r.command) === spec.sidecarCommand).map((r) => r.pid)),
			)
		: null;
	let sidecarPid: number | null = null;

	const releaseSidecar = () => {
		if (sidecarPid !== null) claimedSidecars.delete(sidecarPid);
		sidecarPid = null;
	};

	const sidecarRss = async (rows: readonly PsRow[]): Promise<number> => {
		const name = spec.sidecarCommand;
		if (!name || !preexistingSidecars) return 0;
		const matching = rows.filter((r) => basename(r.command) === name);
		if (sidecarPid !== null) {
			const still = matching.find((r) => r.pid === sidecarPid);
			if (still) return still.rssMb;
			// It went and a replacement may have taken its place; re-claim below.
			releaseSidecar();
		}
		const before = await preexistingSidecars;
		const candidate = matching
			.filter((r) => !before.has(r.pid) && !claimedSidecars.has(r.pid))
			.sort((a, b) => a.pid - b.pid)[0];
		if (!candidate) return 0;
		sidecarPid = candidate.pid;
		claimedSidecars.add(candidate.pid);
		return candidate.rssMb;
	};

	const fail = (why: string) => {
		if (state === "stopped") return;
		setState("failed", why);
		rejectInit(new Error(why));
	};

	const send = (message: JsonRpcMessage) => {
		if (!child?.stdin.writable) return;
		try {
			child.stdin.write(encodeMessage(message));
		} catch {
			// The pipe went away; the `exit` handler reports it.
		}
	};

	// Held open by the readiness settle window above; cleared the moment any
	// progress begins, because that answers the question the window was asking.
	let settleTimer: ReturnType<typeof setTimeout> | null = null;

	/**
	 * 🗝 The Swift half of readiness, and the reason this is not one timer.
	 *
	 * A progress-driven gate cannot work on a server that emits no progress, and
	 * sourcekit-lsp emits none - not one `$/progress`, not one
	 * `window/workDoneProgress/create`, in 45 s of listening on a real iOS app.
	 * So it is ASKED instead, with `workspace/synchronize { index: true }`, which
	 * blocks until the index store is loaded.
	 *
	 * Two things it deliberately does NOT do:
	 *
	 * - It does not hold `initialized`. ⌘click needs compile arguments, not an
	 *   index, so the pane gets its client immediately and can open its document
	 *   while the index loads. Blocking here would cost every Swift file six
	 *   seconds of dead editor for a gate that only symbol search needs.
	 * - It does not report `ready` early on failure. A server whose synchronize
	 *   is not implemented (older sourcekit-lsp: `-32601`) is genuinely usable,
	 *   so it becomes ready with the degradation SPELLED OUT in `detail`, which
	 *   is the difference between a known limitation and a silent one.
	 */
	let indexTimer: ReturnType<typeof setTimeout> | null = null;
	let synchronizing = false;

	const finishIndex = (why?: string) => {
		if (indexTimer) {
			clearTimeout(indexTimer);
			indexTimer = null;
		}
		if (!synchronizing || state === "failed" || state === "stopped") return;
		synchronizing = false;
		setState("ready", why ?? detail);
	};

	const beginIndex = () => {
		synchronizing = true;
		setState("indexing", detail);
		// Resolved HERE, not on `ready`: the server is usable for everything that
		// does not need the index, and the renderer is what decides which of its
		// features to gate.
		resolveInit();
		send({ jsonrpc: "2.0", id: SYNCHRONIZE_ID, method: "workspace/synchronize", params: { index: true } });
		indexTimer = setTimeout(() => {
			indexTimer = null;
			finishIndex(`${spec.command}: the index was still loading after ${Math.round(indexTimeoutMs / 1000)}s`);
		}, indexTimeoutMs);
		indexTimer.unref?.();
	};

	const settle = () => {
		if (state === "failed" || state === "stopped" || synchronizing) return;
		if (progress.size > 0) {
			if (settleTimer) {
				clearTimeout(settleTimer);
				settleTimer = null;
			}
			setState("indexing", detail);
			return;
		}
		// No work outstanding. Believe that only after the settle window, so a load
		// that has not announced itself yet is not reported as readiness.
		if (settleTimer) return;
		settleTimer = setTimeout(() => {
			settleTimer = null;
			if (state === "failed" || state === "stopped" || progress.size > 0) return;
			if (spec.indexReadiness === "synchronize") {
				beginIndex();
				return;
			}
			setState("ready", detail);
			resolveInit();
		}, readinessSettleMs);
		settleTimer.unref?.();
	};

	const handle = (message: JsonRpcMessage) => {
		const method = message.method as string | undefined;

		if (message.id === INITIALIZE_ID) {
			clearTimeout(initTimer);
			if (message.error) {
				fail(`initialize failed: ${JSON.stringify(message.error)}`);
				return;
			}
			const reply = message.result as {
				serverInfo?: { name?: string; version?: string };
				capabilities?: {
					semanticTokensProvider?: { legend?: SemanticTokensLegend };
					completionProvider?: CompletionCapability;
					hoverProvider?: boolean | Record<string, unknown>;
					referencesProvider?: boolean | Record<string, unknown>;
				};
			} | null;
			const info = reply?.serverInfo;
			// Kept whole. A legend is only meaningful as the exact list the server
			// sent, in the exact order: the indices in its answers are offsets into
			// it, so a reconstructed or reordered one silently mislabels every token.
			// 🗝 The two servers disagree, and both halves matter to the renderer.
			// gopls: `{ triggerCharacters: ["."] }` and NO resolveProvider - it ships
			// detail and documentation inline on every item. sourcekit-lsp:
			// `{ resolveProvider: true, triggerCharacters: [".", "("] }`. Monaco reads
			// `triggerCharacters` once, at registration, so it has to travel.
			const advertised = reply?.capabilities?.completionProvider;
			completionCapability = advertised
				? {
						triggerCharacters: Array.isArray(advertised.triggerCharacters)
							? [...advertised.triggerCharacters]
							: undefined,
						resolveProvider: advertised.resolveProvider === true,
					}
				: null;
			// `false`, `undefined` and a missing key all mean "no". Anything else —
			// `true`, or the `{ workDoneProgress }` object form both servers are
			// entitled to send — means yes. Measured: gopls and sourcekit-lsp both
			// answer plain `true` for these two.
			const advertises = (value: boolean | Record<string, unknown> | undefined) =>
				value === true || (typeof value === "object" && value !== null);
			features = {
				hover: advertises(reply?.capabilities?.hoverProvider),
				references: advertises(reply?.capabilities?.referencesProvider),
			};
			const legend = reply?.capabilities?.semanticTokensProvider?.legend;
			semanticTokensLegend =
				legend && Array.isArray(legend.tokenTypes) && Array.isArray(legend.tokenModifiers)
					? { tokenTypes: [...legend.tokenTypes], tokenModifiers: [...legend.tokenModifiers] }
					: null;
			// Carried as `detail` so a test - and a human reading the health panel -
			// can see the root the server was actually given.
			// sourcekit-lsp sends NO serverInfo, so a blind assignment here would erase
			// what `prepare` found out about the workspace - which is the only thing
			// the status pill has to show on the Swift path.
			if (info) detail = `${info.name ?? spec.command} ${info.version ?? ""}`.trim();
			send({ jsonrpc: "2.0", method: "initialized", params: {} });
			settle();
			return;
		}

		if (message.id === SYNCHRONIZE_ID) {
			// `-32601` is the old spelling of this request having been renamed
			// (`workspace/_pollIndex` in earlier sourcekit-lsp). The server still
			// works; it just cannot be asked, so say that rather than imply the
			// index is loaded.
			finishIndex(
				message.error
					? `${detail ?? spec.command} - index readiness is unknown on this toolchain, so symbol results may be incomplete`
					: detail,
			);
			return;
		}

		// Server→client REQUESTS that belong to the connection rather than to any
		// view. An unanswered server request stalls the server silently, which is
		// this stack's characteristic failure - so answer them here, in the only
		// process guaranteed to still be around.
		if (message.id !== undefined && method) {
			switch (method) {
				case "workspace/configuration": {
					const items = (message.params as { items?: unknown[] } | undefined)?.items ?? [];
					send({ jsonrpc: "2.0", id: message.id as number, result: items.map(() => ({})) });
					return;
				}
				case "window/workDoneProgress/create":
				case "client/registerCapability":
				case "client/unregisterCapability":
					send({ jsonrpc: "2.0", id: message.id as number, result: null });
					return;
				case "workspace/workspaceFolders":
					send({ jsonrpc: "2.0", id: message.id as number, result: [{ uri: rootUri, name: "workspace" }] });
					return;
				default:
					// 🗝 EVERY server→client request gets an answer, including ones we do
					// not implement. A request left hanging stalls the server silently -
					// measured on gopls, whose `window/workDoneProgress/create` going
					// unanswered left the workspace unloaded at 24 MB indefinitely, with
					// no error anywhere. MethodNotFound is a real answer; silence is not.
					send({
						jsonrpc: "2.0",
						id: message.id as number,
						error: { code: -32601, message: `client does not implement ${method}` },
					});
					return;
			}
		}

		if (method === "$/progress") {
			const params = message.params as { token?: string | number; value?: { kind?: string } } | undefined;
			const token = String(params?.token ?? "");
			const kind = params?.value?.kind;
			if (kind === "begin") progress.add(token);
			else if (kind === "end") progress.delete(token);
			if (kind === "begin" || kind === "end") settle();
		}

		onMessage(message);
	};

	async function stop(why: string): Promise<void> {
		if (stopping) return stopping;
		clearTimeout(initTimer);
		if (settleTimer) {
			clearTimeout(settleTimer);
			settleTimer = null;
		}
		if (indexTimer) {
			clearTimeout(indexTimer);
			indexTimer = null;
		}
		synchronizing = false;
		releaseSidecar();
		const proc = child;
		child = null;
		if (!proc || proc.exitCode !== null) {
			setState("stopped", why);
			return;
		}
		stopping = new Promise<void>((resolve) => {
			const done = () => {
				clearTimeout(hard);
				setState("stopped", why);
				resolve();
			};
			// LSP's teardown is two steps. SIGTERM alone leaves gopls's cache
			// half-written, so ask politely first and insist only if it does not go.
			try {
				proc.stdin.write(encodeMessage({ jsonrpc: "2.0", id: SHUTDOWN_ID, method: "shutdown", params: null }));
				proc.stdin.write(encodeMessage({ jsonrpc: "2.0", method: "exit" }));
			} catch {
				// pipe already gone
			}
			const hard = setTimeout(() => {
				try {
					proc.kill("SIGKILL");
				} catch {
					// already gone
				}
			}, killGraceMs);
			proc.once("exit", done);
			proc.once("error", done);
		});
		return stopping;
	}

	try {
		child = spawn(spec.command, spec.args({ dataDir }), {
			cwd: lspRoot,
			env: { ...env, ...spec.env({ dataDir, env }) },
			stdio: ["pipe", "pipe", "pipe"],
		});
	} catch (err) {
		fail(`${spec.command}: ${err instanceof Error ? err.message : String(err)}`);
	}

	// A hung handshake is indistinguishable from a slow one from the renderer's
	// side, so the deadline lives here, where the child can actually be killed.
	const initTimer = setTimeout(() => {
		fail(`${spec.command} timed out after ${initializeTimeoutMs}ms waiting for initialize`);
		void stop("initialize timeout");
	}, initializeTimeoutMs);

	if (child) {
		const decoder = createFrameDecoder();
		child.stdout.on("data", (chunk: Buffer) => {
			for (const message of decoder.push(chunk)) handle(message);
		});
		// A language server talks on stderr when it is unhappy, and that text is
		// often the only diagnosis anyone will ever get for a misconfigured server.
		child.stderr.on("data", (d: Buffer) => console.warn(`[lsp:${spec.languageId}] ${d.toString().trimEnd()}`));
		child.once("error", (err) => {
			clearTimeout(initTimer);
			// ENOENT lands here, and it is the PATH trap: gopls at ~/go/bin is
			// invisible to Electron's inherited PATH. NAME the command, always.
			fail(`${spec.command}: ${err.message}`);
		});
		child.once("exit", (code, signal) => {
			clearTimeout(initTimer);
			if (state === "stopped" || stopping) return;
			fail(`${spec.command} exited (${signal ?? code})`);
		});
		// Carries `detail` rather than clearing it: what `prepare` learned about the
		// workspace is the only thing the Swift status pill has, and sourcekit-lsp
		// never sends a serverInfo to replace it.
		setState("initializing", detail);
		send({
			jsonrpc: "2.0",
			id: INITIALIZE_ID,
			method: "initialize",
			params: {
				processId: process.pid,
				// The two fields monaco.lsp gets wrong, and the reason we hand-roll.
				rootUri,
				workspaceFolders: [{ uri: rootUri, name: lspRoot.slice(lspRoot.lastIndexOf("/") + 1) }],
				capabilities: clientCapabilities(),
				initializationOptions: {},
			},
		});
	}

	return {
		get pid() {
			return child?.pid ?? null;
		},
		get state() {
			return state;
		},
		get detail() {
			return detail;
		},
		get semanticTokensLegend() {
			return semanticTokensLegend;
		},
		get features() {
			return features;
		},
		get completionCapability() {
			return completionCapability;
		},
		startedAt,
		initialized,
		send,
		stop,
		/**
		 * `ps` rather than anything in-process: the number that matters is the OS's
		 * view of the whole process, which is what the spike measured and what a
		 * 24 GB machine actually feels.
		 *
		 * 🗝 And it is the TREE plus the sidecar, not the process. Measured on the
		 * real iOS app, one Swift server is three processes - sourcekit-lsp at
		 * 207 MB, the xcode-build-server child at 19 MB, and a `SourceKitService`
		 * XPC service at 390 MB that no tree walk can reach. Reporting the first
		 * alone, which is what the spike published, understates it by ~3x.
		 */
		async rss() {
			const pid = child?.pid;
			if (!pid) return null;
			const table = await psTable();
			const own = treeRss(table, pid);
			if (own === null) return null;
			return own + (await sidecarRss(table));
		},
	};
}
