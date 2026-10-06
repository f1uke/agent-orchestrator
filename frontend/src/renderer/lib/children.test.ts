import { describe, expect, it } from "vitest";
import { type SessionChild, childGroups, childLabel, childTooltip } from "./children";

function child(agentId: string, over: Partial<SessionChild> = {}): SessionChild {
	return {
		agentId,
		sessionId: "demo-1",
		projectId: "demo",
		branch: `ao-child/demo-1/${agentId}`,
		targetBranch: "feature/x",
		baseSha: "abc",
		worktreePath: `/data/child-worktrees/demo/demo-1/${agentId}`,
		state: "running",
		commits: 0,
		filesChanged: 0,
		createdAt: "2026-10-06T00:00:00Z",
		updatedAt: "2026-10-06T00:00:00Z",
		...over,
	};
}

describe("childGroups", () => {
	it("counts children by state, most urgent first, oldest first inside a state", () => {
		const groups = childGroups([
			child("a1", { state: "merged" }),
			child("a2", { state: "removed" }),
			child("a3", { state: "running", createdAt: "2026-10-06T00:00:09Z" }),
			child("a4", { state: "conflict" }),
			child("a5", { state: "running", createdAt: "2026-10-06T00:00:01Z" }),
		]);
		expect(groups.map((g) => [g.state, g.children.map((c) => c.agentId)])).toEqual([
			["conflict", ["a4"]],
			["running", ["a5", "a3"]],
			["merged", ["a1"]],
			["removed", ["a2"]],
		]);
	});
});

describe("child text", () => {
	it("names a child by what the worker said it was for", () => {
		expect(childLabel(child("a1", { description: "Write the parser", agentType: "general-purpose" }))).toBe(
			"Write the parser",
		);
		expect(childLabel(child("a1", { agentType: "general-purpose" }))).toBe("general-purpose");
		expect(childLabel(child("a1"))).toBe("a1");
	});

	it("tells a person where a stuck child's work is", () => {
		const text = childTooltip(
			child("a1", { state: "conflict", commits: 2, filesChanged: 3, detail: "conflicts with feature/x in C" }),
		);
		expect(text).toContain("conflict");
		expect(text).toContain("2 commits, 3 files");
		expect(text).toContain("conflicts with feature/x in C");
		expect(text).toContain("branch ao-child/demo-1/a1");
	});
});
