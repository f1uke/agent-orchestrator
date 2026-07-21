import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { buildFileTree } from "../lib/file-tree";
import { FileTree } from "./FileTree";

type Item = { path: string };
const nodesFor = (...paths: string[]) =>
	buildFileTree<Item>(
		paths.map((path) => ({ path })),
		(f) => f.path,
	);

function Harness({
	paths,
	onSelectFile,
	selectedKey,
}: {
	paths: string[];
	onSelectFile?: (item: Item) => void;
	selectedKey?: string;
}) {
	const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
	return (
		<FileTree
			nodes={nodesFor(...paths)}
			collapsed={collapsed}
			onToggleDir={(key) =>
				setCollapsed((prev) => {
					const next = new Set(prev);
					if (!next.delete(key)) next.add(key);
					return next;
				})
			}
			onSelectFile={onSelectFile}
			selectedKey={selectedKey}
			label="Changed files"
		/>
	);
}

describe("FileTree", () => {
	it("renders directories and their files as a tree", async () => {
		render(<Harness paths={["src/a.ts", "src/b.ts"]} />);
		expect(screen.getByRole("tree", { name: "Changed files" })).toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /src/ })).toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /a\.ts/ })).toBeInTheDocument();
	});

	it("shows a collapsed single-child directory chain as one row", () => {
		render(<Harness paths={["backend/internal/service/session/workspace_changes.go"]} />);
		// Five path levels: one directory row plus the file, not five rows.
		expect(screen.getAllByRole("treeitem")).toHaveLength(2);
		expect(screen.getByRole("treeitem", { name: /^backend\/internal\/service\/session$/ })).toBeInTheDocument();
	});

	it("hides a directory's files when it is collapsed, and shows them again", async () => {
		render(<Harness paths={["src/a.ts", "src/other.ts", "docs/b.md"]} />);
		expect(screen.getByRole("treeitem", { name: /a\.ts/ })).toBeInTheDocument();

		await userEvent.click(screen.getByRole("treeitem", { name: /src/ }));
		expect(screen.queryByRole("treeitem", { name: /a\.ts/ })).not.toBeInTheDocument();
		// a sibling directory is unaffected
		expect(screen.getByRole("treeitem", { name: /b\.md/ })).toBeInTheDocument();

		await userEvent.click(screen.getByRole("treeitem", { name: /src/ }));
		expect(screen.getByRole("treeitem", { name: /a\.ts/ })).toBeInTheDocument();
	});

	it("reports expansion and depth to assistive tech", async () => {
		render(<Harness paths={["src/a.ts", "src/other.ts"]} />);
		const dir = screen.getByRole("treeitem", { name: /src/ });
		expect(dir).toHaveAttribute("aria-expanded", "true");
		expect(dir).toHaveAttribute("aria-level", "1");
		expect(screen.getByRole("treeitem", { name: /a\.ts/ })).toHaveAttribute("aria-level", "2");

		await userEvent.click(dir);
		expect(screen.getByRole("treeitem", { name: /src/ })).toHaveAttribute("aria-expanded", "false");
	});

	// A directory row must never be mistaken for a file: clicking it expands,
	// it does not open a diff.
	it("selects files only, never directories", async () => {
		const onSelectFile = vi.fn();
		render(<Harness paths={["src/a.ts", "src/other.ts"]} onSelectFile={onSelectFile} />);

		await userEvent.click(screen.getByRole("treeitem", { name: /src/ }));
		expect(onSelectFile).not.toHaveBeenCalled();

		await userEvent.click(screen.getByRole("treeitem", { name: /src/ }));
		await userEvent.click(screen.getByRole("treeitem", { name: /a\.ts/ }));
		expect(onSelectFile).toHaveBeenCalledWith({ path: "src/a.ts" });
	});

	it("marks the selected file as current", () => {
		render(<Harness paths={["src/a.ts", "src/b.ts"]} selectedKey="src/b.ts" />);
		expect(screen.getByRole("treeitem", { name: /b\.ts/ })).toHaveAttribute("aria-current", "true");
		expect(screen.getByRole("treeitem", { name: /a\.ts/ })).not.toHaveAttribute("aria-current");
	});

	// Browse mode reuses this component with a different payload, so the
	// per-item chrome has to come from the caller, not from the tree.
	it("renders caller-supplied lead and meta content for each file", () => {
		render(
			<FileTree
				nodes={nodesFor("src/a.ts", "src/other.ts")}
				collapsed={new Set()}
				onToggleDir={() => {}}
				label="Files"
				renderLead={(item) => <span>lead:{item.path}</span>}
				renderMeta={(item) => <span>meta:{item.path}</span>}
			/>,
		);
		expect(screen.getByText("lead:src/a.ts")).toBeInTheDocument();
		expect(screen.getByText("meta:src/a.ts")).toBeInTheDocument();
	});

	it("indents deeper rows further than shallow ones", () => {
		render(<Harness paths={["a/b/deep.ts", "a/b/sibling.ts", "root.ts"]} />);
		const deep = screen.getByRole("treeitem", { name: /deep\.ts/ });
		const root = screen.getByRole("treeitem", { name: /root\.ts/ });
		expect(indentOf(deep)).toBeGreaterThan(indentOf(root));
	});

	// Indent used to stop growing after the fourth level, so a file six levels
	// deep rendered at EXACTLY its parent folder's x and read as that folder's
	// sibling — the tree stating the wrong structure.
	//
	// This needs a BRANCHY fixture: every level below forks, so chain-collapsing
	// has nothing to merge and the rendered depth really reaches six. A
	// single-child path like `a/b/c/d/e/f.ts` collapses to two rows and can never
	// reach the clamp, which is how the clamp shipped past a test that only ever
	// compared level 3 against level 1.
	describe("at depth", () => {
		const deepPaths = [
			"App/Commons/Networking/APIClient.swift",
			"App/Investment/Fund/FundList.swift",
			"App/Investment/Trade/OrderReview/Models/Coupon.swift",
			"App/Investment/Trade/OrderReview/ViewModels/Consent.swift",
			"App/Investment/Trade/Portfolio/Summary.swift",
		];

		it("never renders a file at its parent folder's indent", () => {
			render(<Harness paths={deepPaths} />);
			const models = screen.getByRole("treeitem", { name: /^Models$/ });
			const coupon = screen.getByRole("treeitem", { name: /Coupon\.swift/ });
			// Guard the fixture itself: if chain-collapsing ever flattens these, the
			// indent assertion below would pass while testing nothing.
			expect(models).toHaveAttribute("aria-level", "5");
			expect(coupon).toHaveAttribute("aria-level", "6");
			expect(indentOf(coupon)).toBeGreaterThan(indentOf(models));
		});

		it("gives every level its own indent, all the way down", () => {
			render(<Harness paths={deepPaths} />);
			// One representative row per level, level 1 → 6.
			const chain = [/^App$/, /^Investment$/, /^Trade$/, /^OrderReview$/, /^Models$/, /Coupon\.swift/].map((name) =>
				screen.getByRole("treeitem", { name }),
			);
			expect(chain.map((row) => row.getAttribute("aria-level"))).toEqual(["1", "2", "3", "4", "5", "6"]);

			const indents = chain.map(indentOf);
			for (let i = 1; i < indents.length; i++) {
				expect(indents[i]).toBeGreaterThan(indents[i - 1]);
			}
		});

		// The hairlines are what carry structure once the per-level step narrows,
		// so they have to keep pace with depth rather than stopping with it.
		it("traces a guide for every ancestor, not just the first four", () => {
			render(<Harness paths={deepPaths} />);
			const coupon = screen.getByRole("treeitem", { name: /Coupon\.swift/ });
			expect(coupon.querySelectorAll(".file-tree__guide")).toHaveLength(5);
		});
	});
});

function indentOf(el: HTMLElement): number {
	return Number.parseFloat(el.style.paddingLeft || "0");
}
