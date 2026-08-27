import { serverForLanguage } from "./language-servers";
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

export type LspAttachment = {
	handleId: string;
	key: string;
	state: LspState;
	detail?: string;
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
	onState: (event: { handleId: string; key: string; state: LspState; detail?: string }) => void;
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

export function createLspRegistry(options: LspRegistryOptions): LspRegistry {
	const maxServers = options.maxServers ?? DEFAULT_MAX_SERVERS;
	const idleGraceMs = options.idleGraceMs ?? DEFAULT_IDLE_GRACE_MS;
	const startProcess = options.startProcess ?? startLspProcess;

	const entries = new Map<string, Entry>();
	const handles = new Map<string, Entry>();
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

	function startEntry(languageId: string, root: string, key: string): Entry {
		const env = options.env();
		const spec = serverForLanguage(languageId, env);
		if (!spec) throw new Error(`no language server for "${languageId}"`);
		// 🗝 Asked BEFORE anything is spawned, and allowed to say no.
		//
		// An unconfigured sourcekit-lsp is the sharpest example of this stack's
		// characteristic failure: pointed at a real .xcodeproj with no build
		// settings it initializes in ~60 ms, publishes diagnostics and answers
		// documentSymbol, while returning 0 hits for every ⌘click and 0 results for
		// every symbol query. Spawning it and letting the user discover that is
		// strictly worse than refusing with a sentence they can act on.
		const prepared = spec.prepare?.({ workspaceRoot: root, dataDir: options.dataDir, env }) ?? {
			ok: true as const,
			lspRoot: root,
			documentRoot: root,
		};
		if (!prepared.ok) throw new Error(prepared.reason);
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
				await enforceCap(key);
				entry = startEntry(languageId, root, key);
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
			await Promise.all([...entries.values()].map((entry) => destroy(entry, "app quit")));
		},
	};
}
