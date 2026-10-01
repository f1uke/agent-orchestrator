import { describe, expect, test } from "vitest";
import { CHUNK_LINES, ChunkRing, chunkSimilarity, MAX_CHUNKS } from "./chunk-ring";

const file = (tag: string, n = CHUNK_LINES) => Array.from({ length: n }, (_, i) => `${tag} line ${i} = value${i}`);

describe("ChunkRing", () => {
	test("picked chunks wait in the queue until flush - so typing never changes the extra context", () => {
		const ring = new ChunkRing();
		ring.pick("a.go", file("a"), 0);
		expect(ring.extra()).toEqual([]);
		expect(ring.pending).toBe(1);
		const before = ring.revision;
		expect(ring.flush()).toBe(true);
		expect(ring.revision).toBe(before + 1);
		expect(ring.extra()).toEqual([{ filename: "a.go", text: `${file("a").join("\n")}\n` }]);
		// Nothing queued: nothing changes, nothing to warm up.
		expect(ring.flush()).toBe(false);
	});

	test("a chunk nearly identical to one in the ring replaces it", () => {
		const ring = new ChunkRing();
		ring.pick("a.go", file("a"), 0);
		ring.flush();
		const edited = file("a");
		edited[3] = "a line 3 = changed";
		ring.pick("b.go", edited, 0);
		ring.flush();
		expect(ring.extra().map((c) => c.filename)).toEqual(["b.go"]);
	});

	test("holds at most MAX_CHUNKS, oldest out first", () => {
		const ring = new ChunkRing();
		for (let i = 0; i < MAX_CHUNKS + 2; i++) ring.pick(`f${i}.go`, file(`f${i}`), 0);
		ring.flush();
		const names = ring.extra().map((c) => c.filename);
		expect(names).toHaveLength(MAX_CHUNKS);
		expect(names[0]).toBe("f2.go");
	});

	test("blank or brace-only chunks are not worth their tokens", () => {
		const ring = new ChunkRing();
		ring.pick(
			"a.go",
			Array.from({ length: CHUNK_LINES }, () => "}"),
			0,
		);
		expect(ring.pending).toBe(0);
	});

	test("leaves out a chunk of the current file the prefix/suffix window already carries", () => {
		const ring = new ChunkRing();
		const lines = file("cur");
		ring.pick("cur.go", lines, 0);
		ring.pick("other.go", file("other"), 0);
		ring.flush();
		expect(ring.extra("cur.go", lines).map((c) => c.filename)).toEqual(["other.go"]);
		expect(ring.extra("cur.go", ["unrelated"]).map((c) => c.filename)).toEqual(["cur.go", "other.go"]);
	});

	test("similarity is Jaccard over line sets", () => {
		expect(chunkSimilarity(["a", "b"], ["a", "b"])).toBe(1);
		expect(chunkSimilarity(["a", "b"], ["c"])).toBe(0);
		expect(chunkSimilarity(["a", "b"], ["b", "c"])).toBeCloseTo(1 / 3);
	});
});
