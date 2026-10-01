import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, test } from "vitest";
import type { DownloadArtifact } from "./catalog";
import { ChecksumError, downloadVerified, type FetchLike } from "./download";

const dirs: string[] = [];
afterEach(() => {
	for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

const PAYLOAD = Buffer.from(Array.from({ length: 200_000 }, (_, i) => i % 251));

function artifact(payload = PAYLOAD): DownloadArtifact {
	return {
		label: "test model",
		url: "https://example.invalid/model.gguf",
		sizeBytes: payload.length,
		sha256: createHash("sha256").update(payload).digest("hex"),
	};
}

async function* chunks(buf: Buffer, size = 16_384, failAt?: number) {
	for (let i = 0; i < buf.length; i += size) {
		if (failAt !== undefined && i >= failAt) throw new Error("connection reset");
		yield new Uint8Array(buf.subarray(i, i + size));
	}
}

/** A server that honours Range unless told otherwise, and records what it was asked. */
function fakeFetch(options: { payload?: Buffer; ignoreRange?: boolean; failFirstAt?: number; status?: number } = {}) {
	const payload = options.payload ?? PAYLOAD;
	const calls: { range?: string }[] = [];
	let failAt = options.failFirstAt;
	const fetchImpl: FetchLike = async (_url, init) => {
		const range = init.headers?.Range;
		calls.push({ range });
		if (options.status) return { ok: false, status: options.status, body: null };
		const from = range && !options.ignoreRange ? Number(/bytes=(\d+)-/.exec(range)?.[1] ?? 0) : 0;
		const fail = failAt;
		failAt = undefined;
		return {
			ok: true,
			status: from > 0 ? 206 : 200,
			body: chunks(payload.subarray(from), 16_384, fail === undefined ? undefined : fail - from),
		};
	};
	return { fetchImpl, calls };
}

function paths() {
	const dir = mkdtempSync(path.join(os.tmpdir(), "aoic-dl-"));
	dirs.push(dir);
	return { dest: path.join(dir, "models", "model.gguf"), partPath: path.join(dir, "downloads", "model.gguf.part") };
}

describe("downloadVerified", () => {
	test("writes the file only once size and sha256 match, and reports progress to the end", async () => {
		const { dest, partPath } = paths();
		const progress: number[] = [];
		await downloadVerified({
			artifact: artifact(),
			dest,
			partPath,
			signal: new AbortController().signal,
			fetchImpl: fakeFetch().fetchImpl,
			onProgress: (received) => progress.push(received),
		});
		expect(readFileSync(dest).equals(PAYLOAD)).toBe(true);
		expect(existsSync(partPath)).toBe(false);
		expect(progress.at(-1)).toBe(PAYLOAD.length);
	});

	test("resumes a part left by an earlier run with a Range request", async () => {
		const { dest, partPath } = paths();
		mkdirSync(path.dirname(partPath), { recursive: true });
		writeFileSync(partPath, PAYLOAD.subarray(0, 50_000));
		const fake = fakeFetch();
		await downloadVerified({
			artifact: artifact(),
			dest,
			partPath,
			signal: new AbortController().signal,
			fetchImpl: fake.fetchImpl,
			onProgress: () => {},
		});
		expect(fake.calls[0].range).toBe("bytes=50000-");
		expect(readFileSync(dest).equals(PAYLOAD)).toBe(true);
	});

	test("a server that ignores Range starts the file over instead of corrupting it", async () => {
		const { dest, partPath } = paths();
		mkdirSync(path.dirname(partPath), { recursive: true });
		writeFileSync(partPath, PAYLOAD.subarray(0, 50_000));
		await downloadVerified({
			artifact: artifact(),
			dest,
			partPath,
			signal: new AbortController().signal,
			fetchImpl: fakeFetch({ ignoreRange: true }).fetchImpl,
			onProgress: () => {},
		});
		expect(readFileSync(dest).equals(PAYLOAD)).toBe(true);
	});

	test("a dropped connection is retried from where it stopped", async () => {
		const { dest, partPath } = paths();
		const fake = fakeFetch({ failFirstAt: 98_304 });
		await downloadVerified({
			artifact: artifact(),
			dest,
			partPath,
			signal: new AbortController().signal,
			fetchImpl: fake.fetchImpl,
			onProgress: () => {},
			retryDelayMs: 1,
		});
		expect(fake.calls).toHaveLength(2);
		expect(fake.calls[1].range).toBe("bytes=98304-");
		expect(readFileSync(dest).equals(PAYLOAD)).toBe(true);
	});

	test("a checksum mismatch discards the bytes and never creates the destination", async () => {
		const { dest, partPath } = paths();
		const tampered = Buffer.from(PAYLOAD);
		tampered[1234] ^= 0xff;
		await expect(
			downloadVerified({
				artifact: artifact(),
				dest,
				partPath,
				signal: new AbortController().signal,
				fetchImpl: fakeFetch({ payload: tampered }).fetchImpl,
				onProgress: () => {},
			}),
		).rejects.toBeInstanceOf(ChecksumError);
		expect(existsSync(dest)).toBe(false);
		expect(existsSync(partPath)).toBe(false);
	});

	test("an HTTP error names the artifact and the status", async () => {
		const { dest, partPath } = paths();
		await expect(
			downloadVerified({
				artifact: artifact(),
				dest,
				partPath,
				signal: new AbortController().signal,
				fetchImpl: fakeFetch({ status: 404 }).fetchImpl,
				onProgress: () => {},
				maxAttempts: 1,
			}),
		).rejects.toThrow(/test model: download failed \(HTTP 404\)/);
	});

	test("cancelling stops at once and keeps the part for a later resume", async () => {
		const { dest, partPath } = paths();
		const ac = new AbortController();
		const promise = downloadVerified({
			artifact: artifact(),
			dest,
			partPath,
			signal: ac.signal,
			fetchImpl: fakeFetch().fetchImpl,
			onProgress: (received) => {
				if (received >= 32_768) ac.abort();
			},
		});
		await expect(promise).rejects.toBeDefined();
		expect(existsSync(dest)).toBe(false);
		expect(statSync(partPath).size).toBeGreaterThan(0);
		expect(statSync(partPath).size).toBeLessThan(PAYLOAD.length);
	});
});
