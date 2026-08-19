import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { QueuedMessagesChip } from "./QueuedMessagesChip";

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "sess-3",
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "sleeping worker",
		provider: "claude-code",
		kind: "worker",
		branch: "feat/x",
		status: "needs_input",
		updatedAt: "2031-03-04T09:00:00Z",
		prs: [],
		...overrides,
	} as WorkspaceSession;
}

describe("QueuedMessagesChip", () => {
	it("says nothing for a session with an empty inbox", () => {
		const { container } = render(<QueuedMessagesChip session={session()} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("shows how many messages are waiting, and that they will still arrive", () => {
		render(<QueuedMessagesChip session={session({ isSuspended: true, queuedMessages: 2 })} />);
		const chip = screen.getByLabelText("2 messages waiting for this session's agent");
		expect(chip).toHaveTextContent("2");
		expect(chip.getAttribute("title")).toContain("delivered once its agent is listening again");
	});

	it("uses the singular for one message", () => {
		render(<QueuedMessagesChip session={session({ queuedMessages: 1 })} />);
		expect(screen.getByLabelText("1 message waiting for this session's agent")).toBeInTheDocument();
	});

	// A message that will never arrive is the one worth acting on, so it must not
	// read the same as one that is merely waiting.
	it("distinguishes messages that could not be delivered", () => {
		render(<QueuedMessagesChip session={session({ queuedMessagesFailed: 3 })} />);
		const chip = screen.getByLabelText("3 messages could not be delivered");
		expect(chip).toHaveTextContent("3");
	});

	// While anything is still waiting, the waiting count leads: it is the
	// actionable "there is unread mail in here" signal.
	it("leads with what is still waiting when both exist", () => {
		render(<QueuedMessagesChip session={session({ queuedMessages: 1, queuedMessagesFailed: 4 })} />);
		expect(screen.getByLabelText("1 message waiting for this session's agent")).toBeInTheDocument();
	});
});
