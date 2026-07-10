import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
	postMock: vi.fn(),
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock, POST: postMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { CommentsView } from "./CommentsView";

function comment(id: string, author: string, body: string, resolved = false, system = false) {
	return { id, author, body, url: "", resolved, isBot: false, system, createdAt: "2026-07-09T10:00:00Z" };
}

// One PR with an unresolved thread (T1) and a resolved thread (T2).
function commentsPayload() {
	return {
		prs: [
			{
				prUrl: "https://github.com/o/agent-orchestrator/pull/1",
				htmlUrl: "https://github.com/o/agent-orchestrator/pull/1",
				provider: "github",
				number: 1,
				headSha: "abc",
				threads: [
					{
						threadId: "T1",
						path: "backend/a.go",
						line: 10,
						resolved: false,
						isBot: false,
						comments: [comment("C1", "alice", "please `fix` this")],
					},
					{
						threadId: "T2",
						path: "backend/b.go",
						line: 20,
						resolved: true,
						isBot: false,
						comments: [comment("C2", "bob", "done", true)],
					},
				],
			},
		],
	};
}

let commentsData: ReturnType<typeof commentsPayload>;

beforeEach(() => {
	commentsData = commentsPayload();
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path === "/api/v1/settings/auto-nudge") return { data: { enabled: false }, error: undefined };
		if (path === "/api/v1/sessions/{sessionId}") {
			return {
				data: {
					session: {
						id: "s1",
						autoNudgeComments: null,
						prs: [
							{
								url: "https://github.com/o/agent-orchestrator/pull/1",
								number: 1,
								review: "changes_requested",
								mergeability: "mergeable",
								ci: "passing",
								state: "open",
								reviewComments: true,
								updatedAt: "",
							},
						],
					},
				},
				error: undefined,
			};
		}
		if (path.includes("diff-context")) {
			return {
				data: {
					available: true,
					mode: "hunk",
					path: "backend/a.go",
					truncated: false,
					lines: [
						{ kind: "context", oldLine: 9, newLine: 9, text: "func A() {" },
						{ kind: "add", oldLine: 0, newLine: 10, text: "  return fix" },
					],
				},
				error: undefined,
			};
		}
		return { data: commentsData, error: undefined };
	});
	putMock.mockReset().mockResolvedValue({ data: {}, error: undefined });
	postMock.mockReset().mockResolvedValue({ data: { ok: true, comment: comment("CR", "me", "") }, error: undefined });
});

function renderView(sessionId = "s1", onOpenFile?: (t: unknown) => void) {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<CommentsView sessionId={sessionId} onOpenFile={onOpenFile as never} />
		</QueryClientProvider>,
	);
}

describe("CommentsView (inbox)", () => {
	it("renders the Unresolved inbox with a comment card and inline code", async () => {
		renderView();
		expect(await screen.findByText("Unresolved")).toBeInTheDocument();
		expect(await screen.findByText("alice")).toBeInTheDocument();
		expect(screen.getByText("PR #1")).toBeInTheDocument();
		expect(screen.getByText("fix").tagName.toLowerCase()).toBe("code");
		// resolved thread lives in the collapsed Resolved section, not as a card
		expect(screen.getByText("Resolved")).toBeInTheDocument();
	});

	it("shows the auto-send switch and toggles it via PUT", async () => {
		renderView();
		const sw = await screen.findByRole("switch", { name: /auto-send/i });
		expect(sw).toHaveAttribute("aria-checked", "false");
		await userEvent.click(sw);
		await waitFor(() => expect(putMock).toHaveBeenCalled());
		const [path, opts] = putMock.mock.calls[0];
		expect(path).toBe("/api/v1/sessions/{sessionId}/auto-nudge");
		expect(opts.body).toEqual({ override: true });
	});

	it("resolve posts comment-resolve for the thread", async () => {
		renderView();
		const btn = await screen.findByRole("button", { name: /Resolve/ });
		await userEvent.click(btn);
		await waitFor(() =>
			expect(postMock.mock.calls.some(([p]) => p === "/api/v1/sessions/{sessionId}/comment-resolve")).toBe(true),
		);
		const call = postMock.mock.calls.find(([p]) => p === "/api/v1/sessions/{sessionId}/comment-resolve");
		expect(call![1].body).toMatchObject({ threadId: "T1" });
	});

	it("quick send posts comment-dispatch", async () => {
		renderView();
		const send = await screen.findByRole("button", { name: /Send to worker/ });
		await userEvent.click(send);
		await waitFor(() =>
			expect(postMock.mock.calls.some(([p]) => p === "/api/v1/sessions/{sessionId}/comment-dispatch")).toBe(true),
		);
	});

	it("opens the send-to-worker caret menu (portaled, not clipped)", async () => {
		renderView();
		const caret = await screen.findByRole("button", { name: "Send options" });
		await userEvent.click(caret);
		expect(await screen.findByText(/Quick send/)).toBeInTheDocument();
		const edit = screen.getByText(/Edit prompt/);
		expect(edit).toBeInTheDocument();
		await userEvent.click(edit);
		expect(await screen.findByText(/PROMPT TO WORKER/)).toBeInTheDocument();
	});

	it("select mode reveals a checkbox and the batch bar", async () => {
		renderView();
		const selectBtn = await screen.findByRole("button", { name: "Select" });
		await userEvent.click(selectBtn);
		const cb = await screen.findByRole("checkbox", { name: /Select comment/ });
		await userEvent.click(cb);
		expect(await screen.findByText("1 selected")).toBeInTheDocument();
	});

	it("syntax-highlights the inline diff when expanded", async () => {
		renderView();
		const show = await screen.findByRole("button", { name: /Show diff/ });
		await userEvent.click(show);
		const keyword = await screen.findByText("func");
		expect(keyword.tagName.toLowerCase()).toBe("span");
		expect(keyword).toHaveStyle({ color: "#FC5FA3" });
	});

	it("Expand full file calls onOpenFile with the comment's PR and thread", async () => {
		const onOpenFile = vi.fn();
		renderView("s1", onOpenFile);
		const expand = await screen.findByRole("button", { name: /Expand full file/ });
		await userEvent.click(expand);
		expect(onOpenFile).toHaveBeenCalledTimes(1);
		expect(onOpenFile.mock.calls[0][0]).toMatchObject({
			prNumber: 1,
			provider: "github",
			thread: { threadId: "T1", path: "backend/a.go", line: 10 },
		});
	});

	it("renders a GitLab system note as a de-emphasized line with a clean hyperlink, not a raw URL", async () => {
		// A thread whose outdated diff appended a system note (system:true) after
		// the real user comment. The note embeds a host-relative markdown link.
		const rawUrl = "/finnomena/mobility/nter-ios-app/-/merge_requests/3028/diffs?diff_id=177522";
		const base = commentsPayload().prs[0];
		commentsData = {
			prs: [
				{
					...base,
					threads: [
						{
							threadId: "T1",
							path: "backend/a.go",
							line: 10,
							resolved: false,
							isBot: false,
							comments: [
								comment("C1", "fluke.s", "ฝั่ง api เพิ่ม payment_display_text มาให้แล้ว"),
								comment("C2", "fluke.s", `changed this line in [version 6 of the diff](${rawUrl})`, false, true),
							],
						},
					],
				},
			],
		};
		renderView();
		// The markdown label renders as a real anchor…
		const link = await screen.findByRole("link", { name: "version 6 of the diff" });
		expect(link.tagName.toLowerCase()).toBe("a");
		// …resolved against the PR origin into an absolute, openable URL…
		expect(link).toHaveAttribute("href", `https://github.com${rawUrl}`);
		// …and the raw URL string is never shown as plain text.
		expect(screen.queryByText(new RegExp(rawUrl.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")))).toBeNull();
		// The real user comment still renders; the system note does not add a
		// second "fluke.s" author block (only the one real comment header).
		expect(screen.getAllByText("fluke.s")).toHaveLength(1);
	});

	it("shows Inbox zero when there are no unresolved comments", async () => {
		const base = commentsPayload().prs[0];
		commentsData = { prs: [{ ...base, threads: [base.threads[1]] }] };
		renderView();
		expect(await screen.findByText("Inbox zero")).toBeInTheDocument();
	});
});
