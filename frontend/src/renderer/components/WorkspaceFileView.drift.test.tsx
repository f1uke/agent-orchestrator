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
vi.mock("./MonacoFileEditor", () => ({
	default: (props: Record<string, unknown>) => {
		editorProps.current = props;
		return <div data-testid="monaco-file-editor" />;
	},
}));

import { WorkspaceFileView } from "./WorkspaceFileView";

/**
 * Drift that arrives WITHOUT a save, and drift that arrives again mid-resolve.
 *
 * 🗝 `WorkspaceFileView.test.tsx` reaches the conflict only through a 409 — it
 * types, saves, and is refused. That is the second half of the design. The FIRST
 * half is that a dirty buffer's file is polled, so the reader is told the ground
 * moved BEFORE they press save rather than after five more minutes of typing,
 * and nothing exercised it. Neither did anything exercise the case the design is
 * quietest about: an AO worktree has more than one agent in it, so the file can
 * move again while the reader is still looking at the last move.
 *
 * The whole point is that the buffer is never touched. Every assertion below
 * therefore checks the reader's own text is still there as well as what the
 * banner says.
 */
const BASE = {
	available: true,
	path: "pkg/app.go",
	truncated: false,
	lines: [
		{ kind: "context", oldLine: 1, newLine: 1, text: "package app" },
		{ kind: "context", oldLine: 2, newLine: 2, text: "func Run() {" },
		{ kind: "context", oldLine: 3, newLine: 3, text: "}" },
	],
	changedLines: [],
	contentHash: "sha256:base",
	trailingNewline: true,
};

/** The same file after another agent has written it. */
function afterAgentWrite(hash: string, body: string) {
	return {
		...BASE,
		lines: [
			{ kind: "context", oldLine: 1, newLine: 1, text: "package app" },
			{ kind: "context", oldLine: 2, newLine: 2, text: body },
			{ kind: "context", oldLine: 3, newLine: 3, text: "}" },
		],
		contentHash: hash,
	};
}

let body: Record<string, unknown> = BASE;

beforeEach(() => {
	body = BASE;
	editorProps.current = null;
	putMock.mockReset();
	// "/workspace/file-diff" also contains "/workspace/file", so it is matched first.
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (path.includes("/workspace/file-diff")) return { data: null };
		if (path.includes("/workspace/file")) return { data: body };
		return { data: null };
	});
});

function editor() {
	return editorProps.current as unknown as {
		onDirtyChange: (dirty: boolean) => void;
		onHandle: (handle: { getValue: () => string | null; focus: () => void } | null) => void;
	};
}

function diffOriginal() {
	return editorProps.current?.diffOriginal as { text: string; label: string } | null;
}

/** Open the file and leave an unsaved edit in the buffer, without saving it. */
async function openAndType(client: QueryClient, text = "// my unsaved work") {
	render(
		<QueryClientProvider client={client}>
			<WorkspaceFileView sessionId="proj-1" path="pkg/app.go" onClose={vi.fn()} />
		</QueryClientProvider>,
	);
	await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
	act(() => {
		editor().onHandle({ getValue: () => text, focus: () => {} });
		editor().onDirtyChange(true);
	});
}

/**
 * What the poll does when the file has moved: the same query, new bytes. Driven
 * through the client rather than through a timer, because what is under test is
 * the pane's reaction to new bytes, not react-query's interval.
 */
async function agentWrites(client: QueryClient, hash: string, text: string) {
	body = afterAgentWrite(hash, text);
	await act(async () => {
		await client.refetchQueries({ queryKey: ["workspace-file", "proj-1", "pkg/app.go"] });
	});
}

function newClient() {
	return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

describe("drift that arrives before any save", () => {
	it("raises the banner off the poll alone, with nothing sent and nothing lost", async () => {
		const client = newClient();
		await openAndType(client);
		await agentWrites(client, "sha256:theirs", "func Run(ctx context.Context) {");

		await waitFor(() => expect(screen.getByTestId("file-drift-banner")).toBeInTheDocument());
		expect(screen.getByTestId("file-drift-banner")).toHaveTextContent(/changed on disk/i);
		// 🗝 The point of polling: the reader is told BEFORE they press save.
		expect(putMock).not.toHaveBeenCalled();
		// The buffer is untouched — still dirty, still theirs to save.
		expect(screen.getByTestId("save-file")).toBeEnabled();
		expect(screen.getByLabelText("unsaved changes")).toBeInTheDocument();
		expect(screen.queryByTestId("save-failure")).toBeNull();
	});

	it("rebases a CLEAN buffer silently, because there is nothing to lose", async () => {
		const client = newClient();
		await openAndType(client);
		act(() => editor().onDirtyChange(false));
		await agentWrites(client, "sha256:theirs", "func Run(ctx context.Context) {");

		// No banner: interrupting a reader who has typed nothing would be noise.
		expect(screen.queryByTestId("file-drift-banner")).toBeNull();
	});

	it("compares against the bytes really on disk, not against the stale ones", async () => {
		const client = newClient();
		await openAndType(client);
		await agentWrites(client, "sha256:theirs", "func Run(ctx context.Context) {");
		await waitFor(() => expect(screen.getByTestId("file-drift-banner")).toBeInTheDocument());

		await userEvent.click(screen.getByRole("button", { name: /review changes/i }));

		expect(editorProps.current?.mode).toBe("diff");
		expect(diffOriginal()?.label).toBe("On disk");
		expect(diffOriginal()?.text).toContain("func Run(ctx context.Context) {");
	});

	it("preconditions the save on the hash it showed, and lands", async () => {
		const client = newClient();
		await openAndType(client);
		await agentWrites(client, "sha256:theirs", "func Run(ctx context.Context) {");
		await waitFor(() => expect(screen.getByTestId("file-drift-banner")).toBeInTheDocument());
		await userEvent.click(screen.getByRole("button", { name: /review changes/i }));

		putMock.mockResolvedValue({
			data: { path: "pkg/app.go", contentHash: "sha256:mine", size: 12, changedLines: [] },
		});
		await userEvent.click(screen.getByTestId("save-file"));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock.mock.calls[0][1].body.baseHash).toBe("sha256:theirs");
		await waitFor(() => expect(screen.queryByTestId("file-drift-banner")).toBeNull());
	});
});

/**
 * 🗝 The case the design says least about, and the one an AO worktree makes
 * ordinary: a SECOND agent writes the file while the reader is still comparing
 * against the first one's version. The comparison the reader is looking at is
 * now itself stale, so the hash it would save against is stale too — and saving
 * on it would be exactly the blind clobber the route exists to refuse.
 */
describe("drift that arrives again mid-resolve", () => {
	async function driftTwiceWhileReviewing(client: QueryClient) {
		await openAndType(client);
		await agentWrites(client, "sha256:first", "func Run(ctx context.Context) {");
		await waitFor(() => expect(screen.getByTestId("file-drift-banner")).toBeInTheDocument());
		await userEvent.click(screen.getByRole("button", { name: /review changes/i }));
		await agentWrites(client, "sha256:second", "func Run(ctx context.Context, log *slog.Logger) {");
	}

	it("keeps the reader in the comparison rather than throwing them out of it", async () => {
		const client = newClient();
		await driftTwiceWhileReviewing(client);

		expect(screen.getByTestId("file-drift-banner")).toBeInTheDocument();
		expect(editorProps.current?.mode).toBe("diff");
		// Still theirs to save: a second drift must not discard the buffer either.
		expect(screen.getByTestId("save-file")).toBeEnabled();
	});

	it("re-points the comparison at the newer version on disk", async () => {
		const client = newClient();
		await driftTwiceWhileReviewing(client);

		await waitFor(() => expect(diffOriginal()?.text).toContain("log *slog.Logger"));
		expect(diffOriginal()?.label).toBe("On disk");
	});

	it("saves against the SECOND hash, so the reader never clobbers unseen work", async () => {
		const client = newClient();
		await driftTwiceWhileReviewing(client);
		await waitFor(() => expect(diffOriginal()?.text).toContain("log *slog.Logger"));

		putMock.mockResolvedValue({
			data: { path: "pkg/app.go", contentHash: "sha256:mine", size: 12, changedLines: [] },
		});
		await userEvent.click(screen.getByTestId("save-file"));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock.mock.calls[0][1].body.baseHash).toBe("sha256:second");
	});
});

/**
 * The wire body, not the helper that builds it. `buildSaveRequest` is unit
 * tested, but the guarantee that matters is about what leaves the process:
 * `JSON.stringify` DROPS a key whose value is `undefined`, and a body without
 * `content` once emptied a file and answered 200.
 */
describe("the request that actually goes out", () => {
	it("always carries content, and carries it as a string", async () => {
		const client = newClient();
		await openAndType(client, "");
		putMock.mockResolvedValue({
			data: { path: "pkg/app.go", contentHash: "sha256:empty", size: 0, changedLines: [] },
		});

		await userEvent.click(screen.getByTestId("save-file"));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const sent = putMock.mock.calls[0][1].body;
		// Emptying a file stays possible — spelled as an explicit empty string.
		expect(typeof sent.content).toBe("string");
		expect(Object.keys(JSON.parse(JSON.stringify(sent)))).toContain("content");
	});

	it("sends nothing at all when the buffer has no text to send", async () => {
		const client = newClient();
		render(
			<QueryClientProvider client={client}>
				<WorkspaceFileView sessionId="proj-1" path="pkg/app.go" onClose={vi.fn()} />
			</QueryClientProvider>,
		);
		await waitFor(() => expect(screen.getByTestId("monaco-file-editor")).toBeInTheDocument());
		// A handle whose model has not loaded is the realistic caller of the
		// daemon's own "absent content emptied the file" bug.
		act(() => {
			editor().onHandle({ getValue: () => null, focus: () => {} });
			editor().onDirtyChange(true);
		});

		await userEvent.click(screen.getByTestId("save-file"));

		expect(putMock).not.toHaveBeenCalled();
		// And the refusal is silent-safe: no crash, no error banner, edit intact.
		expect(screen.queryByTestId("save-failure")).toBeNull();
		expect(screen.getByTestId("save-file")).toBeEnabled();
	});
});
