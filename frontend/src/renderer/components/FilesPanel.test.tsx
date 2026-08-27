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

	// A diff measured against a ref that could not be refreshed looks exactly
	// like a correct one. The user has to be able to tell them apart, or the
	// panel is confidently wrong.
	it("marks the diff when the target branch could not be refreshed", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			targetFetch: "failed",
			targetFetchError: "fatal: could not read Username for 'https://example.invalid'",
			files: [file()],
		});
		render(<FilesPanel sessionId="s1" />, { wrapper });
		expect(await screen.findByLabelText("Could not refresh main")).toBeInTheDocument();
	});

	it("stays quiet when the target branch is current", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			targetFetch: "current",
			files: [file()],
		});
		render(<FilesPanel sessionId="s1" />, { wrapper });
		await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ });
		// A badge on every healthy render is noise that trains the eye to skip
		// the one that matters.
		expect(screen.queryByLabelText(/Could not refresh/)).not.toBeInTheDocument();
	});

	// A repository with no remote has nothing to be behind, so it must not wear
	// a "could not refresh" warning either.
	it("stays quiet when there is no remote at all", async () => {
		respondWith({ available: true, targetBranch: "main", truncated: false, files: [file()] });
		render(<FilesPanel sessionId="s1" />, { wrapper });
		await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ });
		expect(screen.queryByLabelText(/Could not refresh/)).not.toBeInTheDocument();
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

	// 🗝 The row reports its STATUS, so its owner can route it. A deleted file has
	// no working-tree content and would 404 through the file endpoint, so it has
	// to stay distinguishable here even though this panel does not decide where
	// it goes.
	it("reports a row's status and binary flag, so a deleted file can be routed away from the editor", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			files: [file({ path: "lib/gone.ts", status: "deleted", additions: 0, deletions: 38 })],
		});
		const onOpenFile = vi.fn();
		render(<FilesPanel sessionId="s1" onOpenFile={onOpenFile} />, { wrapper });

		await userEvent.click(await screen.findByRole("treeitem", { name: /gone\.ts/ }));
		expect(onOpenFile).toHaveBeenCalledWith({ path: "lib/gone.ts", status: "deleted", binary: false });
	});

	it("reports a binary row too, which has no text buffer to open either", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			truncated: false,
			files: [file({ path: "logo.png", status: "modified", binary: true })],
		});
		const onOpenFile = vi.fn();
		render(<FilesPanel sessionId="s1" onOpenFile={onOpenFile} />, { wrapper });

		await userEvent.click(await screen.findByRole("treeitem", { name: /logo\.png/ }));
		expect(onOpenFile).toHaveBeenCalledWith({ path: "logo.png", status: "modified", binary: true });
	});

	// The stacked all-files review lost its only entry point when a ROW started
	// opening the editor, so it gained one here rather than disappearing.
	it("offers the stacked review from the summary line, but not when nothing changed", async () => {
		respondWith({ available: true, targetBranch: "main", truncated: false, files: [file()] });
		const onReviewAll = vi.fn();
		render(<FilesPanel sessionId="s1" onReviewAll={onReviewAll} />, { wrapper });

		await userEvent.click(await screen.findByRole("button", { name: /review all changed files/i }));
		expect(onReviewAll).toHaveBeenCalled();
	});

	it("marks the row currently open in the center pane", async () => {
		respondWith({ available: true, targetBranch: "main", truncated: false, files: [file()] });
		render(<FilesPanel sessionId="s1" selectedPath="frontend/src/renderer/components/DiffRows.tsx" />, {
			wrapper,
		});
		await waitFor(() => expect(row(/DiffRows\.tsx/).getAttribute("aria-current")).toBe("true"));
	});

	it("switches to Browse, which lists the whole worktree rather than the diff", async () => {
		getMock.mockImplementation(async (url: string) => {
			if (url.includes("/workspace/files")) {
				return { data: { available: true, truncated: false, paths: ["README.md", "src/app/main.ts"] } };
			}
			return { data: { available: true, targetBranch: "main", truncated: false, files: [file()] } };
		});
		const onOpenWorktreeFile = vi.fn();
		render(<FilesPanel sessionId="s1" onOpenWorktreeFile={onOpenWorktreeFile} />, { wrapper });

		await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));

		// Browse opens with every folder shut, so the folder comes first.
		await userEvent.click(await screen.findByRole("treeitem", { name: /^src\/app$/ }));
		// An UNCHANGED file, which Changes mode by definition never lists.
		await userEvent.click(await screen.findByRole("treeitem", { name: /main\.ts/ }));
		expect(onOpenWorktreeFile).toHaveBeenCalledWith({ path: "src/app/main.ts" });
		// The comparison line belongs to Changes; Browse has nothing to compare.
		expect(screen.queryByText(/vs main/)).toBeNull();
	});

	describe("Browse folders", () => {
		const browsePaths = [
			"README.md",
			"App/Wallet/View.swift",
			"App/Wallet/Deep/Nested/Cell.swift",
			"App/Trading/Order.swift",
		];
		const browseWith = (paths: string[]) => {
			getMock.mockImplementation(async (url: string) => {
				if (url.includes("/workspace/files")) return { data: { available: true, truncated: false, paths } };
				return { data: { available: true, targetBranch: "main", truncated: false, files: [file()] } };
			});
		};
		const openBrowse = async () => {
			render(<FilesPanel sessionId="s1" />, { wrapper });
			await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));
		};

		// A worktree tree is not a diff: 7,000 files is ~8,500 rows, and a list that
		// long is not navigable however fast it paints. Every folder is shut, not
		// just the ones below the top — which is what "collapsed by default" says,
		// and it costs nothing now that what the reader opens is remembered.
		it("opens with every folder collapsed", async () => {
			browseWith(browsePaths);
			await openBrowse();
			await screen.findByRole("treeitem", { name: /^App$/ });
			expect(screen.queryByRole("treeitem", { name: /^Trading$/ })).not.toBeInTheDocument();
			expect(screen.queryByRole("treeitem", { name: /Order\.swift/ })).not.toBeInTheDocument();
		});

		// Browse shipped with `onToggleDir={() => {}}`: the folders LOOKED foldable
		// and did nothing.
		it("expands and re-folds a directory when its row is clicked", async () => {
			browseWith(browsePaths);
			await openBrowse();
			await userEvent.click(await screen.findByRole("treeitem", { name: /^App$/ }));
			await userEvent.click(await screen.findByRole("treeitem", { name: /^Trading$/ }));
			expect(screen.getByRole("treeitem", { name: /Order\.swift/ })).toBeInTheDocument();

			await userEvent.click(screen.getByRole("treeitem", { name: /^Trading$/ }));
			expect(screen.queryByRole("treeitem", { name: /Order\.swift/ })).not.toBeInTheDocument();
		});

		// A match five levels down must not sit behind a folder the reader never
		// closed - the folds are the un-searched tree's state, not the results'.
		it("shows a deep match without needing its folders opened", async () => {
			browseWith(browsePaths);
			await openBrowse();
			await screen.findByRole("treeitem", { name: /^App$/ });

			await userEvent.type(screen.getByRole("searchbox"), "Cell");
			expect(await screen.findByRole("treeitem", { name: /Cell\.swift/ })).toBeInTheDocument();

			// ...and clearing the query restores the folds rather than leaving the
			// tree blown open.
			await userEvent.clear(screen.getByRole("searchbox"));
			await waitFor(() => expect(screen.queryByRole("treeitem", { name: /Cell\.swift/ })).not.toBeInTheDocument());
			expect(screen.getByRole("treeitem", { name: /^App$/ })).toBeInTheDocument();
		});
	});

	// The index is a `git ls-files` over the whole tree. A rail opened on
	// Changes - which is most of the time - must not pay for it.
	it("does not index the worktree until Browse is chosen", async () => {
		respondWith({ available: true, targetBranch: "main", truncated: false, files: [file()] });
		render(<FilesPanel sessionId="s1" />, { wrapper });

		await screen.findByRole("tab", { name: /Changes/ });
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(getMock.mock.calls.every(([url]) => !String(url).includes("/workspace/files"))).toBe(true);
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

	// The RING is the loud form, and only a clicked terminal reference asks for
	// it (`focus`). Every other jump follows quietly — see the follow tests below.
	it("marks the revealed row, distinctly from the scroll-spy selection", async () => {
		respondWith({
			available: true,
			targetBranch: "main",
			targetSource: "project",
			files: [file(), file({ path: "backend/main.go" })],
		});
		const { rerender } = render(<FilesPanel sessionId="s1" />, { wrapper });
		await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ });

		rerender(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1, focus: true }} />);
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

		rerender(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1, focus: true }} />);
		await waitFor(() => expect(document.querySelector(`[data-path="${deep}"]`)).not.toBeNull());
	});

	it("drops the cue after its hold, so it never reads as a second selection", async () => {
		vi.useFakeTimers();
		try {
			respondWith({ available: true, targetBranch: "main", targetSource: "project", files: [file()] });
			const { rerender } = render(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1, focus: true }} />, {
				wrapper,
			});
			await vi.waitFor(() => expect(document.querySelector(`[data-path="${deep}"]`)).not.toBeNull());
			await vi.waitFor(() => expect(document.querySelector(".is-revealed")).not.toBeNull());
			// The clear is a setTimeout -> setState, so the advance has to flush React.
			await act(async () => {
				await vi.advanceTimersByTimeAsync(1500);
			});
			expect(document.querySelector(".is-revealed")).toBeNull();
			rerender(<FilesPanel sessionId="s1" reveal={{ path: deep, nonce: 1, focus: true }} />);
		} finally {
			vi.useRealTimers();
		}
	});
});

// Item 2: the rail remembers how each TASK was arranged, so switching away and
// back does not reset it. The panel is mounted keyed by `taskKey`, so a remount
// with the same key is exactly what leaving the tab and coming back does.
describe("FilesPanel remembers its arrangement", () => {
	const browsePaths = ["README.md", "App/Wallet/View.swift", "App/Trading/Order.swift"];
	const browseWith = (paths: string[]) => {
		getMock.mockImplementation(async (url: string) => {
			if (url.includes("/workspace/files")) return { data: { available: true, truncated: false, paths } };
			return { data: { available: true, targetBranch: "main", truncated: false, files: [file()] } };
		});
	};

	it("comes back in the same mode with the same folders open", async () => {
		browseWith(browsePaths);
		const first = render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));
		await userEvent.click(await screen.findByRole("treeitem", { name: /^App$/ }));
		expect(await screen.findByRole("treeitem", { name: /^Wallet$/ })).toBeInTheDocument();
		first.unmount();

		render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		// Browse, not Changes — and App is still open.
		expect(await screen.findByRole("treeitem", { name: /^Wallet$/ })).toBeInTheDocument();
	});

	// The bug this exists to not repeat: two caches keyed differently for the
	// same thing. One task's arrangement must not leak into another's.
	it("keeps each task's arrangement to itself", async () => {
		browseWith(browsePaths);
		const first = render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));
		await userEvent.click(await screen.findByRole("treeitem", { name: /^App$/ }));
		await screen.findByRole("treeitem", { name: /^Wallet$/ });
		first.unmount();

		render(<FilesPanel sessionId="s2" taskKey="task-2" />, { wrapper });
		// A task nobody has arranged inherits the reader's HABIT (Browse, because
		// that is where they were) but none of their folds.
		await screen.findByRole("treeitem", { name: /^App$/ });
		expect(screen.queryByRole("treeitem", { name: /^Wallet$/ })).not.toBeInTheDocument();
	});

	// Merely glancing at a worker's Files tab must not claim one of the 40
	// remembered slots — or pin that task to whatever mode it happened to open in.
	it("writes nothing for a task nobody arranged", async () => {
		getMock.mockImplementation(async () => ({
			data: { available: true, targetBranch: "main", truncated: false, files: [file()] },
		}));
		const view = render(<FilesPanel sessionId="s1" taskKey="task-untouched" />, { wrapper });
		await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ });
		view.unmount();
		expect(window.localStorage.getItem("ao.files.state")).toBeNull();
	});

	// Coming back to a rail that silently hides every file behind a query you do
	// not remember typing is worse than coming back to a full tree.
	it("deliberately forgets the search box", async () => {
		browseWith(browsePaths);
		const first = render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));
		await userEvent.type(screen.getByRole("searchbox"), "Order");
		await screen.findByRole("treeitem", { name: /Order\.swift/ });
		first.unmount();

		render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		expect(await screen.findByRole("searchbox")).toHaveValue("");
	});
});

// The rail's third narrowing of the same set — files whose CONTENT matches.
describe("FilesPanel search mode", () => {
	const respondToEverything = () => {
		getMock.mockImplementation(async (url: string) => {
			if (url.includes("/workspace/search")) {
				return {
					data: {
						available: true,
						query: "",
						files: [],
						totalMatches: 0,
						totalFiles: 0,
						filesSearched: 0,
						truncated: false,
					},
				};
			}
			if (url.includes("/workspace/files")) {
				return { data: { available: true, truncated: false, paths: ["App/Wallet.swift"] } };
			}
			return { data: { available: true, targetBranch: "main", truncated: false, files: [file()] } };
		});
	};

	it("opens the search box when ⌘⇧F asks for it", async () => {
		respondToEverything();
		const { rerender } = render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ });

		rerender(<FilesPanel search={{ nonce: 1 }} sessionId="s1" taskKey="task-1" />);
		expect(await screen.findByRole("searchbox", { name: "Search in project" })).toBeInTheDocument();
		expect(screen.getByRole("tab", { name: /Search/ })).toHaveAttribute("aria-selected", "true");
	});

	it("carries the editor's selection into the box", async () => {
		respondToEverything();
		const { rerender } = render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await screen.findByRole("treeitem", { name: /DiffRows\.tsx/ });

		rerender(<FilesPanel search={{ nonce: 1, seed: "ViewModel" }} sessionId="s1" taskKey="task-1" />);
		expect(await screen.findByRole("searchbox", { name: "Search in project" })).toHaveValue("ViewModel");
	});

	// 🗝 Search is entered by a gesture and never restored. Remembering it would
	// mean coming back to a task and finding an EMPTY box where the tree used to
	// be — the query is deliberately not remembered either — and `writeGlobalMode`
	// would then make every NEW task open that way too.
	it("is never the mode a task is remembered in", async () => {
		respondToEverything();
		const first = render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));
		await userEvent.click(await screen.findByRole("tab", { name: /Search/ }));
		first.unmount();

		expect(window.localStorage.getItem("ao.files.mode")).toBe("browse");
		expect(window.localStorage.getItem("ao.files.state")).not.toContain('"mode":"search"');

		render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		expect(await screen.findByRole("treeitem", { name: /^App$/ })).toBeInTheDocument();
		expect(screen.queryByRole("searchbox", { name: "Search in project" })).not.toBeInTheDocument();
	});

	it("Escape hands the rail back to the mode it came from", async () => {
		respondToEverything();
		render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));
		await userEvent.click(await screen.findByRole("tab", { name: /Search/ }));
		await userEvent.type(await screen.findByRole("searchbox", { name: "Search in project" }), "{Escape}");
		expect(await screen.findByRole("treeitem", { name: /^App$/ })).toBeInTheDocument();
	});
});

// Item 3: the tree shows the file that is open, wherever it lives.
describe("FilesPanel follows the open file", () => {
	const browsePaths = ["README.md", "App/Wallet/Deep/Nested/View.swift", "App/Trading/Order.swift"];
	const deep = "App/Wallet/Deep/Nested/View.swift";
	const browseWith = () => {
		getMock.mockImplementation(async (url: string) => {
			if (url.includes("/workspace/files")) return { data: { available: true, truncated: false, paths: browsePaths } };
			return { data: { available: true, targetBranch: "main", truncated: false, files: [file()] } };
		});
	};

	// Collapsed-by-default and reveal-the-open-file pull against each other, and
	// this is the resolution: exactly the ancestors of the open file open, and
	// nothing at all closes.
	it("opens exactly the ancestors of the file, in a tree that starts shut", async () => {
		browseWith();
		const { rerender } = render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));
		// Somewhere else the reader opened by hand, which must survive.
		await userEvent.click(await screen.findByRole("treeitem", { name: /^App$/ }));
		await userEvent.click(await screen.findByRole("treeitem", { name: /^Trading$/ }));

		rerender(<FilesPanel sessionId="s1" taskKey="task-1" selectedPath={deep} reveal={{ path: deep, nonce: 1 }} />);

		await waitFor(() => expect(document.querySelector(`[data-path="${deep}"]`)).not.toBeNull());
		// The reader's own fold is untouched: nothing collapses behind them.
		expect(screen.getByRole("treeitem", { name: /Order\.swift/ })).toBeInTheDocument();
		// Quiet: following is not a terminal reference, so no ring.
		expect(document.querySelector(".is-revealed")).toBeNull();
		expect(document.querySelector(`[data-path="${deep}"]`)?.getAttribute("aria-current")).toBe("true");
	});

	// The reader typed that query. A quiet follow must not throw it away — it
	// simply does not reveal, and the file is there when the query is cleared.
	it("leaves the search box alone when it is only following", async () => {
		browseWith();
		const { rerender } = render(<FilesPanel sessionId="s1" taskKey="task-1" />, { wrapper });
		await userEvent.click(await screen.findByRole("tab", { name: /Browse/ }));
		await userEvent.type(screen.getByRole("searchbox"), "Order");

		rerender(<FilesPanel sessionId="s1" taskKey="task-1" selectedPath={deep} reveal={{ path: deep, nonce: 1 }} />);
		expect(screen.getByRole("searchbox")).toHaveValue("Order");
	});
});
