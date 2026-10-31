import { vi } from "vitest";
import type { ResolvedFile } from "../lib/chat-refs-store";

export const image = (path: string, root: ResolvedFile["root"] = "knowledge"): ResolvedFile => ({
	root,
	path,
	kind: "image",
	mime: "image/png",
	size: 100,
	modifiedAt: "2026-10-31T00:00:00Z",
});

const MIME: Record<ResolvedFile["kind"], string> = {
	image: "image/png",
	markdown: "text/markdown; charset=utf-8",
	html: "text/html; charset=utf-8",
	pdf: "application/pdf",
	json: "application/json",
	text: "text/plain; charset=utf-8",
	log: "text/plain; charset=utf-8",
	diff: "text/plain; charset=utf-8",
};

/** A servable file of any kind, as the resolve route answers for it. */
export const file = (
	path: string,
	kind: ResolvedFile["kind"],
	root: ResolvedFile["root"] = "worktree",
	size = 2048,
): ResolvedFile => ({ root, path, kind, mime: MIME[kind], size, modifiedAt: "2026-10-31T00:00:00Z" });

/**
 * Stands in for the daemon's two chat file routes. `known` maps a ref to what the
 * resolve route answers for it; any other ref names nothing. Returns the fetch mock
 * so a test can read which calls were made.
 */
export function stubChatFiles(known: Record<string, ResolvedFile[]>) {
	let objects = 0;
	URL.createObjectURL = vi.fn(() => `blob:file-${++objects}`);
	URL.revokeObjectURL = vi.fn();
	const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
		const url = String(input);
		if (url.endsWith("/chat/files/resolve")) {
			const { refs } = JSON.parse(String(init?.body)) as { refs: string[] };
			return new Response(JSON.stringify({ files: refs.map((ref) => ({ ref, matches: known[ref] ?? [] })) }), {
				status: 200,
			});
		}
		// Bytes, not a jsdom Blob: Node 20's Response (CI) cannot read a jsdom Blob body.
		return new Response(new TextEncoder().encode("png"), { status: 200, headers: { "Content-Type": "image/png" } });
	});
	vi.stubGlobal("fetch", fetchMock);
	return fetchMock;
}

export const resolveCalls = (fetchMock: ReturnType<typeof stubChatFiles>): string[][] =>
	fetchMock.mock.calls
		.filter(([url]) => String(url).endsWith("/chat/files/resolve"))
		.map(([, init]) => (JSON.parse(String(init?.body)) as { refs: string[] }).refs);
