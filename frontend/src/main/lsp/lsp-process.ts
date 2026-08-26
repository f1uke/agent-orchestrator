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
	root: string;
	dataDir: string;
	env: NodeJS.ProcessEnv;
	initializeTimeoutMs?: number;
	killGraceMs?: number;
	readinessSettleMs?: number;
	onState: (state: LspState, detail?: string) => void;
	/** Server→client traffic main does NOT answer itself. */
	onMessage: (message: JsonRpcMessage) => void;
};

export type LspProcess = {
	readonly pid: number | null;
	readonly state: LspState;
	readonly startedAt: number;
	readonly detail: string | undefined;
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
			documentSymbol: { hierarchicalDocumentSymbolSupport: true },
			publishDiagnostics: {},
		},
		window: { workDoneProgress: true },
	};
}

export function startLspProcess(options: LspProcessOptions): LspProcess {
	const { spec, root, dataDir, env, onState, onMessage } = options;
	const initializeTimeoutMs = options.initializeTimeoutMs ?? DEFAULT_INITIALIZE_TIMEOUT_MS;
	const killGraceMs = options.killGraceMs ?? DEFAULT_KILL_GRACE_MS;
	const readinessSettleMs = options.readinessSettleMs ?? DEFAULT_READINESS_SETTLE_MS;

	let state: LspState = "starting";
	let detail: string | undefined;
	const startedAt = Date.now();
	// Work-done progress tokens the server opens while it loads packages. While
	// any is outstanding the index is not settled and workspace/symbol answers
	// WRONG rather than empty, which is why readiness is tracked and not guessed.
	const progress = new Set<string>();
	let child: ChildProcessWithoutNullStreams | null = null;
	let stopping: Promise<void> | null = null;

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

	const rootUri = pathToFileURL(root).href;

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

	const settle = () => {
		if (state === "failed" || state === "stopped") return;
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
			const info = (message.result as { serverInfo?: { name?: string; version?: string } } | null)?.serverInfo;
			// Carried as `detail` so a test - and a human reading the health panel -
			// can see the root the server was actually given.
			detail = info ? `${info.name ?? spec.command} ${info.version ?? ""}`.trim() : undefined;
			send({ jsonrpc: "2.0", method: "initialized", params: {} });
			settle();
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
		child = spawn(spec.command, spec.args, {
			cwd: root,
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
		setState("initializing");
		send({
			jsonrpc: "2.0",
			id: INITIALIZE_ID,
			method: "initialize",
			params: {
				processId: process.pid,
				// The two fields monaco.lsp gets wrong, and the reason we hand-roll.
				rootUri,
				workspaceFolders: [{ uri: rootUri, name: root.slice(root.lastIndexOf("/") + 1) }],
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
		startedAt,
		initialized,
		send,
		stop,
		/**
		 * `ps` rather than anything in-process: the number that matters is the OS's
		 * view of the whole process, which is what the spike measured and what a
		 * 24 GB machine actually feels.
		 */
		async rss() {
			const pid = child?.pid;
			if (!pid) return null;
			return new Promise<number | null>((resolve) => {
				execFile("ps", ["-o", "rss=", "-p", String(pid)], (err, stdout) => {
					const kb = Number(String(stdout ?? "").trim());
					resolve(err || !Number.isFinite(kb) || kb <= 0 ? null : Math.round(kb / 1024));
				});
			});
		},
	};
}
