import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { FilesPanel } from "./FilesPanel";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => (error instanceof Error ? error.message : fallback),
}));

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const file = (over: Partial<Record<string, unknown>> = {}) => ({
	path: "frontend/src/renderer/components/DiffRows.tsx",
	status: "modified",
	additions: 42,
	deletions: 6,
	binary: false,
	committed: true,
	...over,
});

function respondWith(body: unknown) {
	getMock.mockResolvedValue({ data: body, error: undefined });
}

beforeEach(() => {
	getMock.mockReset();
});

describe("FilesPanel", () => {
	it("lists changed files with their status and counts", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			targetSource: "pr",
			truncated: false,
			files: [file(), file({ path: "a/added.go", status: "added", additions: 10, deletions: 0 })],
		});
		render(<FilesPanel sessionId="s1" />, { wrapper });

		expect(await screen.findByText("DiffRows.tsx")).toBeInTheDocument();
		expect(screen.getByText("added.go")).toBeInTheDocument();
		// Scoped to the row: the summary line carries the same totals, so an
		// unscoped text query matches twice.
		const row = screen.getByRole("button", { name: /DiffRows\.tsx/ });
		expect(within(row).getByText("+42")).toBeInTheDocument();
		expect(within(row).getByText("−6")).toBeInTheDocument();
		// summary line names the branch it compared against
		expect(screen.getByTitle("Comparing against main")).toBeInTheDocument();
	});

	it("renders a rename as old → new", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			files: [file({ path: "lib/tree.ts", oldPath: "lib/session-tree.ts", status: "renamed" })],
		});
		render(<FilesPanel sessionId="s1" />, { wrapper });
		expect(await screen.findByText("session-tree.ts → tree.ts")).toBeInTheDocument();
	});

	// git emits "-" counts for binary files; rendering them arithmetically would
	// produce a nonsense "+0 −0".
	it("marks a binary file instead of showing counts", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			files: [file({ path: "img.png", binary: true, additions: 0, deletions: 0 })],
		});
		render(<FilesPanel sessionId="s1" />, { wrapper });
		const row = await screen.findByRole("button", { name: /img\.png/ });
		expect(within(row).getByText("bin")).toBeInTheDocument();
		// the row must NOT render "+0 −0" arithmetic for a binary file
		expect(within(row).queryByText("+0")).not.toBeInTheDocument();
	});

	it("flags uncommitted work so a mid-task worker is not under-reported", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			files: [file({ committed: false })],
		});
		render(<FilesPanel sessionId="s1" />, { wrapper });
		expect(await screen.findByLabelText("uncommitted")).toBeInTheDocument();
	});

	// The load-bearing product decision: never silently diff against a guessed
	// "main". A wrong target renders a confidently wrong diff.
	it("shows a specific empty state when there is no target branch", async () => {
		respondWith({ available: false, reason: "no_target_branch", files: [], truncated: false });
		render(<FilesPanel sessionId="s1" />, { wrapper });
		expect(await screen.findByText("No target branch to compare")).toBeInTheDocument();
		expect(screen.queryByText(/vs main/)).not.toBeInTheDocument();
	});

	it("shows a cleaned-up worktree as its own state, not an error", async () => {
		respondWith({ available: false, reason: "no_workspace", files: [], truncated: false });
		render(<FilesPanel sessionId="s1" />, { wrapper });
		expect(await screen.findByText("Worktree no longer on disk")).toBeInTheDocument();
	});

	it("shows a no-changes state when the branch matches its target", async () => {
		respondWith({ available: true, targetBranch: "main", files: [], truncated: false });
		render(<FilesPanel sessionId="s1" />, { wrapper });
		expect(await screen.findByText("No changes vs main")).toBeInTheDocument();
	});

	// Every row opens as a diff — including a deleted one, which has no
	// working-tree content and would 404 through the file endpoint.
	it("opens any row, including a deleted file, as a diff", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			files: [file({ path: "lib/gone.ts", status: "deleted", additions: 0, deletions: 38 })],
		});
		const onOpenFile = vi.fn();
		render(<FilesPanel sessionId="s1" onOpenFile={onOpenFile} />, { wrapper });

		await userEvent.click(await screen.findByText("gone.ts"));
		expect(onOpenFile).toHaveBeenCalledWith({ path: "lib/gone.ts" });
	});

	it("marks the row currently open in the center pane", async () => {
		respondWith({ available: true, targetBranch: "main", truncated: false, files: [file()] });
		render(<FilesPanel sessionId="s1" selectedPath="frontend/src/renderer/components/DiffRows.tsx" />, {
			wrapper,
		});
		await waitFor(() =>
			expect(screen.getByRole("button", { name: /DiffRows\.tsx/ }).getAttribute("aria-current")).toBe("true"),
		);
	});

	it("keeps Browse present but disabled until it ships", async () => {
		respondWith({ available: true, targetBranch: "main", truncated: false, files: [] });
		render(<FilesPanel sessionId="s1" />, { wrapper });
		const browse = await screen.findByRole("tab", { name: /Browse/ });
		expect(browse).toBeDisabled();
	});
});
