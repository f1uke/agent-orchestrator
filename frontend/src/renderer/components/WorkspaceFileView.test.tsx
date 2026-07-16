import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { WorkspaceFileView } from "./WorkspaceFileView";

const response = {
	available: true,
	path: "pkg/app.go",
	truncated: false,
	lines: [
		{ kind: "context", oldLine: 0, newLine: 1, text: "package app" },
		{ kind: "context", oldLine: 0, newLine: 2, text: "func Run() {" },
		{ kind: "context", oldLine: 0, newLine: 3, text: "}" },
	],
	changedLines: [{ start: 2, end: 2, kind: "modified" }],
};

beforeEach(() => {
	getMock.mockReset().mockImplementation(async (path: string, opts: { params?: { query?: { path?: string } } }) => {
		if (path.includes("/workspace/file")) {
			expect(opts.params?.query?.path).toBe("pkg/app.go");
			return { data: response };
		}
		return { data: null };
	});
});

function renderView(onClose = vi.fn()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<WorkspaceFileView sessionId="proj-1" path="pkg/app.go" onClose={onClose} />
		</QueryClientProvider>,
	);
	return onClose;
}

describe("WorkspaceFileView", () => {
	it("renders the file's content, syntax-highlighted", async () => {
		renderView();
		await waitFor(() => expect(screen.getByText("package")).toBeInTheDocument());
		expect(screen.getByText("Run")).toBeInTheDocument();
	});

	it("shows a gutter change bar on the modified line", async () => {
		renderView();
		// changedLines modified line 2 → row index 1 → change-bar-1.
		await waitFor(() => expect(screen.getByTestId("change-bar-1")).toHaveAttribute("data-change", "modified"));
	});

	it("shows the file path in the header", async () => {
		renderView();
		await waitFor(() => expect(screen.getAllByText("pkg/app.go").length).toBeGreaterThan(0));
	});

	it("calls onClose when the back button is clicked", async () => {
		const onClose = renderView();
		const back = await screen.findByRole("button", { name: /agent/i });
		await userEvent.click(back);
		expect(onClose).toHaveBeenCalled();
	});
});
