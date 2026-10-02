import { execFile } from "node:child_process";
import { access, mkdir, readdir, rename, rm } from "node:fs/promises";
import path from "node:path";
import {
	DEFAULT_MODEL_ID,
	MODELS,
	type ModelId,
	type ModelKind,
	type ModelSpec,
	type RuntimeArtifact,
	runtimeFor,
} from "./catalog";
import { downloadVerified, type FetchLike, freeBytes, partialBytes } from "./download";
import { type InfillRequest, type InfillResult, postInfill } from "./infill";
import { type NextEditRequest, type NextEditResult, postNextEdit } from "./next-edit";
import { createRequestLane } from "./request-lane";
import {
	type LlamaServer,
	type LlamaServerState,
	MAX_SOCKET_PATH_BYTES,
	reapOrphan,
	startLlamaServer,
} from "./llama-server";
import { formatBytes } from "../../shared/format-bytes";
import { type InlineCompletionSettings, readInlineCompletionSettings, writeInlineCompletionSettings } from "./settings";

/**
 * Everything the UI needs to draw the feature, in one value.
 *
 * Kept as independent facts rather than one enum, because they genuinely
 * overlap: switching from 1.5B to 3B downloads the new model WHILE the old one
 * keeps serving ghost text, and the settings row has to say both.
 */
export type InlineCompletionStatus = {
	/** Why this machine cannot run it at all; null when it can. */
	unsupported: string | null;
	/** The person's switch, as persisted. */
	enabled: boolean;
	/** The model that runs (or will run once its download finishes and it is chosen). */
	modelId: ModelId;
	server: "off" | "starting" | "ready" | "error";
	/**
	 * What the RUNNING model is asked - which decides the editor's whole request
	 * path. Null while nothing runs. Not derived from `modelId`: while a new
	 * model downloads, the old one keeps serving.
	 */
	activeKind: ModelKind | null;
	/** Whether the running model also answers fill-in-the-middle (ghost text at the cursor). */
	activeInfill: boolean;
	/** The reason behind `error`, or what `starting` is doing. */
	serverDetail: string | null;
	/** The pid of the llama-server AO started, while it runs. */
	pid: number | null;
	/** A download waiting for the person's yes, with its size, shown BEFORE it starts. */
	confirm: { modelId: ModelId; bytes: number; runtimeBytes: number; freeBytes: number | null } | null;
	download: { modelId: ModelId; label: string; receivedBytes: number; totalBytes: number } | null;
	/** The last download's failure, until the next attempt. */
	downloadError: string | null;
	models: { id: ModelId; kind: ModelKind; label: string; blurb: string; sizeBytes: number; installed: boolean }[];
};

export type InlineCompletionPaths = {
	stateDir: string;
	root: string;
	runtimeDir: string;
	modelsDir: string;
	downloadsDir: string;
	runDir: string;
	socketPath: string;
	pidFile: string;
	logFile: string;
};

/** `<stateDir>/llm/...` - under ~/.ao, beside the other settings files. Never an OS app-data dir. */
export function inlineCompletionPaths(stateDir: string): InlineCompletionPaths {
	const root = path.join(stateDir, "llm");
	const runDir = path.join(root, "run");
	return {
		stateDir,
		root,
		runtimeDir: path.join(root, "runtime"),
		modelsDir: path.join(root, "models"),
		downloadsDir: path.join(root, "downloads"),
		runDir,
		socketPath: path.join(runDir, "llama.sock"),
		pidFile: path.join(runDir, "llama-server.pid"),
		logFile: path.join(root, "logs", "llama-server.log"),
	};
}

/**
 * The server's command line. Every number here is from the measurements in the
 * PR, and each one is load-bearing:
 *
 * - `-c 8192`: room for the extra-context ring (up to ~6.9k tokens after the
 *   server reserves `n_batch + 2 * n_predict`) without paying for Qwen's full
 *   32k KV cache in RAM.
 * - `-np 1`: one person types into one editor at a time. A request that
 *   supersedes another cancels it (see infill.ts), so a single slot never queues
 *   anything worth waiting for, and the KV cache is not split four ways.
 * - `-b/-ub 1024`: the server keeps the last 3/4 batch of prefix tokens and
 *   1/4 of suffix, so this is the window around the cursor the model sees.
 * - `--cache-reuse 256`: as typing pushes the prefix window forward, chunks of
 *   the previous prompt are shifted rather than recomputed.
 * - `--no-webui`: nobody browses to a Unix socket.
 *
 * A next-edit model differs in two ways (NEXT_EDIT_ARGS):
 * - it is asked TWO kinds of prompt - fill-in-the-middle at the cursor and the
 *   rewrite of the window - so it gets one slot per kind, each with the same
 *   8192 tokens of context. On one shared slot every rewrite evicted the
 *   ghost-text prompt's cache: the next keystroke reprocessed ~1k tokens and
 *   took 322 ms (p50) instead of 172 ms. Cost: ~230 MB more KV cache.
 *   llama-server routes a request to the slot whose cached prompt it shares the
 *   most with, so the two kinds keep to their own slots.
 * - n-gram speculative decoding: a rewrite is mostly the window it was given,
 *   copied, and drafting from the prompt's own n-grams lets the server verify
 *   those copied runs several tokens per step.
 */
export function serverArgs(modelPath: string, socketPath: string, kind: ModelKind = "fim"): string[] {
	return [
		"-m",
		modelPath,
		"--host",
		socketPath,
		...(kind === "next-edit" ? NEXT_EDIT_ARGS : ["-c", "8192", "-np", "1"]),
		"-b",
		"1024",
		"-ub",
		"1024",
		"--cache-reuse",
		"256",
		"-ngl",
		"99",
		"--no-webui",
	];
}

/**
 * Measured on sweep-next-edit-1.5B over 160 real next-edit positions (with the
 * early stop in next-edit.ts): p50 / p90 1877 / 2636 ms with no speculation,
 * 455 / 674 ms with `ngram-mod`, 387 / 607 ms with `ngram-simple` - the same
 * answers every time (speculation verifies, it never changes a greedy result).
 * `ngram-simple` drafts from the prompt's own n-grams, which is exactly where a
 * rewrite of the window copies from.
 */
export const NEXT_EDIT_ARGS: readonly string[] = ["-c", "16384", "-np", "2", "--spec-type", "ngram-simple"];

/**
 * The child's environment, built from nothing on purpose: llama-server reads
 * `LLAMA_ARG_*` and `LLAMA_API_KEY` from its environment, and a person who has
 * set those for their own server must not have them silently change ours.
 */
function serverEnv(): NodeJS.ProcessEnv {
	return {
		HOME: process.env.HOME,
		TMPDIR: process.env.TMPDIR,
		PATH: "/usr/bin:/bin:/usr/sbin:/sbin",
		LANG: "en_US.UTF-8",
	};
}

/** Unpack the runtime tarball with the system `tar` (bsdtar on macOS). */
function extractTarball(tarball: string, destDir: string): Promise<void> {
	return new Promise((resolve, reject) =>
		execFile("tar", ["-xzf", tarball, "-C", destDir], (err, _stdout, stderr) =>
			err
				? reject(new Error(`could not unpack the llama.cpp runtime: ${String(stderr).trim() || err.message}`))
				: resolve(),
		),
	);
}

async function exists(file: string): Promise<boolean> {
	try {
		await access(file);
		return true;
	} catch {
		return false;
	}
}

export type InlineCompletionServiceDeps = {
	paths: InlineCompletionPaths;
	onStatus: (status: InlineCompletionStatus) => void;
	platform?: NodeJS.Platform;
	arch?: string;
	/** Seams for tests: what to fetch, how to unpack it, and what to run. */
	runtime?: RuntimeArtifact | null;
	models?: readonly ModelSpec[];
	fetchImpl?: FetchLike;
	extract?: (tarball: string, destDir: string) => Promise<void>;
	/** The executable inside an unpacked runtime directory. */
	serverBinary?: (runtimeRoot: string) => string;
	/** Overrides `serverArgs`, for a fake server in tests. */
	serverArgs?: (modelPath: string, socketPath: string, kind: ModelKind) => string[];
	readyTimeoutMs?: number;
	/** Disk headroom required beyond the download itself. */
	diskMarginBytes?: number;
	progressIntervalMs?: number;
	requestTimeoutMs?: number;
};

type LaneRequest = { kind: "infill"; request: InfillRequest } | { kind: "nextEdit"; request: NextEditRequest };

export type InlineCompletionService = ReturnType<typeof createInlineCompletionService>;

/** How long one prediction may take before main gives up on it. */
const DEFAULT_REQUEST_TIMEOUT_MS = 5_000;
const DEFAULT_DISK_MARGIN_BYTES = 512 * 1024 * 1024;
const DEFAULT_PROGRESS_INTERVAL_MS = 200;

/**
 * The whole feature on the main-process side: the person's choices, the
 * downloads, and the one llama-server AO owns.
 *
 * Invariants worth keeping:
 * - Nothing is downloaded without an explicit `confirmDownload()` that followed a
 *   `confirm` carrying the size. The one exception is resuming a `.part` at
 *   launch, which the person already said yes to.
 * - At most one server exists, and it is only ever stopped through its own
 *   handle (its pid).
 * - `complete()` answers null whenever the server is not ready: the editor's
 *   provider treats null as "no ghost text", silently.
 */
export function createInlineCompletionService(deps: InlineCompletionServiceDeps) {
	const { paths, onStatus } = deps;
	const platform = deps.platform ?? process.platform;
	const arch = deps.arch ?? process.arch;
	const runtime = deps.runtime === undefined ? runtimeFor(platform, arch) : deps.runtime;
	const models = deps.models ?? MODELS;
	const extract = deps.extract ?? extractTarball;
	const binaryIn = deps.serverBinary ?? ((root: string) => path.join(root, "llama-server"));
	const argsFor = deps.serverArgs ?? serverArgs;
	const diskMargin = deps.diskMarginBytes ?? DEFAULT_DISK_MARGIN_BYTES;
	const progressIntervalMs = deps.progressIntervalMs ?? DEFAULT_PROGRESS_INTERVAL_MS;
	const requestTimeoutMs = deps.requestTimeoutMs ?? DEFAULT_REQUEST_TIMEOUT_MS;

	const runtimeRoot = runtime ? path.join(paths.runtimeDir, `llama-${runtime.tag}`) : null;
	const serverPath = runtimeRoot ? binaryIn(runtimeRoot) : null;
	const modelPath = (m: ModelSpec) => path.join(paths.modelsDir, m.fileName);
	const partPath = (fileName: string) => path.join(paths.downloadsDir, `${fileName}.part`);
	const specOf = (id: ModelId) => models.find((m) => m.id === id) ?? models[0];

	let settings: InlineCompletionSettings = { enabled: false, modelId: DEFAULT_MODEL_ID };
	let server: LlamaServer | null = null;
	let serverModel: ModelId | null = null;
	let serverState: InlineCompletionStatus["server"] = "off";
	let serverDetail: string | null = null;
	let confirm: InlineCompletionStatus["confirm"] = null;
	let download: InlineCompletionStatus["download"] = null;
	let downloadAbort: AbortController | null = null;
	let downloadError: string | null = null;
	let installed = new Set<ModelId>();
	let runtimeInstalled = false;
	// One lane for both kinds: there is one server with one slot, running one model.
	const lane = createRequestLane<LaneRequest, InfillResult | NextEditResult | null>(
		(job, signal) =>
			job.kind === "infill"
				? postInfill(paths.socketPath, job.request, signal)
				: postNextEdit(paths.socketPath, job.request, signal),
		{
			timeoutMs: requestTimeoutMs,
			onError: (err) => console.warn("[inline-completion] request failed:", err instanceof Error ? err.message : err),
			isWarmup: (job) => job.kind === "infill" && job.request.nPredict === 0,
			abortsWhenSuperseded: (job) => job.kind === "nextEdit",
		},
	);
	let lastProgressEmit = 0;
	let disposed = false;
	// Serialises every state transition. Two clicks in quick succession (enable,
	// then a model switch) must not start two servers.
	let queue: Promise<unknown> = Promise.resolve();
	const serial = <T>(fn: () => Promise<T>): Promise<T> => {
		const next = queue.then(fn, fn);
		queue = next.catch(() => {});
		return next;
	};

	const activeKind = (): ModelKind | null => (serverModel ? specOf(serverModel).kind : null);

	const unsupported = (): string | null => {
		if (!runtime)
			return `Inline completion runs llama.cpp locally and is available on Apple silicon Macs only (this is ${platform}-${arch}).`;
		if (Buffer.byteLength(paths.socketPath) > MAX_SOCKET_PATH_BYTES) {
			return `The AO data directory is too deep for a Unix socket (${paths.socketPath}); point AO_RUN_FILE at a shorter path.`;
		}
		return null;
	};

	const status = (): InlineCompletionStatus => ({
		unsupported: unsupported(),
		enabled: settings.enabled,
		modelId: settings.modelId,
		server: serverState,
		activeKind: server ? activeKind() : null,
		activeInfill: server !== null && serverModel !== null && specOf(serverModel).infill,
		serverDetail,
		pid: server?.pid ?? null,
		confirm,
		download,
		downloadError,
		models: models.map((m) => ({
			id: m.id,
			kind: m.kind,
			label: m.label,
			blurb: m.blurb,
			sizeBytes: m.sizeBytes,
			installed: installed.has(m.id),
		})),
	});

	const emit = () => {
		if (!disposed) onStatus(status());
	};

	const refreshInstalled = async () => {
		runtimeInstalled = serverPath ? await exists(serverPath) : false;
		const next = new Set<ModelId>();
		for (const m of models) if (await exists(modelPath(m))) next.add(m.id);
		installed = next;
	};

	const persist = async (next: InlineCompletionSettings) => {
		settings = next;
		await writeInlineCompletionSettings(paths.stateDir, next);
	};

	/** Bytes still to fetch for `id` (the runtime too, when it is missing), net of any resumable part. */
	const missingBytes = async (id: ModelId): Promise<{ bytes: number; runtimeBytes: number }> => {
		let runtimeBytes = 0;
		let modelBytes = 0;
		if (!runtimeInstalled && runtime) {
			runtimeBytes =
				runtime.sizeBytes - Math.min(runtime.sizeBytes, await partialBytes(partPath(path.basename(runtime.url))));
		}
		if (!installed.has(id)) {
			const spec = specOf(id);
			modelBytes = spec.sizeBytes - Math.min(spec.sizeBytes, await partialBytes(partPath(spec.fileName)));
		}
		return { bytes: runtimeBytes + modelBytes, runtimeBytes };
	};

	const stopServer = async (why: string) => {
		const current = server;
		server = null;
		serverModel = null;
		lane.clear();
		if (current) await current.stop(why);
		serverState = "off";
		serverDetail = null;
	};

	const startServer = async () => {
		if (disposed) return;
		const reason = unsupported();
		if (reason || !serverPath) {
			serverState = "error";
			serverDetail = reason;
			emit();
			return;
		}
		const spec = specOf(settings.modelId);
		if (server && serverModel === spec.id) return;
		await stopServer("switching model");
		await mkdir(paths.runDir, { recursive: true, mode: 0o700 });
		await reapOrphan(paths.pidFile, paths.runtimeDir);
		serverState = "starting";
		serverDetail = `loading ${spec.label}`;
		serverModel = spec.id;
		const handle = startLlamaServer({
			command: serverPath,
			args: argsFor(modelPath(spec), paths.socketPath, spec.kind),
			socketPath: paths.socketPath,
			pidFile: paths.pidFile,
			logFile: paths.logFile,
			cwd: paths.runDir,
			env: serverEnv(),
			readyTimeoutMs: deps.readyTimeoutMs,
			onState: (state: LlamaServerState, detail?: string) => {
				if (server !== handle) return;
				if (state === "ready") {
					serverState = "ready";
					serverDetail = spec.label;
				} else if (state === "starting") {
					serverState = "starting";
					serverDetail = detail ?? `loading ${spec.label}`;
				} else if (state === "failed") {
					serverState = "error";
					serverDetail = detail ?? "llama-server stopped";
				} else {
					serverState = "off";
					serverDetail = null;
				}
				emit();
			},
		});
		server = handle;
		emit();
	};

	const installRuntime = async (signal: AbortSignal, onProgress: (received: number) => void) => {
		if (runtimeInstalled || !runtime || !runtimeRoot) return;
		const tarball = path.join(paths.downloadsDir, path.basename(runtime.url));
		await downloadVerified({
			artifact: runtime,
			dest: tarball,
			partPath: partPath(path.basename(runtime.url)),
			signal,
			fetchImpl: deps.fetchImpl,
			onProgress,
		});
		// Unpacked beside its final name and renamed into place, so a crash half
		// way through never leaves a runtime directory that looks installed.
		const staging = path.join(paths.runtimeDir, `.staging-${process.pid}-${Date.now()}`);
		await mkdir(staging, { recursive: true, mode: 0o750 });
		try {
			await extract(tarball, staging);
			await rm(runtimeRoot, { recursive: true, force: true });
			await rename(path.join(staging, `llama-${runtime.tag}`), runtimeRoot);
		} finally {
			await rm(staging, { recursive: true, force: true });
			await rm(tarball, { force: true });
		}
		runtimeInstalled = serverPath ? await exists(serverPath) : false;
		if (!runtimeInstalled) throw new Error(`the llama.cpp runtime unpacked without ${path.basename(serverPath ?? "")}`);
		// A runtime from an earlier pin is dead weight once this one is in place.
		for (const entry of await readdir(paths.runtimeDir)) {
			if (entry.startsWith("llama-") && path.join(paths.runtimeDir, entry) !== runtimeRoot) {
				await rm(path.join(paths.runtimeDir, entry), { recursive: true, force: true });
			}
		}
	};

	/** One progress line across every file in the download, throttled for IPC. */
	const progress = (label: string, receivedBytes: number) => {
		if (!download) return;
		download = { ...download, label, receivedBytes };
		const now = Date.now();
		if (receivedBytes >= download.totalBytes || now - lastProgressEmit >= progressIntervalMs) {
			lastProgressEmit = now;
			emit();
		}
	};

	/**
	 * Fetch whatever `id` still needs, then run it. Runs OUTSIDE the serial queue:
	 * a multi-gigabyte download must not block disable or cancel, which are
	 * exactly what a person reaches for while one is running.
	 */
	const runDownload = async (id: ModelId) => {
		const spec = specOf(id);
		const ac = new AbortController();
		downloadAbort = ac;
		downloadError = null;
		const needsRuntime = !runtimeInstalled && runtime ? runtime.sizeBytes : 0;
		const needsModel = installed.has(id) ? 0 : spec.sizeBytes;
		download = {
			modelId: id,
			label: needsRuntime > 0 && runtime ? runtime.label : spec.label,
			receivedBytes: 0,
			totalBytes: needsRuntime + needsModel,
		};
		emit();
		try {
			await installRuntime(ac.signal, (received) => progress(runtime?.label ?? spec.label, received));
			if (needsModel > 0) {
				await downloadVerified({
					artifact: spec,
					dest: modelPath(spec),
					partPath: partPath(spec.fileName),
					signal: ac.signal,
					fetchImpl: deps.fetchImpl,
					onProgress: (received) => progress(spec.label, needsRuntime + received),
				});
				installed.add(id);
			}
		} catch (err) {
			if (downloadAbort === ac) {
				downloadAbort = null;
				download = null;
				if (!ac.signal.aborted) downloadError = err instanceof Error ? err.message : String(err);
				emit();
			}
			return;
		}
		if (downloadAbort !== ac) return;
		downloadAbort = null;
		download = null;
		await serial(async () => {
			if (disposed || !settings.enabled) return;
			await persist({ ...settings, modelId: id });
			await startServer();
		});
		emit();
	};

	/** Abort a running download. `discard` deletes its part (an explicit cancel); a quit keeps it. */
	const abortDownload = async (discard: boolean) => {
		const ac = downloadAbort;
		const was = download;
		downloadAbort = null;
		download = null;
		if (!ac) return;
		ac.abort();
		if (!discard || !was) return;
		const spec = specOf(was.modelId);
		await rm(partPath(spec.fileName), { force: true });
		if (runtime) await rm(partPath(path.basename(runtime.url)), { force: true });
	};

	const askToDownload = async (id: ModelId) => {
		confirm = { modelId: id, ...(await missingBytes(id)), freeBytes: await freeBytes(paths.root) };
		downloadError = null;
	};

	return {
		status,

		/** Read the settings, clear up after a crashed run, and bring the feature back as it was left. */
		init: () =>
			serial(async () => {
				const read = await readInlineCompletionSettings(paths.stateDir);
				settings = read.settings;
				if (read.migrated) await persist(settings);
				await refreshInstalled();
				await reapOrphan(paths.pidFile, paths.runtimeDir).catch(() => false);
				if (!settings.enabled || unsupported()) {
					emit();
					return;
				}
				if (runtimeInstalled && installed.has(settings.modelId)) {
					await startServer();
					return;
				}
				// Enabled but not installed. A part on disk is a download the person
				// already agreed to that a quit interrupted - resume it. Without one,
				// the files went missing some other way: ask again, with the size.
				const spec = specOf(settings.modelId);
				if ((await partialBytes(partPath(spec.fileName))) > 0) {
					void runDownload(settings.modelId);
					return;
				}
				await askToDownload(settings.modelId);
				emit();
			}),

		/** The switch, turned on. Starts at once when installed; otherwise asks with the size. */
		enable: () =>
			serial(async () => {
				if (unsupported()) return emit();
				await refreshInstalled();
				if (runtimeInstalled && installed.has(settings.modelId)) {
					await persist({ ...settings, enabled: true });
					confirm = null;
					await startServer();
					return emit();
				}
				if (!download) await askToDownload(settings.modelId);
				emit();
			}),

		/** The switch, turned off: stop the server AO started, abandon any download. */
		disable: () =>
			serial(async () => {
				await abortDownload(true);
				confirm = null;
				downloadError = null;
				await persist({ ...settings, enabled: false });
				await stopServer("turned off");
				emit();
			}),

		/** The person said yes to the size they were shown. */
		confirmDownload: () =>
			serial(async () => {
				const pending = confirm;
				if (!pending || download) return;
				if (pending.freeBytes !== null && pending.freeBytes < pending.bytes + diskMargin) {
					downloadError = `Not enough free disk space: this needs ${formatBytes(pending.bytes)} and ${formatBytes(pending.freeBytes)} is free.`;
					return emit();
				}
				confirm = null;
				if (!settings.enabled) await persist({ modelId: pending.modelId, enabled: true });
				void runDownload(pending.modelId);
			}),

		/**
		 * Cancel: the pending question or the running download. When nothing is
		 * left to run (the download WAS the enabling), the switch goes back off.
		 */
		cancelDownload: () =>
			serial(async () => {
				await abortDownload(true);
				confirm = null;
				downloadError = null;
				await refreshInstalled();
				if (!(runtimeInstalled && installed.has(settings.modelId)) || !server) {
					await persist({ ...settings, enabled: false });
					await stopServer("cancelled");
				}
				emit();
			}),

		/** One prediction. Null when the server is not ready, the request was superseded, or it failed. */
		complete: async (requestId: string, request: InfillRequest): Promise<InfillResult | null> => {
			if (serverState !== "ready" || !server || !serverModel || !specOf(serverModel).infill) return null;
			return (await lane.submit(requestId, { kind: "infill", request })) as InfillResult | null;
		},

		/** One next-edit prediction: the window rewritten. Null unless a next-edit model is ready. */
		predictEdit: async (requestId: string, request: NextEditRequest): Promise<NextEditResult | null> => {
			if (serverState !== "ready" || !server || activeKind() !== "next-edit") return null;
			return (await lane.submit(requestId, { kind: "nextEdit", request })) as NextEditResult | null;
		},

		/** The editor moved on. See request-lane.ts for why that rarely means aborting. */
		cancel: (requestId: string) => lane.cancel(requestId),

		/**
		 * App quit. Synchronous where it matters: the SIGTERM to the pid AO started
		 * goes out before this returns, and a download's part is KEPT so the next
		 * launch resumes rather than starting a gigabyte over.
		 */
		dispose: () => {
			disposed = true;
			lane.clear();
			downloadAbort?.abort();
			downloadAbort = null;
			server?.killNow();
			server = null;
		},
	};
}
