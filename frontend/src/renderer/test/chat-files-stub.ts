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
		return new Response(new Blob(["png"], { type: "image/png" }), { status: 200 });
	});
	vi.stubGlobal("fetch", fetchMock);
	return fetchMock;
}

export const resolveCalls = (fetchMock: ReturnType<typeof stubChatFiles>): string[][] =>
	fetchMock.mock.calls
		.filter(([url]) => String(url).endsWith("/chat/files/resolve"))
		.map(([, init]) => (JSON.parse(String(init?.body)) as { refs: string[] }).refs);
