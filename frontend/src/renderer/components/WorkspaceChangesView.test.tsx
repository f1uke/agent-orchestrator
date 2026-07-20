import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { WorkspaceChangesView, autoExpandedPaths } from "./WorkspaceChangesView";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") => (error instanceof Error ? error.message : fallback),
}));

function wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const file = (path: string, additions = 4, deletions = 1) => ({
	path,
	status: "modified",
	additions,
	deletions,
	binary: false,
	committed: true,
});

const diffBody = {
	available: true,
	truncated: false,
	lines: [
		{ kind: "context", text: "before", oldLine: 1, newLine: 1 },
		{ kind: "add", text: "after", newLine: 2 },
	],
};

/** Route the two endpoints this view reads, and count per-file diff fetches. */
function serve(files: ReturnType<typeof file>[]) {
	const diffPaths: string[] = [];
	getMock.mockImplementation((url: string, opts: { params?: { query?: { path?: string } } }) => {
		if (url.endsWith("/workspace/changes")) {
			return Promise.resolve({
				data: { available: true, targetBranch: "main", targetSource: "pr", truncated: false, files },
				error: undefined,
			});
		}
		diffPaths.push(opts?.params?.query?.path ?? "");
		return Promise.resolve({ data: diffBody, error: undefined });
	});
	return diffPaths;
}

const section = (path: string) => screen.getByRole("region", { name: path });

beforeEach(() => {
	getMock.mockReset();
});

describe("autoExpandedPaths", () => {
	// Collapsed-by-default is the entire reason a 50-file diff does not melt the
	// app: a collapsed file costs one header row and zero requests.
	it("collapses a file bigger than the per-file ceiling", () => {
		const expanded = autoExpandedPaths([file("small.ts", 10, 10), file("huge.ts", 600, 0)]);
		expect(expanded.has("small.ts")).toBe(true);
		expect(expanded.has("huge.ts")).toBe(false);
	});

	it("stops expanding once the cumulative budget runs out", () => {
		const many = Array.from({ length: 12 }, (_, i) => file(`f${i}.ts`, 250, 0));
		const expanded = autoExpandedPaths(many);
		// 2000 lines of budget at 250 a file = the first eight, and no more.
		expect(expanded.size).toBe(8);
		expect(expanded.has("f7.ts")).toBe(true);
		expect(expanded.has("f8.ts")).toBe(false);
	});

	it("expands everything in a small diff", () => {
		const expanded = autoExpandedPaths([file("a.ts"), file("b.ts")]);
		expect(expanded.size).toBe(2);
	});

	// A binary file has no lines to render, so it must neither be judged by its
	// counts nor spend the budget real diffs need — and git's own counts for a
	// binary file are not arithmetic to begin with.
	it("does not spend budget on a binary file", () => {
		const expanded = autoExpandedPaths([{ ...file("img.png", 4000, 3000), binary: true }, file("a.ts")]);
		expect(expanded.has("img.png")).toBe(true);
		expect(expanded.has("a.ts")).toBe(true);
	});
});

describe("WorkspaceChangesView", () => {
	it("stacks a section for every changed file, not one file at a time", async () => {
		serve([file("src/a.ts"), file("src/b.ts"), file("docs/c.md")]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		await screen.findByRole("region", { name: "src/a.ts" });
		expect(screen.getAllByRole("region")).toHaveLength(3);
		expect(within(section("src/a.ts")).getByText("+4")).toBeInTheDocument();
		expect(within(section("src/a.ts")).getByText("−1")).toBeInTheDocument();
	});

	// The rail's tree groups directories before files, so stacking in raw API
	// order would make scrolling here and reading down the tree disagree.
	it("stacks the files in the rail's tree order, not the order the API returned", async () => {
		serve([file("root.ts"), file("src/b.ts"), file("src/a.ts"), file("docs/x.md")]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		await screen.findByRole("region", { name: "root.ts" });
		expect(screen.getAllByRole("region").map((r) => r.getAttribute("aria-label"))).toEqual([
			"docs/x.md",
			"src/a.ts",
			"src/b.ts",
			"root.ts",
		]);
	});

	it("fetches a diff only for the files it actually expanded", async () => {
		const diffPaths = serve([file("small.ts", 3, 1), file("huge.ts", 900, 0)]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		await waitFor(() => expect(diffPaths).toContain("small.ts"));
		// The oversized file is collapsed, so it costs a header and NO request.
		expect(diffPaths).not.toContain("huge.ts");
		expect(screen.getByRole("button", { name: /Expand huge\.ts/ })).toBeInTheDocument();
	});

	it("expands a collapsed file on demand, and only then loads it", async () => {
		const diffPaths = serve([file("huge.ts", 900, 0)]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		const toggle = await screen.findByRole("button", { name: /Expand huge\.ts/ });
		expect(diffPaths).not.toContain("huge.ts");

		await userEvent.click(toggle);
		await waitFor(() => expect(diffPaths).toContain("huge.ts"));
		expect(await screen.findByRole("button", { name: /Collapse huge\.ts/ })).toBeInTheDocument();
	});

	it("collapses an expanded file again", async () => {
		serve([file("a.ts")]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		await userEvent.click(await screen.findByRole("button", { name: /Collapse a\.ts/ }));
		expect(await screen.findByRole("button", { name: /Expand a\.ts/ })).toBeInTheDocument();
	});

	it("expands every file at once when asked", async () => {
		const diffPaths = serve([file("huge.ts", 900, 0), file("bigger.ts", 800, 0)]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		await userEvent.click(await screen.findByRole("button", { name: "Expand all" }));
		await waitFor(() => {
			expect(diffPaths).toContain("huge.ts");
			expect(diffPaths).toContain("bigger.ts");
		});
	});

	it("collapses every file at once when asked", async () => {
		serve([file("a.ts"), file("b.ts")]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		await userEvent.click(await screen.findByRole("button", { name: "Collapse all" }));
		expect(await screen.findByRole("button", { name: /Expand a\.ts/ })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /Expand b\.ts/ })).toBeInTheDocument();
	});

	// Clicking a tree row must land on the file even when it was auto-collapsed,
	// otherwise a large file scrolls to a header and shows nothing.
	it("scrolls to the focused file and expands it", async () => {
		const diffPaths = serve([file("a.ts"), file("huge.ts", 900, 0)]);
		const scrollIntoView = vi.fn();
		Element.prototype.scrollIntoView = scrollIntoView;

		const view = render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });
		await screen.findByRole("region", { name: "huge.ts" });

		view.rerender(
			<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
				<WorkspaceChangesView sessionId="s1" focus={{ path: "huge.ts", nonce: 1 }} onClose={() => {}} />
			</QueryClientProvider>,
		);

		await waitFor(() => expect(scrollIntoView).toHaveBeenCalled());
		await waitFor(() => expect(diffPaths).toContain("huge.ts"));
	});

	it("goes back to the agent when closed", async () => {
		serve([file("a.ts")]);
		const onClose = vi.fn();
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={onClose} />, { wrapper });

		await userEvent.click(await screen.findByRole("button", { name: /agent/ }));
		expect(onClose).toHaveBeenCalled();
	});

	it("reports the file count and totals in its header", async () => {
		serve([file("a.ts", 10, 2), file("b.ts", 5, 3)]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		expect(await screen.findByText("2 files")).toBeInTheDocument();
		const header = screen.getByRole("banner");
		expect(within(header).getByText("+15")).toBeInTheDocument();
		expect(within(header).getByText("−5")).toBeInTheDocument();
	});

	it("shows a binary file as a section without a diff body", async () => {
		const diffPaths = serve([{ ...file("img.png", 0, 0), binary: true }]);
		render(<WorkspaceChangesView sessionId="s1" focus={null} onClose={() => {}} />, { wrapper });

		expect(await screen.findByText(/Binary file/)).toBeInTheDocument();
		expect(diffPaths).not.toContain("img.png");
	});
});
