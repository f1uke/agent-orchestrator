import { createHash, type Hash } from "node:crypto";
import { createReadStream } from "node:fs";
import { mkdir, open, rename, rm, stat, statfs } from "node:fs/promises";
import path from "node:path";
import type { DownloadArtifact } from "./catalog";

export type FetchLike = (
	url: string,
	init: { headers?: Record<string, string>; signal?: AbortSignal; redirect?: "follow" },
) => Promise<{
	ok: boolean;
	status: number;
	body: AsyncIterable<Uint8Array> | null;
}>;

export type DownloadOptions = {
	artifact: DownloadArtifact;
	/** Where the verified file ends up. Only ever written by a rename. */
	dest: string;
	/** Where bytes land while they arrive. Kept across an app quit so the next run resumes. */
	partPath: string;
	signal: AbortSignal;
	onProgress: (receivedBytes: number, totalBytes: number) => void;
	fetchImpl?: FetchLike;
	/** Transient failures retried, resuming from the bytes already on disk. */
	maxAttempts?: number;
	retryDelayMs?: number;
};

export class ChecksumError extends Error {}

/**
 * Download `artifact` to `dest`, verified, resumable, cancellable.
 *
 * 🗝 The file only appears at `dest` once its size AND sha256 match the pin. A
 * half-written or tampered model therefore cannot exist under `models/`, so
 * "is it installed" is a plain existence check everywhere else.
 *
 * Resuming is not an optimisation here: a model is up to 8 GB, and a dropped
 * connection at 90% that starts over is the difference between a feature that
 * installs and one that does not. An existing `.part` is re-hashed first (the
 * hash has to cover every byte) and the rest is asked for with a Range; a server
 * that ignores the Range and answers 200 simply starts the file over.
 */
export async function downloadVerified(options: DownloadOptions): Promise<void> {
	const { artifact, dest, partPath, signal, onProgress } = options;
	const fetchImpl = options.fetchImpl ?? (globalThis.fetch as unknown as FetchLike);
	const maxAttempts = options.maxAttempts ?? 4;
	const retryDelayMs = options.retryDelayMs ?? 1_000;
	await mkdir(path.dirname(partPath), { recursive: true, mode: 0o750 });
	await mkdir(path.dirname(dest), { recursive: true, mode: 0o750 });

	let attempt = 0;
	for (;;) {
		signal.throwIfAborted();
		attempt += 1;
		try {
			await downloadOnce(artifact, partPath, signal, onProgress, fetchImpl);
			break;
		} catch (err) {
			if (signal.aborted || err instanceof ChecksumError || attempt >= maxAttempts) throw err;
			await delay(retryDelayMs * attempt, signal);
		}
	}

	const hash = await hashFile(partPath, signal);
	const size = (await stat(partPath)).size;
	if (size !== artifact.sizeBytes || hash !== artifact.sha256) {
		await rm(partPath, { force: true });
		throw new ChecksumError(
			`${artifact.label} did not match its pinned checksum (got ${size} bytes, sha256 ${hash.slice(0, 12)}…); the download was discarded`,
		);
	}
	await rename(partPath, dest);
}

async function downloadOnce(
	artifact: DownloadArtifact,
	partPath: string,
	signal: AbortSignal,
	onProgress: (receivedBytes: number, totalBytes: number) => void,
	fetchImpl: FetchLike,
): Promise<void> {
	let have = await sizeOf(partPath);
	if (have > artifact.sizeBytes) {
		await rm(partPath, { force: true });
		have = 0;
	}
	if (have === artifact.sizeBytes) {
		onProgress(have, artifact.sizeBytes);
		return;
	}
	const headers: Record<string, string> = have > 0 ? { Range: `bytes=${have}-` } : {};
	const res = await fetchImpl(artifact.url, { headers, signal, redirect: "follow" });
	if (!res.ok || !res.body) throw new Error(`${artifact.label}: download failed (HTTP ${res.status})`);
	// 206 continues the part; anything else is the whole file from byte zero.
	const resumed = have > 0 && res.status === 206;
	if (!resumed) have = 0;
	const handle = await open(partPath, resumed ? "a" : "w", 0o600);
	try {
		let received = have;
		onProgress(received, artifact.sizeBytes);
		for await (const chunk of res.body) {
			signal.throwIfAborted();
			await handle.write(chunk);
			received += chunk.byteLength;
			if (received > artifact.sizeBytes) {
				throw new ChecksumError(`${artifact.label}: the server sent more bytes than the pinned size`);
			}
			onProgress(received, artifact.sizeBytes);
		}
		if (received !== artifact.sizeBytes) {
			throw new Error(`${artifact.label}: the connection closed at ${received} of ${artifact.sizeBytes} bytes`);
		}
	} finally {
		await handle.close();
	}
}

async function hashFile(file: string, signal: AbortSignal): Promise<string> {
	const hash: Hash = createHash("sha256");
	for await (const chunk of createReadStream(file, { highWaterMark: 1 << 20 })) {
		signal.throwIfAborted();
		hash.update(chunk as Buffer);
	}
	return hash.digest("hex");
}

async function sizeOf(file: string): Promise<number> {
	try {
		return (await stat(file)).size;
	} catch {
		return 0;
	}
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
	return new Promise((resolve, reject) => {
		const timer = setTimeout(() => {
			signal.removeEventListener("abort", onAbort);
			resolve();
		}, ms);
		const onAbort = () => {
			clearTimeout(timer);
			reject(signal.reason);
		};
		signal.addEventListener("abort", onAbort, { once: true });
	});
}

/** Bytes free on the volume holding `dir` (or its nearest existing ancestor). */
export async function freeBytes(dir: string): Promise<number | null> {
	let probe = dir;
	for (;;) {
		try {
			const fs = await statfs(probe);
			return Number(fs.bavail) * Number(fs.bsize);
		} catch {
			const parent = path.dirname(probe);
			if (parent === probe) return null;
			probe = parent;
		}
	}
}

/** Bytes already on disk toward `artifact` (a resumable `.part`), for the size shown before a download. */
export async function partialBytes(partPath: string): Promise<number> {
	return sizeOf(partPath);
}
