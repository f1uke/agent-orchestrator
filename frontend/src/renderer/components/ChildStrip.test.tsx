import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ChildStrip } from "./ChildStrip";
import type { SessionChild } from "../lib/children";

function child(agentId: string, over: Partial<SessionChild> = {}): SessionChild {
	return {
		agentId,
		sessionId: "demo-1",
		projectId: "demo",
		branch: `ao-child/demo-1/${agentId}`,
		targetBranch: "feature/x",
		baseSha: "abc",
		worktreePath: `/x/${agentId}`,
		state: "running",
		commits: 0,
		filesChanged: 0,
		createdAt: "2026-10-06T00:00:00Z",
		updatedAt: "2026-10-06T00:00:00Z",
		...over,
	};
}

describe("ChildStrip", () => {
	it("renders nothing for a task that never ran an isolated subagent", () => {
		const { container } = render(<ChildStrip items={[]} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("draws one chip per child with its state, named by its task", () => {
		render(
			<ChildStrip
				items={[
					child("a1", { description: "Write the parser", state: "merged", commits: 1, filesChanged: 2 }),
					child("a2", {
						description: "Fix the tests",
						state: "held",
						detail: "the worker has uncommitted changes in A",
					}),
				]}
			/>,
		);
		expect(screen.getByText("Write the parser")).toBeInTheDocument();
		expect(document.querySelector('[data-child-chip="a2"]')).toHaveAttribute("data-child-state", "held");
		expect(document.querySelectorAll("[data-child-chip]")[0]).toHaveAttribute("data-child-chip", "a2");
	});

	it("says how many finished without changes when nothing else is left to draw", () => {
		render(<ChildStrip items={[child("a1", { state: "removed" }), child("a2", { state: "removed" })]} />);
		expect(screen.getByText("2 without changes")).toBeInTheDocument();
		expect(document.querySelectorAll("[data-child-chip]")).toHaveLength(0);
	});
});
