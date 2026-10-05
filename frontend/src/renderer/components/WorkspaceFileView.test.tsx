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
/** The file at the target's merge-base, when a test needs one. */
let targetBaseBody: Record<string, unknown> | null = null;

/** A base answer carrying `text`. */
const baseOf = (text: string) => ({ available: true, path: "pkg/app.go", exists: true, revision: "abc", text });

beforeEach(() => {
	body = response;
	editorProps.current = null;
	putMock.mockReset();
	targetBaseBody = null;
	// `/workspace/file-base` also contains "/workspace/file", so the base route is
	// matched FIRST or the file body would be served as a base.
	getMock.mockReset().mockImplementation(async (path: string, init?: { params?: { query?: { base?: string } } }) => {
		if (path.includes("/workspace/file-base")) {
			return { data: init?.params?.query?.base === "head" ? null : targetBaseBody };
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

	// The gutter follows the buffer, so the count beside it must too: once the
	// editor has measured the live buffer, its number wins over the disk's.
	it("counts the LIVE buffer's uncommitted lines once the editor reports them", async () => {
		renderView();
		await waitFor(() => expect(screen.getByText("1 uncommitted")).toBeInTheDocument());
		act(() => (editorProps.current?.onUncommittedCount as (n: number) => void)(4));
		expect(screen.getByText("4 uncommitted")).toBeInTheDocument();
		act(() => (editorProps.current?.onUncommittedCount as (n: number) => void)(0));
		expect(screen.queryByText(/uncommitted/)).toBeNull();
	});

	it("hands the editor both git bases, so it can measure the buffer against them", async () => {
		targetBaseBody = baseOf("package app\n}\n");
		renderView();
		await waitFor(() => expect(editorProps.current?.targetBase).toBe("package app\n}\n"));
		// HEAD answered nothing usable here: unknown, not "an empty file".
		expect(editorProps.current?.headBase).toBeNull();
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

	// "File not found" about a folder that plainly exists is untrue and leaves
	// the reader nothing to do. It says what the path is, and how much of it
	// the list is standing in for.
	it("says a folder is a folder, and how many files it holds", async () => {
		body = {
			available: false,
			path: "derivedDataPath",
			reason: "directory",
			entryCount: 12438,
			lines: [],
			changedLines: [],
			truncated: false,
		};
		renderView(vi.fn(), "derivedDataPath");
		await waitFor(() => expect(screen.getByText(/folder, not a file/i)).toBeInTheDocument());
		expect(screen.getByText(/12,438 untracked files/i)).toBeInTheDocument();
		expect(screen.queryByTestId("monaco-file-editor")).toBeNull();
	});

	// A submodule bump IS the change a reviewer came for, so the row opens onto
	// the two commits rather than onto an error.
	it("shows the commits a submodule moved between", async () => {
		body = {
			available: false,
			path: "vendor/sub",
			reason: "submodule",
			submoduleFrom: "898af474ff78b98d8b3509125f49009642283106",
			submoduleTo: "4c1d90e2b7a3f5188d0cc1a0f4b2e9d7a6c5b4a3",
			lines: [],
			changedLines: [],
			truncated: false,
		};
		renderView(vi.fn(), "vendor/sub");
		await waitFor(() => expect(screen.getByText(/git submodule, not a file/i)).toBeInTheDocument());
		expect(screen.getByText("898af47 → 4c1d90e")).toBeInTheDocument();
	});

	it("says where a newly added submodule starts", async () => {
		body = {
			available: false,
			path: "vendor/sub",
			reason: "submodule",
			submoduleTo: "4c1d90e2b7a3f5188d0cc1a0f4b2e9d7a6c5b4a3",
			lines: [],
			changedLines: [],
			truncated: false,
		};
		renderView(vi.fn(), "vendor/sub");
		await waitFor(() => expect(screen.getByText("Added at 4c1d90e")).toBeInTheDocument());
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
		// The branch added line 2.
		targetBaseBody = baseOf("package app\n}\n");
		renderView(vi.fn(), "pkg/app.go", undefined, "first-hunk");

		await waitFor(() => expect(editorProps.current?.line).toBe(2));
	});

	// An explicit line always wins: a terminal `:42` and a go-to-definition
	// target both name a line the reader actually asked for.
	it("prefers an explicit line over the first hunk", async () => {
		targetBaseBody = baseOf("package app\n");
		renderView(vi.fn(), "pkg/app.go", 3, "first-hunk");

		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		expect(editorProps.current?.line).toBe(3);
	});

	it("calls onClose when the back button is clicked", async () => {
		const onClose = renderView();
		const back = await screen.findByRole("button", { name: /agent/i });
		await userEvent.click(back);
		expect(onClose).toHaveBeenCalled();
	});
});

// ── the mode across files ───────────────────────────────────────────────────

/**
 * Browse / Changes is the reader's choice of how to look at files, so it has to
 * outlive the file it was made on. Reviewing a branch is clicking from changed
 * file to changed file in Changes; a pane that snapped back to Browse on every
 * click made the reader re-pick the mode each time.
 */
describe("WorkspaceFileView mode across files", () => {
	/** A target base whose second line the branch changed. */
	const changedDiff = (path: string) => ({ ...baseOf("package app\nfunc Old() {\n}\n"), path });
	/** The target base of a file the branch did not touch: the file itself. */
	const unchanged = (path: string) => ({ ...baseOf("package app\nfunc Run() {\n}\n"), path });

	function serveDiffs(bases: Record<string, Record<string, unknown>>) {
		getMock.mockImplementation(
			async (route: string, init?: { params?: { query?: { base?: string; path?: string } } }) => {
				const query = init?.params?.query;
				if (route.includes("/workspace/file-base")) {
					return { data: query?.base === "head" ? null : (bases[query?.path ?? ""] ?? null) };
				}
				if (route.includes("/workspace/file")) return { data: body };
				return { data: null };
			},
		);
	}

	function renderSwitchable(path: string) {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const view = (p: string) => (
			<QueryClientProvider client={client}>
				<WorkspaceFileView sessionId="proj-1" path={p} onClose={vi.fn()} />
			</QueryClientProvider>
		);
		const { rerender } = render(view(path));
		return (next: string) => rerender(view(next));
	}

	const tab = (name: "Browse" | "Changes") => screen.getByRole("tab", { name });

	it("keeps Changes when another changed file is opened", async () => {
		serveDiffs({ "pkg/a.go": changedDiff("pkg/a.go"), "pkg/b.go": changedDiff("pkg/b.go") });
		const open = renderSwitchable("pkg/a.go");
		await waitFor(() => expect(tab("Changes")).toBeEnabled());
		await userEvent.click(tab("Changes"));
		await waitFor(() => expect(editorProps.current?.mode).toBe("diff"));

		open("pkg/b.go");

		await waitFor(() => expect(editorProps.current?.path).toBe("pkg/b.go"));
		expect(editorProps.current?.mode).toBe("diff");
		expect(tab("Changes")).toHaveAttribute("aria-selected", "true");
	});

	it("shows a file with no changes in Browse, and still opens the next changed file in Changes", async () => {
		serveDiffs({
			"pkg/a.go": changedDiff("pkg/a.go"),
			"pkg/same.go": unchanged("pkg/same.go"),
			"pkg/c.go": changedDiff("pkg/c.go"),
		});
		const open = renderSwitchable("pkg/a.go");
		await waitFor(() => expect(tab("Changes")).toBeEnabled());
		await userEvent.click(tab("Changes"));
		await waitFor(() => expect(editorProps.current?.mode).toBe("diff"));

		open("pkg/same.go");

		// Nothing to compare, so the pane says so rather than drawing an empty diff.
		await waitFor(() => expect(tab("Changes")).toBeDisabled());
		expect(tab("Changes")).toHaveAttribute("title", "This file has no changes against the target branch.");
		expect(tab("Browse")).toHaveAttribute("aria-selected", "true");
		expect(editorProps.current?.path).toBe("pkg/same.go");
		expect(editorProps.current?.mode).toBe("code");

		open("pkg/c.go");

		await waitFor(() => expect(editorProps.current?.path).toBe("pkg/c.go"));
		expect(editorProps.current?.mode).toBe("diff");
		expect(tab("Changes")).toHaveAttribute("aria-selected", "true");
	});

	it("keeps Browse when Browse was picked", async () => {
		serveDiffs({ "pkg/a.go": changedDiff("pkg/a.go"), "pkg/b.go": changedDiff("pkg/b.go") });
		const open = renderSwitchable("pkg/a.go");
		await waitFor(() => expect(tab("Changes")).toBeEnabled());
		await userEvent.click(tab("Changes"));
		await userEvent.click(tab("Browse"));

		open("pkg/b.go");

		await waitFor(() => expect(editorProps.current?.path).toBe("pkg/b.go"));
		await waitFor(() => expect(tab("Changes")).toBeEnabled());
		expect(editorProps.current?.mode).toBe("code");
		expect(tab("Browse")).toHaveAttribute("aria-selected", "true");
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
			onHandle: (
				handle: { getValue: () => string | null; focus: () => void; revertToSaved?: () => void } | null,
			) => void;
		};
	}

	const revertToSaved = vi.fn();

	async function conflictOnSave() {
		revertToSaved.mockReset();
		renderView();
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		act(() => {
			editor().onHandle({ getValue: () => "mine", focus: () => {}, revertToSaved });
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

	/**
	 * 🗝 The non-negotiable, in the smallest form that can hold it: not-ready,
	 * ready and failed must be THREE different sentences, and none of them may be
	 * silence. Six silent failures have been paid for on this feature; every one
	 * of them would have passed a test that only checked the pill exists.
	 */
	it("says three different things for starting, ready and failed", async () => {
		renderView();
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		const report = editorProps.current?.onServerState as (s: { state: string; detail?: string }) => void;

		const titleFor = async (state: { state: string; detail?: string }) => {
			act(() => report(state));
			const pill = await screen.findByTestId("lsp-status");
			return { text: pill.textContent ?? "", title: pill.getAttribute("title") ?? "" };
		};

		const starting = await titleFor({ state: "starting" });
		expect(starting.text).toMatch(/starting gopls/i);

		const indexing = await titleFor({ state: "indexing" });
		// Completion does NOT wait for the index - measured, the readiness gate
		// costs ~6 s on Swift and does not make completion any faster. Saying
		// otherwise would send a reader off to wait for nothing.
		expect(indexing.title).toMatch(/completion already works/i);

		const ready = await titleFor({ state: "ready" });
		expect(ready.text).toMatch(/⌘click/);
		expect(ready.text).toMatch(/⌃space/);

		const failed = await titleFor({ state: "failed", detail: "gopls is not on PATH" });
		expect(failed.text).toMatch(/no language server/i);
		// The reason VERBATIM: on Swift it is the actionable half.
		expect(failed.title).toBe("gopls is not on PATH");

		const texts = [starting.text, indexing.text, ready.text, failed.text];
		expect(new Set(texts).size, `the pill said the same thing twice: ${texts.join(" | ")}`).toBe(4);
	});

	/**
	 * 🗝 The 2026-10-02 report, at the pill. A worktree that only needs one Xcode
	 * build must not read as "no language server" in red: that sent the user, and
	 * then an agent, hunting a regression in an install that had not caused it.
	 */
	it("a workspace waiting for its build says so, not 'no language server'", async () => {
		renderView();
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		const report = editorProps.current?.onServerState as (s: { state: string; detail?: string; need?: string }) => void;
		const reason =
			"This worktree moved here from /w/old after Xcode last built it. Build NterWorkspace.xcworkspace in Xcode once from here; the editor connects by itself when the build finishes.";

		act(() => report({ state: "unconfigured", need: "build", detail: reason }));
		const pill = await screen.findByTestId("lsp-status");
		expect(pill).toHaveTextContent(/waiting for a build/i);
		expect(pill).not.toHaveTextContent(/no language server/i);
		expect(pill.getAttribute("title")).toBe(reason);

		act(() => report({ state: "unconfigured", need: "tool", detail: "Install xcode-build-server." }));
		expect(await screen.findByTestId("lsp-status")).toHaveTextContent(/needs setup/i);
	});

	/**
	 * 🗝 The 2026-10-05 report, at the pill. The server is running and healthy,
	 * but THIS file has no compile settings from a real build: its errors are
	 * xcode-build-server's macOS guess ("No such module 'UIKit'"). The pill says
	 * it is waiting for a build - the same words as the workspace-wide wait,
	 * because the fix is the same - and never the healthy ⌘click line.
	 */
	it("a file no build has compiled waits for a build even while the server is ready", async () => {
		renderView();
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		const report = editorProps.current?.onServerState as (s: {
			state: string;
			detail?: string;
			documentWaiting?: string;
		}) => void;
		const reason = "No Xcode build of this worktree that AO has read compiled this file yet.";

		for (const state of ["indexing", "ready"]) {
			act(() => report({ state, documentWaiting: reason }));
			const pill = await screen.findByTestId("lsp-status");
			expect(pill).toHaveTextContent(/waiting for a build/i);
			expect(pill).not.toHaveTextContent(/⌘click/);
			expect(pill.getAttribute("title")).toBe(reason);
		}

		// The build lands: the file has real settings, and the pill is the healthy one.
		act(() => report({ state: "ready" }));
		expect(await screen.findByTestId("lsp-status")).toHaveTextContent(/⌘click/);

		// A server that failed says so; a file's wait never hides that.
		act(() => report({ state: "failed", detail: "sourcekit-lsp crashed", documentWaiting: reason }));
		expect(await screen.findByTestId("lsp-status")).toHaveTextContent(/no language server/i);
	});

	it("asks twice before discarding the reader's edits", async () => {
		await conflictOnSave();

		const discard = screen.getByRole("button", { name: /discard mine and reload/i });
		await userEvent.click(discard);
		expect(screen.getByTestId("file-drift-banner")).toHaveTextContent(/really discard/i);

		expect(revertToSaved).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: /really discard/i }));
		await waitFor(() => expect(screen.queryByTestId("file-drift-banner")).toBeNull());
		// 🗝 The BUFFER is what gets discarded. Hiding the banner and clearing the
		// dirty flag alone left the edits in the editor under a "clean" header.
		expect(revertToSaved).toHaveBeenCalledTimes(1);
	});
});
