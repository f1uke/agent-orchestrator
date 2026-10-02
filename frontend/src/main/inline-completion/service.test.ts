import { createHash } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, test } from "vitest";
import type { ModelSpec, RuntimeArtifact } from "./catalog";
import type { FetchLike } from "./download";
import {
	createInlineCompletionService,
	type InlineCompletionService,
	type InlineCompletionStatus,
	inlineCompletionPaths,
	serverArgs,
} from "./service";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const FAKE_SERVER = path.join(HERE, "fake-llama-server.mjs");

const sha = (b: Buffer) => createHash("sha256").update(b).digest("hex");
const RUNTIME_BYTES = Buffer.from("pretend tarball");
const SMALL = Buffer.alloc(64_000, 1);
const LARGE = Buffer.alloc(128_000, 2);
const EDIT = Buffer.alloc(32_000, 3);

const RUNTIME: RuntimeArtifact = {
	tag: "b1",
	label: "llama.cpp b1",
	url: "https://example.invalid/llama-b1.tar.gz",
	sizeBytes: RUNTIME_BYTES.length,
	sha256: sha(RUNTIME_BYTES),
};
const MODELS: ModelSpec[] = [
	{
		id: "qwen2.5-coder-1.5b",
		kind: "fim",
		label: "Small",
		fileName: "small.gguf",
		url: "https://example.invalid/small.gguf",
		sizeBytes: SMALL.length,
		sha256: sha(SMALL),
		blurb: "small",
	},
	{
		id: "qwen2.5-coder-3b",
		kind: "fim",
		label: "Large",
		fileName: "large.gguf",
		url: "https://example.invalid/large.gguf",
		sizeBytes: LARGE.length,
		sha256: sha(LARGE),
		blurb: "large",
	},
];

MODELS.push({
	id: "sweep-next-edit-1.5b",
	kind: "next-edit",
	label: "Edit",
	fileName: "edit.gguf",
	url: "https://example.invalid/edit.gguf",
	sizeBytes: EDIT.length,
	sha256: sha(EDIT),
	blurb: "edit",
});

const BODIES: Record<string, Buffer> = {
	[RUNTIME.url]: RUNTIME_BYTES,
	[MODELS[0].url]: SMALL,
	[MODELS[1].url]: LARGE,
	[MODELS[2].url]: EDIT,
};

/** Serves the pinned bytes, slowly enough that a test can cancel half way. */
function fakeFetch(delayMs = 0): { fetchImpl: FetchLike; asked: string[] } {
	const asked: string[] = [];
	const fetchImpl: FetchLike = async (url, init) => {
		asked.push(url);
		const body = BODIES[url];
		const from = Number(/bytes=(\d+)-/.exec(init.headers?.Range ?? "")?.[1] ?? 0);
		async function* stream() {
			for (let i = from; i < body.length; i += 8_192) {
				if (delayMs) await new Promise((r) => setTimeout(r, delayMs));
				init.signal?.throwIfAborted();
				yield new Uint8Array(body.subarray(i, i + 8_192));
			}
		}
		return { ok: true, status: from > 0 ? 206 : 200, body: stream() };
	};
	return { fetchImpl, asked };
}

/**
 * "Unpacking" the pretend tarball lays out what a real one would: a
 * `llama-<tag>/llama-server` executable - here a shell script that execs the
 * fake server, so the pid the service records is the server's own.
 */
async function fakeExtract(_tarball: string, destDir: string): Promise<void> {
	const dir = path.join(destDir, `llama-${RUNTIME.tag}`);
	mkdirSync(dir, { recursive: true });
	const binary = path.join(dir, "llama-server");
	writeFileSync(binary, `#!/bin/sh\nexec "${process.execPath}" "${FAKE_SERVER}" "$@"\n`);
	chmodSync(binary, 0o755);
}

const dirs: string[] = [];
const services: InlineCompletionService[] = [];

afterEach(async () => {
	for (const s of services.splice(0)) {
		await s.disable().catch(() => {});
		s.dispose();
	}
	for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

function setup(
	options: { fetchDelayMs?: number; platform?: NodeJS.Platform; arch?: string; diskMarginBytes?: number } = {},
) {
	const stateDir = mkdtempSync(path.join(os.tmpdir(), "aoic-s-"));
	dirs.push(stateDir);
	const paths = inlineCompletionPaths(stateDir);
	const statuses: InlineCompletionStatus[] = [];
	const fetch = fakeFetch(options.fetchDelayMs);
	const service = createInlineCompletionService({
		paths,
		onStatus: (s) => statuses.push(s),
		platform: options.platform ?? "darwin",
		arch: options.arch ?? "arm64",
		runtime: options.platform && options.platform !== "darwin" ? null : RUNTIME,
		models: MODELS,
		fetchImpl: fetch.fetchImpl,
		extract: fakeExtract,
		serverArgs: (_model, socket) => ["--host", socket],
		diskMarginBytes: options.diskMarginBytes ?? 0,
		progressIntervalMs: 0,
	});
	services.push(service);
	const until = async (pred: (s: InlineCompletionStatus) => boolean, timeoutMs = 10_000) => {
		const deadline = Date.now() + timeoutMs;
		while (Date.now() < deadline) {
			const s = service.status();
			if (pred(s)) return s;
			await new Promise((r) => setTimeout(r, 20));
		}
		throw new Error(`timed out; last status ${JSON.stringify(service.status())}`);
	};
	return { service, statuses, paths, stateDir, fetch, until };
}

function alive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch {
		return false;
	}
}

describe("serverArgs", () => {
	test("a next-edit model runs with n-gram speculative decoding; a FIM model without", () => {
		expect(serverArgs("m.gguf", "s.sock", "next-edit")).toEqual(expect.arrayContaining(["--spec-type"]));
		expect(serverArgs("m.gguf", "s.sock", "fim")).not.toContain("--spec-type");
	});
});

describe("inline completion service", () => {
	test("a next-edit model is asked for rewrites, and only for rewrites", async () => {
		const { service, until } = setup();
		await service.init();
		await service.selectModel("sweep-next-edit-1.5b");
		await service.enable();
		await service.confirmDownload();
		const ready = await until((s) => s.server === "ready");
		expect(ready.activeKind).toBe("next-edit");

		const request = {
			path: "a.ts",
			recent: [{ path: "a.ts", original: "let OLD = 1", updated: "let NEW = 1" }],
			original: "let OLD = 1\nuse(OLD)",
			current: "let NEW = 1\nuse(OLD)",
		};
		const answer = await service.predictEdit("e1", request);
		expect(answer?.window).toBe("let NEW = 1\nuse(NEW)");
		// The fill-in-the-middle path answers nothing while a next-edit model runs.
		expect(
			await service.complete("c1", { inputPrefix: "", prompt: "x", inputSuffix: "", inputExtra: [], nIndent: 0 }),
		).toBeNull();

		await service.selectModel("qwen2.5-coder-1.5b");
		await service.confirmDownload();
		const fim = await until((s) => s.server === "ready" && s.activeKind === "fim");
		expect(fim.modelId).toBe("qwen2.5-coder-1.5b");
		expect(await service.predictEdit("e2", request)).toBeNull();
	});

	test("enable on a clean state asks first - with the size - and downloads nothing", async () => {
		const { service, fetch } = setup();
		await service.init();
		await service.enable();
		const s = service.status();
		expect(s.confirm).toEqual({
			modelId: "qwen2.5-coder-1.5b",
			bytes: RUNTIME_BYTES.length + SMALL.length,
			runtimeBytes: RUNTIME_BYTES.length,
			freeBytes: expect.any(Number),
		});
		expect(s.enabled).toBe(false);
		expect(s.server).toBe("off");
		expect(fetch.asked).toHaveLength(0);
	});

	test("confirm -> download with progress -> starting -> ready; disable stops exactly that pid", async () => {
		const { service, statuses, paths, stateDir, until } = setup();
		await service.init();
		await service.enable();
		await service.confirmDownload();
		const ready = await until((s) => s.server === "ready");
		const pid = ready.pid as number;
		expect(pid).toBeGreaterThan(0);
		expect(alive(pid)).toBe(true);
		expect(ready.models.find((m) => m.id === "qwen2.5-coder-1.5b")?.installed).toBe(true);
		// Progress was reported across BOTH files, in one bar that ends full.
		const downloads = statuses.map((s) => s.download).filter((d) => d !== null);
		expect(downloads.at(-1)?.receivedBytes).toBe(RUNTIME_BYTES.length + SMALL.length);
		expect(downloads.every((d) => d?.totalBytes === RUNTIME_BYTES.length + SMALL.length)).toBe(true);
		// Everything under the state dir.
		expect(existsSync(path.join(paths.modelsDir, "small.gguf"))).toBe(true);
		expect(JSON.parse(readFileSync(path.join(stateDir, "inline-completion.json"), "utf8"))).toEqual({
			enabled: true,
			modelId: "qwen2.5-coder-1.5b",
		});

		const answer = await service.complete("r1", {
			inputPrefix: "",
			prompt: "func ma",
			inputSuffix: "\n",
			inputExtra: [],
			nIndent: 0,
		});
		expect(answer?.content).toBe("<func ma>");

		await service.disable();
		expect(alive(pid)).toBe(false);
		expect(service.status()).toMatchObject({ enabled: false, server: "off", pid: null });
		// A prediction asked for now gets nothing, quietly.
		expect(
			await service.complete("r2", { inputPrefix: "", prompt: "x", inputSuffix: "", inputExtra: [], nIndent: 0 }),
		).toBeNull();
	});

	test("installing a runtime clears out one from an earlier pin", async () => {
		const { service, paths, until } = setup();
		mkdirSync(path.join(paths.runtimeDir, "llama-b0"), { recursive: true });
		await service.init();
		await service.enable();
		await service.confirmDownload();
		await until((s) => s.server === "ready");
		expect(existsSync(path.join(paths.runtimeDir, "llama-b0"))).toBe(false);
		expect(existsSync(path.join(paths.runtimeDir, `llama-${RUNTIME.tag}`, "llama-server"))).toBe(true);
	});

	test("re-enabling with the files present starts at once, with no question", async () => {
		const { service, fetch, until } = setup();
		await service.init();
		await service.enable();
		await service.confirmDownload();
		await until((s) => s.server === "ready");
		await service.disable();
		const asked = fetch.asked.length;
		await service.enable();
		expect(service.status().confirm).toBeNull();
		await until((s) => s.server === "ready");
		expect(fetch.asked).toHaveLength(asked);
	});

	test("cancel during the download discards the part and turns the switch back off", async () => {
		const { service, paths, until } = setup({ fetchDelayMs: 20 });
		await service.init();
		await service.enable();
		await service.confirmDownload();
		await until((s) => (s.download?.receivedBytes ?? 0) > RUNTIME_BYTES.length);
		await service.cancelDownload();
		const s = service.status();
		expect(s).toMatchObject({ enabled: false, server: "off", download: null, confirm: null });
		expect(existsSync(path.join(paths.downloadsDir, "small.gguf.part"))).toBe(false);
		expect(existsSync(path.join(paths.modelsDir, "small.gguf"))).toBe(false);
	});

	test("switching to a model that is not downloaded asks first and keeps the current one serving", async () => {
		const { service, until } = setup();
		await service.init();
		await service.enable();
		await service.confirmDownload();
		const first = await until((s) => s.server === "ready");

		await service.selectModel("qwen2.5-coder-3b");
		let s = service.status();
		expect(s.confirm).toMatchObject({ modelId: "qwen2.5-coder-3b", bytes: LARGE.length });
		expect(s.modelId).toBe("qwen2.5-coder-1.5b");
		expect(s.server).toBe("ready");
		expect(s.pid).toBe(first.pid);

		// Cancelling the question leaves everything as it was.
		await service.cancelDownload();
		s = service.status();
		expect(s).toMatchObject({ enabled: true, server: "ready", modelId: "qwen2.5-coder-1.5b", pid: first.pid });

		await service.selectModel("qwen2.5-coder-3b");
		await service.confirmDownload();
		const switched = await until((x) => x.modelId === "qwen2.5-coder-3b" && x.server === "ready");
		expect(switched.pid).not.toBe(first.pid);
		expect(alive(first.pid as number)).toBe(false);
	});

	test("a removed model frees its file; the running one cannot be removed", async () => {
		const { service, paths, until } = setup();
		await service.init();
		await service.enable();
		await service.confirmDownload();
		await until((s) => s.server === "ready");
		await service.removeModel("qwen2.5-coder-1.5b");
		expect(existsSync(path.join(paths.modelsDir, "small.gguf"))).toBe(true);
		await service.disable();
		await service.removeModel("qwen2.5-coder-1.5b");
		expect(existsSync(path.join(paths.modelsDir, "small.gguf"))).toBe(false);
	});

	test("not enough disk space is said before a byte is fetched", async () => {
		const { service, fetch } = setup({ diskMarginBytes: Number.MAX_SAFE_INTEGER / 2 });
		await service.init();
		await service.enable();
		await service.confirmDownload();
		expect(service.status().downloadError).toMatch(/Not enough free disk space/);
		expect(fetch.asked).toHaveLength(0);
	});

	test("a platform without a pinned runtime says so and never offers a download", async () => {
		const { service } = setup({ platform: "linux", arch: "x64" });
		await service.init();
		await service.enable();
		const s = service.status();
		expect(s.unsupported).toMatch(/Apple silicon/);
		expect(s.confirm).toBeNull();
	});

	test("the app coming back starts the server it left running, from the saved settings", async () => {
		const first = setup();
		await first.service.init();
		await first.service.enable();
		await first.service.confirmDownload();
		await first.until((s) => s.server === "ready");
		const oldPid = first.service.status().pid as number;
		// A quit: the process goes, the settings and files stay.
		first.service.dispose();
		const deadline = Date.now() + 3_000;
		while (alive(oldPid) && Date.now() < deadline) await new Promise((r) => setTimeout(r, 20));
		expect(alive(oldPid)).toBe(false);

		const again = createInlineCompletionService({
			paths: first.paths,
			onStatus: () => {},
			platform: "darwin",
			arch: "arm64",
			runtime: RUNTIME,
			models: MODELS,
			fetchImpl: first.fetch.fetchImpl,
			extract: fakeExtract,
			serverArgs: (_model, socket) => ["--host", socket],
		});
		services.push(again);
		const asked = first.fetch.asked.length;
		await again.init();
		const deadline2 = Date.now() + 10_000;
		while (again.status().server !== "ready" && Date.now() < deadline2) await new Promise((r) => setTimeout(r, 20));
		expect(again.status().server).toBe("ready");
		expect(first.fetch.asked).toHaveLength(asked);
	});
});
