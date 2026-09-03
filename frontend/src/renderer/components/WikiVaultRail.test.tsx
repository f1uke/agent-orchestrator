import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { WikiVaultRail } from "./WikiVaultRail";

const NOTES = [
	{ path: "index.md", size: 10, modifiedAt: new Date().toISOString() },
	{ path: "Projects/MOBILITY-4713-Webview-Zoom/_tasks.md", size: 20, modifiedAt: new Date().toISOString() },
	{ path: "Projects/STAR-2195-Navigate/_tasks.md", size: 20, modifiedAt: new Date().toISOString() },
];

function rail(openPath: string | null = null) {
	return (
		<WikiVaultRail
			files={{ notes: NOTES, truncated: false }}
			loading={false}
			openPath={openPath}
			onOpenNote={vi.fn()}
			onRefresh={vi.fn()}
			query=""
			onQueryChange={vi.fn()}
		/>
	);
}

function folderRow(name: string) {
	return screen.getByRole("button", { name: new RegExp(`^${name}`) });
}

beforeEach(() => {
	window.localStorage.clear();
});

/**
 * 55 folders is unusable if every visit starts from scratch, which is what a
 * per-row `useState` gave. These assert the arrangement outlives BOTH a remount
 * (leaving the page and coming back) and a parent folder closing over its
 * children.
 */
describe("WikiVaultRail folder state", () => {
	it("remembers an expanded folder across a remount", async () => {
		const user = userEvent.setup();
		const first = render(rail());
		// Top level is open by default, so the nested folder's row is already
		// there — shut, which is the default this test then deviates from.
		await user.click(folderRow("MOBILITY-4713-Webview-Zoom"));
		expect(screen.getByText("_tasks.md")).toBeTruthy();
		first.unmount();

		render(rail());
		expect(screen.getByText("_tasks.md")).toBeTruthy();
		expect(folderRow("MOBILITY-4713-Webview-Zoom").getAttribute("aria-expanded")).toBe("true");
	});

	it("remembers a top-level folder the reader shut", async () => {
		const user = userEvent.setup();
		const first = render(rail());
		expect(folderRow("Projects").getAttribute("aria-expanded")).toBe("true");
		await user.click(folderRow("Projects"));
		first.unmount();

		render(rail());
		expect(folderRow("Projects").getAttribute("aria-expanded")).toBe("false");
	});

	it("keeps a deep folder open after its parent is shut and reopened", async () => {
		const user = userEvent.setup();
		render(rail());
		await user.click(folderRow("MOBILITY-4713-Webview-Zoom"));
		// Shutting the parent unmounts the child row; reopening must not reset it.
		await user.click(folderRow("Projects"));
		expect(screen.queryByText("_tasks.md")).toBeNull();
		await user.click(folderRow("Projects"));
		expect(folderRow("MOBILITY-4713-Webview-Zoom").getAttribute("aria-expanded")).toBe("true");
	});
});
