import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
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

/** Tree mode renders rows as tree items; the flat list renders them as options. */
const row = (name: RegExp) => screen.getByRole("treeitem", { name });
const listRows = () => screen.getAllByRole("option");

beforeEach(() => {
	getMock.mockReset();
	window.localStorage.clear();
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

		// Both paths are only-child chains, so each renders as one merged row.
		expect(await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ })).toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /added\.go/ })).toBeInTheDocument();
		// Scoped to the row: the summary line carries the same totals, so an
		// unscoped text query matches twice.
		const target = row(/DiffRows\.tsx/);
		expect(within(target).getByText("+42")).toBeInTheDocument();
		expect(within(target).getByText("−6")).toBeInTheDocument();
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
		const target = await screen.findByRole("treeitem", { name: /img\.png/ });
		expect(within(target).getByText("bin")).toBeInTheDocument();
		expect(within(target).queryByText("+0")).not.toBeInTheDocument();
	});

	// GitLab shows status as a trailing box, not a leading letter — and it has to
	// survive in both views.
	it("marks each file's status with a trailing icon, after the counts", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			files: [
				file({ path: "a/added.go", status: "added" }),
				file({ path: "b/gone.go", status: "deleted" }),
				file({ path: "c/kept.go", status: "modified" }),
			],
		});
		render(<FilesPanel sessionId="s1" />, { wrapper });

		const added = await screen.findByRole("treeitem", { name: /added\.go/ });
		expect(within(added).getByRole("img", { name: "Added" })).toBeInTheDocument();
		expect(
			within(screen.getByRole("treeitem", { name: /gone\.go/ })).getByRole("img", { name: "Deleted" }),
		).toBeInTheDocument();

		// The status box follows the counts in DOM order, so it reads last.
		const meta = added.querySelector(".files-panel__meta");
		expect(meta?.lastElementChild).toHaveAttribute("aria-label", "Added");

		await userEvent.click(screen.getByRole("button", { name: "List view" }));
		expect(
			within(screen.getByRole("option", { name: /kept\.go/ })).getByRole("img", { name: "Modified" }),
		).toBeInTheDocument();
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

		await userEvent.click(await screen.findByRole("treeitem", { name: /gone\.ts/ }));
		expect(onOpenFile).toHaveBeenCalledWith({ path: "lib/gone.ts" });
	});

	it("marks the row currently open in the center pane", async () => {
		respondWith({ available: true, targetBranch: "main", truncated: false, files: [file()] });
		render(<FilesPanel sessionId="s1" selectedPath="frontend/src/renderer/components/DiffRows.tsx" />, {
			wrapper,
		});
		await waitFor(() => expect(row(/DiffRows\.tsx/).getAttribute("aria-current")).toBe("true"));
	});

	it("keeps Browse present but disabled until it ships", async () => {
		respondWith({ available: true, targetBranch: "main", truncated: false, files: [] });
		render(<FilesPanel sessionId="s1" />, { wrapper });
		const browse = await screen.findByRole("tab", { name: /Browse/ });
		expect(browse).toBeDisabled();
	});

	describe("tree view", () => {
		const deepFiles = [
			file({ path: "backend/internal/service/session/workspace_changes.go" }),
			file({ path: "frontend/src/renderer/components/DiffRows.tsx" }),
		];

		it("defaults to the folder tree, with single-child chains collapsed into one row", async () => {
			respondWith({ available: true, targetBranch: "main", truncated: false, files: deepFiles });
			render(<FilesPanel sessionId="s1" />, { wrapper });

			expect(await screen.findByRole("tree")).toBeInTheDocument();
			// Five path levels render as ONE directory row — the mechanism that makes
			// a deep tree fit the rail's 280px floor.
			expect(row(/^backend\/internal\/service\/session$/)).toBeInTheDocument();
			expect(screen.queryByRole("treeitem", { name: /^internal$/ })).not.toBeInTheDocument();
		});

		it("collapses a directory so its files disappear from the rail", async () => {
			respondWith({
				available: true,
				targetBranch: "main",
				truncated: false,
				// Two files under one directory, so that directory is a real branch
				// point and keeps a collapsible row.
				files: [
					file({ path: "backend/internal/service/session/workspace_changes.go" }),
					file({ path: "backend/internal/service/session/workspace_file.go" }),
					file({ path: "frontend/src/renderer/components/DiffRows.tsx" }),
				],
			});
			render(<FilesPanel sessionId="s1" />, { wrapper });

			await userEvent.click(await screen.findByRole("treeitem", { name: /^backend\/internal\/service\/session$/ }));
			expect(screen.queryByText("workspace_changes.go")).not.toBeInTheDocument();
			// the other branch of the tree is untouched
			expect(screen.getByRole("treeitem", { name: /DiffRows\.tsx/ })).toBeInTheDocument();
		});

		it("switches to the flat list and back", async () => {
			respondWith({ available: true, targetBranch: "main", truncated: false, files: deepFiles });
			render(<FilesPanel sessionId="s1" />, { wrapper });
			await screen.findByRole("tree");

			await userEvent.click(screen.getByRole("button", { name: "List view" }));
			expect(screen.queryByRole("tree")).not.toBeInTheDocument();
			// the flat list shows the parent directory on its own line instead of nesting
			expect(screen.getByText("backend/internal/service/session")).toBeInTheDocument();
			expect(listRows()).toHaveLength(2);

			await userEvent.click(screen.getByRole("button", { name: "Tree view" }));
			expect(screen.getByRole("tree")).toBeInTheDocument();
		});

		it("remembers the chosen view across remounts", async () => {
			respondWith({ available: true, targetBranch: "main", truncated: false, files: deepFiles });
			const first = render(<FilesPanel sessionId="s1" />, { wrapper });
			await screen.findByRole("tree");
			await userEvent.click(screen.getByRole("button", { name: "List view" }));
			first.unmount();

			render(<FilesPanel sessionId="s1" />, { wrapper });
			await screen.findByRole("option", { name: /DiffRows\.tsx/ });
			expect(screen.queryByRole("tree")).not.toBeInTheDocument();
		});
	});

	describe("search", () => {
		const searchable = [
			file({ path: "hotfix/login-crash.ts" }),
			file({ path: "src/app/Main.vue" }),
			file({ path: "src/app/Main.tsx" }),
		];

		// Substring, NOT prefix: this panel shipped prefix-only matching once and
		// had to fix it, so `fix` must still find `hotfix/login-crash.ts`.
		it("matches anywhere in the path, not just its start", async () => {
			respondWith({ available: true, targetBranch: "main", truncated: false, files: searchable });
			render(<FilesPanel sessionId="s1" />, { wrapper });
			await screen.findByRole("treeitem", { name: /login-crash\.ts/ });

			await userEvent.type(screen.getByRole("searchbox", { name: /search/i }), "fix");
			expect(screen.getByRole("treeitem", { name: /login-crash\.ts/ })).toBeInTheDocument();
			expect(screen.queryByRole("treeitem", { name: /Main\.vue/ })).not.toBeInTheDocument();
			expect(screen.queryByRole("treeitem", { name: /Main\.tsx/ })).not.toBeInTheDocument();
		});

		it("supports a glob, as the placeholder advertises", async () => {
			respondWith({ available: true, targetBranch: "main", truncated: false, files: searchable });
			render(<FilesPanel sessionId="s1" />, { wrapper });
			await screen.findByRole("treeitem", { name: /Main\.vue/ });

			await userEvent.type(screen.getByRole("searchbox", { name: /search/i }), "*.vue");
			expect(screen.getByRole("treeitem", { name: /Main\.vue/ })).toBeInTheDocument();
			expect(screen.queryByRole("treeitem", { name: /Main\.tsx/ })).not.toBeInTheDocument();
		});

		it("filters the flat list too, not only the tree", async () => {
			respondWith({ available: true, targetBranch: "main", truncated: false, files: searchable });
			render(<FilesPanel sessionId="s1" />, { wrapper });
			await screen.findByRole("treeitem", { name: /login-crash\.ts/ });
			await userEvent.click(screen.getByRole("button", { name: "List view" }));

			await userEvent.type(screen.getByRole("searchbox", { name: /search/i }), "fix");
			expect(listRows()).toHaveLength(1);
			expect(screen.getByText("login-crash.ts")).toBeInTheDocument();
		});

		it("says so when nothing matches, rather than showing an empty rail", async () => {
			respondWith({ available: true, targetBranch: "main", truncated: false, files: searchable });
			render(<FilesPanel sessionId="s1" />, { wrapper });
			await screen.findByRole("treeitem", { name: /Main\.vue/ });

			await userEvent.type(screen.getByRole("searchbox", { name: /search/i }), "nothing-here");
			expect(screen.getByText(/No files match/)).toBeInTheDocument();
			expect(screen.queryAllByRole("treeitem")).toHaveLength(0);
		});
	});
});

// --- reveal from a clicked terminal reference --------------------------------
//
// jsdom cannot see scrolling (test/setup.ts stubs scrollIntoView) or CSS, so
// these assert the things that DECIDE whether a human sees anything: that the
// target row EXISTS after the panel's own state is undone, and that the reveal
// marker lands on it. The scroll itself is verified visually, not here.
describe("FilesPanel reveal", () => {
	const deep = "frontend/src/renderer/components/DiffRows.tsx";

	it("marks the revealed row, distinctly from the scroll-spy selection", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			targetSource: "project",
			files: [file(), file({ path: "backend/main.go" })],
		});
		const { rerender } = render(<FilesPanel sessionId="s1" />, { wrapper });
		await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ });

		rerender(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1 }} />);
		const revealed = await waitFor(() => {
			const el = document.querySelector(`[data-path="${deep}"]`);
			expect(el?.className).toContain("is-revealed");
			return el as HTMLElement;
		});
		// The reveal cue must NOT borrow the scroll-spy marker's class, or the two
		// facts become indistinguishable on the same row.
		expect(revealed.className).not.toContain("is-selected");
	});

	// collapsedDirs names the CLOSED directories, so revealing has to DELETE
	// ancestor keys. Adding them (the intuitive reading) would collapse the target
	// out of the tree instead of opening it — and the row would never render.
	it("expands collapsed ancestors so the target row exists", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			targetSource: "project",
			files: [file(), file({ path: "backend/main.go" })],
		});
		const { rerender } = render(<FilesPanel sessionId="s1" />, { wrapper });
		const dir = await screen.findByRole("treeitem", { name: /frontend/ });
		await userEvent.click(dir);
		expect(document.querySelector(`[data-path="${deep}"]`)).toBeNull();

		rerender(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1 }} />);
		await waitFor(() => expect(document.querySelector(`[data-path="${deep}"]`)).not.toBeNull());
	});

	// The search box filters BEFORE the tree is built, so a query that excludes
	// the target leaves no row to reveal at all.
	it("clears a search query that would filter the target out", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			targetSource: "project",
			files: [file(), file({ path: "backend/main.go" })],
		});
		const { rerender } = render(<FilesPanel sessionId="s1" />, { wrapper });
		await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ });
		await userEvent.type(screen.getByRole("searchbox"), "main.go");
		await waitFor(() => expect(document.querySelector(`[data-path="${deep}"]`)).toBeNull());

		rerender(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1 }} />);
		await waitFor(() => expect(document.querySelector(`[data-path="${deep}"]`)).not.toBeNull());
	});

	it("drops the cue after its hold, so it never reads as a second selection", async () => {
		vi.useFakeTimers();
		try {
			respondWith({ available: true, targetBranch: "main", targetSource: "project", files: [file()] });
			const { rerender } = render(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1 }} />, { wrapper });
			await vi.waitFor(() => expect(document.querySelector(`[data-path="${deep}"]`)).not.toBeNull());
			await vi.waitFor(() => expect(document.querySelector(".is-revealed")).not.toBeNull());
			// The clear is a setTimeout -> setState, so the advance has to flush React.
			await act(async () => {
				await vi.advanceTimersByTimeAsync(1500);
			});
			expect(document.querySelector(".is-revealed")).toBeNull();
			rerender(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1 }} />);
		} finally {
			vi.useRealTimers();
		}
	});
});
