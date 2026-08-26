import { serverForLanguage } from "./language-servers";
import type { JsonRpcMessage } from "./lsp-framing";
import { type LspProcess, type LspState, startLspProcess } from "./lsp-process";

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

export type LspAttachment = { handleId: string; key: string; state: LspState; detail?: string };

export type LspResultOutcome = "ok" | "empty" | "error";

export type LspRegistryOptions = {
	dataDir: string;
	env: () => NodeJS.ProcessEnv;
	maxServers?: number;
	idleGraceMs?: number;
	initializeTimeoutMs?: number;
	killGraceMs?: number;
	readinessSettleMs?: number;
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
		let entry: Entry | undefined;
		const proc = startProcess({
			spec,
			root,
			dataDir: options.dataDir,
			env,
			initializeTimeoutMs: options.initializeTimeoutMs,
			killGraceMs: options.killGraceMs,
			readinessSettleMs: options.readinessSettleMs,
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
			return { handleId, key, state: entry.proc.state, detail: entry.proc.detail };
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
