import { createElement } from "react";
import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { useReplyToThread, useResolveThread } from "./useThreadActions";
import type { PRCommentGroup } from "./useSessionPRComments";

const CACHE_KEY = ["session-pr-comments", "s1"];

function seedGroups(): PRCommentGroup[] {
	return [
		{
			prUrl: "https://gh/pr/1",
			headSha: "abc123",
			htmlUrl: "https://gh/pr/1",
			number: 1,
			provider: "github",
			threads: [
				{
					threadId: "T1",
					resolved: false,
					isBot: false,
					line: 10,
					path: "src/foo.ts",
					comments: [
						{
							id: "c1",
							author: "alice",
							body: "please fix this",
							url: "https://gh/pr/1#c1",
							resolved: false,
							isBot: false,
							system: false,
							createdAt: "2026-07-08T00:00:00Z",
						},
					],
				},
			],
		},
	];
}

function makeClient(): QueryClient {
	return new QueryClient({ defaultOptions: { mutations: { retry: false } } });
}

function wrapperFor(qc: QueryClient) {
	return function wrapper({ children }: { children: ReactNode }) {
		return createElement(QueryClientProvider, { client: qc }, children);
	};
}

beforeEach(() => {
	postMock.mockReset();
});

describe("useReplyToThread", () => {
	it("appends the returned comment to the matching thread without invalidating the query", async () => {
		const qc = makeClient();
		qc.setQueryData(CACHE_KEY, seedGroups());
		postMock.mockResolvedValue({
			data: {
				ok: true,
				comment: {
					id: "c2",
					author: "me",
					body: "thanks",
					url: "",
					resolved: false,
					isBot: false,
					createdAt: "2026-07-09T00:00:00Z",
				},
			},
			error: undefined,
		});

		const { result } = renderHook(() => useReplyToThread("s1"), { wrapper: wrapperFor(qc) });

		result.current.mutate({ prUrl: "https://gh/pr/1", threadId: "T1", body: "thanks" });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(postMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/comment-reply",
			expect.objectContaining({
				params: { path: { sessionId: "s1" } },
				body: { prUrl: "https://gh/pr/1", threadId: "T1", body: "thanks" },
			}),
		);

		const groups = qc.getQueryData<PRCommentGroup[]>(CACHE_KEY);
		const thread = groups?.[0]?.threads[0];
		expect(thread?.comments).toHaveLength(2);
		expect(thread?.comments[1]).toEqual(expect.objectContaining({ id: "c2", author: "me", body: "thanks" }));
	});

	it("surfaces the error message and leaves the cache untouched on failure", async () => {
		const qc = makeClient();
		const seeded = seedGroups();
		qc.setQueryData(CACHE_KEY, seeded);
		postMock.mockResolvedValue({ data: undefined, error: new Error("nope") });

		const { result } = renderHook(() => useReplyToThread("s1"), { wrapper: wrapperFor(qc) });

		result.current.mutate({ prUrl: "https://gh/pr/1", threadId: "T1", body: "thanks" });

		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(result.current.error).toBeInstanceOf(Error);
		expect(qc.getQueryData<PRCommentGroup[]>(CACHE_KEY)).toEqual(seeded);
	});
});

describe("useResolveThread", () => {
	it("marks the matching thread resolved without invalidating the query", async () => {
		const qc = makeClient();
		qc.setQueryData(CACHE_KEY, seedGroups());
		postMock.mockResolvedValue({ data: { ok: true, sessionId: "s1", resolved: true }, error: undefined });

		const { result } = renderHook(() => useResolveThread("s1"), { wrapper: wrapperFor(qc) });

		result.current.mutate({ prUrl: "https://gh/pr/1", threadId: "T1" });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(postMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/comment-resolve",
			expect.objectContaining({
				params: { path: { sessionId: "s1" } },
				body: { prUrl: "https://gh/pr/1", threadId: "T1" },
			}),
		);

		const groups = qc.getQueryData<PRCommentGroup[]>(CACHE_KEY);
		expect(groups?.[0]?.threads[0]?.resolved).toBe(true);
	});
});
