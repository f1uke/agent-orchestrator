import { type LanguageServerSpec, type SetupNeed, serverForLanguage } from "./language-servers";
import type { JsonRpcMessage } from "./lsp-framing";
import {
	type CompletionCapability,
	type LspProcess,
	type LspState,
	type SemanticTokensLegend,
	type ServerFeatures,
	startLspProcess,
} from "./lsp-process";

/**
 * Every language server this app has alive, and the policy that keeps them from
 * eating the machine.
 *
 * 🗝 The KEY is (languageId, workspaceRoot), which is correct - but note what it
 * does NOT buy. The editor spike argued one-server-per-workspace dedupes across
 * AO sessions on the same repo; it does not, because every AO session gets its
 * own WORKTREE: a different directory, on a different branch, whose type graph
 * gopls cannot share with any other. The key dedupes panes, views, and crew
 * members working on one task worktree.
 *
 * What actually holds the ceiling is `maxServers` plus the idle stop, which is
 * why the eviction rule below gets the attention. Measured cost per server:
 * ~2.4 GB resident unbounded, ~1.0 GB with GOMEMLIMIT=1GiB.
 */
export type LspHealth = {
	key: string;
	languageId: string;
	root: string;
	state: LspState;
	detail?: string;
	pid: number | null;
	uptimeMs: number;
	attachments: number;
	requests: number;
	errors: number;
	emptyWhileReady: number;
	rssMb: number | null;
	peakRssMb: number | null;
};

/**
 * What a pane can be told about its server. `unconfigured` is the registry's own:
 * the workspace still needs something (see `SetupNeed`), no process exists, and
 * the registry keeps checking - when the need is met it reports `stopped`, which
 * is what tells a pane to attach again.
 */
export type LspAttachState = LspState | "unconfigured";

export type LspStateEvent = { handleId: string; key: string; state: LspAttachState; detail?: string; need?: SetupNeed };

export type LspAttachment = {
	handleId: string;
	key: string;
	state: LspAttachState;
	detail?: string;
	/** Set only while `state` is `unconfigured`: what the workspace is missing. */
	need?: SetupNeed;
	/**
	 * The directory the renderer must address documents under.
	 *
	 * 🗝 Equal to the workspace root for every language but Swift, and carried
	 * rather than re-derived because getting it wrong is INVISIBLE: address a
	 * Swift document by its real path instead of through the shadow root's
	 * symlink and every ⌘click returns 0 hits in ~60 ms, with no error, while
	 * symbol search carries on working perfectly.
	 */
	documentRoot: string;
	/** Configured enough to run, but a feature will find nothing. Say which. */
	warning?: string;
	/**
	 * The server's semantic-token vocabulary, or null where it advertised none.
	 * Sent with the attachment so the renderer can map by NAME: the two servers
	 * this app runs publish legends of different shapes, and the indices in every
	 * answer are offsets into whichever one arrived.
	 */
	semanticTokens: SemanticTokensLegend | null;
	/**
	 * What the server can do about completion, or null where it advertised no
	 * `completionProvider`. See `CompletionCapability`: Monaco reads the trigger
	 * characters once, at registration, so they cannot be discovered later.
	 */
	completion: CompletionCapability | null;
	/** Plain yes/no for hover and references, so a refusal can say which silence it is. */
	features: ServerFeatures;
};

/**
 * `cancelled` counts as a request and as neither a fault nor an empty answer:
 * `-32800 RequestCancelled` is what a server replies when the CLIENT changed its
 * mind, and letting it reach `errors` or `emptyWhileReady` would make the two
 * numbers this app watches for silent failure stop meaning anything.
 */
export type LspResultOutcome = "ok" | "empty" | "error" | "cancelled";

export type LspRegistryOptions = {
	dataDir: string;
	env: () => NodeJS.ProcessEnv;
	maxServers?: number;
	idleGraceMs?: number;
	initializeTimeoutMs?: number;
	killGraceMs?: number;
	readinessSettleMs?: number;
	indexTimeoutMs?: number;
	/** How often a workspace that is not set up yet is checked again. */
	setupPollMs?: number;
	onState: (event: LspStateEvent) => void;
	onMessage: (event: { handleId: string; message: JsonRpcMessage }) => void;
	/** Injected in tests. */
	startProcess?: typeof startLspProcess;
};

export type LspRegistry = {
	attach(input: { root: string; languageId: string }): Promise<LspAttachment>;
	detach(handleId: string): void;
	send(handleId: string, message: JsonRpcMessage): void;
	noteResult(handleId: string, outcome: LspResultOutcome): void;
	health(): Promise<LspHealth[]>;
	disposeAll(): Promise<void>;
};

type Entry = {
	key: string;
	languageId: string;
	root: string;
	documentRoot: string;
	warning?: string;
	proc: LspProcess;
	handles: Set<string>;
	lastUsedAt: number;
	idleTimer: ReturnType<typeof setTimeout> | null;
	rssTimer: ReturnType<typeof setInterval> | null;
	requests: number;
	errors: number;
	emptyWhileReady: number;
	rssMb: number | null;
	peakRssMb: number | null;
};

/**
 * A workspace that `prepare` turned down, held so the registry can notice when it
 * stops being turned down. No process, so it costs nothing against the cap.
 */
type Pending = {
	key: string;
	languageId: string;
	root: string;
	spec: LanguageServerSpec;
	need: SetupNeed;
	reason: string;
	handles: Set<string>;
	timer: ReturnType<typeof setInterval>;
};

/**
 * Worst case ~2 GB on a 24 GB machine, given GOMEMLIMIT=1GiB per server. gopls
 * unbounded rests at ~2.4 GB for one mid-size Go module, so this pair of knobs
 * is what makes an in-app language server affordable at all.
 */
const DEFAULT_MAX_SERVERS = 2;

/**
 * Not zero: closing one Go file and opening another must not pay the cold start
 * again. The spike measured seconds, not milliseconds, to a first definition.
 */
const DEFAULT_IDLE_GRACE_MS = 60_000;

const RSS_SAMPLE_MS = 5_000;

/**
 * 🗝 Why the registry polls at all: "build it in Xcode once" is only half an
 * answer if the pane then has to be closed and reopened to notice the build. A
 * check is a readdir of DerivedData and one small plist per entry, so asking
 * every few seconds while a pane is waiting is free next to a person wondering
 * whether the build they just ran did anything.
 */
const SETUP_POLL_MS = 3_000;

export function createLspRegistry(options: LspRegistryOptions): LspRegistry {
	const maxServers = options.maxServers ?? DEFAULT_MAX_SERVERS;
	const idleGraceMs = options.idleGraceMs ?? DEFAULT_IDLE_GRACE_MS;
	const startProcess = options.startProcess ?? startLspProcess;

	const entries = new Map<string, Entry>();
	const handles = new Map<string, Entry>();
	const pending = new Map<string, Pending>();
	const pendingHandles = new Map<string, Pending>();
	let handleSeq = 0;

	const keyFor = (languageId: string, root: string) => `${languageId} ${root}`;

	const emitState = (entry: Entry, state: LspState, detail?: string) => {
		for (const handleId of entry.handles) options.onState({ handleId, key: entry.key, state, detail });
	};

	async function destroy(entry: Entry, why: string): Promise<void> {
		if (entry.idleTimer) clearTimeout(entry.idleTimer);
		if (entry.rssTimer) clearInterval(entry.rssTimer);
		entry.idleTimer = null;
		entry.rssTimer = null;
		entries.delete(entry.key);
		// Told BEFORE the await, so a renderer learns its server is going even if
		// the shutdown handshake takes the whole kill grace. Silence here is the
		// spike's carried bug: a pane with no intelligence and no error.
		emitState(entry, "stopped", why);
		for (const handleId of entry.handles) handles.delete(handleId);
		entry.handles.clear();
		await entry.proc.stop(why);
	}

	function scheduleIdleStop(entry: Entry): void {
		if (entry.idleTimer) clearTimeout(entry.idleTimer);
		entry.idleTimer = null;
		if (entry.handles.size > 0) return;
		entry.idleTimer = setTimeout(() => {
			if (entry.handles.size === 0) void destroy(entry, "idle");
		}, idleGraceMs);
		entry.idleTimer.unref?.();
	}

	async function enforceCap(exceptKey: string): Promise<void> {
		while (entries.size >= maxServers) {
			// 🗝 Least recently USED, not least referenced. A pane that has sat on
			// screen untouched is a worse thing to keep than the workspace someone is
			// actively clicking through - and the evicted pane self-heals, because
			// `destroy` tells it `stopped`.
			let victim: Entry | null = null;
			for (const entry of entries.values()) {
				if (entry.key === exceptKey) continue;
				if (!victim || entry.lastUsedAt < victim.lastUsedAt) victim = entry;
			}
			if (!victim) return;
			await destroy(victim, "evicted: server cap reached");
		}
	}

	// 🗝 Asked BEFORE anything is spawned, and allowed to say no.
	//
	// An unconfigured sourcekit-lsp is the sharpest example of this stack's
	// characteristic failure: pointed at a real .xcodeproj with no build settings
	// it initializes in ~60 ms, publishes diagnostics and answers documentSymbol,
	// while returning 0 hits for every ⌘click and 0 results for every symbol
	// query. Spawning it and letting the user discover that is strictly worse than
	// declining with a sentence they can act on.
	function prepare(spec: LanguageServerSpec, root: string, env: NodeJS.ProcessEnv) {
		return (
			spec.prepare?.({ workspaceRoot: root, dataDir: options.dataDir, env }) ?? {
				ok: true as const,
				lspRoot: root,
				documentRoot: root,
			}
		);
	}

	function emitPending(entry: Pending, state: LspAttachState, detail?: string, need?: SetupNeed) {
		for (const handleId of entry.handles) options.onState({ handleId, key: entry.key, state, detail, need });
	}

	function dropPending(entry: Pending): void {
		clearInterval(entry.timer);
		pending.delete(entry.key);
		for (const handleId of entry.handles) pendingHandles.delete(handleId);
		entry.handles.clear();
	}

	function startPending(input: {
		key: string;
		languageId: string;
		root: string;
		spec: LanguageServerSpec;
		need: SetupNeed;
		reason: string;
	}): Pending {
		const entry: Pending = {
			...input,
			handles: new Set(),
			timer: setInterval(() => {
				let prepared: ReturnType<typeof prepare>;
				try {
					prepared = prepare(entry.spec, entry.root, options.env());
				} catch {
					// A timer must not throw into the main process. Hand it to the pane's
					// re-attach instead, which meets the same error inside `attach` and
					// reports it as `failed`, with its message.
					emitPending(entry, "stopped", "setup check failed");
					dropPending(entry);
					return;
				}
				if (prepared.ok) {
					// `stopped` is the word a pane already re-attaches on, and the
					// re-attach goes through `prepare` again and starts the real server.
					emitPending(entry, "stopped", "set up: starting the language server");
					dropPending(entry);
					return;
				}
				// Still not ready, but maybe for a different reason - xcode-build-server
				// installed, the project not built yet - and the pane should say so.
				if (prepared.reason !== entry.reason || prepared.need !== entry.need) {
					entry.reason = prepared.reason;
					entry.need = prepared.need;
					emitPending(entry, "unconfigured", entry.reason, entry.need);
				}
			}, options.setupPollMs ?? SETUP_POLL_MS),
		};
		// `unref` so a waiting pane never holds the app - or a test run - open.
		entry.timer.unref?.();
		pending.set(entry.key, entry);
		return entry;
	}

	function startEntry(
		languageId: string,
		root: string,
		key: string,
		spec: LanguageServerSpec,
		env: NodeJS.ProcessEnv,
		prepared: { lspRoot: string; documentRoot: string; detail?: string; warning?: string },
	): Entry {
		let entry: Entry | undefined;
		const proc = startProcess({
			spec,
			root,
			lspRoot: prepared.lspRoot,
			initialDetail: prepared.detail,
			dataDir: options.dataDir,
			env,
			initializeTimeoutMs: options.initializeTimeoutMs,
			killGraceMs: options.killGraceMs,
			readinessSettleMs: options.readinessSettleMs,
			indexTimeoutMs: options.indexTimeoutMs,
			onState: (state, detail) => {
				if (!entry) return;
				emitState(entry, state, detail);
				// A failed server is not kept around to be re-used: the next attach
				// should get a fresh spawn and a fresh chance.
				if (state === "failed") void destroy(entry, detail ?? "failed");
			},
			onMessage: (message) => {
				if (!entry) return;
				for (const handleId of entry.handles) options.onMessage({ handleId, message });
			},
		});
		entry = {
			key,
			languageId,
			root,
			documentRoot: prepared.documentRoot,
			warning: prepared.warning,
			proc,
			handles: new Set(),
			lastUsedAt: Date.now(),
			idleTimer: null,
			rssTimer: null,
			requests: 0,
			errors: 0,
			emptyWhileReady: 0,
			rssMb: null,
			peakRssMb: null,
		};
		const sampled = entry;
		sampled.rssTimer = setInterval(() => {
			void proc.rss().then((mb) => {
				if (mb === null) return;
				sampled.rssMb = mb;
				sampled.peakRssMb = Math.max(sampled.peakRssMb ?? 0, mb);
			});
		}, RSS_SAMPLE_MS);
		// `unref` so a sampling timer never holds the app - or a test run - open.
		sampled.rssTimer.unref?.();
		entries.set(key, entry);
		return entry;
	}

	return {
		async attach({ root, languageId }) {
			const key = keyFor(languageId, root);
			let entry = entries.get(key);
			if (!entry) {
				const env = options.env();
				const spec = serverForLanguage(languageId, env);
				if (!spec) throw new Error(`no language server for "${languageId}"`);
				// A workspace already waiting is asked again rather than trusted: this
				// attach may be the re-mount of a person who just finished the build.
				const prepared = prepare(spec, root, env);
				if (!prepared.ok) {
					let waiting = pending.get(key);
					if (waiting) {
						waiting.reason = prepared.reason;
						waiting.need = prepared.need;
					} else {
						waiting = startPending({ key, languageId, root, spec, need: prepared.need, reason: prepared.reason });
					}
					const handleId = `lsp-${++handleSeq}`;
					waiting.handles.add(handleId);
					pendingHandles.set(handleId, waiting);
					return {
						handleId,
						key,
						state: "unconfigured",
						detail: prepared.reason,
						need: prepared.need,
						documentRoot: root,
						semanticTokens: null,
						completion: null,
						features: { hover: false, references: false },
					};
				}
				const waiting = pending.get(key);
				if (waiting) {
					// Set up between two checks: the panes still waiting re-attach too.
					emitPending(waiting, "stopped", "set up: starting the language server");
					dropPending(waiting);
				}
				await enforceCap(key);
				entry = startEntry(languageId, root, key, spec, env, prepared);
			}
			if (entry.idleTimer) {
				clearTimeout(entry.idleTimer);
				entry.idleTimer = null;
			}
			const handleId = `lsp-${++handleSeq}`;
			entry.handles.add(handleId);
			handles.set(handleId, entry);
			entry.lastUsedAt = Date.now();
			try {
				// Resolves only once the handshake has settled, so the renderer never
				// holds a channel that looks connected and answers nothing.
				await entry.proc.initialized;
			} catch (err) {
				handles.delete(handleId);
				entry.handles.delete(handleId);
				throw err;
			}
			return {
				handleId,
				key,
				state: entry.proc.state,
				detail: entry.proc.detail,
				documentRoot: entry.documentRoot,
				warning: entry.warning,
				semanticTokens: entry.proc.semanticTokensLegend,
				completion: entry.proc.completionCapability,
				features: entry.proc.features,
			};
		},

		detach(handleId) {
			const waiting = pendingHandles.get(handleId);
			if (waiting) {
				pendingHandles.delete(handleId);
				waiting.handles.delete(handleId);
				if (waiting.handles.size === 0) dropPending(waiting);
				return;
			}
			const entry = handles.get(handleId);
			if (!entry) return;
			handles.delete(handleId);
			entry.handles.delete(handleId);
			scheduleIdleStop(entry);
		},

		send(handleId, message) {
			const entry = handles.get(handleId);
			if (!entry) return;
			// Every send counts as use, which is what the LRU eviction reads.
			entry.lastUsedAt = Date.now();
			entry.proc.send(message);
		},

		noteResult(handleId, outcome) {
			const entry = handles.get(handleId);
			if (!entry) return;
			entry.requests++;
			if (outcome === "error") entry.errors++;
			// Only counted while READY: an empty answer from a server that is still
			// indexing is expected, and lumping the two together would hide the one
			// that matters.
			else if (outcome === "empty" && entry.proc.state === "ready") entry.emptyWhileReady++;
		},

		async health() {
			return Promise.all(
				[...entries.values()].map(async (entry) => {
					const rssMb = (await entry.proc.rss()) ?? entry.rssMb;
					if (rssMb !== null) entry.peakRssMb = Math.max(entry.peakRssMb ?? 0, rssMb);
					return {
						key: entry.key,
						languageId: entry.languageId,
						root: entry.root,
						state: entry.proc.state,
						detail: entry.proc.detail,
						pid: entry.proc.pid,
						uptimeMs: Date.now() - entry.proc.startedAt,
						attachments: entry.handles.size,
						requests: entry.requests,
						errors: entry.errors,
						emptyWhileReady: entry.emptyWhileReady,
						rssMb,
						peakRssMb: entry.peakRssMb,
					};
				}),
			);
		},

		async disposeAll() {
			// A language server left running after the app quits is ~1 GB of orphaned
			// resident memory with nothing to reap it.
			for (const waiting of [...pending.values()]) dropPending(waiting);
			await Promise.all([...entries.values()].map((entry) => destroy(entry, "app quit")));
		},
	};
}
