import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync, symlinkSync, mkdirSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, test } from "vitest";
import { postInfill } from "./infill";
import { type LlamaServer, type LlamaServerState, probeHealth, reapOrphan, startLlamaServer } from "./llama-server";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FAKE = path.join(HERE, "fake-llama-server.mjs");

const dirs: string[] = [];
const live: LlamaServer[] = [];

function tempDir(): string {
	// Short on purpose: a Unix socket path must fit in 104 bytes.
	const dir = mkdtempSync(path.join(os.tmpdir(), "aoic-"));
	dirs.push(dir);
	return dir;
}

function alive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch {
		return false;
	}
}

function start(env: Record<string, string> = {}, over: Partial<Parameters<typeof startLlamaServer>[0]> = {}) {
	const dir = tempDir();
	const states: { state: LlamaServerState; detail?: string }[] = [];
	const socketPath = path.join(dir, "llama.sock");
	const server = startLlamaServer({
		command: process.execPath,
		args: [FAKE, "--host", socketPath],
		socketPath,
		pidFile: path.join(dir, "llama-server.pid"),
		logFile: path.join(dir, "logs", "llama-server.log"),
		cwd: dir,
		env: { ...process.env, ...env },
		healthPollMs: 20,
		restartBackoffMs: 20,
		killGraceMs: 500,
		onState: (state, detail) => states.push({ state, detail }),
		...over,
	});
	live.push(server);
	const until = async (want: LlamaServerState, timeoutMs = 10_000) => {
		const deadline = Date.now() + timeoutMs;
		while (Date.now() < deadline) {
			if (states.some((s) => s.state === want)) return states;
			await new Promise((r) => setTimeout(r, 20));
		}
		throw new Error(`never reached ${want}: ${JSON.stringify(states)}`);
	};
	return { server, states, dir, socketPath, until };
}

afterEach(async () => {
	await Promise.all(live.splice(0).map((s) => s.stop("test cleanup")));
	for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

describe("startLlamaServer", () => {
	test("reports starting while the model loads and ready once /health answers 200", async () => {
		const { server, states, socketPath, until } = start({ FAKE_LOAD_MS: "200" });
		await until("ready");
		expect(states[0].state).toBe("starting");
		expect(await probeHealth(socketPath)).toBe(200);
		expect(server.pid).toBeGreaterThan(0);
	});

	test("records the pid it started, and stop() ends exactly that process", async () => {
		const { server, dir, until, socketPath } = start();
		await until("ready");
		const pid = server.pid as number;
		const record = JSON.parse(readFileSync(path.join(dir, "llama-server.pid"), "utf8"));
		expect(record.pid).toBe(pid);
		expect(record.command).toBe(process.execPath);

		await server.stop("turned off");
		expect(alive(pid)).toBe(false);
		expect(server.state).toBe("stopped");
		// Nothing left behind for the next start to trip over.
		expect(existsSync(path.join(dir, "llama-server.pid"))).toBe(false);
		expect(existsSync(socketPath)).toBe(false);
	});

	test("a server that ignores SIGTERM is SIGKILLed after the grace period", async () => {
		const { server, until } = start({ FAKE_IGNORE_TERM: "1" }, { killGraceMs: 200 });
		await until("ready");
		const pid = server.pid as number;
		await server.stop("turned off");
		expect(alive(pid)).toBe(false);
	});

	test("a crash is restarted, and the new process becomes ready", async () => {
		const { server, states, until } = start({ FAKE_EXIT_AFTER: "300" }, { maxRestarts: 5 });
		await until("ready");
		const first = server.pid;
		const deadline = Date.now() + 5_000;
		while (Date.now() < deadline && !states.some((s) => s.detail?.includes("restarting"))) {
			await new Promise((r) => setTimeout(r, 20));
		}
		const restart = states.find((s) => s.detail?.includes("restarting"));
		// The reason carries the log's own words, so a person can act on it.
		expect(restart?.detail).toContain("simulated crash");
		const readyAgain = Date.now() + 5_000;
		while (Date.now() < readyAgain && (server.pid === first || server.state !== "ready")) {
			await new Promise((r) => setTimeout(r, 20));
		}
		expect(server.pid).not.toBe(first);
		expect(server.state).toBe("ready");
	});

	test("gives up to failed after too many crashes in the window", async () => {
		const { states, until } = start({ FAKE_EXIT_AFTER: "10", FAKE_LOAD_MS: "5000" }, { maxRestarts: 2 });
		await until("failed");
		const failed = states.find((s) => s.state === "failed");
		expect(failed?.detail).toMatch(/gave up after 2 restarts/);
	});

	test("a model that never loads fails at the deadline, and its process is stopped", async () => {
		const { server, states, until } = start({ FAKE_LOAD_MS: "60000" }, { readyTimeoutMs: 300 });
		const pid = await (async () => {
			while (!server.pid) await new Promise((r) => setTimeout(r, 10));
			return server.pid;
		})();
		await until("failed");
		expect(states.find((s) => s.state === "failed")?.detail).toMatch(/not ready after/);
		const deadline = Date.now() + 3_000;
		while (alive(pid) && Date.now() < deadline) await new Promise((r) => setTimeout(r, 20));
		expect(alive(pid)).toBe(false);
	});

	test("answers /infill over the socket", async () => {
		const { socketPath, until } = start();
		await until("ready");
		const result = await postInfill(
			socketPath,
			{ inputPrefix: "", prompt: "func ma", inputSuffix: "\n", inputExtra: [], nIndent: 0 },
			new AbortController().signal,
		);
		expect(result.content).toBe("<func ma>");
	});

	test("stop() before the spawn has happened never leaves a process behind", async () => {
		const { server } = start();
		await server.stop("changed my mind");
		await new Promise((r) => setTimeout(r, 200));
		expect(server.pid).toBeNull();
		expect(server.state).toBe("stopped");
	});
});

describe("reapOrphan", () => {
	test("stops a process left running from AO's own runtime directory", async () => {
		const dir = tempDir();
		const runtimeDir = path.join(dir, "runtime");
		mkdirSync(path.join(runtimeDir, "llama-b1"), { recursive: true });
		const binary = path.join(runtimeDir, "llama-b1", "llama-server");
		symlinkSync(process.execPath, binary);
		const orphan = spawn(binary, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore" });
		const pidFile = path.join(dir, "llama-server.pid");
		writeFileSync(pidFile, JSON.stringify({ pid: orphan.pid, command: binary, startedAt: Date.now() }));
		await new Promise((r) => setTimeout(r, 200));

		expect(await reapOrphan(pidFile, runtimeDir)).toBe(true);
		expect(alive(orphan.pid as number)).toBe(false);
		expect(existsSync(pidFile)).toBe(false);
	});

	test("never touches a pid that now belongs to something else", async () => {
		const dir = tempDir();
		// A process that is NOT from the runtime dir - e.g. the person's own
		// llama-server or LM Studio, or any program that reused the pid.
		const other = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore" });
		const pidFile = path.join(dir, "llama-server.pid");
		writeFileSync(pidFile, JSON.stringify({ pid: other.pid, command: "/somewhere/llama-server", startedAt: 1 }));
		try {
			expect(await reapOrphan(pidFile, path.join(dir, "runtime"))).toBe(false);
			expect(alive(other.pid as number)).toBe(true);
			// The stale record is dropped so it is not re-examined forever.
			expect(existsSync(pidFile)).toBe(false);
		} finally {
			other.kill("SIGKILL");
		}
	});

	test("a missing or corrupt pid file is nothing to do", async () => {
		const dir = tempDir();
		expect(await reapOrphan(path.join(dir, "absent.pid"), dir)).toBe(false);
		writeFileSync(path.join(dir, "bad.pid"), "{not json");
		expect(await reapOrphan(path.join(dir, "bad.pid"), dir)).toBe(false);
	});
});
