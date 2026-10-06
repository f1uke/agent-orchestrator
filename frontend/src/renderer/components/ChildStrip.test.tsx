import { render } from "@testing-library/react";
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

	it("draws one pip per state with its count, the state needing a person first", () => {
		render(
			<ChildStrip
				items={[
					child("a1", { state: "merged" }),
					child("a2", { state: "merged" }),
					child("a3", { state: "held", detail: "the worker has uncommitted changes in A" }),
					child("a4", { state: "running" }),
				]}
			/>,
		);
		const pips = [...document.querySelectorAll("[data-child-pip]")];
		expect(pips.map((p) => [p.getAttribute("data-child-pip"), p.getAttribute("data-child-count")])).toEqual([
			["held", "1"],
			["running", "1"],
			["merged", "2"],
		]);
	});
});
