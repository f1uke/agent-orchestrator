import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { FileDiffView } from "./FileDiffView";

const target = {
	prUrl: "https://github.com/o/agent-orchestrator/pull/36",
	htmlUrl: "https://github.com/o/agent-orchestrator/pull/36",
	prNumber: 36,
	provider: "github",
	thread: {
		threadId: "T1",
		path: "backend/internal/observe/scmobserver.go",
		line: 936,
		resolved: false,
		isBot: false,
		comments: [
			{ id: "C1", author: "f1uke", body: "re-poll on the `reviewInterval`", url: "", resolved: false, isBot: false, createdAt: "2026-07-09T10:00:00Z" },
		],
	},
};

beforeEach(() => {
	getMock.mockReset().mockImplementation(async (path: string, opts: { params?: { query?: { mode?: string } } }) => {
		if (path.includes("diff-context")) {
			expect(opts.params?.query?.mode).toBe("file");
			return {
				data: {
					available: true,
					mode: "file",
					path: target.thread.path,
					truncated: false,
					lines: [
						{ kind: "context", oldLine: 934, newLine: 934, text: "  }" },
						{ kind: "del", oldLine: 936, newLine: 0, text: "  return false" },
						{ kind: "add", oldLine: 0, newLine: 936, text: "  return next" },
					],
				},
				error: undefined,
			};
		}
		return { data: {}, error: undefined };
	});
	postMock.mockReset().mockResolvedValue({ data: { ok: true }, error: undefined });
});

function renderView(onClose = vi.fn()) {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<FileDiffView sessionId="s1" target={target as never} onClose={onClose} />
		</QueryClientProvider>,
	);
	return { onClose };
}

describe("FileDiffView", () => {
	it("renders the full-file diff header, syntax-highlighted rows, and the anchored comment", async () => {
		renderView();
		// header: DIFF badge + PR ref + path + line
		expect(await screen.findByText("DIFF")).toBeInTheDocument();
		expect(screen.getByText(/PR #36/)).toBeInTheDocument();
		expect(screen.getByText(":936")).toBeInTheDocument();
		// syntax highlighting: `return` renders as a keyword-colored span
		const keyword = await screen.findByText("next");
		expect(keyword.tagName.toLowerCase()).toBe("span");
		// anchored comment body with inline code
		expect(await screen.findByText("f1uke")).toBeInTheDocument();
		expect(screen.getByText("reviewInterval").tagName.toLowerCase()).toBe("code");
	});

	it("Back button invokes onClose", async () => {
		const { onClose } = renderView();
		await userEvent.click(await screen.findByRole("button", { name: /agent/ }));
		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it("Resolve posts comment-resolve for the anchored thread", async () => {
		renderView();
		await userEvent.click(await screen.findByRole("button", { name: /Resolve/ }));
		await waitFor(() =>
			expect(postMock.mock.calls.some(([p]) => p === "/api/v1/sessions/{sessionId}/comment-resolve")).toBe(true),
		);
		const call = postMock.mock.calls.find(([p]) => p === "/api/v1/sessions/{sessionId}/comment-resolve");
		expect(call![1].body).toMatchObject({ threadId: "T1" });
	});
});
