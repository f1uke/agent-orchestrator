import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { CommentsView } from "./CommentsView";

function renderView(sessionId = "s1") {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<CommentsView sessionId={sessionId} />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path.includes("diff-context")) {
			return { data: { available: false, mode: "hunk", path: "", lines: [], truncated: false }, error: undefined };
		}
		return {
			data: {
				sessionId: "s1",
				prs: [
					{
						prUrl: "https://gh/pr/1",
						htmlUrl: "https://gh/pr/1",
						provider: "github",
						number: 1,
						headSha: "abc",
						threads: [
							{
								threadId: "T1",
								path: "a.go",
								line: 10,
								resolved: false,
								isBot: false,
								comments: [
									{
										id: "C1",
										author: "alice",
										body: "please fix",
										url: "",
										resolved: false,
										isBot: false,
										createdAt: "2026-07-09T10:00:00Z",
									},
								],
							},
							{
								threadId: "T2",
								path: "x/y/Z.swift",
								line: 20,
								resolved: true,
								isBot: false,
								comments: [
									{
										id: "C2",
										author: "bob",
										body: "looks good",
										url: "",
										resolved: true,
										isBot: false,
										createdAt: "2026-07-09T10:05:00Z",
									},
								],
							},
						],
					},
				],
			},
			error: undefined,
		};
	});
});

describe("CommentsView", () => {
	it("renders a thread's file, author, and comment body", async () => {
		renderView();
		expect(await screen.findByText("a.go")).toBeInTheDocument();
		expect(await screen.findByText("please fix")).toBeInTheDocument();
		expect(screen.getByText("alice")).toBeInTheDocument();
	});

	it("shows an empty state when there are no threads", async () => {
		getMock.mockReset().mockResolvedValue({ data: { sessionId: "s1", prs: [] }, error: undefined });
		renderView();
		expect(await screen.findByText(/no review comments/i)).toBeInTheDocument();
	});

	it("renders a resolved thread collapsed and an unresolved thread expanded, expanding on click", async () => {
		const user = userEvent.setup();
		renderView();

		// Unresolved thread (T1) is expanded without any interaction.
		expect(await screen.findByText("please fix")).toBeInTheDocument();

		// Resolved thread (T2) starts collapsed: body hidden, header (badge +
		// comment count) visible.
		const resolvedHeader = await screen.findByRole("button", { name: /Z\.swift.*Resolved.*1 comment/is });
		expect(resolvedHeader).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("looks good")).not.toBeInTheDocument();
		expect(within(resolvedHeader).getByText("Resolved")).toBeInTheDocument();

		await user.click(resolvedHeader);

		expect(resolvedHeader).toHaveAttribute("aria-expanded", "true");
		expect(await screen.findByText("looks good")).toBeInTheDocument();
	});
});
