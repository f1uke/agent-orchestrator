import { type ChildProcess, execFile, spawn } from "node:child_process";
import { closeSync, openSync, statSync, truncateSync, unlinkSync } from "node:fs";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import path from "node:path";

/**
 * One supervised llama-server, started and stopped by this app and nothing else.
 *
 * 🗝 OWNERSHIP IS BY PID, NEVER BY NAME. The person may well be running LM Studio
 * or their own llama-server; this module only ever signals the pid it spawned,
 * and the one recovery path that signals a pid it did not spawn THIS run (an
 * orphan left by an app crash, see `reapOrphan`) checks that the pid is still
 * running a binary from AO's own runtime directory first.
 */
export type LlamaServerState = "starting" | "ready" | "failed" | "stopped";

export type LlamaServerOptions = {
	command: string;
	args: string[];
	/** The Unix socket the server listens on (`--host <path>.sock`). */
	socketPath: string;
	pidFile: string;
	logFile: string;
	cwd: string;
	env: NodeJS.ProcessEnv;
	/** Model load can take a while for a cold 8 GB file; a hang must still end. */
	readyTimeoutMs?: number;
	killGraceMs?: number;
	/** Restarts allowed inside `restartWindowMs` before giving up to `failed`. */
	maxRestarts?: number;
	restartWindowMs?: number;
	restartBackoffMs?: number;
	healthPollMs?: number;
	onState: (state: LlamaServerState, detail?: string) => void;
};

export type LlamaServer = {
	readonly pid: number | null;
	readonly state: LlamaServerState;
	/** Resolves once the process has exited (or was never running). */
	stop(why: string): Promise<void>;
	/** Synchronous SIGTERM for the `exit` handler, where nothing can be awaited. */
	killNow(): void;
};

const DEFAULT_READY_TIMEOUT_MS = 180_000;
const DEFAULT_KILL_GRACE_MS = 3_000;
const DEFAULT_MAX_RESTARTS = 3;
const DEFAULT_RESTART_WINDOW_MS = 60_000;
const DEFAULT_RESTART_BACKOFF_MS = 1_000;
const DEFAULT_HEALTH_POLL_MS = 100;
/** A log larger than this is started over rather than appended to. */
const MAX_LOG_BYTES = 2 << 20;

/** macOS `sun_path` is 104 bytes including the terminator. */
export const MAX_SOCKET_PATH_BYTES = 103;

type PidRecord = { pid: number; command: string; startedAt: number };

/** GET /health: 200 once the model is loaded, 503 while it loads, refused before it listens. */
export function probeHealth(socketPath: string, timeoutMs = 1_000): Promise<number> {
	return new Promise((resolve) => {
		const req = http.request({ socketPath, path: "/health", method: "GET", timeout: timeoutMs }, (res) => {
			res.resume();
			resolve(res.statusCode ?? 0);
		});
		req.on("timeout", () => req.destroy());
		req.on("error", () => resolve(0));
		req.end();
	});
}

/** The last few meaningful lines of the server's log, for a `failed` reason a person can act on. */
async function logTail(logFile: string, lines = 4): Promise<string> {
	try {
		const text = await readFile(logFile, "utf8");
		const tail = text
			.split("\n")
			.map((l) => l.trim())
			.filter(Boolean)
			.slice(-200);
		const errors = tail.filter((l) => /error|failed|unable|cannot|abort/i.test(l));
		return (errors.length > 0 ? errors : tail).slice(-lines).join(" | ");
	} catch {
		return "";
	}
}

export function startLlamaServer(options: LlamaServerOptions): LlamaServer {
	const readyTimeoutMs = options.readyTimeoutMs ?? DEFAULT_READY_TIMEOUT_MS;
	const killGraceMs = options.killGraceMs ?? DEFAULT_KILL_GRACE_MS;
	const maxRestarts = options.maxRestarts ?? DEFAULT_MAX_RESTARTS;
	const restartWindowMs = options.restartWindowMs ?? DEFAULT_RESTART_WINDOW_MS;
	const restartBackoffMs = options.restartBackoffMs ?? DEFAULT_RESTART_BACKOFF_MS;
	const healthPollMs = options.healthPollMs ?? DEFAULT_HEALTH_POLL_MS;

	let state: LlamaServerState = "starting";
	let child: ChildProcess | null = null;
	let stopping: Promise<void> | null = null;
	let restartTimer: ReturnType<typeof setTimeout> | null = null;
	let healthTimer: ReturnType<typeof setTimeout> | null = null;
	const restarts: number[] = [];

	const setState = (next: LlamaServerState, detail?: string) => {
		state = next;
		options.onState(next, detail);
	};

	const clearTimers = () => {
		if (restartTimer) clearTimeout(restartTimer);
		if (healthTimer) clearTimeout(healthTimer);
		restartTimer = null;
		healthTimer = null;
	};

	const removePidFile = async (pid: number | undefined) => {
		try {
			const record = JSON.parse(await readFile(options.pidFile, "utf8")) as PidRecord;
			if (record.pid !== pid) return;
		} catch {
			return;
		}
		await rm(options.pidFile, { force: true });
	};

	const watchHealth = (proc: ChildProcess, startedAt: number) => {
		const tick = async () => {
			healthTimer = null;
			if (child !== proc || stopping) return;
			const status = await probeHealth(options.socketPath);
			if (child !== proc || stopping) return;
			if (status === 200) {
				setState("ready");
				return;
			}
			if (Date.now() - startedAt > readyTimeoutMs) {
				const tail = await logTail(options.logFile);
				setState(
					"failed",
					`llama-server was not ready after ${Math.round(readyTimeoutMs / 1000)}s${tail ? `: ${tail}` : ""}`,
				);
				signal(proc, "SIGTERM");
				return;
			}
			healthTimer = setTimeout(() => void tick(), healthPollMs);
		};
		healthTimer = setTimeout(() => void tick(), healthPollMs);
	};

	const launch = async () => {
		if (stopping) return;
		await mkdir(path.dirname(options.socketPath), { recursive: true, mode: 0o700 });
		await mkdir(path.dirname(options.logFile), { recursive: true, mode: 0o750 });
		// A socket file left by a previous run makes the bind fail.
		await rm(options.socketPath, { force: true });
		let logFd: number;
		try {
			if ((statSync(options.logFile, { throwIfNoEntry: false })?.size ?? 0) > MAX_LOG_BYTES) {
				truncateSync(options.logFile, 0);
			}
			logFd = openSync(options.logFile, "a", 0o600);
		} catch (err) {
			setState("failed", `cannot open the llama-server log: ${err instanceof Error ? err.message : String(err)}`);
			return;
		}
		// stop() may have been called while the awaits above were pending; spawning
		// now would start a process nothing is left holding.
		if (stopping) {
			closeSync(logFd);
			return;
		}
		let proc: ChildProcess;
		try {
			proc = spawn(options.command, options.args, {
				cwd: options.cwd,
				env: options.env,
				stdio: ["ignore", logFd, logFd],
			});
		} catch (err) {
			closeSync(logFd);
			setState("failed", `llama-server: ${err instanceof Error ? err.message : String(err)}`);
			return;
		}
		// The child holds its own copy; ours was only needed for the spawn.
		closeSync(logFd);
		child = proc;
		const startedAt = Date.now();
		setState("starting", restarts.length > 0 ? `restarting (attempt ${restarts.length})` : undefined);
		if (proc.pid) {
			const record: PidRecord = { pid: proc.pid, command: options.command, startedAt };
			await writeFile(options.pidFile, `${JSON.stringify(record)}\n`, { mode: 0o600 }).catch(() => {});
		}
		proc.once("error", (err) => {
			if (child !== proc) return;
			clearTimers();
			child = null;
			setState("failed", `llama-server: ${err.message}`);
		});
		proc.once("exit", (code, sig) => {
			void removePidFile(proc.pid);
			if (child !== proc) return;
			child = null;
			clearTimers();
			if (stopping) return;
			void onUnexpectedExit(code, sig);
		});
		watchHealth(proc, startedAt);
	};

	const onUnexpectedExit = async (code: number | null, sig: NodeJS.Signals | null) => {
		const tail = await logTail(options.logFile);
		const why = `llama-server exited (${sig ?? `code ${code}`})${tail ? `: ${tail}` : ""}`;
		if (stopping) return;
		const now = Date.now();
		while (restarts.length > 0 && now - restarts[0] > restartWindowMs) restarts.shift();
		if (restarts.length >= maxRestarts) {
			setState("failed", `${why} - gave up after ${maxRestarts} restarts in ${Math.round(restartWindowMs / 1000)}s`);
			return;
		}
		restarts.push(now);
		setState("starting", `${why} - restarting`);
		restartTimer = setTimeout(() => {
			restartTimer = null;
			void launch();
		}, restartBackoffMs * restarts.length);
	};

	function signal(proc: ChildProcess, sig: NodeJS.Signals) {
		try {
			proc.kill(sig);
		} catch {
			// already gone
		}
	}

	async function stop(why: string): Promise<void> {
		if (stopping) return stopping;
		clearTimers();
		const proc = child;
		stopping = new Promise<void>((resolve) => {
			if (!proc || proc.exitCode !== null || proc.signalCode !== null) {
				child = null;
				void rm(options.socketPath, { force: true }).finally(() => {
					setState("stopped", why);
					resolve();
				});
				return;
			}
			const hard = setTimeout(() => signal(proc, "SIGKILL"), killGraceMs);
			proc.once("exit", () => {
				clearTimeout(hard);
				child = null;
				void Promise.all([removePidFile(proc.pid), rm(options.socketPath, { force: true })]).finally(() => {
					setState("stopped", why);
					resolve();
				});
			});
			signal(proc, "SIGTERM");
		});
		return stopping;
	}

	void launch();

	return {
		get pid() {
			return child?.pid ?? null;
		},
		get state() {
			return state;
		},
		stop,
		killNow() {
			clearTimers();
			const proc = child;
			if (!proc || proc.exitCode !== null) return;
			signal(proc, "SIGTERM");
			for (const file of [options.pidFile, options.socketPath]) {
				try {
					unlinkSync(file);
				} catch {
					// already gone; and a pid file left behind is reaped by pid next launch
				}
			}
		},
	};
}

/** `ps -o command=` for one pid, or null when it is not running. */
function commandOf(pid: number): Promise<string | null> {
	return new Promise((resolve) =>
		execFile("ps", ["-p", String(pid), "-o", "command="], (err, stdout) => {
			const text = String(stdout ?? "").trim();
			resolve(err || !text ? null : text);
		}),
	);
}

function alive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch {
		return false;
	}
}

/**
 * Stop a llama-server THIS APP started in an earlier run that never got to stop
 * it (a crash, a force-quit). Called before every start.
 *
 * 🗝 The pid file alone is not proof: pids are reused. The process is signalled
 * only if it is still running a binary under `runtimeDir` - AO's own unpacked
 * runtime, which nothing else on the machine executes from. A pid that now
 * belongs to anything else is left alone and the stale file is dropped.
 */
export async function reapOrphan(
	pidFile: string,
	runtimeDir: string,
	graceMs = DEFAULT_KILL_GRACE_MS,
): Promise<boolean> {
	let record: PidRecord;
	try {
		record = JSON.parse(await readFile(pidFile, "utf8")) as PidRecord;
	} catch {
		return false;
	}
	const ours = runtimeDir.endsWith(path.sep) ? runtimeDir : `${runtimeDir}${path.sep}`;
	const command = Number.isInteger(record.pid) && record.pid > 1 ? await commandOf(record.pid) : null;
	if (!command || !command.startsWith(ours)) {
		await rm(pidFile, { force: true });
		return false;
	}
	try {
		process.kill(record.pid, "SIGTERM");
	} catch {
		await rm(pidFile, { force: true });
		return false;
	}
	const deadline = Date.now() + graceMs;
	while (alive(record.pid) && Date.now() < deadline) await new Promise((r) => setTimeout(r, 50));
	if (alive(record.pid)) {
		try {
			process.kill(record.pid, "SIGKILL");
		} catch {
			// gone between the check and the signal
		}
	}
	await rm(pidFile, { force: true });
	return true;
}
