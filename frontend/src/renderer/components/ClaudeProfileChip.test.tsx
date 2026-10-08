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
	it.each([false, true])("names a routed profile (compact=%s)", (compact) => {
		render(<ClaudeProfileChip session={{ ...session, claudeProfile: "OmniRoute" }} compact={compact} />);
		expect(screen.getByLabelText("Claude profile OmniRoute")).toHaveTextContent("OmniRoute");
	});

	it.each([undefined, "", "Subscription", "subscription"])("stays quiet for the subscription (%s)", (profile) => {
		const { container } = render(<ClaudeProfileChip session={{ ...session, claudeProfile: profile }} />);
		expect(container).toBeEmptyDOMElement();
	});
});
