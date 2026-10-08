import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { ClaudeProfileChip } from "./ClaudeProfileChip";

const session: WorkspaceSession = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "ao/sess-1",
	status: "working",
	updatedAt: "2026-10-08T00:00:00Z",
	prs: [],
};

describe("ClaudeProfileChip", () => {
	it("names a routed profile on the card", () => {
		render(<ClaudeProfileChip session={{ ...session, claudeProfile: "OmniRoute" }} />);
		expect(screen.getByLabelText("Claude profile OmniRoute")).toHaveTextContent("OmniRoute");
	});

	it("marks a routed profile on the sidebar row and names it on hover", () => {
		render(<ClaudeProfileChip session={{ ...session, claudeProfile: "OmniRoute" }} compact />);
		expect(screen.getByRole("img", { name: "Claude profile OmniRoute" })).toHaveAttribute(
			"title",
			expect.stringContaining("OmniRoute"),
		);
	});

	it.each([
		[undefined, false],
		["", false],
		["Subscription", false],
		["subscription", true],
	])("stays quiet for the subscription (%s, compact=%s)", (profile, compact) => {
		const { container } = render(
			<ClaudeProfileChip session={{ ...session, claudeProfile: profile }} compact={compact} />,
		);
		expect(container).toBeEmptyDOMElement();
	});
});
