import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock, editorProps } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
	editorProps: { current: null as Record<string, unknown> | null },
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock },
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
	contentHash: "sha256:base",
	trailingNewline: true,
};

// The body the mocked endpoint returns; overridden per test.
let body: Record<string, unknown> = response;
/** The branch-level diff, when a test needs one. */
let diffBody: Record<string, unknown> | null = null;

beforeEach(() => {
	body = response;
	editorProps.current = null;
	putMock.mockReset();
	diffBody = null;
	// `/workspace/file-diff` also contains "/workspace/file", so the diff route is
	// matched FIRST or the file body would be served as a diff.
	getMock.mockReset().mockImplementation(async (path: string, init?: { params?: { query?: { base?: string } } }) => {
		if (path.includes("/workspace/file-diff")) {
			return { data: init?.params?.query?.base === "head" ? null : diffBody };
		}
		if (path.includes("/workspace/file")) return { data: body };
		return { data: null };
	});
});

function renderView(onClose = vi.fn(), path = "pkg/app.go", line?: number, focus?: "first-hunk") {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<WorkspaceFileView sessionId="proj-1" path={path} line={line} focus={focus} onClose={onClose} />
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

	// A truncated read is not a label any more, it is a REFUSAL: saving what is
	// on screen would delete everything past line 2000, so the pane says so and
	// offers no save control at all.
	it("makes a truncated file read-only, and says what would be lost", async () => {
		body = { ...response, truncated: true, contentHash: "sha256:abc" };
		renderView();
		await waitFor(() => expect(screen.getByTestId("read-only-chip")).toHaveTextContent("truncated"));
		expect(screen.getByTestId("read-only-detail")).toHaveTextContent(/delete everything after them/i);
		expect(screen.queryByTestId("save-file")).toBeNull();
		expect(editorProps.current?.readOnly).toBe(true);
	});

	// A Changes row means "show me this file's changes", and line 1 is almost
	// never where they are.
	it("lands on the first branch hunk when asked to, not on line 1", async () => {
		diffBody = {
			available: true,
			truncated: false,
			mode: "file",
			path: "pkg/app.go",
			lines: [
				{ kind: "context", text: "package app", oldLine: 1, newLine: 1 },
				{ kind: "context", text: "", oldLine: 2, newLine: 2 },
				{ kind: "add", text: "func Run() {", oldLine: 0, newLine: 3 },
			],
		};
		renderView(vi.fn(), "pkg/app.go", undefined, "first-hunk");

		await waitFor(() => expect(editorProps.current?.branchLines).toEqual([3]));
		expect(editorProps.current?.line).toBe(3);
	});

	// An explicit line always wins: a terminal `:42` and a go-to-definition
	// target both name a line the reader actually asked for.
	it("prefers an explicit line over the first hunk", async () => {
		diffBody = {
			available: true,
			truncated: false,
			mode: "file",
			path: "pkg/app.go",
			lines: [{ kind: "add", text: "x", oldLine: 0, newLine: 3 }],
		};
		renderView(vi.fn(), "pkg/app.go", 2, "first-hunk");

		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		expect(editorProps.current?.line).toBe(2);
	});

	it("calls onClose when the back button is clicked", async () => {
		const onClose = renderView();
		const back = await screen.findByRole("button", { name: /agent/i });
		await userEvent.click(back);
		expect(onClose).toHaveBeenCalled();
	});
});

// ── editing and save ─────────────────────────────────────────────────────────

/**
 * The save path, driven through the chrome rather than through Monaco: the
 * mocked editor reports dirtiness and hands back a buffer, which is exactly the
 * contract the real one implements.
 */
describe("WorkspaceFileView saving", () => {
	function editor() {
		return editorProps.current as unknown as {
			onDirtyChange: (dirty: boolean) => void;
			onHandle: (handle: { getValue: () => string | null; focus: () => void } | null) => void;
			readOnly: boolean;
		};
	}

	async function openAndType(text: string | null) {
		renderView();
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		act(() => {
			editor().onHandle({ getValue: () => text, focus: () => {} });
			editor().onDirtyChange(true);
		});
	}

	it("offers no save control until the buffer is dirty, then enables it", async () => {
		renderView();
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		expect(screen.getByTestId("save-file")).toBeDisabled();

		act(() => {
			editor().onHandle({ getValue: () => "package app\nfunc Run() {}\n", focus: () => {} });
			editor().onDirtyChange(true);
		});
		expect(screen.getByTestId("save-file")).toBeEnabled();
	});

	it("sends the buffer with the hash it was read at, and adopts the new hash", async () => {
		await openAndType("package app\nEDITED\n}");
		putMock.mockResolvedValue({ data: { path: "pkg/app.go", contentHash: "sha256:next", size: 24, changedLines: [] } });

		await userEvent.click(screen.getByTestId("save-file"));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock.mock.calls[0][1].body).toEqual({
			path: "pkg/app.go",
			content: "package app\nEDITED\n}\n",
			baseHash: "sha256:base",
		});

		// A second save must precondition on the hash the FIRST one returned, or
		// every save after the first would conflict with our own write.
		act(() => {
			editor().onHandle({ getValue: () => "package app\nEDITED TWICE\n}", focus: () => {} });
			editor().onDirtyChange(true);
		});
		await userEvent.click(screen.getByTestId("save-file"));
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(2));
		expect(putMock.mock.calls[1][1].body.baseHash).toBe("sha256:next");
	});

	// 🗝 The guard for the daemon's own data-loss bug, from the caller that
	// caused it: `JSON.stringify` drops an undefined `content`, and a body with
	// no content once emptied the file and answered 200.
	it("refuses to send a request at all when the buffer has no content", async () => {
		await openAndType(null);

		await userEvent.click(screen.getByTestId("save-file"));

		expect(putMock).not.toHaveBeenCalled();
	});

	it("puts back a trailing newline the read reported, so a one-line edit stays one line", async () => {
		await openAndType("a\nb");
		putMock.mockResolvedValue({ data: { path: "pkg/app.go", contentHash: "sha256:n", size: 4, changedLines: [] } });

		await userEvent.click(screen.getByTestId("save-file"));

		await waitFor(() => expect(putMock).toHaveBeenCalled());
		expect(putMock.mock.calls[0][1].body.content).toBe("a\nb\n");
	});

	it("keeps a file with no trailing newline without one", async () => {
		body = { ...response, trailingNewline: false };
		await openAndType("a\nb");
		putMock.mockResolvedValue({ data: { path: "pkg/app.go", contentHash: "sha256:n", size: 3, changedLines: [] } });

		await userEvent.click(screen.getByTestId("save-file"));

		await waitFor(() => expect(putMock).toHaveBeenCalled());
		expect(putMock.mock.calls[0][1].body.content).toBe("a\nb");
	});

	it("renders a file outside the workspace read-only, with no save control", async () => {
		const abs = "/Users/x/notes.md";
		body = { ...response, path: abs };
		renderView(vi.fn(), abs);
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());

		expect(screen.getByTestId("read-only-chip")).toHaveTextContent("outside this workspace");
		expect(screen.queryByTestId("save-file")).toBeNull();
		expect(editorProps.current?.readOnly).toBe(true);
	});

	it("explains a refusal in the pane and leaves the buffer alone", async () => {
		await openAndType("too many lines");
		putMock.mockResolvedValue({
			error: { code: "WORKSPACE_FILE_CONTENT_REJECTED", details: { reason: "too_many_lines" } },
		});

		await userEvent.click(screen.getByTestId("save-file"));

		await waitFor(() => expect(screen.getByTestId("save-failure")).toBeInTheDocument());
		expect(screen.getByTestId("save-failure")).toHaveTextContent(/2000 lines/);
		// Still dirty, still editable: nothing was written and nothing was lost.
		expect(screen.getByTestId("save-file")).toBeEnabled();
	});
});

// ── the conflict ─────────────────────────────────────────────────────────────

describe("WorkspaceFileView conflicts", () => {
	function editor() {
		return editorProps.current as unknown as {
			onDirtyChange: (dirty: boolean) => void;
			onHandle: (handle: { getValue: () => string | null; focus: () => void } | null) => void;
		};
	}

	async function conflictOnSave() {
		renderView();
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		act(() => {
			editor().onHandle({ getValue: () => "mine", focus: () => {} });
			editor().onDirtyChange(true);
		});
		putMock.mockResolvedValue({
			error: {
				code: "WORKSPACE_FILE_CONFLICT",
				details: { currentHash: "sha256:theirs", currentSize: 1440, currentModifiedAt: new Date().toISOString() },
			},
		});
		await userEvent.click(screen.getByTestId("save-file"));
		await waitFor(() => expect(screen.getByTestId("file-drift-banner")).toBeInTheDocument());
	}

	it("shows the drift banner rather than an error, and keeps the edit", async () => {
		await conflictOnSave();

		expect(screen.getByTestId("file-drift-banner")).toHaveTextContent(/changed on disk/i);
		expect(screen.getByTestId("file-drift-banner")).toHaveTextContent(/1.4 KB/);
		expect(screen.queryByTestId("save-failure")).toBeNull();
		expect(screen.getByTestId("save-file")).toBeEnabled();
	});

	// 🗝 The whole answer to "what does the human see on a 409". There is no
	// force: Review changes puts the two versions side by side, and saving from
	// there preconditions on the version the reader was SHOWN.
	it("resolves by comparing, then saves against the hash that was shown", async () => {
		await conflictOnSave();

		await userEvent.click(screen.getByRole("button", { name: /review changes/i }));
		expect(editorProps.current?.mode).toBe("diff");
		expect((editorProps.current?.diffOriginal as { label: string }).label).toBe("On disk");

		putMock.mockResolvedValue({ data: { path: "pkg/app.go", contentHash: "sha256:mine", size: 4, changedLines: [] } });
		await userEvent.click(screen.getByTestId("save-file"));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(2));
		expect(putMock.mock.calls[1][1].body.baseHash).toBe("sha256:theirs");
	});

	it("asks twice before discarding the reader's edits", async () => {
		await conflictOnSave();

		const discard = screen.getByRole("button", { name: /discard mine and reload/i });
		await userEvent.click(discard);
		expect(screen.getByTestId("file-drift-banner")).toHaveTextContent(/really discard/i);

		await userEvent.click(screen.getByRole("button", { name: /really discard/i }));
		await waitFor(() => expect(screen.queryByTestId("file-drift-banner")).toBeNull());
	});
});
