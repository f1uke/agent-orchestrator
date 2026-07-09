import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { replyMutate, resolveMutate, replyState, resolveState } = vi.hoisted(() => ({
	replyMutate: vi.fn(),
	resolveMutate: vi.fn(),
	replyState: { isPending: false, isError: false, error: null as unknown, isSuccess: false },
	resolveState: { isPending: false, isError: false, error: null as unknown },
}));

vi.mock("../hooks/useThreadActions", () => ({
	useReplyToThread: () => ({ mutate: replyMutate, ...replyState }),
	useResolveThread: () => ({ mutate: resolveMutate, ...resolveState }),
}));

vi.mock("../lib/api-client", () => ({
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import type { Thread } from "./CommentsView";
import { ThreadActions } from "./ThreadActions";

const baseThread: Thread = {
	threadId: "T1",
	path: "a.go",
	line: 10,
	resolved: false,
	isBot: false,
	comments: [],
};

function renderActions(thread: Thread = baseThread) {
	const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<ThreadActions sessionId="s1" prUrl="https://gh/pr/1" thread={thread} />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	replyMutate.mockReset();
	resolveMutate.mockReset();
	Object.assign(replyState, { isPending: false, isError: false, error: null, isSuccess: false });
	Object.assign(resolveState, { isPending: false, isError: false, error: null });
});

describe("ThreadActions", () => {
	it("disables the Reply button while the textarea is empty", () => {
		renderActions();
		expect(screen.getByRole("button", { name: "Reply" })).toBeDisabled();
	});

	it("types a reply and calls reply.mutate with prUrl/threadId/body", async () => {
		const user = userEvent.setup();
		renderActions();

		await user.type(screen.getByRole("textbox"), "sounds good, will fix");
		await user.click(screen.getByRole("button", { name: "Reply" }));

		expect(replyMutate).toHaveBeenCalledWith({
			prUrl: "https://gh/pr/1",
			threadId: "T1",
			body: "sounds good, will fix",
		});
	});

	it("clears the reply textarea after a successful reply", async () => {
		const user = userEvent.setup();
		const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
		// A fresh element per render — passing the same element reference to
		// rerender hits React's same-element bailout and skips the re-render.
		const view = () => (
			<QueryClientProvider client={qc}>
				<ThreadActions sessionId="s1" prUrl="https://gh/pr/1" thread={baseThread} />
			</QueryClientProvider>
		);
		const { rerender } = render(view());

		const textbox = screen.getByRole("textbox");
		await user.type(textbox, "will fix");
		expect(textbox).toHaveValue("will fix");

		// The mutation resolves: flipping isSuccess must clear the textarea.
		Object.assign(replyState, { isSuccess: true });
		rerender(view());

		await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue(""));
	});

	it("shows the Resolve button when the thread is unresolved and calls resolve.mutate", async () => {
		const user = userEvent.setup();
		renderActions();

		await user.click(screen.getByRole("button", { name: "Resolve" }));

		expect(resolveMutate).toHaveBeenCalledWith({ prUrl: "https://gh/pr/1", threadId: "T1" });
	});

	it("hides the Resolve button when the thread is already resolved", () => {
		renderActions({ ...baseThread, resolved: true });
		expect(screen.queryByRole("button", { name: "Resolve" })).not.toBeInTheDocument();
	});

	it("renders the error line when the reply mutation is in error state", () => {
		Object.assign(replyState, { isError: true, error: new Error("reply failed") });
		renderActions();
		expect(screen.getByRole("alert")).toHaveTextContent("reply failed");
	});

	it("renders the error line when the resolve mutation is in error state", () => {
		Object.assign(resolveState, { isError: true, error: new Error("resolve failed") });
		renderActions();
		expect(screen.getByRole("alert")).toHaveTextContent("resolve failed");
	});

	it("disables both buttons while a mutation is pending", () => {
		Object.assign(replyState, { isPending: true });
		renderActions();
		expect(screen.getByRole("button", { name: "Reply" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Resolve" })).toBeDisabled();
	});
});
