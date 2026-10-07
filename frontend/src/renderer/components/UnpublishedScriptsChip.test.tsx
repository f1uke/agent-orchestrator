import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { SessionScriptsStore } from "../lib/scripts-store";
import type { SessionStatus, WorkspaceSession } from "../types/workspace";
import { UnpublishedScriptsChip } from "./UnpublishedScriptsChip";

function session(status: SessionStatus, scriptsStore?: SessionScriptsStore): WorkspaceSession {
	return {
		id: "nter-7",
		workspaceId: "nter-ios-app",
		workspaceName: "nter-ios-app",
		title: "login flow script",
		provider: "claude-code",
		kind: "worker",
		branch: "feature/login",
		status,
		updatedAt: "2026-10-07T00:00:00Z",
		prs: [],
		scriptsStore,
	};
}

const pending: SessionScriptsStore = {
	uncommitted: 1,
	unpublished: 2,
	files: ["projects/nter/login/flow.yaml"],
};

describe("UnpublishedScriptsChip", () => {
	it("says the task's scripts are not in the store once its agent stops, with the files and the command", () => {
		render(<UnpublishedScriptsChip session={session("needs_input", pending)} />);
		const chip = screen.getByText("Unpublished scripts").closest("[data-unpublished-scripts]");
		expect(chip).not.toBeNull();
		const title = chip?.getAttribute("title") ?? "";
		expect(title).toContain("1 uncommitted file and 2 unpublished commits");
		expect(title).toContain("projects/nter/login/flow.yaml");
		expect(title).toContain("ao scripts publish");
	});

	it("stays quiet while the agent is working, when a draft mid-task is expected", () => {
		const { container } = render(<UnpublishedScriptsChip session={session("working", pending)} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("stays quiet when everything is in the store, or the session has no store worktree", () => {
		const clean = render(
			<UnpublishedScriptsChip session={session("idle", { uncommitted: 0, unpublished: 0, files: [] })} />,
		);
		expect(clean.container).toBeEmptyDOMElement();
		const none = render(<UnpublishedScriptsChip session={session("idle")} />);
		expect(none.container).toBeEmptyDOMElement();
	});

	it("always shows a worktree a teardown kept, and why", () => {
		const held: SessionScriptsStore = {
			uncommitted: 0,
			unpublished: 1,
			heldReason: "publish_conflict",
			files: ["projects/nter/login/flow.yaml"],
		};
		render(<UnpublishedScriptsChip session={session("working", held)} />);
		const chip = screen.getByText("Scripts held").closest("[data-unpublished-scripts]");
		expect(chip?.getAttribute("title")).toContain("its commits conflict with the store");
	});

	it("shrinks to a glyph in the sidebar, keeping the tooltip", () => {
		render(<UnpublishedScriptsChip session={session("idle", pending)} compact />);
		const glyph = screen.getByLabelText("Unpublished scripts");
		expect(glyph.closest("[title]")?.getAttribute("title")).toContain("ao scripts publish");
	});
});
