import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, editorProps } = vi.hoisted(() => ({
	getMock: vi.fn(),
	editorProps: { current: null as Record<string, unknown> | null },
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

// Monaco needs a real browser — canvas metrics, ResizeObserver, workers — so the
// editor itself is exercised in `e2e/editor.spec.ts`. What this file owns is the
// viewer AROUND it: the chrome, the load/unavailable states, and the contract it
// hands the editor.
vi.mock("./MonacoFileEditor", () => ({
	default: (props: Record<string, unknown>) => {
		editorProps.current = props;
		return <div data-testid="monaco-file-editor" />;
	},
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
	editorProps.current = null;
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path.includes("/workspace/file")) return { data: body };
		return { data: null };
	});
});

function renderView(onClose = vi.fn(), path = "pkg/app.go", line?: number) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<WorkspaceFileView sessionId="proj-1" path={path} line={line} onClose={onClose} />
		</QueryClientProvider>,
	);
	return onClose;
}

describe("WorkspaceFileView", () => {
	it("hands the file's text and change ranges to the editor", async () => {
		renderView();
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		expect(editorProps.current).toMatchObject({
			sessionId: "proj-1",
			path: "pkg/app.go",
			// Lossless: the editor gets the file, not a per-row render of it.
			text: "package app\nfunc Run() {\n}",
			changedLines: [{ start: 2, end: 2, kind: "modified" }],
		});
	});

	it("passes the referenced line through, so the editor lands on it", async () => {
		renderView(vi.fn(), "pkg/app.go", 2);
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		expect(editorProps.current?.line).toBe(2);
	});

	it("counts the uncommitted lines in the header", async () => {
		renderView();
		await waitFor(() => expect(screen.getByText("1 uncommitted")).toBeInTheDocument());
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

	it("explains WHY an unavailable file can't be shown, and opens no editor", async () => {
		body = { available: false, path: "blob.bin", reason: "binary", lines: [], changedLines: [], truncated: false };
		renderView(vi.fn(), "blob.bin");
		await waitFor(() => expect(screen.getByText(/binary file/i)).toBeInTheDocument());
		expect(screen.queryByTestId("monaco-file-editor")).toBeNull();
	});

	it("says a too-large file is too large", async () => {
		body = { available: false, path: "huge.log", reason: "too_large", lines: [], changedLines: [], truncated: false };
		renderView(vi.fn(), "huge.log");
		await waitFor(() => expect(screen.getByText(/too large/i)).toBeInTheDocument());
	});

	it("passes no change markers for a file outside any git repo", async () => {
		body = { ...response, path: "/Users/x/notes.md", changedLines: [] };
		renderView(vi.fn(), "/Users/x/notes.md");
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		expect(editorProps.current?.changedLines).toEqual([]);
		expect(screen.queryByText(/uncommitted/)).toBeNull();
	});

	it("says so when the backend truncated the file", async () => {
		body = { ...response, truncated: true };
		renderView();
		await waitFor(() => expect(screen.getByText("truncated")).toBeInTheDocument());
	});

	it("calls onClose when the back button is clicked", async () => {
		const onClose = renderView();
		const back = await screen.findByRole("button", { name: /agent/i });
		await userEvent.click(back);
		expect(onClose).toHaveBeenCalled();
	});
});
