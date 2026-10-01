import type { InfillRequest } from "../../../main/inline-completion/infill";

/**
 * Extra context from around the project: chunks of the files the person has
 * recently opened, saved or jumped around in, sent ahead of the prefix so the
 * model can predict names that are not in the file being edited (llama.vim's
 * "ring buffer").
 *
 * 🗝 THE RING ONLY CHANGES WHEN THE PERSON IS IDLE. The chunks come first in the
 * prompt, so any change to them invalidates the server's KV cache for everything
 * after - which is the whole prompt. New chunks therefore wait in a queue and
 * are moved into the ring by `flush()`, which the provider calls only after a
 * pause in typing and follows with a warm-up request; while someone types, the
 * extra context is byte-for-byte stable and every request reuses the cache.
 */
export const CHUNK_LINES = 64;
/**
 * Ring capacity. 8 x 64 lines is ~5k tokens - inside the ~6.9k the server
 * leaves for extra context at `-c 8192`, so nothing sent is truncated away.
 */
export const MAX_CHUNKS = 8;
/** A queued chunk this similar to a ring chunk replaces it rather than joining it. */
export const SIMILARITY_EVICT = 0.9;
const MAX_QUEUED = 16;

export type Chunk = { filename: string; text: string; lines: readonly string[] };

/** Jaccard similarity of the two chunks' line sets, as llama.vim measures it. */
export function chunkSimilarity(a: readonly string[], b: readonly string[]): number {
	const setA = new Set(a);
	const setB = new Set(b);
	let common = 0;
	for (const line of setA) if (setB.has(line)) common++;
	const union = setA.size + setB.size - common;
	return union === 0 ? 1 : common / union;
}

export class ChunkRing {
	private ring: Chunk[] = [];
	private queue: Chunk[] = [];
	private version = 0;

	/** Bumped on every change to what `extra()` returns. */
	get revision(): number {
		return this.version;
	}

	get pending(): number {
		return this.queue.length;
	}

	/**
	 * Queue the `CHUNK_LINES` lines of `lines` starting at `start` (0-based). A
	 * chunk of nothing but blank lines or braces is not worth its tokens.
	 */
	pick(filename: string, lines: readonly string[], start: number): void {
		const from = Math.max(0, Math.min(start, Math.max(0, lines.length - CHUNK_LINES)));
		const slice = lines.slice(from, from + CHUNK_LINES);
		const meaningful = slice.filter((l) => l.trim().length > 2).length;
		if (meaningful < Math.min(8, slice.length / 2)) return;
		// The same text already waiting or already in the ring is not news.
		const isDuplicate = (c: Chunk) => chunkSimilarity(c.lines, slice) > SIMILARITY_EVICT;
		if (this.queue.some(isDuplicate)) return;
		if (this.ring.some((c) => c.filename === filename && isDuplicate(c))) return;
		this.queue.push({ filename, lines: slice, text: `${slice.join("\n")}\n` });
		if (this.queue.length > MAX_QUEUED) this.queue.shift();
	}

	/** Move the queue into the ring. True when the ring changed (so a warm-up is worth sending). */
	flush(): boolean {
		if (this.queue.length === 0) return false;
		for (const chunk of this.queue.splice(0)) {
			this.ring = this.ring.filter((c) => chunkSimilarity(c.lines, chunk.lines) <= SIMILARITY_EVICT);
			this.ring.push(chunk);
			if (this.ring.length > MAX_CHUNKS) this.ring.shift();
		}
		this.version++;
		return true;
	}

	/**
	 * The ring as `input_extra`, minus chunks of `currentFile` that overlap the
	 * window already sent as prefix/suffix (they would be the same tokens twice).
	 */
	extra(currentFile?: string, window?: readonly string[]): InfillRequest["inputExtra"] {
		const seen = window ? new Set(window) : null;
		const insideWindow = (c: Chunk) => {
			if (!seen || c.filename !== currentFile) return false;
			const own = new Set(c.lines);
			let inside = 0;
			for (const line of own) if (seen.has(line)) inside++;
			return own.size > 0 && inside / own.size > 0.5;
		};
		return this.ring.filter((c) => !insideWindow(c)).map((c) => ({ filename: c.filename, text: c.text }));
	}

	clear(): void {
		this.ring = [];
		this.queue = [];
		this.version++;
	}
}
