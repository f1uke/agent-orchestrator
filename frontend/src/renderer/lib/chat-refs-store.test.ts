import { describe, expect, it, vi } from "vitest";
import {
	chatFileUrl,
	type ChatFileRef,
	RefStore,
	type ResolvedFile,
	type ResolveFiles,
	resolveOverHttp,
} from "./chat-refs-store";

const file = (path: string): ResolvedFile => ({
	root: "knowledge",
	path,
	kind: "image",
	mime: "image/png",
	size: 10,
	modifiedAt: "2026-10-31T00:00:00Z",
});

const answer =
	(known: Record<string, string[]>): ResolveFiles =>
	async (refs) =>
		refs.map((ref): ChatFileRef => ({ ref, matches: (known[ref] ?? []).map(file) }));

const flush = () => new Promise((r) => setTimeout(r, 0));

describe("RefStore", () => {
	it("asks for every new ref of one tick in a single call and caches each answer", async () => {
		const resolve = vi.fn(answer({ "a.png": ["a.png"], "b-*.png": ["b-1.png", "b-2.png"] }));
		const store = new RefStore(resolve);
		store.ensure(["a.png"]);
		store.ensure(["b-*.png", "gone.png"]);
		await flush();
		expect(resolve).toHaveBeenCalledTimes(1);
		expect(resolve).toHaveBeenCalledWith(["a.png", "b-*.png", "gone.png"]);
		expect(store.matches("a.png")?.map((m) => m.path)).toEqual(["a.png"]);
		expect(store.matches("b-*.png")?.map((m) => m.path)).toEqual(["b-1.png", "b-2.png"]);
		expect(store.matches("gone.png")).toEqual([]);
	});

	it("says a ref is not resolved yet with undefined, not []", () => {
		const store = new RefStore(answer({}));
		store.ensure(["a.png"]);
		expect(store.matches("a.png")).toBeUndefined();
		expect(store.matches("never-asked.png")).toBeUndefined();
	});

	it("never asks again for a ref it holds or is already waiting on", async () => {
		let release: (files: ChatFileRef[]) => void = () => undefined;
		const resolve = vi.fn<ResolveFiles>(() => new Promise((r) => (release = r)));
		const store = new RefStore(resolve);
		store.ensure(["a.png"]);
		await flush();
		store.ensure(["a.png", "b.png"]);
		await flush();
		expect(resolve.mock.calls).toEqual([[["a.png"]], [["b.png"]]]);
		release([{ ref: "a.png", matches: [file("a.png")] }]);
		await flush();
		store.ensure(["a.png"]);
		await flush();
		expect(resolve).toHaveBeenCalledTimes(2);
	});

	it("retries a failed call once, then holds [] and does not throw", async () => {
		const resolve = vi.fn<ResolveFiles>().mockRejectedValue(new Error("down"));
		const store = new RefStore(resolve);
		store.ensure(["a.png"]);
		await flush();
		await flush();
		expect(resolve).toHaveBeenCalledTimes(2);
		expect(store.matches("a.png")).toEqual([]);
		store.ensure(["a.png"]);
		await flush();
		expect(resolve).toHaveBeenCalledTimes(2);
	});

	it("keeps the answer of a retry that succeeds", async () => {
		const resolve = vi
			.fn<ResolveFiles>()
			.mockRejectedValueOnce(new Error("blip"))
			.mockImplementation(answer({ "a.png": ["a.png"] }));
		const store = new RefStore(resolve);
		store.ensure(["a.png"]);
		await flush();
		await flush();
		expect(store.matches("a.png")?.map((m) => m.path)).toEqual(["a.png"]);
	});

	it("holds [] for a ref the answer leaves out", async () => {
		const store = new RefStore(async () => []);
		store.ensure(["a.png"]);
		await flush();
		expect(store.matches("a.png")).toEqual([]);
	});

	it("sends at most 64 refs per call", async () => {
		const resolve = vi.fn(answer({}));
		const store = new RefStore(resolve);
		store.ensure(Array.from({ length: 70 }, (_, i) => `r${i}.png`));
		await flush();
		expect(resolve.mock.calls.map(([refs]) => refs.length)).toEqual([64, 6]);
	});

	it("tells subscribers when an answer lands and hands out a new snapshot", async () => {
		const store = new RefStore(answer({ "a.png": ["a.png"] }));
		const listener = vi.fn();
		const unsubscribe = store.subscribe(listener);
		const before = store.snapshot();
		store.ensure(["a.png"]);
		expect(listener).not.toHaveBeenCalled();
		await flush();
		expect(listener).toHaveBeenCalled();
		expect(store.snapshot()).not.toBe(before);
		expect(store.snapshot()).toBe(store.snapshot());
		unsubscribe();
		listener.mockClear();
		store.ensure(["b.png"]);
		await flush();
		expect(listener).not.toHaveBeenCalled();
	});
});

describe("chatFileUrl", () => {
	it("escapes each path segment and keeps the slashes", () => {
		expect(chatFileUrl("s 1", { root: "knowledge", path: "plans/a b/c#1.png" })).toBe(
			"/api/v1/sessions/s%201/chat/files/knowledge/plans/a%20b/c%231.png",
		);
	});
});

describe("resolveOverHttp", () => {
	it("posts the refs to the session's resolve route and returns the files", async () => {
		const fetchMock = vi.fn(
			async () =>
				new Response(JSON.stringify({ files: [{ ref: "a.png", matches: [file("a.png")] }] }), { status: 200 }),
		);
		vi.stubGlobal("fetch", fetchMock);
		const files = await resolveOverHttp("s1")(["a.png"]);
		expect(fetchMock).toHaveBeenCalledWith(
			"/api/v1/sessions/s1/chat/files/resolve",
			expect.objectContaining({ method: "POST", body: JSON.stringify({ refs: ["a.png"] }) }),
		);
		expect(files.map((f) => f.ref)).toEqual(["a.png"]);
		vi.unstubAllGlobals();
	});

	it("throws on a failed answer, so the store can retry", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => new Response("{}", { status: 500 })),
		);
		await expect(resolveOverHttp("s1")(["a.png"])).rejects.toThrow("resolve 500");
		vi.unstubAllGlobals();
	});
});
