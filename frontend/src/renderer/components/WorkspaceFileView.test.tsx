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

// The body the mocked endpoint returns; overridden per test.
let body: Record<string, unknown> = response;

beforeEach(() => {
	body = response;
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path.includes("/workspace/file")) return { data: body };
		return { data: null };
	});
});

function renderView(onClose = vi.fn(), path = "pkg/app.go") {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<WorkspaceFileView sessionId="proj-1" path={path} onClose={onClose} />
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
		await waitFor(() => expect(screen.getAllByTitle("pkg/app.go").length).toBeGreaterThan(0));
	});

	it("keeps the filename of a long absolute path visible, truncating the directory", async () => {
		const abs = "/Users/x/some/very/deeply/nested/directory/tree/notes.md";
		body = { ...response, path: abs };
		renderView(vi.fn(), abs);
		// The basename sits in its own non-shrinking span, so only the directory
		// part can be ellipsised.
		await waitFor(() => expect(screen.getAllByText("notes.md").length).toBeGreaterThan(0));
		expect(screen.getAllByTitle(abs).length).toBeGreaterThan(0);
	});

	it("explains WHY an unavailable file can't be shown", async () => {
		body = { available: false, path: "blob.bin", reason: "binary", lines: [], changedLines: [], truncated: false };
		renderView(vi.fn(), "blob.bin");
		await waitFor(() => expect(screen.getByText(/binary file/i)).toBeInTheDocument());
	});

	it("says a too-large file is too large", async () => {
		body = { available: false, path: "huge.log", reason: "too_large", lines: [], changedLines: [], truncated: false };
		renderView(vi.fn(), "huge.log");
		await waitFor(() => expect(screen.getByText(/too large/i)).toBeInTheDocument());
	});

	it("renders no gutter markers for a file outside any git repo", async () => {
		body = { ...response, path: "/Users/x/notes.md", changedLines: [] };
		renderView(vi.fn(), "/Users/x/notes.md");
		await waitFor(() => expect(screen.getByText("package")).toBeInTheDocument());
		expect(screen.queryByTestId("change-bar-1")).toBeNull();
	});

	it("calls onClose when the back button is clicked", async () => {
		const onClose = renderView();
		const back = await screen.findByRole("button", { name: /agent/i });
		await userEvent.click(back);
		expect(onClose).toHaveBeenCalled();
	});
});
